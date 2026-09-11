#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy TLS Certificate Rotation Example on AKS
# ==============================================================================

set -euo pipefail

DOMAIN=""
KEYVAULT_NAME="kv-dso-dev-jc"
RESOURCE_GROUP=""

print_usage() {
    echo "Usage: $0 -d <DOMAIN> [-k <KEYVAULT_NAME>] [-g <RESOURCE_GROUP>]"
    echo "  -d    Target Domain Name (e.g., myapp.contoso.com)"
    echo "  -k    Azure Key Vault Name (default: kv-dso-dev-jc)"
    echo "  -g    Azure Resource Group Name (auto-detected if omitted)"
    exit 1
}

while getopts "d:k:g:h" opt; do
    case "${opt}" in
        d) DOMAIN="${OPTARG}" ;;
        k) KEYVAULT_NAME="${OPTARG}" ;;
        g) RESOURCE_GROUP="${OPTARG}" ;;
        h) print_usage ;;
        *) print_usage ;;
    esac
done

if [ -z "${DOMAIN}" ]; then
    echo "❌ Error: Domain (-d) is required."
    print_usage
fi

DNS_ZONE_NAME="${DOMAIN}"


export AZURE_EXTENSION_DIR="${AZURE_EXTENSION_DIR:-$HOME/.azure/ext}"
mkdir -p "${AZURE_EXTENSION_DIR}"

echo "=================================================================="
echo "🚀 Deploying TLS Certificate Rotation Example to AKS Cluster..."
echo "🌐 Target Domain:     ${DOMAIN}"
echo "🔑 Target Key Vault:  ${KEYVAULT_NAME}"
echo "=================================================================="

# 1. Check prerequisites
command -v kubectl >/dev/null 2>&1 || { echo "❌ Error: 'kubectl' is required."; exit 1; }
command -v az >/dev/null 2>&1 || { echo "❌ Error: 'az' Azure CLI is required."; exit 1; }

# 2. Check cluster connection
CURRENT_CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
if [ -z "${CURRENT_CONTEXT}" ]; then
    echo "❌ Error: Not connected to any Kubernetes cluster. Please run 'az aks get-credentials' first."
    exit 1
fi
echo "☸️  Using Kubernetes Context: ${CURRENT_CONTEXT}"

# 3. Verify Key Vault & resolve Resource Group
echo "🔑 Verifying access to Azure Key Vault '${KEYVAULT_NAME}'..."
KV_INFO="$(az keyvault show --name "${KEYVAULT_NAME}" -o json 2>/dev/null)" || {
    echo "❌ Error: Unable to access Key Vault '${KEYVAULT_NAME}'. Please verify name and permissions."
    exit 1
}

