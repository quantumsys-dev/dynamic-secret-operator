# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO Fullstack DB Rotation Example
# ==============================================================================
# This multi-cloud demonstration showcases real-time rotation of database
# credentials (PostgreSQL password) synchronized by External Secrets Operator (ESO)
# and safely validated with synthetic native PostgreSQL probes and zero downtime
# via Dynamic Secret Operator (DSO).
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
    [string]$RemoteSecretName = "db-password"
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

Write-Step "Deploying ESO Fullstack Database Rotation Example..."
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
Write-Step "Step 3: Creating bootstrap initial secret for PostgreSQL in namespace '$Namespace'..."
$b1 = kubectl create secret generic db-status-app-db-password-initial `
    --namespace $Namespace `
    --from-literal=db-password="InitialSecretPassword123!" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Failed to create bootstrap secret.`nDetails: $b1"
}
Write-Success "Bootstrap initial secret created."

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
Write-Step "Step 5: Applying ESO ExternalSecret, PostgreSQL, Web Dashboard, and DSO Policy..."
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
    $manifestContent = $manifestContent -replace 'postgres\.dso-examples\.svc\.cluster\.local', "postgres.$Namespace.svc.cluster.local"
}

$applyOut = $manifestContent | kubectl apply -f - -n $Namespace 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}
Write-Success "Manifests applied successfully."

# 6. Wait for deployment rollouts
Write-Step "Step 6: Waiting for deployments in '$Namespace' to be ready..."
$r1 = kubectl rollout status deployment/postgres -n $Namespace --timeout=120s 2>&1
Write-Host $r1
if ($LASTEXITCODE -ne 0) { throw "PostgreSQL rollout failed or timed out.`nDetails: $r1" }

$r2 = kubectl rollout status deployment/db-status-app -n $Namespace --timeout=180s 2>&1
Write-Host $r2
if ($LASTEXITCODE -ne 0) { throw "Web Dashboard rollout failed or timed out.`nDetails: $r2" }

Write-Success "All database workloads are running and ready."

# 7. Check and display Public LoadBalancer Service IP
Write-Step "Step 7: Checking Public Service endpoint for db-status-app..."
$svcJson = kubectl get svc db-status-app -n $Namespace -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

$displayIp = if ($extIp) { $extIp } else { "<EXTERNAL-IP>" }

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "🎉 ESO FULLSTACK DATABASE ROTATION EXAMPLE DEPLOYED SUCCESSFULLY!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 Deployed Configuration in '$Namespace':
------------------------------------------------------------------
SecretStore Reference:   $SecretStoreName ($SecretStoreKind)
ExternalSecret:          db-password-eso
Intermediate Secret:     db-password-synced
Workload Deployment:     db-status-app
Database Deployment:     postgres
Validation Probe:        PostgreSQL (native synthetic query)
DynamicSecretPolicy:     eso-database-password-policy

🌐 How to View the Live Database Dashboard:
------------------------------------------------------------------
Option 1: Using Port-Forward (Immediate):
  kubectl port-forward svc/db-status-app 8080:80 -n $Namespace
  Open in your browser at: http://localhost:8080

Option 2: Using LoadBalancer External IP:
  kubectl get svc db-status-app -n $Namespace -w
  (Open http://$displayIp in your browser once assigned)

🔍 STEP-BY-STEP VERIFICATION & ROTATION GUIDE:
------------------------------------------------------------------

1️⃣ Monitor Database Status & DSO Policy (in separate terminals):
   - Watch DynamicSecretPolicy State Machine:
     kubectl get dynamicsecretpolicy eso-database-password-policy -n $Namespace -w

   - Watch Pod Rollout:
     kubectl get pods -n $Namespace -l app=db-status-app -w

   - Watch ESO Secret Synchronization:
     kubectl get externalsecrets -n $Namespace -w

2️⃣ Execute Safe PostgreSQL Database Credential Rotation:
   🔹 Step 2.1: Update password inside running PostgreSQL (simulating DBA/Rotation Engine):
      kubectl exec deployment/postgres -n $Namespace -- psql -U postgres -d appdb -c "ALTER USER postgres WITH PASSWORD 'NewSecret2026_Rotated!';"

   🔹 Step 2.2: Update secret '${RemoteSecretName}' in your Secret Provider (Vault, AWS, GCP, Azure, etc.):
      Set the secret '${RemoteSecretName}' value to 'NewSecret2026_Rotated!'

   🔹 Step 2.3: Observe Autonomous Validation & Zero Downtime:
      1. ESO synchronizes the new secret to 'db-password-synced'.
      2. DSO creates candidate revision and provisions an isolated Canary Pod.
      3. DSO executes native PostgreSQL probe (CONNECT + SELECT query).
      4. Upon probe success, DSO safely promotes 'db-status-app' with rolling update.
      5. The live web dashboard reflects the new password hint seamlessly with ZERO connection errors!

3️⃣ Test Invalid Secret & Circuit Breaker Protection:
   - Update secret '${RemoteSecretName}' in your provider with an invalid value (e.g. 'WrongPassword999!') WITHOUT updating PostgreSQL.
   - ESO synchronizes the intermediate secret.
   - DSO launches Canary and runs the PostgreSQL probe which fails authentication.
   - DSO immediately rejects the canary revision, surfaces the failure condition, and trips the circuit breaker!
   - The production dashboard remains untouched on the valid password with 100% uptime.
==================================================================
"@ -ForegroundColor Cyan
