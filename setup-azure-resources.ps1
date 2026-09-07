# ==============================================================================
# Dynamic Secret Operator (DSO) – Complete Azure Infrastructure Provisioner
# ==============================================================================
# This script provisions all necessary Azure resources for the Dynamic Secret
# Operator using the lowest possible SKUs/tiers to minimize cost:
# - Resource Group
# - User-Assigned Managed Identity
# - Azure Key Vault (Standard, RBAC enabled, purge protection disabled, no locks)
# - Azure Service Bus (Basic tier, queue with dead-lettering)
# - Azure Event Grid System Topic & Subscription (Key Vault -> Service Bus Queue)
# - Azure Kubernetes Service (Free tier, 1x B2s node, OIDC & Workload Identity enabled)
# - Azure Container Registry (Basic tier - lowest cost, ~$0.16/day, attached to AKS)
# - Azure RBAC Role Assignments (Key Vault Secrets User, Service Bus Data Receiver, AcrPull)
# - Workload Identity Federated Credential
#
# All resources are verified before creation for full idempotency.
# No random suffixes are appended and no deletion locks are created.
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$ResourceGroupName = "rg-dso-dev",

    [Parameter(Mandatory = $false)]
    [string]$Location = "eastus",

    [Parameter(Mandatory = $false)]
    [string]$ClusterName = "aks-dso-dev",

    [Parameter(Mandatory = $false)]
    [string]$IdentityName = "id-dso-dev",

    [Parameter(Mandatory = $false)]
    [string]$KeyVaultName = "kv-dso-dev-jc",

    [Parameter(Mandatory = $false)]
    [string]$ServiceBusNamespace = "sb-dso-dev",

    [Parameter(Mandatory = $false)]
    [string]$ServiceBusQueueName = "dso-vault-events",

    [Parameter(Mandatory = $false)]
    [string]$EventGridTopicName = "egst-kv-dso",

    [Parameter(Mandatory = $false)]
    [string]$EventSubscriptionName = "sub-kv-to-sb",

    [Parameter(Mandatory = $false)]
    [string]$AcrName = "",

    [Parameter(Mandatory = $false)]
    [string]$ServiceAccountNamespace = "dso-system",

    [Parameter(Mandatory = $false)]
    [string]$ServiceAccountName = "dso-dynamic-secret-operator",

    [Parameter(Mandatory = $false)]
    [string]$NodeVmSize = "standard_b2ps_v2"
)

$ErrorActionPreference = "Stop"

function Write-Step {
    param([string]$Message)
    Write-Host "`n==================================================================" -ForegroundColor Cyan
    Write-Host "🚀 $Message" -ForegroundColor Cyan
    Write-Host "==================================================================" -ForegroundColor Cyan
}

function Write-Success {
    param([string]$Message)
    Write-Host "✅ $Message" -ForegroundColor Green
}

function Write-Info {
    param([string]$Message)
    Write-Host "ℹ️  $Message" -ForegroundColor Yellow
}

# ------------------------------------------------------------------------------
# 0. Check Azure CLI Prerequisites & Active Account
# ------------------------------------------------------------------------------
Write-Step "Checking Azure CLI prerequisites and login status..."

if (-not (Get-Command az -ErrorAction SilentlyContinue)) {
    Write-Error "Azure CLI ('az') is not installed or not in PATH. Please install it: https://learn.microsoft.com/en-us/cli/azure/install-azure-cli"
}

$accountJson = az account show 2>$null
if (-not $accountJson) {
    Write-Error "Not logged into Azure. Please run 'az login' before executing this script."
}

$account = $accountJson | ConvertFrom-Json
$subscriptionId = $account.id
$tenantId = $account.tenantId
Write-Success "Connected to Subscription: $($account.name) ($subscriptionId)"
Write-Success "Tenant ID: $tenantId"

