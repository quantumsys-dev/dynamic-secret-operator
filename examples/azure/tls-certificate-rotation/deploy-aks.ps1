# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy TLS Certificate Rotation Example on AKS
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [Alias("d")]
    [string]$Domain,

    [Parameter(Mandatory = $true, Position = 1)]
    [Alias("k")]
    [string]$KeyVaultName,

    [Parameter(Mandatory = $false, Position = 2)]
    [Alias("g")]
    [string]$ResourceGroupName = ""
)


$ErrorActionPreference = "Stop"

# Ensure Azure CLI extension directory doesn't hit broken extensions
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

Write-Step "Deploying TLS Certificate Rotation Example to AKS Cluster..."
Write-Info "Target Domain:     $Domain"
Write-Info "Target Key Vault:  $KeyVaultName"

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

# 3. Check Key Vault accessibility & resolve Resource Group
Write-Step "Verifying access to Azure Key Vault '$KeyVaultName'..."
$kvInfoRaw = az keyvault show --name $KeyVaultName 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "Unable to access Key Vault '$KeyVaultName'. Please verify the name and your Azure permissions.`nDetails: $kvInfoRaw"
}
$kvInfo = $kvInfoRaw | ConvertFrom-Json
if (-not $ResourceGroupName) {
    $ResourceGroupName = $kvInfo.resourceGroup
}
Write-Success "Key Vault '$KeyVaultName' verified in Resource Group '$ResourceGroupName'."

# 4. Automatically ensure caller has Key Vault Certificates Officer role
Write-Step "Ensuring Azure RBAC role 'Key Vault Certificates Officer' for caller..."
$currentAccount = az account show --query "user.name" -o tsv 2>$null
$currentUserId = az ad signed-in-user show --query id -o tsv 2>$null
if (-not $currentUserId -and $currentAccount) {
    $currentUserId = az ad user show --id $currentAccount --query id -o tsv 2>$null
    if (-not $currentUserId) {
        $currentUserId = az ad sp show --id $currentAccount --query id -o tsv 2>$null
    }
}

