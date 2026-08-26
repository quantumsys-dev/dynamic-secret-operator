#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Nginx Color Rotation Example on AKS
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
echo "🚀 Deploying Nginx Color Rotation Example to AKS Cluster..."
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

# 3. Seed initial secret in Azure Key Vault if not exists
echo "🔑 Checking secret 'nginx-bg-color' in Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault secret show --vault-name "${KEYVAULT_NAME}" --name "nginx-bg-color" >/dev/null 2>&1; then
    echo "ℹ️  Creating initial secret 'nginx-bg-color' in Key Vault..."
    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "nginx-bg-color" \
        --value "#3b82f6" \
        --output none
    echo "✅ Initial secret 'nginx-bg-color' seeded in Key Vault."
else
    echo "ℹ️  Secret 'nginx-bg-color' already exists in Key Vault."
fi

# 4. Install DSO CRD if not present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply -k "${REPO_ROOT}/config/crd"
fi

# 5. Apply manifests with Key Vault replacement
echo "📄 Deploying Nginx Color App and DynamicSecretPolicy manifests..."
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f -

echo "⏳ Waiting for Nginx Color App deployment to be ready..."
kubectl rollout status deployment/nginx-color-app --timeout=120s

echo "=================================================================="
echo "✅ Nginx Color Rotation Example deployed successfully on AKS!"
echo ""
echo "Next Steps:"
echo "1. Port-forward the app: kubectl port-forward svc/nginx-color-app 8080:80"
echo "2. Open http://localhost:8080"
echo "3. Trigger a rotation in Key Vault:"
echo "   az keyvault secret set --vault-name ${KEYVAULT_NAME} --name 'nginx-bg-color' --value '#10b981'"
echo "=================================================================="