if (-not $AcrName) {
    # Generate deterministic lowercase alphanumeric ACR name (5-50 chars, no hyphens)
    $subClean = $subscriptionId.Replace("-", "").Substring(0, 6).ToLowerInvariant()
    $AcrName = "acrdsodev$subClean"
}
Write-Info "Target Azure Container Registry (ACR): $AcrName (Basic Tier)"

# ------------------------------------------------------------------------------
# 1. Resource Group
# ------------------------------------------------------------------------------
Write-Step "Step 1: Checking Resource Group '$ResourceGroupName'..."

$rgExists = az group exists --name $ResourceGroupName
if ($rgExists -eq "true") {
    Write-Info "Resource Group '$ResourceGroupName' already exists."
} else {
    Write-Info "Creating Resource Group '$ResourceGroupName' in '$Location'..."
    az group create --name $ResourceGroupName --location $Location --output none
    Write-Success "Resource Group '$ResourceGroupName' created."
}

# ------------------------------------------------------------------------------
# 2. User-Assigned Managed Identity
# ------------------------------------------------------------------------------
Write-Step "Step 2: Checking User-Assigned Managed Identity '$IdentityName'..."

$identityJson = az identity show --name $IdentityName --resource-group $ResourceGroupName 2>$null
if ($identityJson) {
    Write-Info "Managed Identity '$IdentityName' already exists."
} else {
    Write-Info "Creating Managed Identity '$IdentityName'..."
    $identityJson = az identity create --name $IdentityName --resource-group $ResourceGroupName --location $Location
    Write-Success "Managed Identity '$IdentityName' created."
}

$identity = $identityJson | ConvertFrom-Json
$managedIdentityClientId = $identity.clientId
$managedIdentityPrincipalId = $identity.principalId
$managedIdentityId = $identity.id
Write-Success "Managed Identity Client ID: $managedIdentityClientId"
Write-Success "Managed Identity Principal ID: $managedIdentityPrincipalId"

# ------------------------------------------------------------------------------
# 3. Azure Key Vault (Lowest SKU: Standard, Purge Protection Disabled, No Locks)
# ------------------------------------------------------------------------------
Write-Step "Step 3: Checking Azure Key Vault '$KeyVaultName'..."

$keyVaultId = az keyvault show --name $KeyVaultName --resource-group $ResourceGroupName --query id -o tsv 2>$null
if ($keyVaultId) {
    Write-Info "Key Vault '$KeyVaultName' already exists."
} else {
    Write-Info "Creating Key Vault '$KeyVaultName' (SKU: standard, RBAC enabled, purge protection: disabled by default)..."
    az keyvault create `
        --name $KeyVaultName `
        --resource-group $ResourceGroupName `
        --location $Location `
        --sku standard `
        --enable-rbac-authorization true `
        --retention-days 7 `
        --output none
    $keyVaultId = az keyvault show --name $KeyVaultName --resource-group $ResourceGroupName --query id -o tsv
    Write-Success "Key Vault '$KeyVaultName' created."
}

$keyVaultUri = "https://$KeyVaultName.vault.azure.net"
Write-Success "Key Vault URI: $keyVaultUri"

# ------------------------------------------------------------------------------
# 4. Azure Service Bus (Lowest SKU: Basic) & Queue
# ------------------------------------------------------------------------------
Write-Step "Step 4: Checking Azure Service Bus Namespace '$ServiceBusNamespace' and Queue '$ServiceBusQueueName'..."

$serviceBusId = az servicebus namespace show --name $ServiceBusNamespace --resource-group $ResourceGroupName --query id -o tsv 2>$null
if ($serviceBusId) {
    Write-Info "Service Bus namespace '$ServiceBusNamespace' already exists."
} else {
    Write-Info "Creating Service Bus namespace '$ServiceBusNamespace' (SKU: Basic)..."
    az servicebus namespace create `
        --name $ServiceBusNamespace `
        --resource-group $ResourceGroupName `
        --location $Location `
        --sku Basic `
        --output none
    $serviceBusId = az servicebus namespace show --name $ServiceBusNamespace --resource-group $ResourceGroupName --query id -o tsv
    Write-Success "Service Bus namespace '$ServiceBusNamespace' created."
}

