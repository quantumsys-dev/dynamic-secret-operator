# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO NGINX Color Rotation Example
# ==============================================================================
# This visual demonstration showcases real-time web application configuration
# changes (background color rotation) driven by External Secrets Operator (ESO)
# and safely validated with zero downtime via Dynamic Secret Operator (DSO).
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
    [string]$RemoteSecretName = "nginx-bg-color"
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

Write-Step "Deploying ESO NGINX Color Rotation Example..."
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

# 3. Create bootstrap initial secret
Write-Step "Step 3: Creating bootstrap initial secret for NGINX color..."
$secretCheck = kubectl get secret nginx-color-app-nginx-bg-color-initial -n $Namespace 2>$null
if (-not $secretCheck) {
    $secretOut = kubectl create secret generic nginx-color-app-nginx-bg-color-initial `
        --namespace $Namespace `
        --from-literal=nginx-bg-color="#3b82f6" `
        --dry-run=client -o yaml | kubectl apply -f - 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create bootstrap color secret.`nDetails: $secretOut"
    }
    Write-Success "Bootstrap initial secret created with default color '#3b82f6' (Blue)."
} else {
    Write-Info "Bootstrap initial secret already exists."
}

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
Write-Step "Step 5: Applying ESO ExternalSecret, NGINX App, and DSO Policy..."
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
}

$applyOut = $manifestContent | kubectl apply -f - -n $Namespace 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}
Write-Success "Manifests applied successfully."

# 6. Wait for deployment rollout
Write-Step "Step 6: Waiting for NGINX Color App deployment rollout..."
$rolloutOut = kubectl rollout status deployment/nginx-color-app -n $Namespace --timeout=120s 2>&1
Write-Host $rolloutOut
if ($LASTEXITCODE -ne 0) {
    throw "Timed out waiting for deployment/nginx-color-app to roll out."
}
Write-Success "Deployment 'nginx-color-app' is running and ready."

# 7. Display access and testing instructions
Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "🎉 ESO NGINX COLOR ROTATION EXAMPLE DEPLOYED SUCCESSFULLY!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 Deployed Configuration in '$Namespace':
------------------------------------------------------------------
Deployment:           nginx-color-app
Service:              nginx-color-app (LoadBalancer)
SecretStore:          $SecretStoreName ($SecretStoreKind)
ExternalSecret:       nginx-bg-color-eso
Remote Secret Name:   $RemoteSecretName
DynamicSecretPolicy:  eso-nginx-color-policy

🌐 How to View the Live Web Application:
------------------------------------------------------------------
Option 1: Using Port-Forward (Immediate):
  kubectl port-forward svc/nginx-color-app 8080:80 -n $Namespace
  Open in your browser at: http://localhost:8080

Option 2: Using LoadBalancer External IP:
  kubectl get svc nginx-color-app -n $Namespace -w

🔄 External Secrets & Safe Rotation Walkthrough:
------------------------------------------------------------------
1. Create Initial Secret in your Secret Provider:
   Create secret '$RemoteSecretName' with an initial CSS hex color (e.g. '#3b82f6' - Blue) in your secret provider.

2. Wait for ESO Polling Interval & Verify:
   Wait for ESO to poll and sync the secret (refresh interval: 15s):
     kubectl get externalsecret nginx-bg-color-eso -n $Namespace -w
   Verify that STATUS is 'SecretSynced' and READY is 'True'.
   Open http://localhost:8080 to see the active Blue background (#3b82f6).

3. Trigger a Valid Color Rotation (Canary Promotion):
   Update secret '$RemoteSecretName' in your secret provider to a new CSS hex color (e.g. '#10b981' - Emerald Green).

   Watch ESO poll the change and DSO validate the canary:
     kubectl get dynamicsecretpolicy eso-nginx-color-policy -n $Namespace -w
     kubectl get pods -n $Namespace -w
   Refresh http://localhost:8080 — the background updates to Green with zero downtime!

4. Test Invalid Color & Circuit Breaker Protection:
   Update secret '$RemoteSecretName' in your secret provider with an invalid CSS color (e.g. 'not-a-color').

   Watch ESO poll the update, then observe DSO run the validation probe:
     kubectl get dynamicsecretpolicy eso-nginx-color-policy -n $Namespace -w
     kubectl get jobs -n $Namespace -w
   DSO rejects the invalid canary revision, keeps production untouched, and trips the circuit breaker!
   Refresh http://localhost:8080 — the production workload remains running smoothly on the last valid color.
==================================================================
"@ -ForegroundColor Cyan
