# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Nginx Color Rotation Example on EKS
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

Write-Step "Deploying Nginx Color Rotation Example to EKS Cluster..."
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
Write-Step "Checking secret 'nginx-bg-color' in AWS Secrets Manager '$SecretId'..."
$secretCheck = aws secretsmanager get-secret-value --secret-id $SecretId --secret-id "nginx-bg-color" 2>$null
if (-not $secretCheck) {
    Write-Info "Creating initial secret 'nginx-bg-color' in Secrets Manager..."
    $setOut = aws secretsmanager put-secret-value `
        --secret-id $SecretId `
        --secret-id "nginx-bg-color" `
        --secret-string "#3b82f6" `
        --output none 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create secret 'nginx-bg-color' in Secrets Manager '$SecretId'.`nDetails: $setOut"
    }
    Write-Success "Initial secret 'nginx-bg-color' seeded in Secrets Manager."
} else {
    Write-Info "Secret 'nginx-bg-color' already exists in Secrets Manager."
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

# 6. Apply manifests with Secrets Manager replacement
Write-Step "Deploying Nginx Color App and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
if (-not (Test-Path $manifestPath)) {
    throw "Manifest file not found: $manifestPath"
}
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $SecretId

$applyOut = $manifestContent | kubectl apply -f - 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}

# 7. Wait for deployment to be ready
Write-Info "Waiting for Nginx Color App deployment to become ready..."
$rolloutOut = kubectl rollout status deployment/nginx-color-app -n dso-examples --timeout=120s 2>&1
Write-Host $rolloutOut
if ($LASTEXITCODE -ne 0) {
    throw "Deployment rollout failed or timed out.`nDetails: $rolloutOut"
}

# 8. Check and display Public LoadBalancer Service IP
Write-Step "Checking Public LoadBalancer IP for nginx-color-app..."
$svcJson = kubectl get svc nginx-color-app -n dso-examples -o json 2>$null
$extIp = $null
if ($svcJson) {
    $svcInfo = $svcJson | ConvertFrom-Json
    if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
        $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
    }
}

if (-not $extIp) {
    Write-Info "LoadBalancer Public IP is still being provisioned by AWS (status: <pending>)."
    Write-Info "Run 'kubectl get svc nginx-color-app -n dso-examples -w' to view the public IP as soon as AWS assigns it."
} else {
    Write-Success "Public IP assigned: http://$extIp"
}

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Nginx Color Rotation Example deployed successfully on EKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 STEP-BY-STEP VERIFICATION GUIDE:
------------------------------------------------------------------

1️⃣ Access the Nginx Web App:
   - Public URL (LoadBalancer):
     kubectl get svc nginx-color-app -n dso-examples
     (Open http://<EXTERNAL-IP> in your browser)

   - Fallback (Port-Forward):
     kubectl port-forward svc/nginx-color-app 8080:80 -n dso-examples
     (Open http://localhost:8080)

2️⃣ Monitor DSO and Workload in Real Time (in a separate terminal):
   - Watch DSO State Machine & Conditions:
     kubectl get dynamicsecretpolicy EKS-nginx-color-policy -n dso-examples -w

   - Watch Pod Rollout & Canary Lifecycle:
     kubectl get pods -n dso-examples -l app=nginx-color-app -w

   - Stream Operator Logs:
     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f

3️⃣ Trigger a Secret Rotation in AWS Secrets Manager:
   aws secretsmanager put-secret-value --secret-id $SecretId --secret-id "nginx-bg-color" --secret-string "#10b981"

4️⃣ Observe Zero-Downtime Promotion:
   - Secrets Manager publishes SecretNewVersionCreated event to AWS Service Bus.
   - DSO triggers Canary Provisioning, runs synthetic Job validation probe to assert valid hex color.
   - Target Deployment 'nginx-color-app' is promoted to the new color with zero downtime!
   - Refresh your browser to see the background change from Blue (#3b82f6) to Green (#10b981)!

5️⃣ Test Circuit Breaker & Safe Abort (Optional):
   - Inject an invalid value that fails format validation:
     aws secretsmanager put-secret-value --secret-id $SecretId --secret-id "nginx-bg-color" --secret-string "INVALID_COLOR"
   - Watch DSO Job probe fail hex format validation ('INVALID_COLOR' is not a valid hex code).
   - DSO aborts promotion and protects production workloads from invalid secrets.
   - After reaching threshold (3 failures), DSO trips the Circuit Breaker (CircuitBreakerTripped: True)!
   - Live traffic remains 100% online on the previous stable color!
==================================================================
"@ -ForegroundColor Cyan

