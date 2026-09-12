# ==============================================================================
# Dynamic Secret Operator (DSO) – Trigger Valid TLS Certificate Rotation
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

Write-Step "Triggering Valid TLS Certificate Rotation for '$Domain' in Key Vault '$KeyVaultName'..."

$policyTemplatePath = Join-Path $PSScriptRoot "certificate-policy.json"
if (-not (Test-Path $policyTemplatePath)) {
    throw "Policy template not found at $policyTemplatePath"
}
$policyContent = Get-Content $policyTemplatePath -Raw
$policyContent = $policyContent -replace '\$\{DOMAIN\}', $Domain

$tempPolicyFile = New-TemporaryFile
Set-Content -Path $tempPolicyFile.FullName -Value $policyContent

    $currentAccount = az account show --query "user.name" -o tsv 2>$null
    $currentUserId = az ad signed-in-user show --query id -o tsv 2>$null
    if (-not $currentUserId -and $currentAccount) {
        $currentUserId = az ad user show --id $currentAccount --query id -o tsv 2>$null
        if (-not $currentUserId) {
            $currentUserId = az ad sp show --id $currentAccount --query id -o tsv 2>$null
        }
    }

    $kvId = az keyvault show --name $KeyVaultName --query id -o tsv 2>$null
    if ($kvId) {
        $hasCertRole = $false
        if ($currentUserId) {
            $checkRole = az role assignment list --assignee $currentUserId --scope $kvId --role "Key Vault Certificates Officer" --query "[0].id" -o tsv 2>$null
            if ($checkRole) { $hasCertRole = $true }
        }
        if (-not $hasCertRole -and $currentAccount) {
            $checkRole = az role assignment list --assignee $currentAccount --scope $kvId --role "Key Vault Certificates Officer" --query "[0].id" -o tsv 2>$null
            if ($checkRole) { $hasCertRole = $true }
        }

        if (-not $hasCertRole) {
            Write-Info "Assigning 'Key Vault Certificates Officer' role to caller..."
            $accType = az account show --query "user.type" -o tsv 2>$null
            $assigneeType = if ($accType -eq "servicePrincipal") { "ServicePrincipal" } else { "User" }
            if ($currentUserId) {
                az role assignment create --role "Key Vault Certificates Officer" --assignee-object-id $currentUserId --assignee-principal-type $assigneeType --scope $kvId --output none 2>&1 | Out-Null
            } elseif ($currentAccount) {
                az role assignment create --role "Key Vault Certificates Officer" --assignee $currentAccount --scope $kvId --output none 2>&1 | Out-Null
            }
            Write-Info "Waiting 15 seconds for Azure RBAC propagation..."
            Start-Sleep -Seconds 15
        }
    }

    Write-Info "Creating new certificate version in Azure Key Vault..."
    $createSuccess = $false
    $createAttempts = 0
    $createOut = ""
    while (-not $createSuccess -and $createAttempts -lt 6) {
        $createAttempts++
        $createOut = az keyvault certificate create `
            --vault-name $KeyVaultName `
            --name "ingress-tls-cert" `
            --policy "@$($tempPolicyFile.FullName)" `
            --output json 2>&1
        if ($LASTEXITCODE -eq 0) {
            $createSuccess = $true
            break
        }
        if ($createOut -match "ForbiddenByRbac" -or $createOut -match "Forbidden") {
            Write-Info "Waiting for Azure RBAC propagation... (attempt $createAttempts/6)"
            Start-Sleep -Seconds 10
        } else {
            break
        }
    }
    if (-not $createSuccess) {
        throw "Failed to create new certificate version in Key Vault.`nDetails: $createOut"
    }

    $certObj = $createOut | ConvertFrom-Json
    $newThumbprint = $certObj.x509ThumbprintHex
    Write-Success "New certificate version generated in Azure Key Vault!"
    Write-Info "New SHA-1 Thumbprint: $newThumbprint"
} finally {
    Remove-Item -Path $tempPolicyFile.FullName -ErrorAction SilentlyContinue
}

Write-Step "Observing Automated DSO Canary & Promotion on AKS..."
Write-Info "Event Path: Azure Key Vault -> Event Grid -> Service Bus -> DSO AMQP Peek-Lock -> Canary Sandbox -> TLS Probe -> Production Promotion"

Write-Host @"

Watching DynamicSecretPolicy status (Press Ctrl+C to stop watching):
Command: kubectl get dynamicsecretpolicy aks-ingress-tls-policy -n dso-examples -w

Operator Logs command:
kubectl logs -n dso-system -l app.kubernetes.io/name=dynamic-secret-operator -f
"@ -ForegroundColor Yellow

$timeout = 60
$elapsed = 0
while ($elapsed -lt $timeout) {
    $policyJson = kubectl get dynamicsecretpolicy aks-ingress-tls-policy -n dso-examples -o json 2>$null
    if ($policyJson) {
        $p = $policyJson | ConvertFrom-Json
        $rev = $p.status.currentRevision
        $conds = $p.status.conditions
        $promoting = $conds | Where-Object { $_.type -eq "Promoting" -and $_.status -eq "True" }
        if ($promoting) {
            Write-Success "DSO promoted target workload to revision: $rev"
            break
        }
    }
    Start-Sleep -Seconds 3
    $elapsed += 3
}

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ Valid TLS Certificate Rotation successfully tested!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green
