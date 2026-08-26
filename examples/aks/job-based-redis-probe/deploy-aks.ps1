# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Job-Based Redis Probe Example on AKS
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [Alias("k")]
    [string]$KeyVaultName,

    [Parameter(Mandatory = $false)]
    [string]$Namespace = "production"
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

Write-Step "Deploying Job-Based Redis Probe Example to AKS Cluster..."
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

# 3. Ensure target namespace exists
Write-Step "Ensuring namespace '$Namespace' exists..."
kubectl create namespace $Namespace --dry-run=client -o yaml | kubectl apply -f - | Out-Null
Write-Success "Namespace '$Namespace' ready."

# 4. Seed initial Redis AUTH password in Azure Key Vault
Write-Step "Checking secret 'redis-auth-password' in Azure Key Vault '$KeyVaultName'..."
$secretCheck = az keyvault secret show --vault-name $KeyVaultName --name "redis-auth-password" 2>$null
if (-not $secretCheck) {
    Write-Info "Secret 'redis-auth-password' not found. Creating initial secret in Key Vault..."
    az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "redis-auth-password" `
        --value "InitialRedisPassword123!" `
        --output none
    Write-Success "Initial secret 'redis-auth-password' seeded in Key Vault."
} else {
    Write-Info "Secret 'redis-auth-password' already exists in Key Vault."
}

# 5. Create bootstrap secrets in the cluster so pods can start before DSO materializes the first revision.
Write-Step "Creating bootstrap secrets in namespace '$Namespace'..."

kubectl create secret generic redis-master-redis-auth-password-initial `
    --namespace $Namespace `
    --from-literal=redis-auth-password="InitialRedisPassword123!" `
    --dry-run=client -o yaml | kubectl apply -f - | Out-Null

kubectl create secret generic redis-consumer-redis-auth-password-initial `
    --namespace $Namespace `
    --from-literal=redis-auth-password="InitialRedisPassword123!" `
    --dry-run=client -o yaml | kubectl apply -f - | Out-Null

Write-Success "Bootstrap secrets created."

# 6. Apply DynamicSecretPolicy CRD (if repo root is available)
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd"))) {
    Write-Step "Applying DynamicSecretPolicy CRD from repo..."
    kubectl apply -k (Join-Path $RepoRoot "config/crd") | Out-Null
    Write-Success "CRD applied."
}

# 7. Apply manifests with Key Vault substitution
Write-Step "Deploying Redis workloads and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $KeyVaultName
$manifestContent = $manifestContent -replace '\$\{NAMESPACE\}', $Namespace

$manifestContent | kubectl apply -n $Namespace -f -

# 8. Wait for workloads to be ready
Write-Info "Waiting for Redis master to become ready..."
kubectl rollout status deployment/redis-master -n $Namespace --timeout=120s

Write-Info "Waiting for redis-consumer to become ready..."
kubectl rollout status deployment/redis-consumer -n $Namespace --timeout=120s

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Job-Based Redis Probe Example deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

Next Steps:
1. Tail the redis-consumer logs to observe live PING results:
   kubectl logs -n $Namespace -l app=redis-consumer -f

2. Watch the DynamicSecretPolicy conditions:
   kubectl get dynamicsecretpolicy redis-cache-rotation -n $Namespace -w

3. Trigger a Redis AUTH password rotation in Azure Key Vault:
   az keyvault secret set --vault-name $KeyVaultName --name "redis-auth-password" --value "RotatedRedisPassword456!"

4. Observe DSO:
   - Launch an ephemeral probe Job (kubectl get jobs -n $Namespace -w)
   - Validate with redis-cli PING against the new password
   - Promote redis-consumer with zero downtime if PING succeeds
   - Auto-delete the probe Job
==================================================================
"@ -ForegroundColor Cyan
