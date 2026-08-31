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

# 3. Check Key Vault accessibility
Write-Step "Verifying access to Azure Key Vault '$KeyVaultName'..."
$kvCheck = az keyvault show --name $KeyVaultName 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Unable to access Key Vault '$KeyVaultName'. Please verify the name and your Azure permissions.`nDetails: $kvCheck"
}
Write-Success "Key Vault '$KeyVaultName' verified."

# 4. Seed initial secret in Azure Key Vault if not exists
Write-Step "Checking secret 'payment-db-password' in Azure Key Vault '$KeyVaultName'..."
$secretCheck = az keyvault secret show --vault-name $KeyVaultName --name "payment-db-password" 2>$null
if (-not $secretCheck) {
    Write-Info "Creating initial secret 'payment-db-password' in Key Vault..."
    $setOut = az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "payment-db-password" `
        --value "initial-database-password-v1" `
        --output none 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create secret 'payment-db-password' in Key Vault '$KeyVaultName'.`nDetails: $setOut"
    }
    Write-Success "Initial secret 'payment-db-password' seeded in Key Vault."
} else {
    Write-Info "Secret 'payment-db-password' already exists in Key Vault."
}

# 5. Install Argo Rollouts controller if not present
$nsCheck = kubectl get namespace argo-rollouts 2>$null
if (-not $nsCheck) {
    Write-Info "Installing Argo Rollouts ($ArgoRolloutsVersion) on AKS..."
    $createNs = kubectl create namespace argo-rollouts --dry-run=client -o yaml | kubectl apply -f - 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create namespace 'argo-rollouts'.`nDetails: $createNs"
    }
    $installOut = kubectl apply -n argo-rollouts -f "https://github.com/argoproj/argo-rollouts/releases/download/${ArgoRolloutsVersion}/install.yaml" 2>&1
    Write-Host $installOut
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to install Argo Rollouts controller.`nDetails: $installOut"
    }
    Write-Info "Waiting for Argo Rollouts controller to become ready..."
    $rolloutOut = kubectl rollout status deployment/argo-rollouts -n argo-rollouts --timeout=120s 2>&1
    Write-Host $rolloutOut
    if ($LASTEXITCODE -ne 0) {
        throw "Argo Rollouts controller rollout failed or timed out.`nDetails: $rolloutOut"
    }
}

# 6. Apply manifests with Key Vault replacement
Write-Step "Deploying Rollout, Services, and DynamicSecretPolicy manifests..."
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
