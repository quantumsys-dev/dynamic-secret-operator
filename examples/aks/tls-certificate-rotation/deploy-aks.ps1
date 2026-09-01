# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy TLS Certificate Rotation Example on AKS
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

Write-Step "Deploying TLS Certificate Rotation Example to AKS Cluster..."
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

# 4. Create or verify certificate in Azure Key Vault
Write-Step "Checking certificate 'ingress-tls-cert' in Azure Key Vault '$KeyVaultName'..."
$certCheck = az keyvault certificate show --vault-name $KeyVaultName --name "ingress-tls-cert" 2>$null
if (-not $certCheck) {
    Write-Info "Creating initial self-signed certificate 'ingress-tls-cert' in Key Vault..."
    $defaultPolicy = az keyvault certificate get-default-policy 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to get default certificate policy.`nDetails: $defaultPolicy"
    }

    $createCertOut = az keyvault certificate create `
        --vault-name $KeyVaultName `
        --name "ingress-tls-cert" `
        --policy $defaultPolicy `
        --output none 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create certificate 'ingress-tls-cert' in Key Vault '$KeyVaultName'.`nDetails: $createCertOut"
    }

    Write-Info "Waiting for Key Vault certificate creation to complete..."
    $timeoutSeconds = 60
    $elapsed = 0
    $certReady = $false
    while ($elapsed -lt $timeoutSeconds) {
        $status = az keyvault certificate show --vault-name $KeyVaultName --name "ingress-tls-cert" --query "attributes.enabled" -o tsv 2>$null
        if ($status -eq "true") {
            $certReady = $true
            break
        }
        Start-Sleep -Seconds 2
        $elapsed += 2
    }
    if (-not $certReady) {
        throw "Timed out waiting for certificate 'ingress-tls-cert' to become ready in Key Vault."
    }
    Write-Success "Initial certificate created in Key Vault."
} else {
    Write-Info "Certificate 'ingress-tls-cert' already exists in Key Vault."
}

# 5. Ensure target namespace exists and apply CRD
Write-Step "Ensuring namespace 'dso-examples' exists..."
$nsOut = kubectl create namespace dso-examples --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to ensure namespace 'dso-examples'.`nDetails: $nsOut" }
Write-Success "Namespace 'dso-examples' ready."

$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd/bases"))) {
    Write-Step "Applying DynamicSecretPolicy CRD from repo..."
    $crdOut = kubectl apply --server-side --force-conflicts -f (Join-Path $RepoRoot "config/crd/bases") 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Failed to apply CRD.`nDetails: $crdOut" }
    Write-Success "CRD applied."
}

# 6. Apply manifests with Key Vault replacement
Write-Step "Deploying Nginx TLS Gateway and DynamicSecretPolicy manifests..."
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

# 7. Wait for deployment to be ready
Write-Info "Waiting for TLS Gateway deployment to become ready..."
$rolloutOut = kubectl rollout status deployment/tls-gateway -n dso-examples --timeout=120s 2>&1
Write-Host $rolloutOut
if ($LASTEXITCODE -ne 0) {
    throw "TLS Gateway rollout failed or timed out.`nDetails: $rolloutOut"
}

# 8. Check and display Public LoadBalancer Service IP
Write-Step "Checking Public LoadBalancer IP for tls-gateway..."
$svcJson = kubectl get svc tls-gateway -n dso-examples -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

if (-not $extIp) {
    Write-Info "LoadBalancer Public IP is still being provisioned by Azure (status: <pending>)."
    Write-Info "Run 'kubectl get svc tls-gateway -n dso-examples -w' to view the public IP as soon as Azure assigns it."
} else {
    Write-Success "Public HTTPS Endpoint: https://${extIp}:8443"
}

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ TLS Certificate Rotation Example deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 STEP-BY-STEP VERIFICATION GUIDE:
------------------------------------------------------------------

1️⃣ Access the HTTPS TLS Gateway:
   - Public Endpoint (LoadBalancer):
     kubectl get svc tls-gateway -n dso-examples
     curl -kv https://<EXTERNAL-IP>:8443

   - Fallback (Port-Forward):
     kubectl port-forward svc/tls-gateway 8443:8443 -n dso-examples
     curl -kv https://localhost:8443

2️⃣ Monitor TLS Expiration & DSO in Real Time (in separate terminals):
   - Inspect active TLS Certificate Subject & Expiration:
     curl -kv https://localhost:8443 2>&1 | Select-String "expire date"

   - Watch DSO State Machine & Validation Conditions:
     kubectl get dynamicsecretpolicy aks-ingress-tls-policy -n dso-examples -w

   - Stream Operator Logs:
     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f

3️⃣ Trigger a Certificate Renewal in Azure Key Vault:
   az keyvault certificate create --vault-name $KeyVaultName --name "ingress-tls-cert" --policy (az keyvault certificate get-default-policy)

4️⃣ Observe Zero-Downtime TLS Rollover:
   - Azure Key Vault generates a new x509 certificate and private key.
   - DSO auto-extracts certificate blocks (tls.crt) and RSA private key (tls.key).
   - DSO launches an isolated Canary and validates TLS handshakes with native TLS probe.
   - Live traffic switches to the new certificate with zero downtime and valid TLS handshakes!
==================================================================
"@ -ForegroundColor Cyan
