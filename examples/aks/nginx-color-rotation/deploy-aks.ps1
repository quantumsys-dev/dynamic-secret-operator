# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Nginx Color Rotation Example on AKS
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

Write-Step "Deploying Nginx Color Rotation Example to AKS Cluster..."
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
Write-Step "Checking secret 'nginx-bg-color' in Azure Key Vault '$KeyVaultName'..."
$secretCheck = az keyvault secret show --vault-name $KeyVaultName --name "nginx-bg-color" 2>$null
if (-not $secretCheck) {
    Write-Info "Creating initial secret 'nginx-bg-color' in Key Vault..."
    az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "nginx-bg-color" `
        --value "#3b82f6" `
        --output none
    Write-Success "Initial secret 'nginx-bg-color' seeded in Key Vault."
} else {
    Write-Info "Secret 'nginx-bg-color' already exists in Key Vault."
}

# 4. Apply manifests with Key Vault replacement
Write-Step "Deploying Nginx Color App and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $KeyVaultName

$manifestContent | kubectl apply -f -

# 5. Wait for deployment to be ready
Write-Info "Waiting for Nginx Color App deployment to become ready..."
kubectl rollout status deployment/nginx-color-app --timeout=120s

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Nginx Color Rotation Example deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

Next Steps:
1. Port-forward the app:
   kubectl port-forward svc/nginx-color-app 8080:80

2. Open http://localhost:8080 in your browser.

3. Trigger a rotation in Azure Key Vault:
   az keyvault secret set --vault-name $KeyVaultName --name "nginx-bg-color" --value "#10b981"

4. Watch DSO promote the workload with zero downtime!
==================================================================
"@ -ForegroundColor Cyan
