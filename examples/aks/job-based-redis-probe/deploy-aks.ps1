# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Job-Based Redis Probe Example on AKS
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [Alias("k")]
    [string]$KeyVaultName,

    [Parameter(Mandatory = $false)]
    [string]$Namespace = "dso-examples"
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

# 5. Seed initial Redis AUTH password in Azure Key Vault
Write-Step "Checking secret 'redis-auth-password' in Azure Key Vault '$KeyVaultName'..."
$secretCheck = az keyvault secret show --vault-name $KeyVaultName --name "redis-auth-password" 2>$null
if (-not $secretCheck) {
    Write-Info "Secret 'redis-auth-password' not found. Creating initial secret in Key Vault..."
    $setOut = az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "redis-auth-password" `
        --value "InitialRedisPassword123!" `
        --output none 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create secret 'redis-auth-password' in Key Vault '$KeyVaultName'.`nDetails: $setOut"
    }
    Write-Success "Initial secret 'redis-auth-password' seeded in Key Vault."
} else {
    Write-Info "Secret 'redis-auth-password' already exists in Key Vault."
}

# 6. Create bootstrap secrets in the cluster so pods can start before DSO materializes the first revision.
Write-Step "Creating bootstrap secrets in namespace '$Namespace'..."

$b1 = kubectl create secret generic redis-master-redis-auth-password-initial `
    --namespace $Namespace `
    --from-literal=redis-auth-password="InitialRedisPassword123!" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create redis-master bootstrap secret.`nDetails: $b1" }

$b2 = kubectl create secret generic redis-consumer-redis-auth-password-initial `
    --namespace $Namespace `
    --from-literal=redis-auth-password="InitialRedisPassword123!" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create redis-consumer bootstrap secret.`nDetails: $b2" }

Write-Success "Bootstrap secrets created."

# 7. Apply DynamicSecretPolicy CRD (if repo root is available)
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd/bases"))) {
    Write-Step "Applying DynamicSecretPolicy CRD from repo..."
    $crdOut = kubectl apply --server-side --force-conflicts -f (Join-Path $RepoRoot "config/crd/bases") 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Failed to apply CRD.`nDetails: $crdOut" }
    Write-Success "CRD applied."
}

# 8. Apply manifests with Key Vault substitution
Write-Step "Deploying Redis workloads and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
if (-not (Test-Path $manifestPath)) {
    throw "Manifest file not found: $manifestPath"
}
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $KeyVaultName
$manifestContent = $manifestContent -replace '\$\{NAMESPACE\}', $Namespace

$applyOut = $manifestContent | kubectl apply -n $Namespace -f - 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}

# 9. Wait for workloads to be ready
Write-Info "Waiting for Redis master to become ready..."
$r1 = kubectl rollout status deployment/redis-master -n $Namespace --timeout=120s 2>&1
Write-Host $r1
if ($LASTEXITCODE -ne 0) { throw "Redis master rollout failed or timed out.`nDetails: $r1" }

Write-Info "Waiting for redis-consumer to become ready..."
$r2 = kubectl rollout status deployment/redis-consumer -n $Namespace --timeout=120s 2>&1
Write-Host $r2
if ($LASTEXITCODE -ne 0) { throw "Redis consumer rollout failed or timed out.`nDetails: $r2" }

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Job-Based Redis Probe Example deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 STEP-BY-STEP VERIFICATION GUIDE:
------------------------------------------------------------------

1️⃣ Tail Redis Consumer Logs (in a dedicated terminal):
   kubectl logs -n $Namespace -l app=redis-consumer -f
   (Observe continuous PING/PONG heartbeats with current AUTH password)

2️⃣ Monitor DSO Policy & Ephemeral Validation Jobs (in separate terminals):
   - Watch Ephemeral Probe Job Lifecycle:
     kubectl get jobs -n $Namespace -w

   - Watch DynamicSecretPolicy State Machine:
     kubectl get dynamicsecretpolicy redis-cache-rotation -n $Namespace -w

   - Stream Operator Logs:
     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f

3️⃣ Execute Redis AUTH Password Rotation:
   🔹 Step 3.1: Retrieve the current active password from Key Vault (or inspect secret):
      `$CurrentPass = (az keyvault secret show --vault-name $KeyVaultName --name "redis-auth-password" --query value -o tsv)`

   🔹 Step 3.2: Update the password inside running Redis Master:
      kubectl exec deployment/redis-master -n $Namespace -- redis-cli -a `$CurrentPass CONFIG SET requirepass "RotatedRedisPassword456!"

   🔹 Step 3.3: Update secret in Azure Key Vault:
      az keyvault secret set --vault-name $KeyVaultName --name "redis-auth-password" --value "RotatedRedisPassword456!"

4️⃣ Observe Job-Based Validation & Zero-Downtime Promotion:
   - Key Vault emits SecretNewVersionCreated event to Azure Service Bus.
   - DSO launches an ephemeral Kubernetes Job with 'redis:alpine' image.
   - The Job executes 'redis-cli -a <new-password> ping' against the master.
   - Upon successful PING validation (exit 0), DSO promotes 'redis-consumer'.
   - The ephemeral validation Job is automatically cleaned up.
   - Consumer logs reflect continuous zero-downtime PONG responses with no dropped connections!
==================================================================
"@ -ForegroundColor Cyan
