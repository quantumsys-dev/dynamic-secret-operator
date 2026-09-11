# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO Argo Rollouts Blue/Green Example
# ==============================================================================
# This multi-cloud demonstration showcases automated zero-downtime Blue/Green
# secret delivery with Argo Rollouts driven by External Secrets Operator (ESO)
# and Dynamic Secret Operator (DSO).
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
    [string]$RemoteSecretName = "payment-db-password",

    [Parameter(Mandatory = $false)]
    [string]$ArgoRolloutsVersion = "v1.7.2"
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

Write-Step "Deploying ESO + Argo Rollouts Blue/Green Example..."
Write-Info "Target Namespace:    $Namespace"
Write-Info "SecretStore Name:    $SecretStoreName"
Write-Info "SecretStore Kind:    $SecretStoreKind"
Write-Info "Remote Secret Name:  $RemoteSecretName"
Write-Info "Argo Rollouts Ver:   $ArgoRolloutsVersion"

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

# 3. Check and install Argo Rollouts controller if needed
Write-Step "Step 3: Verifying Argo Rollouts controller in cluster..."
$crdCheck = kubectl get crd rollouts.argoproj.io 2>$null
if (-not $crdCheck) {
    Write-Info "Argo Rollouts CRD not found. Installing Argo Rollouts ($ArgoRolloutsVersion)..."
    $createArgoNs = kubectl create namespace argo-rollouts --dry-run=client -o yaml | kubectl apply -f - 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create namespace 'argo-rollouts'.`nDetails: $createArgoNs"
    }

    $installOut = kubectl apply -n argo-rollouts -f "https://github.com/argoproj/argo-rollouts/releases/download/${ArgoRolloutsVersion}/install.yaml" 2>&1
    Write-Host $installOut
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to install Argo Rollouts controller.`nDetails: $installOut"
    }

    Write-Info "Waiting for Argo Rollouts controller deployment to become ready..."
    $rolloutStatus = kubectl rollout status deployment/argo-rollouts -n argo-rollouts --timeout=120s 2>&1
    Write-Host $rolloutStatus
    if ($LASTEXITCODE -ne 0) {
        throw "Argo Rollouts controller rollout failed or timed out.`nDetails: $rolloutStatus"
    }
    Write-Success "Argo Rollouts controller ($ArgoRolloutsVersion) installed and ready."
} else {
    Write-Success "Argo Rollouts is already installed in the cluster."
}

# 4. Create bootstrap initial secret
Write-Step "Step 4: Creating bootstrap initial secret in namespace '$Namespace'..."
$b1 = kubectl create secret generic rollout-payment-service-payment-db-password-initial `
    --namespace $Namespace `
    --from-literal=payment-db-password="initial-database-password-v1" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Failed to create bootstrap secret.`nDetails: $b1"
}
Write-Success "Bootstrap initial secret created."

# 5. Apply DynamicSecretPolicy CRD if present
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd/bases"))) {
    Write-Step "Step 5: Ensuring DynamicSecretPolicy CRD is applied..."
    $crdOut = kubectl apply --server-side --force-conflicts -f (Join-Path $RepoRoot "config/crd/bases") 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Success "DynamicSecretPolicy CRD applied."
    }
}

# 6. Apply manifests
Write-Step "Step 6: Applying ESO ExternalSecret, Argo Rollout, Services, and DSO Policy..."
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
    $manifestContent = $manifestContent -replace 'payment-service-active\.dso-examples\.svc\.cluster\.local', "payment-service-active.$Namespace.svc.cluster.local"
}

$applyOut = $manifestContent | kubectl apply -f - -n $Namespace 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}
Write-Success "Manifests applied successfully."

# 7. Check and display Public LoadBalancer Service IP
Write-Step "Step 7: Checking Public Service endpoint for payment-service-active..."
$svcJson = kubectl get svc payment-service-active -n $Namespace -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

$displayIp = if ($extIp) { $extIp } else { "<EXTERNAL-IP>" }

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "🎉 ESO + ARGO ROLLOUTS BLUE/GREEN EXAMPLE DEPLOYED SUCCESSFULLY!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 Deployed Configuration in '$Namespace':
------------------------------------------------------------------
SecretStore Reference:   $SecretStoreName ($SecretStoreKind)
ExternalSecret:          payment-db-password-eso
Intermediate Secret:     payment-db-password-synced
Workload Rollout:        rollout-payment-service (Argo Rollout)
Active Service:          payment-service-active (LoadBalancer)
Preview Service:         payment-service-preview (ClusterIP)
DynamicSecretPolicy:     eso-rollout-payment-policy

🌐 How to Access the Live Active Payment Service:
------------------------------------------------------------------
Option 1: Using Port-Forward (Immediate):
  kubectl port-forward svc/payment-service-active 8080:80 -n $Namespace
  Open in your browser or curl: http://localhost:8080

Option 2: Using LoadBalancer External IP:
  kubectl get svc payment-service-active -n $Namespace -w
  (Open http://$displayIp in your browser once assigned)

🔍 STEP-BY-STEP VERIFICATION & ROTATION GUIDE:
------------------------------------------------------------------

1️⃣ Monitor Argo Rollouts & DSO in Real Time (in separate terminals):
   - Watch Argo Rollouts Blue/Green State & ReplicaSets:
     kubectl argo rollouts get rollout rollout-payment-service -n $Namespace --watch
     (or standard kubectl: kubectl get pods -n $Namespace -l app=payment-service -w)

   - Watch DynamicSecretPolicy State Machine:
     kubectl get dynamicsecretpolicy eso-rollout-payment-policy -n $Namespace -w

   - Watch ESO Secret Synchronization:
     kubectl get externalsecrets -n $Namespace -w

2️⃣ Execute Safe Blue/Green Secret Rotation:
   🔹 Step 2.1: Update secret '${RemoteSecretName}' in your Secret Provider (Vault, AWS, GCP, Azure, etc.):
      Set the secret '${RemoteSecretName}' value to 'NewPaymentPassword2026_Rotated!'

   🔹 Step 2.2: Observe Progressive Delivery & Zero-Downtime Shift:
      1. ESO synchronizes the new secret to 'payment-db-password-synced'.
      2. DSO creates candidate revision and updates the Rollout template.
      3. Argo Rollouts spins up the new Green ReplicaSet alongside Blue.
      4. DSO validates preview pods using configured validation probes.
      5. Once Healthy, Argo Rollouts performs an atomic cutover of active traffic from Blue to Green.
      6. The old Blue ReplicaSet is gracefully scaled down after the scaleDownDelaySeconds window.
      7. Active service experiences zero dropped connections or 5xx errors!

3️⃣ Test Invalid Secret & Circuit Breaker Protection:
   - Update secret '${RemoteSecretName}' in your provider with an invalid value.
   - ESO synchronizes the intermediate secret.
   - DSO detects the change and attempts validation.
   - If the candidate revision fails probe validation, rollout promotion is aborted!
   - Active traffic remains securely pointed to the healthy Blue ReplicaSet with 100% uptime.
==================================================================
"@ -ForegroundColor Cyan
