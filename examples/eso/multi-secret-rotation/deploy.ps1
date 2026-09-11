# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO Multi-Secret Rotation Example
# ==============================================================================
# This multi-cloud demonstration showcases real-time rotation of multiple
# independent secrets (PostgreSQL, Redis, Payment API Key) driven by External
# Secrets Operator (ESO) and safely validated with zero downtime via
# Dynamic Secret Operator (DSO).
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
    [Alias("dbSecret")]
    [string]$RemoteDbSecretName = "db-password",

    [Parameter(Mandatory = $false)]
    [Alias("redisSecret")]
    [string]$RemoteRedisSecretName = "redis-auth-token",

    [Parameter(Mandatory = $false)]
    [Alias("paymentSecret")]
    [string]$RemotePaymentSecretName = "payment-api-key"
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

Write-Step "Deploying ESO Multi-Secret Rotation Example..."
Write-Info "Target Namespace:            $Namespace"
Write-Info "SecretStore Name:            $SecretStoreName"
Write-Info "SecretStore Kind:            $SecretStoreKind"
Write-Info "Remote DB Secret Key:        $RemoteDbSecretName"
Write-Info "Remote Redis Secret Key:     $RemoteRedisSecretName"
Write-Info "Remote Payment Secret Key:   $RemotePaymentSecretName"

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
$b1 = kubectl create secret generic multi-secret-app-db-password-initial `
    --namespace $Namespace `
    --from-literal=db-password="InitialPsqlPass123!" `
    --from-literal=db-user="postgres" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create db-password bootstrap secret.`nDetails: $b1" }

$b2 = kubectl create secret generic multi-secret-app-redis-auth-token-initial `
    --namespace $Namespace `
    --from-literal=redis-auth-token="InitialRedisToken456!" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create redis-auth-token bootstrap secret.`nDetails: $b2" }

$b3 = kubectl create secret generic multi-secret-app-payment-api-key-initial `
    --namespace $Namespace `
    --from-literal=payment-api-key="sk_live_pay_9876543210" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create payment-api-key bootstrap secret.`nDetails: $b3" }

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
Write-Step "Step 5: Applying ESO ExternalSecrets, backend microservices, and DSO Policies..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
if (-not (Test-Path $manifestPath)) {
    throw "Manifest file not found at: $manifestPath"
}

Write-Info "Substituting parameters: Store='$SecretStoreName', Kind='$SecretStoreKind'..."
$manifestContent = Get-Content -Path $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{SECRET_STORE_NAME\}', $SecretStoreName
$manifestContent = $manifestContent -replace '\$\{SECRET_STORE_KIND\}', $SecretStoreKind
$manifestContent = $manifestContent -replace '\$\{REMOTE_DB_SECRET_NAME\}', $RemoteDbSecretName
$manifestContent = $manifestContent -replace '\$\{REMOTE_REDIS_SECRET_NAME\}', $RemoteRedisSecretName
$manifestContent = $manifestContent -replace '\$\{REMOTE_PAYMENT_SECRET_NAME\}', $RemotePaymentSecretName