$serviceBusFqdn = "$ServiceBusNamespace.servicebus.windows.net"

$queueId = az servicebus queue show --name $ServiceBusQueueName --namespace-name $ServiceBusNamespace --resource-group $ResourceGroupName --query id -o tsv 2>$null
if ($queueId) {
    Write-Info "Service Bus queue '$ServiceBusQueueName' already exists."
} else {
    Write-Info "Creating Service Bus queue '$ServiceBusQueueName' with dead-lettering enabled..."
    az servicebus queue create `
        --name $ServiceBusQueueName `
        --namespace-name $ServiceBusNamespace `
        --resource-group $ResourceGroupName `
        --enable-dead-lettering-on-message-expiration true `
        --max-delivery-count 10 `
        --output none
    $queueId = az servicebus queue show --name $ServiceBusQueueName --namespace-name $ServiceBusNamespace --resource-group $ResourceGroupName --query id -o tsv
    Write-Success "Service Bus queue '$ServiceBusQueueName' created."
}

# ------------------------------------------------------------------------------
# 5. Event Grid System Topic & Subscription (Key Vault -> Service Bus Queue)
# ------------------------------------------------------------------------------
Write-Step "Step 5: Checking Event Grid System Topic '$EventGridTopicName' & Subscription..."

# Ensure Microsoft.EventGrid provider is registered in subscription
$egRegistrationState = az provider show --namespace Microsoft.EventGrid --query "registrationState" -o tsv 2>$null
if ($egRegistrationState -ne "Registered") {
    Write-Info "Registering Microsoft.EventGrid resource provider..."
    az provider register --namespace Microsoft.EventGrid --output none
}

$topicId = az eventgrid system-topic show --name $EventGridTopicName --resource-group $ResourceGroupName --query id -o tsv 2>$null
if ($topicId) {
    Write-Info "Event Grid System Topic '$EventGridTopicName' already exists."
} else {
    Write-Info "Creating Event Grid System Topic for Key Vault '$KeyVaultName'..."
    az eventgrid system-topic create `
        --name $EventGridTopicName `
        --resource-group $ResourceGroupName `
        --location $Location `
        --topic-type Microsoft.KeyVault.vaults `
        --source $keyVaultId `
        --output none
    $topicId = az eventgrid system-topic show --name $EventGridTopicName --resource-group $ResourceGroupName --query id -o tsv
    Write-Success "Event Grid System Topic '$EventGridTopicName' created."
}

