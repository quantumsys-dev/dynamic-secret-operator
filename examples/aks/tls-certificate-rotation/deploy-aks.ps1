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

# 3. Create or verify certificate in Azure Key Vault
Write-Step "Checking certificate 'ingress-tls-cert' in Azure Key Vault '$KeyVaultName'..."
$certCheck = az keyvault certificate show --vault-name $KeyVaultName --name "ingress-tls-cert" 2>$null
if (-not $certCheck) {
    Write-Info "Creating initial self-signed certificate 'ingress-tls-cert' in Key Vault..."
    $defaultPolicy = az keyvault certificate get-default-policy
    az keyvault certificate create `
        --vault-name $KeyVaultName `
        --name "ingress-tls-cert" `
        --policy $defaultPolicy `
        --output none

    Write-Info "Waiting for Key Vault certificate creation to complete..."
    while ($true) {
        $status = az keyvault certificate show --vault-name $KeyVaultName --name "ingress-tls-cert" --query "attributes.enabled" -o tsv 2>$null
        if ($status -eq "true") {
            break
        }
        Start-Sleep -Seconds 2
    }
    Write-Success "Initial certificate created in Key Vault."
} else {
    Write-Info "Certificate 'ingress-tls-cert' already exists in Key Vault."
}

# 4. Apply manifests with Key Vault replacement
Write-Step "Deploying Nginx TLS Gateway and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $KeyVaultName

$manifestContent | kubectl apply -f -

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ TLS Certificate Rotation Example deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

Next Steps:
1. Port-forward the HTTPS gateway:
   kubectl port-forward svc/tls-gateway 8443:8443

2. Query the endpoint:
   curl -k https://localhost:8443

3. Trigger a certificate rotation in Azure Key Vault:
   az keyvault certificate create --vault-name $KeyVaultName --name "ingress-tls-cert" --policy (az keyvault certificate get-default-policy)

4. Observe DSO auto-parse the new certificate, validate TLS handshakes, and promote the gateway!
==================================================================
"@ -ForegroundColor Cyan