if ($Namespace -ne "dso-examples") {
    $manifestContent = $manifestContent -replace 'namespace:\s*dso-examples', "namespace: $Namespace"
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
if ($LASTEXITCODE -ne 0) { throw "Postgres rollout failed or timed out.`nDetails: $r1" }

$r2 = kubectl rollout status deployment/redis -n $Namespace --timeout=120s 2>&1
Write-Host $r2
if ($LASTEXITCODE -ne 0) { throw "Redis rollout failed or timed out.`nDetails: $r2" }

$r3 = kubectl rollout status deployment/payment-gateway -n $Namespace --timeout=120s 2>&1
Write-Host $r3
if ($LASTEXITCODE -ne 0) { throw "Payment Gateway rollout failed or timed out.`nDetails: $r3" }

$r4 = kubectl rollout status deployment/multi-secret-app -n $Namespace --timeout=180s 2>&1
Write-Host $r4
if ($LASTEXITCODE -ne 0) { throw "Multi-Secret App rollout failed or timed out.`nDetails: $r4" }

Write-Success "All deployments are running and ready."

# 7. Check and display Public LoadBalancer Service IP
Write-Step "Step 7: Checking Public Service endpoint for multi-secret-app..."
$svcJson = kubectl get svc multi-secret-app -n $Namespace -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

$displayIp = if ($extIp) { $extIp } else { "<EXTERNAL-IP>" }

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "🎉 ESO MULTI-SECRET ROTATION EXAMPLE DEPLOYED SUCCESSFULLY!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 Deployed Configuration in '$Namespace':
------------------------------------------------------------------
SecretStore Reference:   $SecretStoreName ($SecretStoreKind)
ExternalSecrets:         db-password-eso, redis-auth-token-eso, payment-api-key-eso
Intermediate Secrets:    db-password-synced, redis-auth-token-synced, payment-api-key-synced
Workload Deployment:     multi-secret-app
DynamicSecretPolicies:   multi-secret-db-policy, multi-secret-redis-policy, multi-secret-payment-policy

🌐 How to View the Live Microservice Dashboard:
------------------------------------------------------------------
Option 1: Using Port-Forward (Immediate):
  kubectl port-forward svc/multi-secret-app 8080:80 -n $Namespace
  Open in your browser at: http://localhost:8080

Option 2: Using LoadBalancer External IP:
  kubectl get svc multi-secret-app -n $Namespace -w
  (Open http://$displayIp in your browser once assigned)

🔄 External Secrets & Safe Rotation Walkthrough:
------------------------------------------------------------------
1. Create Initial Secrets in your Secret Provider (Vault, AWS, GCP, Azure, etc.):
   - DB Password ('${RemoteDbSecretName}'):       'InitialPsqlPass123!'
   - Redis Token ('${RemoteRedisSecretName}'):    'InitialRedisToken456!'
   - Payment Key ('${RemotePaymentSecretName}'):  'sk_live_pay_9876543210'

2. Monitor ExternalSecrets and DynamicSecretPolicies:
   Watch ESO synchronize secrets from your provider:
     kubectl get externalsecrets -n $Namespace -w

   Watch DSO validate canaries and promote secrets independently:
     kubectl get dynamicsecretpolicies -n $Namespace -w
     kubectl get pods -n $Namespace -w

3. Test Independent Secret Rotations:

   1. Rotate PostgreSQL Database Password:
      a) Update Postgres user password in cluster:
         kubectl exec deployment/postgres -n $Namespace -- psql -U postgres -d appdb -c "ALTER USER postgres WITH PASSWORD 'NewRotatedPsqlPass999!';"
      b) Update secret '${RemoteDbSecretName}' in your secret provider to 'NewRotatedPsqlPass999!'
      -> ESO polls and syncs db-password-synced.
      -> DSO launches Canary and validates native PostgreSQL probe.
      -> Workload rolling update mutates only 'db-secret-volume'.

   2. Rotate Redis Auth Token:
      a) Update Redis password in cluster:
         kubectl exec deployment/redis -n $Namespace -- redis-cli -a InitialRedisToken456! CONFIG SET requirepass "NewRotatedRedisToken888!"
      b) Update secret '${RemoteRedisSecretName}' in your secret provider to 'NewRotatedRedisToken888!'
      -> ESO polls and syncs redis-auth-token-synced.
      -> DSO launches Canary and validates Redis connection probe.
      -> Workload rolling update mutates only 'redis-secret-volume'.

   3. Rotate Payment API Gateway Key:
      Update secret '${RemotePaymentSecretName}' in your secret provider to 'sk_live_pay_new_777777'
      -> ESO polls and syncs payment-api-key-synced.
      -> DSO launches Canary and validates Payment API HTTP probe.
      -> Workload rolling update mutates only 'payment-secret-volume'.

4. Test Invalid Secret and Circuit Breaker Protection:
   Update secret '${RemotePaymentSecretName}' in your secret provider with an invalid value.
   -> ESO syncs to intermediate secret.
   -> DSO Canary probe fails HTTP health check, rejects rollout, and trips circuit breaker!
   -> The production workload remains completely healthy on the last valid secret.
==================================================================
"@ -ForegroundColor Cyan
