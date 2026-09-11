# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO Job-Based Redis Probe Example
# ==============================================================================
# This multi-cloud demonstration showcases real-time rotation of custom protocol
# secrets (Redis AUTH) synchronized by External Secrets Operator (ESO) and safely
# validated using an ephemeral batch/v1.Job probe via Dynamic Secret Operator (DSO).
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [Alias("n")]
    [string]$Namespace = "dso-examples",

    [Parameter(Mandatory = $true)]
    [Alias("s", "StoreName")]
    [string]$SecretStoreName,

    [Parameter(Mandatory = $true)]
    [Alias("k", "StoreKind")]
    [string]$SecretStoreKind,

    [Parameter(Mandatory = $false)]
    [Alias("r", "SecretName")]
    [string]$RemoteSecretName = "redis-auth-password"
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

Write-Step "Deploying ESO Job-Based Redis Probe Example..."
Write-Info "Target Namespace:   $Namespace"
Write-Info "SecretStore Name:   $SecretStoreName"
Write-Info "SecretStore Kind:   $SecretStoreKind"
Write-Info "Remote Secret Name: $RemoteSecretName"

# 1. Check prerequisites
Write-Step "Step 1: Checking prerequisites..."
if (-not (Get-Command kubectl -ErrorAction SilentlyContinue)) {
    throw "'kubectl' is required. Please install kubectl: https://kubernetes.io/docs/tasks/tools/"
}

$currentContext = kubectl config current-context 2>$null
if (-not $currentContext) {
    throw "Not connected to any Kubernetes cluster. Please configure your kubeconfig first."
}
Write-Success "Connected to cluster: $currentContext"

# 2. Ensure target namespace exists
Write-Step "Step 2: Ensuring namespace '$Namespace' exists..."
$nsOut = kubectl create namespace $Namespace --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Failed to ensure namespace '$Namespace'.`nDetails: $nsOut"
}
Write-Success "Namespace '$Namespace' is ready."

# 3. Create bootstrap initial secrets
Write-Step "Step 3: Creating bootstrap initial secrets in namespace '$Namespace'..."
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

Write-Success "Bootstrap initial secrets created."

# 4. Apply DynamicSecretPolicy CRD if present
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd/bases"))) {
    Write-Step "Step 4: Ensuring DynamicSecretPolicy CRD is applied..."
    $crdOut = kubectl apply --server-side --force-conflicts -f (Join-Path $RepoRoot "config/crd/bases") 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Success "DynamicSecretPolicy CRD applied."
    }
}

# 5. Apply manifests
Write-Step "Step 5: Applying ESO ExternalSecret, Redis Deployments, and DSO Policy..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
if (-not (Test-Path $manifestPath)) {
    throw "Manifest file not found at: $manifestPath"
}

Write-Info "Substituting parameters: Store='$SecretStoreName', Kind='$SecretStoreKind', RemoteKey='$RemoteSecretName'..."
$manifestContent = Get-Content -Path $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{SECRET_STORE_NAME\}', $SecretStoreName
$manifestContent = $manifestContent -replace '\$\{SECRET_STORE_KIND\}', $SecretStoreKind
$manifestContent = $manifestContent -replace '\$\{REMOTE_SECRET_NAME\}', $RemoteSecretName
if ($Namespace -ne "dso-examples") {
    $manifestContent = $manifestContent -replace 'namespace:\s*dso-examples', "namespace: $Namespace"
    $manifestContent = $manifestContent -replace 'redis-master\.dso-examples\.svc\.cluster\.local', "redis-master.$Namespace.svc.cluster.local"
}

$applyOut = $manifestContent | kubectl apply -f - -n $Namespace 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}
Write-Success "Manifests applied successfully."

# 6. Wait for deployment rollouts
Write-Step "Step 6: Waiting for deployments in '$Namespace' to be ready..."
$r1 = kubectl rollout status deployment/redis-master -n $Namespace --timeout=120s 2>&1
Write-Host $r1
if ($LASTEXITCODE -ne 0) { throw "Redis master rollout failed or timed out.`nDetails: $r1" }

$r2 = kubectl rollout status deployment/redis-consumer -n $Namespace --timeout=120s 2>&1
Write-Host $r2
if ($LASTEXITCODE -ne 0) { throw "Redis consumer rollout failed or timed out.`nDetails: $r2" }

Write-Success "All Redis deployments are running and ready."

# 7. Display step-by-step verification instructions
Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "🎉 ESO JOB-BASED REDIS PROBE EXAMPLE DEPLOYED SUCCESSFULLY!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 Deployed Configuration in '$Namespace':
------------------------------------------------------------------
SecretStore Reference:   $SecretStoreName ($SecretStoreKind)
ExternalSecret:          redis-auth-password-eso
Intermediate Secret:     redis-auth-password-synced
Workload Deployment:     redis-consumer
Target Env Var:          REDIS_AUTH_PASSWORD
Validation Probe:        batch/v1.Job (redis-cli PING)
DynamicSecretPolicy:     redis-cache-rotation

🔍 STEP-BY-STEP VERIFICATION & ROTATION GUIDE:
------------------------------------------------------------------

1️⃣ Tail Redis Consumer Logs (in a dedicated terminal):
   kubectl logs -l app=redis-consumer -n $Namespace -f
   (Observe continuous heartbeat logs: 'redis-ping=PONG')

2️⃣ Monitor DSO Policy & Ephemeral Validation Jobs (in separate terminals):
   - Watch Ephemeral Probe Job Lifecycle:
     kubectl get jobs -n $Namespace -w

   - Watch DynamicSecretPolicy State Machine:
     kubectl get dynamicsecretpolicy redis-cache-rotation -n $Namespace -w

   - Watch ESO Secret Synchronization:
     kubectl get externalsecrets -n $Namespace -w

3️⃣ Execute Safe Redis AUTH Password Rotation:
   🔹 Step 3.1: Update password inside running Redis Master:
      kubectl exec deployment/redis-master -n $Namespace -- redis-cli -a InitialRedisPassword123! --no-auth-warning CONFIG SET requirepass "RotatedRedisPassword456!"

   🔹 Step 3.2: Update secret '${RemoteSecretName}' in your Secret Provider (Vault, AWS, GCP, Azure, etc.):
      Set the secret '${RemoteSecretName}' value to 'RotatedRedisPassword456!'

   🔹 Step 3.3: Observe Autonomous Validation & Zero Downtime:
      1. ESO synchronizes the new secret to 'redis-auth-password-synced'.
      2. DSO creates candidate revision and spawns an ephemeral Job probe.
      3. The probe runs 'redis-cli -h redis-master -p 6379 -a <new-secret> PING'.
      4. Upon receiving 'PONG' (exit code 0), DSO rolls out 'redis-consumer' with the new password.
      5. Ephemeral Job probe is cleaned up automatically.
      6. Consumer logs show continuous 'PONG' with zero failed connections!

4️⃣ Test Invalid Secret & Circuit Breaker Protection:
   - Update secret '${RemoteSecretName}' in your provider with an invalid value (e.g. 'BadPassword999!').
   - ESO synchronizes the intermediate secret.
   - DSO launches the ephemeral Job probe which fails AUTH against Redis master.
   - DSO immediately rejects the canary revision, logs the probe failure, and trips the circuit breaker!
   - The production 'redis-consumer' workload remains untouched on the valid password with 100% uptime.
==================================================================
"@ -ForegroundColor Cyan
