# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Fullstack DB Rotation Example on AKS
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [Alias("k")]
    [string]$KeyVaultName
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

Write-Step "Deploying Fullstack DB Rotation Example to AKS Cluster..."
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
Write-Step "Checking secret 'db-password' in Azure Key Vault '$KeyVaultName'..."
$secretCheck = az keyvault secret show --vault-name $KeyVaultName --name "db-password" 2>$null
if (-not $secretCheck) {
    Write-Info "Secret 'db-password' not found. Creating initial secret in Key Vault..."
    az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "db-password" `
        --value "InitialSecretPassword123!" `
        --output none
    Write-Success "Initial secret 'db-password' seeded in Key Vault."
} else {
    Write-Info "Secret 'db-password' already exists in Key Vault."
}

# 4. Create bootstrap secret in cluster for PostgreSQL initial startup
Write-Info "Creating bootstrap secret in cluster for PostgreSQL initialization..."
kubectl create secret generic db-status-app-db-password-initial `
    --from-literal=db-password="InitialSecretPassword123!" `
    --dry-run=client -o yaml | kubectl apply -f - | Out-Null
Write-Success "Bootstrap secret created."

# 5. Apply manifests with Key Vault replacement
Write-Step "Deploying PostgreSQL, Web Dashboard, and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $KeyVaultName

$manifestContent | kubectl apply -f -

# 6. Wait for deployments to be ready
Write-Info "Waiting for PostgreSQL deployment to become ready..."
kubectl rollout status deployment/postgres --timeout=120s

Write-Info "Waiting for Web Dashboard deployment to become ready..."
kubectl rollout status deployment/db-status-app --timeout=120s

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Fullstack DB Rotation PoC deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

Next Steps:
1. Port-forward the dashboard:
   kubectl port-forward svc/db-status-app 8080:80

2. Open http://localhost:8080 in your browser.

3. Trigger a live rotation in Azure Key Vault:
   az keyvault secret set --vault-name $KeyVaultName --name "db-password" --value "NewSecret2026_Rotated!"

4. Observe the live audit log on the web dashboard update with zero downtime!
==================================================================
"@ -ForegroundColor Cyan