$subId = az eventgrid system-topic event-subscription show --name $EventSubscriptionName --system-topic-name $EventGridTopicName --resource-group $ResourceGroupName --query id -o tsv 2>$null
if ($subId) {
    Write-Info "Event Grid subscription '$EventSubscriptionName' already exists."
} else {
    Write-Info "Creating Event Grid subscription for 'Microsoft.KeyVault.SecretNewVersionCreated' -> Service Bus Queue..."
    az eventgrid system-topic event-subscription create `
        --name $EventSubscriptionName `
        --system-topic-name $EventGridTopicName `
        --resource-group $ResourceGroupName `
        --endpoint-type servicebusqueue `
        --endpoint $queueId `
        --included-event-types Microsoft.KeyVault.SecretNewVersionCreated Microsoft.KeyVault.CertificateNewVersionCreated `
        --output none
    Write-Success "Event Grid subscription '$EventSubscriptionName' created."
}

# ------------------------------------------------------------------------------
# 6. Azure Kubernetes Service (AKS) (Free Tier, 1x B2s Node, OIDC & Workload Identity)
# ------------------------------------------------------------------------------
Write-Step "Step 6: Checking AKS Cluster '$ClusterName'..."

$aksId = az aks show --name $ClusterName --resource-group $ResourceGroupName --query id -o tsv 2>$null
if ($aksId) {
    Write-Info "AKS cluster '$ClusterName' already exists."
} else {
    Write-Info "Creating AKS cluster '$ClusterName' (Tier: Free, 1x $NodeVmSize node, OIDC & Workload Identity enabled)..."
    az aks create `
        --name $ClusterName `
        --resource-group $ResourceGroupName `
        --location $Location `
        --tier free `
        --node-count 1 `
        --node-vm-size $NodeVmSize `
        --enable-oidc-issuer `
        --enable-workload-identity `
        --generate-ssh-keys `
        --output none
    Write-Success "AKS cluster '$ClusterName' created."
}

$aksOidcIssuerUrl = az aks show --name $ClusterName --resource-group $ResourceGroupName --query "oidcIssuerProfile.issuerUrl" -o tsv
Write-Success "AKS OIDC Issuer URL: $aksOidcIssuerUrl"

# ------------------------------------------------------------------------------
# 7. Azure Container Registry (ACR) (Basic Tier - Lowest Cost, Attached to AKS)
# ------------------------------------------------------------------------------
Write-Step "Step 7: Checking Azure Container Registry (ACR) '$AcrName'..."

$acrId = az acr show --name $AcrName --resource-group $ResourceGroupName --query id -o tsv 2>$null
if ($acrId) {
    Write-Info "ACR '$AcrName' already exists."
} else {
    Write-Info "Creating ACR '$AcrName' (SKU: Basic - lowest cost tier, ~$0.16/day)..."
    az acr create `
        --name $AcrName `
        --resource-group $ResourceGroupName `
        --location $Location `
        --sku Basic `
        --admin-enabled true `
        --output none
    $acrId = az acr show --name $AcrName --resource-group $ResourceGroupName --query id -o tsv
    Write-Success "ACR '$AcrName' created."
}

$acrLoginServer = az acr show --name $AcrName --resource-group $ResourceGroupName --query loginServer -o tsv
Write-Success "ACR Login Server: $acrLoginServer"

# Ensure AKS kubelet identity is attached to ACR with AcrPull role
$kubeletObjectId = az aks show --name $ClusterName --resource-group $ResourceGroupName --query "identityProfile.kubeletidentity.objectId" -o tsv 2>$null
if ($kubeletObjectId) {
    $acrRoleAssigned = az role assignment list --assignee $kubeletObjectId --scope $acrId --role "AcrPull" --query "[0].id" -o tsv 2>$null
    if ($acrRoleAssigned) {
        Write-Info "AKS kubelet identity already has 'AcrPull' role on ACR '$AcrName'."
    } else {
        Write-Info "Attaching ACR '$AcrName' to AKS cluster '$ClusterName'..."
        az aks update --name $ClusterName --resource-group $ResourceGroupName --attach-acr $AcrName --output none
        Write-Success "AKS cluster '$ClusterName' attached to ACR '$AcrName'."
    }
}

# ------------------------------------------------------------------------------
# 8. Azure RBAC Role Assignments (Idempotent)
# ------------------------------------------------------------------------------
Write-Step "Step 8: Configuring Azure RBAC Role Assignments..."

# Role 1: Key Vault Secrets User
$kvRoleAssigned = az role assignment list --assignee $managedIdentityPrincipalId --scope $keyVaultId --role "Key Vault Secrets User" --query "[0].id" -o tsv 2>$null
if ($kvRoleAssigned) {
    Write-Info "Role 'Key Vault Secrets User' is already assigned to Managed Identity on Key Vault."
} else {
    Write-Info "Assigning 'Key Vault Secrets User' role to Managed Identity on Key Vault..."
    az role assignment create `
        --role "Key Vault Secrets User" `
        --assignee-object-id $managedIdentityPrincipalId `
        --assignee-principal-type "ServicePrincipal" `
        --scope $keyVaultId `
        --output none
    Write-Success "Role 'Key Vault Secrets User' assigned."
}

