# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Fullstack DB Rotation Example on EKS
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [Alias("k")]
    [string]$SecretId
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

Write-Step "Deploying Fullstack DB Rotation Example to EKS Cluster..."
Write-Info "Target Secrets Manager: $SecretId"

# 1. Check prerequisites
if (-not (Get-Command kubectl -ErrorAction SilentlyContinue)) {
    Write-Error "'kubectl' is required. Please install kubectl: https://kubernetes.io/docs/tasks/tools/"
}

if (-not (Get-Command az -ErrorAction SilentlyContinue)) {
    Write-Error "AWS CLI ('az') is required. Please install az: https://learn.microsoft.com/en-us/cli/AWS/install-AWS-cli"
}

# 2. Check cluster connection
$currentContext = kubectl config current-context 2>$null
if (-not $currentContext) {
    Write-Error "Not connected to any Kubernetes cluster. Please run 'az EKS get-credentials' first."
}
Write-Success "Using Kubernetes Context: $currentContext"

# 3. Check Secrets Manager accessibility
Write-Step "Verifying access to AWS Secrets Manager '$SecretId'..."
$kvCheck = aws secretsmanager describe-secret --secret-id $SecretId 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Unable to access Secrets Manager '$SecretId'. Please verify the name and your AWS permissions.`nDetails: $kvCheck"
}
Write-Success "Secrets Manager '$SecretId' verified."

# 4. Seed initial secret in AWS Secrets Manager if not exists
Write-Step "Checking secret 'db-password' in AWS Secrets Manager '$SecretId'..."
$secretCheck = aws secretsmanager get-secret-value --secret-id $SecretId --secret-id "db-password" 2>$null
if (-not $secretCheck) {
    Write-Info "Secret 'db-password' not found. Creating initial secret in Secrets Manager..."
    $setOut = aws secretsmanager put-secret-value `
        --secret-id $SecretId `
        --secret-id "db-password" `
        --secret-string "InitialSecretPassword123!" `
        --output none 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create secret 'db-password' in Secrets Manager '$SecretId'.`nDetails: $setOut"
    }
    Write-Success "Initial secret 'db-password' seeded in Secrets Manager."
} else {
    Write-Info "Secret 'db-password' already exists in Secrets Manager."
}

# 5. Ensure target namespace exists and create bootstrap secret
Write-Step "Ensuring namespace 'dso-examples' exists..."
$nsOut = kubectl create namespace dso-examples --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to ensure namespace 'dso-examples'.`nDetails: $nsOut" }
Write-Success "Namespace 'dso-examples' ready."

Write-Info "Creating bootstrap secret in cluster for PostgreSQL initialization..."
$b1 = kubectl create secret generic db-status-app-db-password-initial `
    --secret-idspace dso-examples `
    --from-literal=db-password="InitialSecretPassword123!" `
    --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to create bootstrap secret.`nDetails: $b1" }
Write-Success "Bootstrap secret created."

# 6. Apply DynamicSecretPolicy CRD (if repo root is available)
$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd/bases"))) {
    Write-Step "Applying DynamicSecretPolicy CRD from repo..."
    $crdOut = kubectl apply --server-side --force-conflicts -f (Join-Path $RepoRoot "config/crd/bases") 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Failed to apply CRD.`nDetails: $crdOut" }
    Write-Success "CRD applied."
}

# 7. Apply manifests with Secrets Manager replacement
Write-Step "Deploying PostgreSQL, Web Dashboard, and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
if (-not (Test-Path $manifestPath)) {
    throw "Manifest file not found: $manifestPath"
}
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $SecretId

$applyOut = $manifestContent | kubectl apply -n dso-examples -f - 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}

# 8. Wait for deployments to be ready
Write-Info "Waiting for PostgreSQL deployment to become ready..."
$r1 = kubectl rollout status deployment/postgres -n dso-examples --timeout=120s 2>&1
Write-Host $r1
if ($LASTEXITCODE -ne 0) { throw "PostgreSQL rollout failed or timed out.`nDetails: $r1" }

Write-Info "Waiting for Web Dashboard deployment to become ready..."
$r2 = kubectl rollout status deployment/db-status-app -n dso-examples --timeout=120s 2>&1
Write-Host $r2
if ($LASTEXITCODE -ne 0) { throw "Web Dashboard rollout failed or timed out.`nDetails: $r2" }

# 9. Check and display Public LoadBalancer Service IP
Write-Step "Checking Public LoadBalancer IP for db-status-app..."
$svcJson = kubectl get svc db-status-app -n dso-examples -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

if (-not $extIp) {
    Write-Info "LoadBalancer Public IP is still being provisioned by AWS (status: <pending>)."
    Write-Info "Run 'kubectl get svc db-status-app -n dso-examples -w' to view the public IP as soon as AWS assigns it."
} else {
    Write-Success "Public IP assigned: http://$extIp"
}

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Fullstack DB Rotation PoC deployed successfully on EKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 STEP-BY-STEP VERIFICATION GUIDE:
------------------------------------------------------------------

1️⃣ Open the Live PostgreSQL Status Dashboard:
   - Public URL (LoadBalancer):
     kubectl get svc db-status-app -n dso-examples
     (Open http://<EXTERNAL-IP> in your browser)

   - Fallback (Port-Forward):
     kubectl port-forward svc/db-status-app 8080:80 -n dso-examples
     (Open http://localhost:8080)

2️⃣ Monitor Database Connections & DSO in Real Time (in separate terminals):
   - Watch Live Audit Log on Dashboard: The web UI displays real-time DB query status and active password hash.
   - Watch DSO State Machine & Validation Conditions:
     kubectl get dynamicsecretpolicy EKS-database-password-policy -n dso-examples -w

   - Watch Pod Rollout:
     kubectl get pods -n dso-examples -l app=db-status-app -w

   - Stream Operator Logs:
     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f

3️⃣ Execute Database Credential Rotation:
   🔹 Step 3.1: Update the user password directly inside PostgreSQL (simulating DBA/Rotation Engine):
      kubectl exec deployment/postgres -n dso-examples -- psql -U postgres -d appdb -c "ALTER USER postgres WITH PASSWORD 'NewSecret2026_Rotated!';"

   🔹 Step 3.2: Update the secret in AWS Secrets Manager:
      aws secretsmanager put-secret-value --secret-id $SecretId --secret-id "db-password" --secret-string "NewSecret2026_Rotated!"

4️⃣ Observe Zero-Downtime Database Rollover:
   - AWS Secrets Manager emits SecretNewVersionCreated event to Service Bus.
   - DSO receives the event and provisions an isolated Canary Pod.
   - DSO executes native PostgreSQL probe: runs test query against PostgreSQL with the new credentials.
   - Once validated, DSO promotes 'db-status-app' with rolling update.
   - The web dashboard switches to the new credential seamlessly without dropping queries!

5️⃣ Test Circuit Breaker & Safe Abort (Optional):
   - Change Secrets Manager secret WITHOUT updating PostgreSQL:
     aws secretsmanager put-secret-value --secret-id $SecretId --secret-id "db-password" --secret-string "WrongPassword999!"
   - DSO Canary probe will fail authentication against PostgreSQL and ABORT the rollout, keeping production safe!
==================================================================
"@ -ForegroundColor Cyan
