#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy TLS Certificate Rotation Example on AKS
# ==============================================================================

set -euo pipefail

KEYVAULT_NAME=""

print_usage() {
    echo "Usage: $0 -k <KEYVAULT_NAME>"
    echo "  -k    Name of the Azure Key Vault (e.g., kv-dso-dev)"
    exit 1
}

while getopts "k:h" opt; do
    case "${opt}" in
        k) KEYVAULT_NAME="${OPTARG}" ;;
        h) print_usage ;;
        *) print_usage ;;
    esac
done

if [ -z "${KEYVAULT_NAME}" ]; then
    echo "❌ Error: Key Vault name is required."
    print_usage
fi

echo "=================================================================="
echo "🚀 Deploying TLS Certificate Rotation Example to AKS Cluster..."
echo "🔑 Target Key Vault: ${KEYVAULT_NAME}"
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

# 3. Verify Key Vault accessibility
echo "🔑 Verifying access to Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault show --name "${KEYVAULT_NAME}" >/dev/null 2>&1; then
    echo "❌ Error: Unable to access Key Vault '${KEYVAULT_NAME}'. Please verify the name and your Azure permissions."
    exit 1
fi
echo "✅ Key Vault '${KEYVAULT_NAME}' verified."

# 4. Create or verify certificate in Azure Key Vault
echo "🔑 Checking certificate 'ingress-tls-cert' in Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault certificate show --vault-name "${KEYVAULT_NAME}" --name "ingress-tls-cert" >/dev/null 2>&1; then
    echo "ℹ️  Creating initial self-signed certificate 'ingress-tls-cert' in Key Vault..."
    az keyvault certificate create \
        --vault-name "${KEYVAULT_NAME}" \
        --name "ingress-tls-cert" \
        --policy "$(az keyvault certificate get-default-policy)" \
        --output none || { echo "❌ Error: Failed to create certificate 'ingress-tls-cert' in Key Vault '${KEYVAULT_NAME}'."; exit 1; }
    echo "⏳ Waiting for Key Vault certificate creation to finish..."
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
        echo "❌ Error: Timed out waiting for certificate 'ingress-tls-cert' to become ready in Key Vault."
        exit 1
    fi
    echo "✅ Initial certificate created in Key Vault."
else
    echo "ℹ️  Certificate 'ingress-tls-cert' already exists in Key Vault."
fi

# 5. Generate initial placeholder secret in cluster for initial bootstrap
echo "🔒 Creating bootstrap TLS secret in cluster..."
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
    -keyout "${TMP_DIR}/tls.key" \
    -out "${TMP_DIR}/tls.crt" \
    -subj "/CN=localhost/O=DynamicSecretOperator" \
    >/dev/null 2>&1 || { echo "❌ Error: Failed to generate bootstrap self-signed TLS cert."; exit 1; }

kubectl create secret tls tls-gateway-ingress-tls-cert-initial \
    --cert="${TMP_DIR}/tls.crt" \
    --key="${TMP_DIR}/tls.key" \
    --dry-run=client -o yaml | kubectl apply -f - || { echo "❌ Error: Failed to create bootstrap TLS secret."; exit 1; }

# 6. Install DSO CRD if not present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply -k "${REPO_ROOT}/config/crd" || { echo "❌ Error: Failed to apply DynamicSecretPolicy CRD."; exit 1; }
fi

# 7. Apply manifests with Key Vault replacement
echo "📄 Deploying Nginx TLS Gateway and DynamicSecretPolicy manifests..."
if [ ! -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    echo "❌ Error: Manifest file not found at ${SCRIPT_DIR}/manifests.yaml"
    exit 1
fi
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f - || { echo "❌ Error: Failed to apply manifests."; exit 1; }

echo "⏳ Waiting for TLS Gateway deployment to be ready..."
kubectl rollout status deployment/tls-gateway --timeout=120s || { echo "❌ Error: TLS Gateway rollout failed or timed out."; exit 1; }

echo "=================================================================="
echo "✅ TLS Certificate Rotation Example deployed successfully on AKS!"
echo ""
echo "Next Steps:"
echo "1. Port-forward the HTTPS gateway: kubectl port-forward svc/tls-gateway 8443:8443"
echo "2. Query endpoint: curl -k https://localhost:8443"
echo "3. Trigger a certificate rotation in Key Vault:"
echo "   az keyvault certificate create --vault-name ${KEYVAULT_NAME} --name 'ingress-tls-cert' --policy \"\$(az keyvault certificate get-default-policy)\""
echo "=================================================================="