if [ -z "${RESOURCE_GROUP}" ]; then
    RESOURCE_GROUP="$(echo "${KV_INFO}" | grep -o '"resourceGroup": *"[^"]*"' | head -n1 | cut -d'"' -f4 || true)"
fi
echo "✅ Key Vault '${KEYVAULT_NAME}' verified in Resource Group '${RESOURCE_GROUP}'."

# 4. Automatically ensure caller has Key Vault Certificates Officer role
echo "🔑 Ensuring Azure RBAC role 'Key Vault Certificates Officer' for caller..."
CURRENT_ACCOUNT="$(az account show --query "user.name" -o tsv 2>/dev/null || true)"
CURRENT_USER_ID="$(az ad signed-in-user show --query id -o tsv 2>/dev/null || true)"
if [ -z "${CURRENT_USER_ID}" ] && [ -n "${CURRENT_ACCOUNT}" ]; then
    CURRENT_USER_ID="$(az ad user show --id "${CURRENT_ACCOUNT}" --query id -o tsv 2>/dev/null || true)"
    if [ -z "${CURRENT_USER_ID}" ]; then
        CURRENT_USER_ID="$(az ad sp show --id "${CURRENT_ACCOUNT}" --query id -o tsv 2>/dev/null || true)"
    fi
fi

KV_ID="$(echo "${KV_INFO}" | grep -o '"id": *"[^"]*"' | head -n1 | cut -d'"' -f4 || true)"
if [ -n "${KV_ID}" ]; then
    HAS_CERT_ROLE=false
    if [ -n "${CURRENT_USER_ID}" ]; then
        if az role assignment list --assignee "${CURRENT_USER_ID}" --scope "${KV_ID}" --role "Key Vault Certificates Officer" --query "[0].id" -o tsv 2>/dev/null | grep -q .; then
            HAS_CERT_ROLE=true
        fi
    fi
    if [ "${HAS_CERT_ROLE}" != "true" ] && [ -n "${CURRENT_ACCOUNT}" ]; then
        if az role assignment list --assignee "${CURRENT_ACCOUNT}" --scope "${KV_ID}" --role "Key Vault Certificates Officer" --query "[0].id" -o tsv 2>/dev/null | grep -q .; then
            HAS_CERT_ROLE=true
        fi
    fi

    if [ "${HAS_CERT_ROLE}" != "true" ]; then
        echo "ℹ️  Assigning 'Key Vault Certificates Officer' role on Key Vault '${KEYVAULT_NAME}'..."
        USER_TYPE="$(az account show --query "user.type" -o tsv 2>/dev/null || true)"
        ASSIGNEE_TYPE="User"
        if [ "${USER_TYPE}" = "servicePrincipal" ]; then
            ASSIGNEE_TYPE="ServicePrincipal"
        fi

        ROLE_CREATED=false
        if [ -n "${CURRENT_USER_ID}" ]; then
            if az role assignment create --role "Key Vault Certificates Officer" --assignee-object-id "${CURRENT_USER_ID}" --assignee-principal-type "${ASSIGNEE_TYPE}" --scope "${KV_ID}" --output none 2>/dev/null; then
                ROLE_CREATED=true
            fi
        fi
        if [ "${ROLE_CREATED}" != "true" ] && [ -n "${CURRENT_ACCOUNT}" ]; then
            if az role assignment create --role "Key Vault Certificates Officer" --assignee "${CURRENT_ACCOUNT}" --scope "${KV_ID}" --output none 2>/dev/null; then
                ROLE_CREATED=true
            fi
        fi

        if [ "${ROLE_CREATED}" = "true" ]; then
            echo "✅ Role 'Key Vault Certificates Officer' automatically assigned to caller."
            echo "⏳ Awaiting initial RBAC propagation (10s)..."
            sleep 10
        fi
    else
        echo "✅ Role 'Key Vault Certificates Officer' already assigned."
    fi
fi

# 5. Create or verify Azure DNS Zone
echo "🌐 Configuring Azure DNS Zone '${DNS_ZONE_NAME}' in Resource Group '${RESOURCE_GROUP}'..."
if ! az network dns zone show --resource-group "${RESOURCE_GROUP}" --name "${DNS_ZONE_NAME}" >/dev/null 2>&1; then
    echo "ℹ️  Creating Azure DNS Zone '${DNS_ZONE_NAME}'..."
    az network dns zone create --resource-group "${RESOURCE_GROUP}" --name "${DNS_ZONE_NAME}" -o none || {
        echo "❌ Error: Failed to create Azure DNS Zone '${DNS_ZONE_NAME}'."
        exit 1
    }
    echo "✅ Azure DNS Zone '${DNS_ZONE_NAME}' created."
else
    echo "✅ Azure DNS Zone '${DNS_ZONE_NAME}' exists and is accessible."
fi

# 5.1. Name Server delegation instructions & validation
echo ""
echo "=================================================================="
echo "⚠️  ACTION REQUIRED: UPDATE DOMAIN NAME SERVERS AT REGISTRAR"
echo "=================================================================="
echo "The DNS Zone '${DNS_ZONE_NAME}' has been configured in Azure DNS."
echo "You MUST update the Name Servers for domain '${DOMAIN}' at your domain"
echo "registrar (GoDaddy, Namecheap, Cloudflare, Hostinger, etc.)"
echo "to the following authoritative Azure DNS servers:"
echo ""
AZURE_NS="$(az network dns zone show --resource-group "${RESOURCE_GROUP}" --name "${DNS_ZONE_NAME}" --query "nameServers" -o tsv 2>/dev/null || true)"
for ns in ${AZURE_NS}; do
    echo "   📌 ${ns}"
done
echo ""
echo "⏳ Validating DNS delegation every 30 seconds for up to 5 minutes..."
echo "=================================================================="

MAX_WAIT=300
POLL_INTERVAL=30
ELAPSED=0
NS_DELEGATED=false

while [ $ELAPSED -lt $MAX_WAIT ]; do
    ATTEMPT=$(( (ELAPSED / POLL_INTERVAL) + 1 ))
    TOTAL_ATTEMPTS=$(( MAX_WAIT / POLL_INTERVAL ))
    echo "ℹ️  Attempt ${ATTEMPT}/${TOTAL_ATTEMPTS} (${ELAPSED}s/${MAX_WAIT}s): Verifying NS records for '${DOMAIN}'..."

    NS_OUTPUT=""
    if command -v dig >/dev/null 2>&1; then
        NS_OUTPUT="$(dig +short NS "${DOMAIN}" 2>/dev/null || true)"
        if ! echo "${NS_OUTPUT}" | grep -qi "azure-dns"; then
            NS_OUTPUT="$(dig +short NS "${DOMAIN}" @8.8.8.8 2>/dev/null || true)"
        fi
        if ! echo "${NS_OUTPUT}" | grep -qi "azure-dns"; then
            NS_OUTPUT="$(dig +short NS "${DOMAIN}" @1.1.1.1 2>/dev/null || true)"
        fi
    elif command -v host >/dev/null 2>&1; then
        NS_OUTPUT="$(host -t NS "${DOMAIN}" 2>/dev/null || true)"
    fi
    if [ -z "${NS_OUTPUT}" ] || ! echo "${NS_OUTPUT}" | grep -qi "azure-dns"; then
        NS_OUTPUT="$(nslookup -type=NS "${DOMAIN}" 8.8.8.8 2>/dev/null || true)"
    fi
    if [ -z "${NS_OUTPUT}" ] || ! echo "${NS_OUTPUT}" | grep -qi "azure-dns"; then
        NS_OUTPUT="$(nslookup -type=NS "${DOMAIN}" 2>/dev/null || true)"
    fi

    if echo "${NS_OUTPUT}" | grep -qi "azure-dns"; then
        NS_DELEGATED=true
        echo "✅ Name Servers successfully verified! '${DOMAIN}' is delegated to Azure DNS."
        break
    fi

    sleep $POLL_INTERVAL
    ELAPSED=$((ELAPSED + POLL_INTERVAL))
done

if [ "${NS_DELEGATED}" != "true" ]; then
    echo ""
    echo "=================================================================="
    echo "❌ TIMEOUT: Name Server delegation verification timed out (5 minutes)."
    echo "=================================================================="
    echo "The Name Servers for domain '${DOMAIN}' are not yet pointing to Azure DNS."
    echo ""
    echo "Authoritative Azure DNS Name Servers required:"
    for ns in ${AZURE_NS}; do
        echo "   📌 ${ns}"
    done
    echo ""
    echo "👉 WHAT TO DO NEXT:"
    echo "1. Log into your domain registrar control panel."
    echo "2. Update the Name Server (NS) records to the Azure DNS servers listed above."
    echo "3. Wait for global DNS propagation across the network."
    echo "4. Run this deployment script again to continue:"
    echo "   ./deploy-aks.sh -d \"${DOMAIN}\" -k \"${KEYVAULT_NAME}\""
    echo "=================================================================="
    exit 1
fi

# 6. Create or verify certificate in Azure Key Vault for the domain
echo "🔑 Checking certificate 'ingress-tls-cert' for '${DOMAIN}' in Key Vault '${KEYVAULT_NAME}'..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POLICY_TEMPLATE="${SCRIPT_DIR}/certificate-policy.json"
TMP_POLICY="$(mktemp)"
trap 'rm -f "${TMP_POLICY}"' EXIT

sed "s/\${DOMAIN}/${DOMAIN}/g" "${POLICY_TEMPLATE}" > "${TMP_POLICY}"

if ! az keyvault certificate show --vault-name "${KEYVAULT_NAME}" --name "ingress-tls-cert" >/dev/null 2>&1; then
    echo "ℹ️  Creating initial certificate 'ingress-tls-cert' (CN=${DOMAIN}) in Key Vault..."
    CREATE_SUCCESS=false
    for attempt in 1 2 3 4 5 6; do
        if az keyvault certificate create \
            --vault-name "${KEYVAULT_NAME}" \
            --name "ingress-tls-cert" \
            --policy "@${TMP_POLICY}" \
            --output none 2>&1; then
            CREATE_SUCCESS=true
            break
        fi
        echo "⏳ Waiting for Azure RBAC propagation... (attempt ${attempt}/6)"
        sleep 10
    done

    if [ "${CREATE_SUCCESS}" != "true" ]; then
        echo "❌ Error: Failed to create certificate in Key Vault."
        exit 1
    fi


    echo "⏳ Waiting for Key Vault certificate creation to complete..."
    TIMEOUT=60
    ELAPSED=0
    CERT_READY=false
    while [ $ELAPSED -lt $TIMEOUT ]; do
        STATUS="$(az keyvault certificate show --vault-name "${KEYVAULT_NAME}" --name "ingress-tls-cert" --query "attributes.enabled" -o tsv 2>/dev/null || true)"
        if [ "${STATUS}" = "true" ]; then
            CERT_READY=true
            break
        fi
        sleep 2
        ELAPSED=$((ELAPSED + 2))
    done
    if [ "${CERT_READY}" != "true" ]; then
        echo "❌ Error: Timed out waiting for certificate 'ingress-tls-cert' in Key Vault."
        exit 1
    fi
    echo "✅ Initial certificate created in Key Vault for '${DOMAIN}'."
else
    echo "ℹ️  Certificate 'ingress-tls-cert' already exists in Key Vault."
fi

# 6. Ensure target namespace and apply bootstrap secret
echo "📦 Ensuring namespace 'dso-examples' exists..."
kubectl create namespace dso-examples --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "🔒 Creating bootstrap TLS secret for '${DOMAIN}' in cluster..."
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}" "${TMP_POLICY}"' EXIT

openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
    -keyout "${TMP_DIR}/tls.key" \
    -out "${TMP_DIR}/tls.crt" \
    -subj "/CN=${DOMAIN}/O=DynamicSecretOperator" \
    -addext "subjectAltName=DNS:${DOMAIN},DNS:localhost" \
    >/dev/null 2>&1 || {
    openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
        -keyout "${TMP_DIR}/tls.key" \
        -out "${TMP_DIR}/tls.crt" \
        -subj "/CN=${DOMAIN}/O=DynamicSecretOperator" >/dev/null 2>&1
}

kubectl create secret tls tls-gateway-ingress-tls-cert-initial \
    --namespace dso-examples \
    --cert="${TMP_DIR}/tls.crt" \
    --key="${TMP_DIR}/tls.key" \
    --dry-run=client -o yaml | kubectl apply -f - || { echo "❌ Error: Failed to create bootstrap TLS secret."; exit 1; }

REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
if [ -d "${REPO_ROOT}/config/crd/bases" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/config/crd/bases" || true
    echo "✅ CRD applied."
fi

# 7. Apply manifests
echo "📄 Deploying Nginx TLS Gateway and DynamicSecretPolicy manifests..."
sed -e "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" -e "s/\${DOMAIN}/${DOMAIN}/g" "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f - || {
    echo "❌ Error: Failed to apply manifests."
    exit 1
}

echo "⏳ Waiting for TLS Gateway deployment to be ready..."
kubectl rollout status deployment/tls-gateway -n dso-examples --timeout=120s || {
    echo "❌ Error: TLS Gateway rollout failed or timed out."
    exit 1
}

# 8. Check and register Public LoadBalancer IP in Azure DNS
echo "🔍 Retrieving Public LoadBalancer IP for tls-gateway..."
EXT_IP=""
MAX_WAIT=60
WAITED=0
while [ $WAITED -lt $MAX_WAIT ]; do
    EXT_IP="$(kubectl get svc tls-gateway -n dso-examples -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)"
    if [ -n "${EXT_IP}" ]; then
        break
    fi
    echo "ℹ️  Waiting for Azure LoadBalancer IP assignment... (${WAITED}/${MAX_WAIT} s)"
    sleep 5
    WAITED=$((WAITED + 5))
done

if [ -n "${EXT_IP}" ]; then
    echo "✅ LoadBalancer Public IP assigned: ${EXT_IP}"
    echo "🌐 Registering A record '@' in Azure DNS Zone '${DNS_ZONE_NAME}' -> ${EXT_IP}..."
    az network dns record-set a add-record \
        --resource-group "${RESOURCE_GROUP}" \
        --zone-name "${DNS_ZONE_NAME}" \
        --record-set-name "@" \
        --ipv4-address "${EXT_IP}" \
        --ttl 300 -o none 2>/dev/null && echo "✅ DNS A record '@' registered successfully." || echo "ℹ️  Note: DNS A record could not be set automatically."
else
    echo "ℹ️  LoadBalancer Public IP is still pending in Azure."
fi

echo "=================================================================="
echo "✅ TLS Certificate Rotation Example deployed successfully on AKS!"
echo "=================================================================="
echo ""
echo "📋 VERIFICATION & ROTATION TESTING GUIDE:"
echo "------------------------------------------------------------------"
echo "1️⃣ Test HTTPS Endpoint:"
echo "   curl -kv --resolve \"${DOMAIN}:8443:${EXT_IP:-127.0.0.1}\" https://${DOMAIN}:8443"
echo ""
echo "2️⃣ Test VALID Certificate Rotation (Canary Rollout):"
echo "   ./rotate-cert.sh -d \"${DOMAIN}\" -k \"${KEYVAULT_NAME}\""
echo ""
echo "3️⃣ Test INVALID Certificate Rotation (Circuit Breaker Protection):"
echo "   ./simulate-invalid-cert.sh -d \"${DOMAIN}\" -k \"${KEYVAULT_NAME}\""
echo "=================================================================="
