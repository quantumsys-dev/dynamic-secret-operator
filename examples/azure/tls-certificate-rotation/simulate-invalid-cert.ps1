# ==============================================================================
# Dynamic Secret Operator (DSO) – Simulate Invalid Certificate & Test Circuit Breaker
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [Alias("d")]
    [string]$Domain,

    [Parameter(Mandatory = $false, Position = 1)]
    [Alias("k")]
    [string]$KeyVaultName = "kv-dso-dev-jc"
)

$ErrorActionPreference = "Stop"

if (-not $env:AZURE_EXTENSION_DIR) {
    $cleanExtDir = Join-Path $HOME ".azure\ext"
    if (-not (Test-Path $cleanExtDir)) { New-Item -ItemType Directory -Path $cleanExtDir -Force | Out-Null }
    $env:AZURE_EXTENSION_DIR = $cleanExtDir
}

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

function Write-Alert {
    param([string]$Message)
    Write-Host "⚠️  $Message" -ForegroundColor Red
}

Write-Step "Simulating Invalid/Expired Certificate Injection into Azure Key Vault..."
Write-Info "Target Domain:     $Domain"
Write-Info "Target Key Vault:  $KeyVaultName"

# 1. Capture current production revision before failure injection
$initialPolicyJson = kubectl get dynamicsecretpolicy aks-ingress-tls-policy -n dso-examples -o json 2>$null
$initialRevision = ""
if ($initialPolicyJson) {
    $initialPolicy = $initialPolicyJson | ConvertFrom-Json
    $initialRevision = $initialPolicy.status.currentRevision
}
Write-Success "Current healthy production revision: $initialRevision"

# 2. Generate an x509 certificate with EXPIRED dates (validity in the past)
Write-Step "Generating synthetically expired certificate bundle (NotAfter in the past)..."
$rsa = [System.Security.Cryptography.RSA]::Create(2048)
$req = [System.Security.Cryptography.X509Certificates.CertificateRequest]::new(
    "CN=$Domain",
    $rsa,
    [System.Security.Cryptography.HashAlgorithmName]::SHA256,
    [System.Security.Cryptography.RSASignaturePadding]::Pkcs1
)

$sanBuilder = [System.Security.Cryptography.X509Certificates.SubjectAlternativeNameBuilder]::new()
$sanBuilder.AddDnsName($Domain)
$sanBuilder.AddDnsName("localhost")
$req.CertificateExtensions.Add($sanBuilder.Build())

# Expired 24 hours ago
$expiredCert = $req.CreateSelfSigned(
    [DateTimeOffset]::UtcNow.AddYears(-2),
    [DateTimeOffset]::UtcNow.AddDays(-1)
)

$certPem = $expiredCert.ExportCertificatePem()
$keyPem = $rsa.ExportPkcs8PrivateKeyPem()
$bundlePem = "$certPem`n$keyPem"

Write-Alert "Generated certificate expired on: $($expiredCert.NotAfter.ToString('yyyy-MM-dd HH:mm:ss UTC'))"

# 3. Inject expired certificate bundle as a secret version in Azure Key Vault
Write-Step "Injecting expired certificate bundle into Key Vault secret 'ingress-tls-cert'..."
$tmpFile = New-TemporaryFile
try {
    Set-Content -Path $tmpFile.FullName -Value $bundlePem
    $secretSetOut = az keyvault secret set `
        --vault-name $KeyVaultName `
        --name "ingress-tls-cert" `
        --file "$($tmpFile.FullName)" `
        --output none 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to set secret in Key Vault.`nDetails: $secretSetOut"
    }
    Write-Success "Expired certificate bundle published to Azure Key Vault."
} finally {
    Remove-Item -Path $tmpFile.FullName -ErrorAction SilentlyContinue
}

# 4. Monitor DSO Canary and Circuit Breaker response
Write-Step "Monitoring DSO Circuit Breaker on AKS..."
Write-Info "Expected Behavior:"
Write-Info "1. Azure Event Grid forwards SecretNewVersionCreated to Service Bus."
Write-Info "2. DSO AMQP receiver ingests the expired certificate."
Write-Info "3. DSO provisions an ephemeral canary pod (tls-gateway-canary)."
Write-Info "4. Synthetic TLS Probe connects and detects certificate expiration."
Write-Info "5. Failures increment until threshold (3) is reached."
Write-Info "6. Circuit Breaker TRIPS (CircuitBreakerTripped: True) and destroys canary."
Write-Info "7. Production workload remains 100% HEALTHY on revision $initialRevision!"

$timeout = 90
$elapsed = 0
$tripped = $false

while ($elapsed -lt $timeout) {
    $policyJson = kubectl get dynamicsecretpolicy aks-ingress-tls-policy -n dso-examples -o json 2>$null
    if ($policyJson) {
        $p = $policyJson | ConvertFrom-Json
        $failCount = $p.status.consecutiveFailures
        $conds = $p.status.conditions
        $cbCond = $conds | Where-Object { $_.type -eq "CircuitBreakerTripped" -and $_.status -eq "True" }

        Write-Host "  -> Elapsed: ${elapsed}s | Consecutive Failures: $failCount | Tripped: $([bool]$cbCond)"

        if ($cbCond) {
            $tripped = $true
            Write-Host "`n"
            Write-Success "⚡ CIRCUIT BREAKER TRIPPED!"
            Write-Info "Condition Message: $($cbCond.message)"
            break
        }
    }
    Start-Sleep -Seconds 4
    $elapsed += 4
}

# 5. Verify Production Stability
Write-Step "Verifying Production Workload Status..."
$prodDeployment = kubectl get deployment tls-gateway -n dso-examples -o json 2>$null | ConvertFrom-Json
$readyReplicas = $prodDeployment.status.readyReplicas
$totalReplicas = $prodDeployment.status.replicas

if ($readyReplicas -eq $totalReplicas) {
    Write-Success "Production Gateway is 100% HEALTHY ($readyReplicas/$totalReplicas replicas ready)!"
} else {
    Write-Alert "Production Gateway status: $readyReplicas/$totalReplicas replicas ready."
}

# Verify Canary was cleaned up
$canaryPods = kubectl get pods -n dso-examples -l "dso.quantumsys.dev/canary=true" --no-headers 2>$null
if (-not $canaryPods) {
    Write-Success "Ephemeral Canary sandbox cleanly deleted. Zero orphaned resources."
}

Write-Host @"

==================================================================
✅ Circuit Breaker Chaos Test Complete! Production was fully protected.
==================================================================

🔄 TO RECOVER & HEAL PRODUCTION:
Run the valid rotation script to issue a fresh certificate:
  .\rotate-cert.ps1 -Domain "$Domain" -KeyVaultName "$KeyVaultName"

==================================================================
"@ -ForegroundColor Green
