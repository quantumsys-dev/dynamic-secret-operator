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

# 3. Check Key Vault accessibility
Write-Step "Verifying access to Azure Key Vault '$KeyVaultName'..."
$kvCheck = az keyvault show --name $KeyVaultName 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Unable to access Key Vault '$KeyVaultName'. Please verify the name and your Azure permissions.`nDetails: $kvCheck"
}
Write-Success "Key Vault '$KeyVaultName' verified."

# 4. Seed initial secret in Azure Key Vault if not exists
Write-Step "Checking secret 'nginx-bg-color' in Azure Key Vault '$KeyVaultName'..."
$secretCheck = az keyvault secret show --vault-name $KeyVaultName --name "nginx-bg-color" 2>$null
if (-not $secretCheck) {
    Write-Info "Creating initial secret 'nginx-bg-color' in Key Vault..."
    $setOut = az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "nginx-bg-color" `
        --value "#3b82f6" `
        --output none 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create secret 'nginx-bg-color' in Key Vault '$KeyVaultName'.`nDetails: $setOut"
    }
    Write-Success "Initial secret 'nginx-bg-color' seeded in Key Vault."
} else {
    Write-Info "Secret 'nginx-bg-color' already exists in Key Vault."
}

# 5. Apply DynamicSecretPolicy CRD (if repo root is available)
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd/bases"))) {
    Write-Step "Applying DynamicSecretPolicy CRD from repo..."
    $crdOut = kubectl apply --server-side --force-conflicts -f (Join-Path $RepoRoot "config/crd/bases") 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Failed to apply CRD.`nDetails: $crdOut" }
    Write-Success "CRD applied."
}

# 6. Apply manifests with Key Vault replacement
Write-Step "Deploying Nginx Color App and DynamicSecretPolicy manifests..."
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

# 6. Wait for deployment to be ready
Write-Info "Waiting for Nginx Color App deployment to become ready..."
$rolloutOut = kubectl rollout status deployment/nginx-color-app --timeout=120s 2>&1
Write-Host $rolloutOut
if ($LASTEXITCODE -ne 0) {
    throw "Deployment rollout failed or timed out.`nDetails: $rolloutOut"
}

# 7. Check and display Public LoadBalancer Service IP
Write-Step "Checking Public LoadBalancer IP for nginx-color-app..."
$svcJson = kubectl get svc nginx-color-app -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

if (-not $extIp) {
    Write-Info "LoadBalancer Public IP is still being provisioned by Azure (status: <pending>)."
    Write-Info "Run 'kubectl get svc nginx-color-app -w' to view the public IP as soon as Azure assigns it."
} else {
    Write-Success "Public IP assigned: http://$extIp"
}

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Nginx Color Rotation Example deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 STEP-BY-STEP VERIFICATION GUIDE:
------------------------------------------------------------------

1️⃣ Access the Nginx Web App:
   - Public URL (LoadBalancer):
     kubectl get svc nginx-color-app
     (Open http://<EXTERNAL-IP> in your browser)

   - Fallback (Port-Forward):
     kubectl port-forward svc/nginx-color-app 8080:80
     (Open http://localhost:8080)

2️⃣ Monitor DSO and Workload in Real Time (in a separate terminal):
   - Watch DSO State Machine & Conditions:
     kubectl get dynamicsecretpolicy aks-nginx-color-policy -w

   - Watch Pod Rollout & Canary Lifecycle:
     kubectl get pods -l app=nginx-color-app -w

   - Stream Operator Logs:
     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f

3️⃣ Trigger a Secret Rotation in Azure Key Vault:
   az keyvault secret set --vault-name $KeyVaultName --name "nginx-bg-color" --value "#10b981"

4️⃣ Observe Zero-Downtime Promotion:
   - Key Vault publishes SecretNewVersionCreated event to Azure Service Bus.
   - DSO triggers Canary Provisioning, runs synthetic HTTP /health probe.
   - Target Deployment 'nginx-color-app' is promoted to the new color with zero downtime!
   - Refresh your browser to see the background change from Blue (#3b82f6) to Green (#10b981)!

5️⃣ Test Circuit Breaker & Auto-Rollback (Optional):
   - Inject an invalid value that fails health checks:
     az keyvault secret set --vault-name $KeyVaultName --name "nginx-bg-color" --value "INVALID_COLOR"
   - Watch DSO Canary fail synthetic probes and automatically abort rollout without affecting live traffic!
==================================================================
"@ -ForegroundColor Cyan

