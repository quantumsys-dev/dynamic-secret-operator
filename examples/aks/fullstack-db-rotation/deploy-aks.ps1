# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Fullstack DB Rotation Example on AKS
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

Write-Step "Deploying Fullstack DB Rotation Example to AKS Cluster..."
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
Write-Step "Checking secret 'db-password' in Azure Key Vault '$KeyVaultName'..."
$secretCheck = az keyvault secret show --vault-name $KeyVaultName --name "db-password" 2>$null
if (-not $secretCheck) {
    Write-Info "Secret 'db-password' not found. Creating initial secret in Key Vault..."
    $setOut = az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "db-password" `
        --value "InitialSecretPassword123!" `
        --output none 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create secret 'db-password' in Key Vault '$KeyVaultName'.`nDetails: $setOut"
    }
    Write-Success "Initial secret 'db-password' seeded in Key Vault."
} else {
    Write-Info "Secret 'db-password' already exists in Key Vault."
}

# 5. Create bootstrap secret in cluster for PostgreSQL initial startup
Write-Info "Creating bootstrap secret in cluster for PostgreSQL initialization..."
$b1 = kubectl create secret generic db-status-app-db-password-initial `
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

# 7. Apply manifests with Key Vault replacement
Write-Step "Deploying PostgreSQL, Web Dashboard, and DynamicSecretPolicy manifests..."
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

# 8. Wait for deployments to be ready
Write-Info "Waiting for PostgreSQL deployment to become ready..."
$r1 = kubectl rollout status deployment/postgres --timeout=120s 2>&1
Write-Host $r1
if ($LASTEXITCODE -ne 0) { throw "PostgreSQL rollout failed or timed out.`nDetails: $r1" }

Write-Info "Waiting for Web Dashboard deployment to become ready..."
$r2 = kubectl rollout status deployment/db-status-app --timeout=120s 2>&1
Write-Host $r2
if ($LASTEXITCODE -ne 0) { throw "Web Dashboard rollout failed or timed out.`nDetails: $r2" }

# 8. Check and display Public LoadBalancer Service IP
Write-Step "Checking Public LoadBalancer IP for db-status-app..."
$svcJson = kubectl get svc db-status-app -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

if (-not $extIp) {
    Write-Info "LoadBalancer Public IP is still being provisioned by Azure (status: <pending>)."
    Write-Info "Run 'kubectl get svc db-status-app -w' to view the public IP as soon as Azure assigns it."
} else {
    Write-Success "Public IP assigned: http://$extIp"
}

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Fullstack DB Rotation PoC deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 STEP-BY-STEP VERIFICATION GUIDE:
------------------------------------------------------------------

1️⃣ Open the Live PostgreSQL Status Dashboard:
   - Public URL (LoadBalancer):
     kubectl get svc db-status-app
     (Open http://<EXTERNAL-IP> in your browser)

   - Fallback (Port-Forward):
     kubectl port-forward svc/db-status-app 8080:80
     (Open http://localhost:8080)

2️⃣ Monitor Database Connections & DSO in Real Time (in separate terminals):
   - Watch Live Audit Log on Dashboard: The web UI displays real-time DB query status and active password hash.
   - Watch DSO State Machine & Validation Conditions:
     kubectl get dynamicsecretpolicy aks-database-password-policy -w

   - Watch Pod Rollout:
     kubectl get pods -l app=db-status-app -w

   - Stream Operator Logs:
     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f

3️⃣ Execute Database Credential Rotation:
   🔹 Step 3.1: Update the user password directly inside PostgreSQL (simulating DBA/Rotation Engine):
      kubectl exec deployment/postgres -- psql -U postgres -d appdb -c "ALTER USER postgres WITH PASSWORD 'NewSecret2026_Rotated!';"

   🔹 Step 3.2: Update the secret in Azure Key Vault:
      az keyvault secret set --vault-name $KeyVaultName --name "db-password" --value "NewSecret2026_Rotated!"

4️⃣ Observe Zero-Downtime Database Rollover:
   - Azure Key Vault emits SecretNewVersionCreated event to Service Bus.
   - DSO receives the event and provisions an isolated Canary Pod.
   - DSO executes native PostgreSQL probe: runs test query against PostgreSQL with the new credentials.
   - Once validated, DSO promotes 'db-status-app' with rolling update.
   - The web dashboard switches to the new credential seamlessly without dropping queries!

5️⃣ Test Circuit Breaker & Safe Abort (Optional):
   - Change Key Vault secret WITHOUT updating PostgreSQL:
     az keyvault secret set --vault-name $KeyVaultName --name "db-password" --value "WrongPassword999!"
   - DSO Canary probe will fail authentication against PostgreSQL and ABORT the rollout, keeping production safe!
==================================================================
"@ -ForegroundColor Cyan