# Role 2: Azure Service Bus Data Receiver
$sbRoleAssigned = az role assignment list --assignee $managedIdentityPrincipalId --scope $serviceBusId --role "Azure Service Bus Data Receiver" --query "[0].id" -o tsv 2>$null
if ($sbRoleAssigned) {
    Write-Info "Role 'Azure Service Bus Data Receiver' is already assigned to Managed Identity on Service Bus."
} else {
    Write-Info "Assigning 'Azure Service Bus Data Receiver' role to Managed Identity on Service Bus..."
    az role assignment create `
        --role "Azure Service Bus Data Receiver" `
        --assignee-object-id $managedIdentityPrincipalId `
        --assignee-principal-type "ServicePrincipal" `
        --scope $serviceBusId `
        --output none
    Write-Success "Role 'Azure Service Bus Data Receiver' assigned."
}

# Role 3: Assign 'Key Vault Secrets Officer' to Current User (Convenience for setting demo secrets)
$currentUserId = az ad signed-in-user show --query id -o tsv 2>$null
if ($currentUserId) {
    $userKvRoleAssigned = az role assignment list --assignee $currentUserId --scope $keyVaultId --role "Key Vault Secrets Officer" --query "[0].id" -o tsv 2>$null
    if (-not $userKvRoleAssigned) {
        Write-Info "Assigning 'Key Vault Secrets Officer' role to current user for secret seeding..."
        az role assignment create `
            --role "Key Vault Secrets Officer" `
            --assignee-object-id $currentUserId `
            --assignee-principal-type "User" `
            --scope $keyVaultId `
            --output none 2>$null
        Write-Success "Role 'Key Vault Secrets Officer' assigned to current user."
    }
}

# ------------------------------------------------------------------------------
# 9. Workload Identity Federated Credential
# ------------------------------------------------------------------------------
Write-Step "Step 9: Configuring Workload Identity Federated Credential..."

