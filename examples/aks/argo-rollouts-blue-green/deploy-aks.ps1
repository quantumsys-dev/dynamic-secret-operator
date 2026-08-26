# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Argo Rollouts Blue/Green Example on AKS
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [Alias("k")]
    [string]$KeyVaultName
)

$ErrorActionPreference = "Stop"
$ArgoRolloutsVersion = "v1.7.2"

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

Write-Step "Deploying Argo Rollouts Blue/Green Example to AKS Cluster..."
Write-Info "Target Key Vault: $KeyVaultName"

# 1. Check prerequisites
if (-not (Get-Command kubectl -ErrorAction SilentlyContinue)) {
    Write-Error "'kubectl' is required. Please install kubectl: https://kubernetes.io/docs/tasks/tools/"
}

if (-not (Get-Command az -ErrorAction SilentlyContinue)) {
    Write-Error "Azure CLI ('az') is required. Please install az: https://learn.microsoft.com/en-us/cli/azure/install-azure-cli"
}

# 2. Check cluster connection
$currentContext = kubectl config current-context 2>$null
if (-not $currentContext) {
    Write-Error "Not connected to any Kubernetes cluster. Please run 'az aks get-credentials' first."
}
Write-Success "Using Kubernetes Context: $currentContext"

# 3. Seed initial secret in Azure Key Vault if not exists
Write-Step "Checking secret 'payment-db-password' in Azure Key Vault '$KeyVaultName'..."
$secretCheck = az keyvault secret show --vault-name $KeyVaultName --name "payment-db-password" 2>$null
if (-not $secretCheck) {
    Write-Info "Creating initial secret 'payment-db-password' in Key Vault..."
    az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "payment-db-password" `
        --value "initial-database-password-v1" `
        --output none
    Write-Success "Initial secret 'payment-db-password' seeded in Key Vault."
} else {
    Write-Info "Secret 'payment-db-password' already exists in Key Vault."
}

# 4. Install Argo Rollouts controller if not present
$nsCheck = kubectl get namespace argo-rollouts 2>$null
if (-not $nsCheck) {
    Write-Info "Installing Argo Rollouts ($ArgoRolloutsVersion) on AKS..."
    kubectl create namespace argo-rollouts --dry-run=client -o yaml | kubectl apply -f -
    kubectl apply -n argo-rollouts -f "https://github.com/argoproj/argo-rollouts/releases/download/${ArgoRolloutsVersion}/install.yaml"
    Write-Info "Waiting for Argo Rollouts controller to become ready..."
    kubectl rollout status deployment/argo-rollouts -n argo-rollouts --timeout=120s
}

# 5. Apply manifests with Key Vault replacement
Write-Step "Deploying Rollout, Services, and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $KeyVaultName

$manifestContent | kubectl apply -f -

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Argo Rollouts Blue/Green Example deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

Next Steps:
1. Watch the rollout:
   kubectl argo rollouts get rollout rollout-payment-service --watch

2. Trigger a secret rotation in Azure Key Vault:
   az keyvault secret set --vault-name $KeyVaultName --name "payment-db-password" --value "NewPaymentPassword2026_Rotated!"

3. Observe the green preview ReplicaSet provision, validate, and cut over live traffic!
==================================================================
"@ -ForegroundColor Cyan