if ($kvInfo.id) {
    $hasCertRole = $false
    if ($currentUserId) {
        $checkRole = az role assignment list --assignee $currentUserId --scope $kvInfo.id --role "Key Vault Certificates Officer" --query "[0].id" -o tsv 2>$null
        if ($checkRole) { $hasCertRole = $true }
    }
    if (-not $hasCertRole -and $currentAccount) {
        $checkRole = az role assignment list --assignee $currentAccount --scope $kvInfo.id --role "Key Vault Certificates Officer" --query "[0].id" -o tsv 2>$null
        if ($checkRole) { $hasCertRole = $true }
    }

    if (-not $hasCertRole) {
        Write-Info "Assigning 'Key Vault Certificates Officer' role on Key Vault '$KeyVaultName'..."
        $accType = az account show --query "user.type" -o tsv 2>$null
        $assigneeType = if ($accType -eq "servicePrincipal") { "ServicePrincipal" } else { "User" }
        
        $roleCreated = $false
        if ($currentUserId) {
            az role assignment create `
                --role "Key Vault Certificates Officer" `
                --assignee-object-id $currentUserId `
                --assignee-principal-type $assigneeType `
                --scope $kvInfo.id `
                --output none 2>&1 | Out-Null
            if ($LASTEXITCODE -eq 0) { $roleCreated = $true }
        }
        if (-not $roleCreated -and $currentAccount) {
            az role assignment create `
                --role "Key Vault Certificates Officer" `
                --assignee $currentAccount `
                --scope $kvInfo.id `
                --output none 2>&1 | Out-Null
            if ($LASTEXITCODE -eq 0) { $roleCreated = $true }
        }

        if ($roleCreated) {
            Write-Success "Role 'Key Vault Certificates Officer' automatically assigned to caller."
            Write-Info "Awaiting initial RBAC propagation (10s)..."
            Start-Sleep -Seconds 10
        } else {
            Write-Info "Role assignment command finished. Proceeding..."
        }
    } else {
        Write-Success "Role 'Key Vault Certificates Officer' already assigned."
    }
}

# 5. Create or verify Azure DNS Zone
$DnsZoneName = $Domain

Write-Step "Configuring Azure DNS Zone '$DnsZoneName' in Resource Group '$ResourceGroupName'..."
$dnsCheck = az network dns zone show --resource-group $ResourceGroupName --name $DnsZoneName 2>$null
if (-not $dnsCheck) {
    Write-Info "Creating Azure DNS Zone '$DnsZoneName'..."
    $createDnsOut = az network dns zone create --resource-group $ResourceGroupName --name $DnsZoneName 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create Azure DNS Zone '$DnsZoneName'.`nDetails: $createDnsOut"
    }
    Write-Success "Azure DNS Zone '$DnsZoneName' created."
} else {
    Write-Success "Azure DNS Zone '$DnsZoneName' exists and is accessible."
}

# 5.1. Name Server delegation instructions & validation
$nsJson = az network dns zone show --resource-group $ResourceGroupName --name $DnsZoneName --query "nameServers" -o json 2>$null
$nsList = @()
if ($nsJson) { $nsList = $nsJson | ConvertFrom-Json }

Write-Host "`n==================================================================" -ForegroundColor Yellow
Write-Host "⚠️  ACTION REQUIRED: UPDATE DOMAIN NAME SERVERS AT REGISTRAR" -ForegroundColor Yellow
Write-Host "==================================================================" -ForegroundColor Yellow
Write-Host "The DNS Zone '$DnsZoneName' has been created/verified in Azure DNS." -ForegroundColor White
Write-Host "You MUST update the Name Servers for domain '$Domain' at your domain" -ForegroundColor White
Write-Host "registrar (e.g. GoDaddy, Namecheap, Cloudflare, Hostinger, etc.)" -ForegroundColor White
Write-Host "to the following authoritative Azure DNS servers:" -ForegroundColor White
Write-Host ""
foreach ($ns in $nsList) {
    Write-Host "   📌 $ns" -ForegroundColor Cyan
}
Write-Host ""
Write-Host "⏳ Validating DNS delegation every 30 seconds for up to 5 minutes..." -ForegroundColor Yellow
Write-Host "==================================================================" -ForegroundColor Yellow

$maxWaitSeconds = 300 # 5 minutes
$pollInterval = 30
$elapsed = 0
$nsDelegated = $false

while ($elapsed -lt $maxWaitSeconds) {
    $attemptNumber = [math]::Floor($elapsed / $pollInterval) + 1
    $totalAttempts = [math]::Floor($maxWaitSeconds / $pollInterval)
    Write-Info "Attempt $attemptNumber/$totalAttempts (${elapsed}s/${maxWaitSeconds}s): Verifying NS records for '$Domain'..."
    
    $foundAzureNs = $false

    # 1. Check with Resolve-DnsName locally and with public resolvers
    if (Get-Command Resolve-DnsName -ErrorAction SilentlyContinue) {
        # Check local resolution
        $resolved = Resolve-DnsName -Name $Domain -Type NS -ErrorAction SilentlyContinue
        if ($resolved) {
            foreach ($r in $resolved) {
                if ($r.NameHost -match "azure-dns") {
                    $foundAzureNs = $true
                    break
                }
            }
        }
        # Check Google DNS directly (bypasses stale local cache)
        if (-not $foundAzureNs) {
            $resolvedPublic = Resolve-DnsName -Name $Domain -Type NS -Server "8.8.8.8" -ErrorAction SilentlyContinue
            if ($resolvedPublic) {
                foreach ($r in $resolvedPublic) {
                    if ($r.NameHost -match "azure-dns") {
                        $foundAzureNs = $true
                        break
                    }
                }
            }
        }
        # Check Cloudflare DNS directly
        if (-not $foundAzureNs) {
            $resolvedCf = Resolve-DnsName -Name $Domain -Type NS -Server "1.1.1.1" -ErrorAction SilentlyContinue
            if ($resolvedCf) {
                foreach ($r in $resolvedCf) {
                    if ($r.NameHost -match "azure-dns") {
                        $foundAzureNs = $true
                        break
                    }
                }
            }
        }
    }
    
    # 2. Fallback using nslookup (public and default)
    if (-not $foundAzureNs) {
        $nslookupOut = nslookup -type=NS $Domain 8.8.8.8 2>&1 | Out-String
        if ($nslookupOut -match "azure-dns") {
            $foundAzureNs = $true
        }
    }
    if (-not $foundAzureNs) {
        $nslookupOut = nslookup -type=NS $Domain 2>&1 | Out-String
        if ($nslookupOut -match "azure-dns") {
            $foundAzureNs = $true
        }
    }

    if ($foundAzureNs) {
        $nsDelegated = $true
        Write-Success "Name Servers successfully verified! '$Domain' is delegated to Azure DNS."
        break
    }

    Start-Sleep -Seconds $pollInterval
    $elapsed += $pollInterval
}

if (-not $nsDelegated) {
    Write-Host "`n==================================================================" -ForegroundColor Red
    Write-Host "❌ TIMEOUT: Name Server delegation verification timed out (5 minutes)." -ForegroundColor Red
    Write-Host "==================================================================" -ForegroundColor Red
    Write-Host "The Name Servers for domain '$Domain' are not yet pointing to Azure DNS." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "Authoritative Azure DNS Name Servers required:" -ForegroundColor White
    foreach ($ns in $nsList) {
        Write-Host "   📌 $ns" -ForegroundColor Cyan
    }
    Write-Host @"

👉 WHAT TO DO NEXT:
1. Log into your domain registrar control panel.
2. Update the Name Server (NS) records to the Azure DNS servers listed above.
3. Wait for global DNS propagation across the network.
4. Run this deployment script again to continue:
   .\deploy-aks.ps1 -d "$Domain" -k "$KeyVaultName" -g "$ResourceGroupName"
==================================================================
"@ -ForegroundColor Yellow
    throw "Please update the Name Servers at your domain registrar to the Azure DNS servers listed above, wait for propagation, and try again."
}

# 6. Create or verify certificate in Azure Key Vault for the domain
Write-Step "Checking certificate 'ingress-tls-cert' for '$Domain' in Key Vault '$KeyVaultName'..."
$policyTemplatePath = Join-Path $PSScriptRoot "certificate-policy.json"
if (-not (Test-Path $policyTemplatePath)) {
    throw "Policy template not found at $policyTemplatePath"
}
$policyContent = Get-Content $policyTemplatePath -Raw
$policyContent = $policyContent -replace '\$\{DOMAIN\}', $Domain

$tempPolicyFile = New-TemporaryFile
Set-Content -Path $tempPolicyFile.FullName -Value $policyContent
try {

    $certCheck = az keyvault certificate show --vault-name $KeyVaultName --name "ingress-tls-cert" 2>$null
    if (-not $certCheck) {
        Write-Info "Creating initial certificate 'ingress-tls-cert' (CN=$Domain) in Key Vault..."
        
        $createSuccess = $false
        $createAttempts = 0
        $createCertOut = ""
        while (-not $createSuccess -and $createAttempts -lt 6) {
            $createAttempts++
            $createCertOut = az keyvault certificate create `
                --vault-name $KeyVaultName `
                --name "ingress-tls-cert" `
                --policy "@$($tempPolicyFile.FullName)" `
                --output none 2>&1
            if ($LASTEXITCODE -eq 0) {
                $createSuccess = $true
                break
            }
            if ($createCertOut -match "ForbiddenByRbac" -or $createCertOut -match "Forbidden") {
                Write-Info "Waiting for Azure RBAC propagation... (attempt $createAttempts/6)"
                Start-Sleep -Seconds 10
            } else {
                break
            }
        }
        if (-not $createSuccess) {
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
        Write-Success "Initial certificate created in Key Vault for '$Domain'."
    } else {
        Write-Info "Certificate 'ingress-tls-cert' already exists in Key Vault."
    }
} finally {
    Remove-Item -Path $tempPolicyFile.FullName -ErrorAction SilentlyContinue
}

# 6. Ensure target namespace exists and apply bootstrap TLS secret
Write-Step "Ensuring namespace 'dso-examples' exists..."
$nsOut = kubectl create namespace dso-examples --dry-run=client -o yaml | kubectl apply -f - 2>&1
if ($LASTEXITCODE -ne 0) { throw "Failed to ensure namespace 'dso-examples'.`nDetails: $nsOut" }
Write-Success "Namespace 'dso-examples' ready."

Write-Step "Creating bootstrap TLS secret for '$Domain' in cluster..."
$rsa = [System.Security.Cryptography.RSA]::Create(2048)
$req = [System.Security.Cryptography.X509Certificates.CertificateRequest]::new("CN=$Domain", $rsa, [System.Security.Cryptography.HashAlgorithmName]::SHA256, [System.Security.Cryptography.RSASignaturePadding]::Pkcs1)
$sanBuilder = [System.Security.Cryptography.X509Certificates.SubjectAlternativeNameBuilder]::new()
$sanBuilder.AddDnsName($Domain)
$sanBuilder.AddDnsName("localhost")
$req.CertificateExtensions.Add($sanBuilder.Build())

$cert = $req.CreateSelfSigned([DateTimeOffset]::UtcNow.AddDays(-1), [DateTimeOffset]::UtcNow.AddYears(1))
$certPem = $cert.ExportCertificatePem()
$keyPem = $rsa.ExportPkcs8PrivateKeyPem()

$certFile = New-TemporaryFile
$keyFile = New-TemporaryFile
try {
    Set-Content -Path $certFile.FullName -Value $certPem
    Set-Content -Path $keyFile.FullName -Value $keyPem
    $secretOut = kubectl create secret tls tls-gateway-ingress-tls-cert-initial `
        --namespace dso-examples `
        --cert=$certFile.FullName `
        --key=$keyFile.FullName `
        --dry-run=client -o yaml | kubectl apply -f - 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create bootstrap TLS secret.`nDetails: $secretOut"
    }
} finally {
    Remove-Item -Path $certFile.FullName -ErrorAction SilentlyContinue
    Remove-Item -Path $keyFile.FullName -ErrorAction SilentlyContinue
}
Write-Success "Bootstrap TLS secret created."

$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "../../..") -ErrorAction SilentlyContinue
if ($RepoRoot -and (Test-Path (Join-Path $RepoRoot "config/crd/bases"))) {
    Write-Step "Applying DynamicSecretPolicy CRD from repo..."
    $crdOut = kubectl apply --server-side --force-conflicts -f (Join-Path $RepoRoot "config/crd/bases") 2>&1
    if ($LASTEXITCODE -ne 0) { throw "Failed to apply CRD.`nDetails: $crdOut" }
    Write-Success "CRD applied."
}

# 7. Apply manifests with Key Vault and Domain replacement
Write-Step "Deploying Nginx TLS Gateway and DynamicSecretPolicy manifests..."
$manifestPath = Join-Path $PSScriptRoot "manifests.yaml"
if (-not (Test-Path $manifestPath)) {
    throw "Manifest file not found: $manifestPath"
}
$manifestContent = Get-Content $manifestPath -Raw
$manifestContent = $manifestContent -replace '\$\{KEYVAULT_NAME\}', $KeyVaultName
$manifestContent = $manifestContent -replace '\$\{DOMAIN\}', $Domain

$applyOut = $manifestContent | kubectl apply -f - 2>&1
Write-Host $applyOut
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply manifests.`nDetails: $applyOut"
}

# 8. Wait for deployment to be ready
Write-Info "Waiting for TLS Gateway deployment to become ready..."
$rolloutOut = kubectl rollout status deployment/tls-gateway -n dso-examples --timeout=120s 2>&1
Write-Host $rolloutOut
if ($LASTEXITCODE -ne 0) {
    throw "TLS Gateway rollout failed or timed out.`nDetails: $rolloutOut"
}

# 9. Check and register Public LoadBalancer IP in Azure DNS
Write-Step "Retrieving Public LoadBalancer IP for tls-gateway..."
$extIp = $null
$maxWait = 60
$waited = 0
while ($waited -lt $maxWait) {
    $svcJson = kubectl get svc tls-gateway -n dso-examples -o json 2>$null
    if ($svcJson) {
        $svcInfo = $svcJson | ConvertFrom-Json
        if ($svcInfo.status.loadBalancer.ingress -and $svcInfo.status.loadBalancer.ingress.Count -gt 0) {
            $extIp = $svcInfo.status.loadBalancer.ingress[0].ip
            if ($extIp) { break }
        }
    }
    Write-Info "Waiting for Azure LoadBalancer IP assignment... ($waited/$maxWait s)"
    Start-Sleep -Seconds 5
    $waited += 5
}

if ($extIp) {
    Write-Success "LoadBalancer Public IP assigned: $extIp"
    Write-Step "Registering A record '@' in Azure DNS Zone '$DnsZoneName' -> $extIp..."
    $recordOut = az network dns record-set a add-record `
        --resource-group $ResourceGroupName `
        --zone-name $DnsZoneName `
        --record-set-name "@" `
        --ipv4-address $extIp `
        --ttl 300 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Success "DNS A record '@' created in zone '$DnsZoneName' pointing to $extIp."
    } else {
        Write-Info "Notice: Could not automatically set A record. Details: $recordOut"
    }
} else {
    Write-Info "LoadBalancer Public IP is still pending in Azure. You can register the A record once ready using:"
    Write-Info "az network dns record-set a add-record -g $ResourceGroupName -z $DnsZoneName -n '@' --ipv4-address <EXTERNAL-IP>"
}

Write-Host "`n==================================================================" -ForegroundColor Green
Write-Host "✅ TLS Certificate Rotation Example deployed successfully on AKS!" -ForegroundColor Green
Write-Host "==================================================================" -ForegroundColor Green

Write-Host @"

📋 VERIFICATION & ROTATION TESTING GUIDE:
------------------------------------------------------------------

1️⃣ Test HTTPS Endpoint:
   - Using your Domain (with DNS or host resolution):
     curl -kv --resolve "${Domain}:8443:${extIp}" https://${Domain}:8443

   - Using Port-Forwarding (Local Fallback):
     kubectl port-forward svc/tls-gateway 8443:8443 -n dso-examples
     curl -kv --resolve "${Domain}:8443:127.0.0.1" https://${Domain}:8443

2️⃣ Test VALID Certificate Rotation (Canary Rollout):
   PowerShell:
     .\rotate-cert.ps1 -Domain "$Domain" -KeyVaultName "$KeyVaultName"
   Bash:
     ./rotate-cert.sh -d "$Domain" -k "$KeyVaultName"

3️⃣ Test INVALID Certificate Rotation (Circuit Breaker Protection):
   PowerShell:
     .\simulate-invalid-cert.ps1 -Domain "$Domain" -KeyVaultName "$KeyVaultName"
   Bash:
     ./simulate-invalid-cert.sh -d "$Domain" -k "$KeyVaultName"

==================================================================
"@ -ForegroundColor Cyan