$federatedCredentialName = "dso-federated-credential"
$fedCredId = az identity federated-credential show --name $federatedCredentialName --identity-name $IdentityName --resource-group $ResourceGroupName --query id -o tsv 2>$null
if ($fedCredId) {
    Write-Info "Federated credential '$federatedCredentialName' already exists."
} else {
    Write-Info "Creating federated credential linking AKS ServiceAccount '${ServiceAccountNamespace}:${ServiceAccountName}'..."
    az identity federated-credential create `
        --name $federatedCredentialName `
        --identity-name $IdentityName `
        --resource-group $ResourceGroupName `
        --issuer $aksOidcIssuerUrl `
        --subject "system:serviceaccount:${ServiceAccountNamespace}:${ServiceAccountName}" `
        --audience "api://AzureADTokenExchange" `
        --output none
    Write-Success "Federated credential '$federatedCredentialName' created."
}

# ------------------------------------------------------------------------------
# 10. Get AKS Credentials (Kubeconfig)
# ------------------------------------------------------------------------------
Write-Step "Step 10: Merging AKS Cluster Kubeconfig credentials..."
az aks get-credentials --resource-group $ResourceGroupName --name $ClusterName --overwrite-existing
Write-Success "Kubeconfig updated for cluster '$ClusterName'."

# ------------------------------------------------------------------------------
# 11. Summary & Deployment Configuration
# ------------------------------------------------------------------------------
Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "🎉 ALL AZURE INFRASTRUCTURE PROVISIONED SUCCESSFULLY!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 Resource Summary:
------------------------------------------------------------------
Resource Group:             $ResourceGroupName
Location:                   $Location
AKS Cluster (Free Tier):    $ClusterName
Azure Container Registry:   $AcrName (Basic Tier - Lowest Cost)
  Login Server:             $acrLoginServer
Managed Identity:           $IdentityName
  Client ID:                $managedIdentityClientId
  Principal ID:             $managedIdentityPrincipalId
Key Vault (Standard):       $KeyVaultName
  Vault URI:                $keyVaultUri
Service Bus (Basic):        $ServiceBusNamespace
  Namespace FQDN:           $serviceBusFqdn
  Queue Name:               $ServiceBusQueueName
Event Grid Topic:           $EventGridTopicName
OIDC Issuer URL:            $aksOidcIssuerUrl
Federated ServiceAccount:   system:serviceaccount:${ServiceAccountNamespace}:${ServiceAccountName}

⚙️ Local .env Configuration Snippet:
------------------------------------------------------------------
export AZURE_TENANT_ID="$tenantId"
export AZURE_CLIENT_ID="$managedIdentityClientId"
export SERVICEBUS_NAMESPACE="$serviceBusFqdn"
export SERVICEBUS_QUEUE_NAME="$ServiceBusQueueName"
export KEYVAULT_URI="$keyVaultUri"
export ARGOCD_AUTOPATCH_ENABLED="true"

🐳 Build & Push Operator Image to ACR:
------------------------------------------------------------------
az acr login --name $AcrName
docker tag dynamic-secret-operator:local "$acrLoginServer/dynamic-secret-operator:0.2.1"
docker push "$acrLoginServer/dynamic-secret-operator:0.2.1"

📦 Helm Installation Options:
------------------------------------------------------------------

👉 OPTION 1: Native Azure Event-Driven Mode (Azure Key Vault + Service Bus)
   Ingests real-time rotation events via Azure Service Bus using Workload Identity.

   PowerShell:
   helm install dso ./deploy/helm/dso ``
     --namespace $ServiceAccountNamespace ``
     --create-namespace ``
     --set mode=event-driven ``
     --set provider=azure ``
     --set image.repository="$acrLoginServer/dynamic-secret-operator" ``
     --set image.tag="0.2.1" ``
     --set azure.workloadIdentity.enabled=true ``
     --set azure.workloadIdentity.clientId="$managedIdentityClientId" ``
     --set azure.workloadIdentity.tenantId="$tenantId" ``
     --set azure.serviceBus.namespace="$serviceBusFqdn" ``
     --set azure.serviceBus.queueName="$ServiceBusQueueName" ``
     --wait

   Bash:
   helm install dso ./deploy/helm/dso \
     --namespace $ServiceAccountNamespace \
     --create-namespace \
     --set mode=event-driven \
     --set provider=azure \
     --set azure.workloadIdentity.enabled=true \
     --set azure.workloadIdentity.clientId="$managedIdentityClientId" \
     --set azure.workloadIdentity.tenantId="$tenantId" \
     --set azure.serviceBus.namespace="$serviceBusFqdn" \
     --set azure.serviceBus.queueName="$ServiceBusQueueName" \
     --wait

------------------------------------------------------------------

👉 OPTION 2: Universal Multi-Cloud Mode via External Secrets Operator (ESO)
   Decoupled architecture: ESO handles vault synchronization, DSO handles canary rollouts.
   Zero cloud IAM credentials needed inside DSO!

   Step 2.1: Install External Secrets Operator (ESO):
   helm repo add external-secrets https://charts.external-secrets.io
   helm repo update
   helm install external-secrets external-secrets/external-secrets \
     --namespace external-secrets \
     --create-namespace \
     --set installCRDs=true \
     --wait

   Step 2.2: Install Dynamic Secret Operator (DSO) in ESO Mode:
   PowerShell:
   helm install dso ./deploy/helm/dso ``
     --namespace $ServiceAccountNamespace ``
     --create-namespace ``
     --set mode=eso ``
     --set image.repository="$acrLoginServer/dynamic-secret-operator" ``
     --set image.tag="0.2.1" ``
     --wait

   Bash:
   helm install dso ./deploy/helm/dso \
     --namespace $ServiceAccountNamespace \
     --create-namespace \
     --set mode=eso \
     --set image.repository="$acrLoginServer/dynamic-secret-operator" \
     --set image.tag="0.2.1" \
     --wait

   Note: In ESO mode, label your synced ExternalSecret target with:
         dso.quantumsys.dev/managed: "watch"

------------------------------------------------------------------
🔍 Verification & Diagnostics:
------------------------------------------------------------------
kubectl get pods -n $ServiceAccountNamespace
kubectl logs -n $ServiceAccountNamespace -l app.kubernetes.io/name=dynamic-secret-operator -f

==================================================================
"@ -ForegroundColor Cyan
