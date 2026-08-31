# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Multi-Secret Rotation Demo on AKS
# ==============================================================================
# This script provisions and seeds all 3 secrets in Azure Key Vault:
# 1. db-password (PostgreSQL)
# 2. redis-auth-token (Redis)
# 3. payment-api-key (Payment API Gateway)
#
# Then deploys the Multi-Secret Microservice application and 3 DynamicSecretPolicy
# resources with dedicated validation probes for each secret.
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [Alias("k")]
    [string]$KeyVaultName,

    [Parameter(Mandatory = $false)]
    [string]$Namespace = "multi-secret-demo"
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

Write-Step "Deploying Multi-Secret Microservice Example to AKS Cluster..."
Write-Info "Target Key Vault : $KeyVaultName"
Write-Info "Target Namespace : $Namespace"

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

# 3. Check Key Vault accessibility
Write-Step "Verifying access to Azure Key Vault '$KeyVaultName'..."
$kvCheck = az keyvault show --name $KeyVaultName 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Unable to access Key Vault '$KeyVaultName'. Please verify the name and your Azure permissions.`nDetails: $kvCheck"
}
Write-Success "Key Vault '$KeyVaultName' verified."

# 4. Ensure target namespace exists
Write-Step "Ensuring namespace '$Namespace' exists..."
$nsOut = kubectl create namespace $Namespace --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Failed to ensure namespace '$Namespace'.`nDetails: $nsOut"
}
Write-Success "Namespace '$Namespace' ready."

# 5. Seed all 3 secrets in Azure Key Vault
$secrets = @(
    @{ Name = "db-password"; Value = "InitialPsqlPass123!" },
    @{ Name = "redis-auth-token"; Value = "InitialRedisToken456!" },
    @{ Name = "payment-api-key"; Value = "sk_live_pay_9876543210" }
)

Write-Step "Checking & seeding initial secrets in Azure Key Vault '$KeyVaultName'..."
foreach ($sec in $secrets) {
    $secCheck = az keyvault secret show --vault-name $KeyVaultName --name $sec.Name 2>$null
    if (-not $secCheck) {
        Write-Info "Secret '$($sec.Name)' not found. Creating in Key Vault..."
        $setOut = az keyvault secret set `
            --vault-name $KeyVaultName `
            --name $sec.Name `
            --value $sec.Value `
            --output none 2>&1
        if ($LASTEXITCODE -ne 0) {
            throw "Failed to create secret '$($sec.Name)' in Key Vault '$KeyVaultName'.`nDetails: $setOut"
        }
        Write-Success "Secret '$($sec.Name)' created in Key Vault."
    } else {
        Write-Info "Secret '$($sec.Name)' already exists in Key Vault."
    }
}

# 6. Create initial bootstrap secrets in Kubernetes
Write-Step "Creating bootstrap secrets in namespace '$Namespace'..."
$b1 = kubectl create secret generic multi-secret-app-db-password-initial `
    --namespace $Namespace `
    --from-literal=db-password="InitialPsqlPass123!" `
    --from-literal=db-user="postgres" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create db-password bootstrap secret.`nDetails: $b1" }

$b2 = kubectl create secret generic multi-secret-app-redis-auth-token-initial `
    --namespace $Namespace `
    --from-literal=redis-auth-token="InitialRedisToken456!" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create redis-auth-token bootstrap secret.`nDetails: $b2" }

$b3 = kubectl create secret generic multi-secret-app-payment-api-key-initial `
    --namespace $Namespace `
    --from-literal=payment-api-key="sk_live_pay_9876543210" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create payment-api-key bootstrap secret.`nDetails: $b3" }

Write-Success "Bootstrap Kubernetes secrets created."

# 7. Apply DynamicSecretPolicy CRD (if repo root is available)
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd/bases"))) {
    Write-Step "Applying DynamicSecretPolicy CRD from repo..."
    $crdOut = kubectl apply --server-side --force-conflicts -f (Join-Path $RepoRoot "config/crd/bases") 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Failed to apply CRD.`nDetails: $crdOut" }
    Write-Success "CRD applied."
}

# 8. Apply manifests with Key Vault name substitution
Write-Step "Deploying Multi-Secret workloads and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
if (-not (Test-Path $manifestPath)) {
    throw "Manifest file not found: $manifestPath"
}
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $KeyVaultName

$applyOut = $manifestContent | kubectl apply -f - 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}
Write-Success "All manifests deployed successfully!"

# 9. Wait for pods to roll out
Write-Step "Waiting for deployments in '$Namespace' to be ready..."
$r1 = kubectl rollout status deployment/postgres -n $Namespace --timeout=120s 2>&1
Write-Host $r1
if ($LASTEXITCODE -ne 0) { throw "Postgres rollout failed or timed out.`nDetails: $r1" }

$r2 = kubectl rollout status deployment/redis -n $Namespace --timeout=120s 2>&1
Write-Host $r2
if ($LASTEXITCODE -ne 0) { throw "Redis rollout failed or timed out.`nDetails: $r2" }

$r3 = kubectl rollout status deployment/payment-gateway -n $Namespace --timeout=120s 2>&1
Write-Host $r3
if ($LASTEXITCODE -ne 0) { throw "Payment Gateway rollout failed or timed out.`nDetails: $r3" }

$r4 = kubectl rollout status deployment/multi-secret-app -n $Namespace --timeout=180s 2>&1
Write-Host $r4
if ($LASTEXITCODE -ne 0) { throw "Multi-Secret App rollout failed or timed out.`nDetails: $r4" }

# 10. Check and display Public LoadBalancer Service IP
Write-Step "Checking Public LoadBalancer IP for multi-secret-app..."
$svcJson = kubectl get svc multi-secret-app -n $Namespace -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

if (-not $extIp) {
    Write-Info "LoadBalancer Public IP is still being provisioned by Azure (status: <pending>)."
    Write-Info "Run 'kubectl get svc multi-secret-app -n $Namespace -w' to view the public IP as soon as Azure assigns it."
} else {
    Write-Success "Public IP assigned: http://$extIp"
}

Write-Host @"

==================================================================
🎉 MULTI-SECRET MICROSERVICE DEPLOYED SUCCESSFULLY!
==================================================================

📋 STEP-BY-STEP VERIFICATION GUIDE:
------------------------------------------------------------------

1️⃣ Access the Multi-Secret Web Dashboard:
   - Public URL (LoadBalancer):
     kubectl get svc multi-secret-app -n $Namespace
     (Open http://<EXTERNAL-IP> in your browser)

   - Fallback (Port-Forward):
     kubectl port-forward svc/multi-secret-app 8080:80 -n $Namespace
     (Open http://localhost:8080)

2️⃣ Monitor 3 Independent Secret Policies in Real Time:
   - Watch All DynamicSecretPolicies:
     kubectl get dynamicsecretpolicies -n $Namespace -w

   - Stream Operator Logs:
     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f

3️⃣ Test Independent Secret Rotations:

   🔹 1. Rotate PostgreSQL Database Password:
      a) Update Postgres user password in cluster:
         kubectl exec deployment/postgres -n $Namespace -- psql -U appuser -d production_db -c "ALTER USER appuser WITH PASSWORD 'NewRotatedPsqlPass999!';"
      b) Update secret in Azure Key Vault:
         az keyvault secret set --vault-name "$KeyVaultName" --name "db-password" --value "NewRotatedPsqlPass999!"
      -> DSO launches Canary and validates native PostgreSQL probe.

   🔹 2. Rotate Redis Auth Token:
      a) Update Redis password in cluster:
         kubectl exec deployment/redis -n $Namespace -- redis-cli -a initialRedisToken123 CONFIG SET requirepass "NewRotatedRedisToken888!"
      b) Update secret in Azure Key Vault:
         az keyvault secret set --vault-name "$KeyVaultName" --name "redis-auth-token" --value "NewRotatedRedisToken888!"
      -> DSO launches Canary and validates Redis connection probe.

   🔹 3. Rotate Payment API Gateway Key:
      az keyvault secret set --vault-name "$KeyVaultName" --name "payment-api-key" --value "sk_live_pay_new_777777"
      -> DSO launches Canary and validates Payment API probe.

4️⃣ Verify Granular Zero-Downtime Rollout:
   - Refresh the web dashboard: each subsystem indicator turns green independently as its secret rotates!
==================================================================
"@ -ForegroundColor Green
