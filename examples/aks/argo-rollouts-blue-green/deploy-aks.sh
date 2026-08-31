#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Argo Rollouts Blue/Green Example on AKS
# ==============================================================================

set -euo pipefail

KEYVAULT_NAME=""
ARGO_ROLLOUTS_VERSION="v1.7.2"

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
echo "🚀 Deploying Argo Rollouts Blue/Green Example to AKS Cluster..."
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

# 4. Seed initial secret in Azure Key Vault if not exists
echo "🔑 Checking secret 'payment-db-password' in Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault secret show --vault-name "${KEYVAULT_NAME}" --name "payment-db-password" >/dev/null 2>&1; then
    echo "ℹ️  Creating initial secret 'payment-db-password' in Key Vault..."
    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "payment-db-password" \
        --value "initial-database-password-v1" \
        --output none || { echo "❌ Error: Failed to create secret 'payment-db-password' in Key Vault '${KEYVAULT_NAME}'."; exit 1; }
    echo "✅ Initial secret 'payment-db-password' seeded in Key Vault."
else
    echo "ℹ️  Secret 'payment-db-password' already exists in Key Vault."
fi

# 5. Install Argo Rollouts controller if not present
if ! kubectl get namespace argo-rollouts >/dev/null 2>&1; then
    echo "📦 Installing Argo Rollouts (${ARGO_ROLLOUTS_VERSION}) on AKS..."
    kubectl create namespace argo-rollouts --dry-run=client -o yaml | kubectl apply -f - || { echo "❌ Error: Failed to create namespace 'argo-rollouts'."; exit 1; }
    kubectl apply -n argo-rollouts -f "https://github.com/argoproj/argo-rollouts/releases/download/${ARGO_ROLLOUTS_VERSION}/install.yaml" || { echo "❌ Error: Failed to install Argo Rollouts controller."; exit 1; }
    echo "⏳ Waiting for Argo Rollouts controller to become ready..."
    kubectl rollout status deployment/argo-rollouts -n argo-rollouts --timeout=120s || { echo "❌ Error: Argo Rollouts rollout failed or timed out."; exit 1; }
else
    echo "ℹ️  Argo Rollouts namespace already exists on cluster."
fi

# 6. Install DSO CRD if not present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply -k "${REPO_ROOT}/config/crd" || { echo "❌ Error: Failed to apply DynamicSecretPolicy CRD."; exit 1; }
fi

# 7. Apply manifests with Key Vault replacement
echo "📄 Deploying Rollout, Services, and DynamicSecretPolicy manifests..."
if [ ! -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    echo "❌ Error: Manifest file not found at ${SCRIPT_DIR}/manifests.yaml"
    exit 1
fi
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f - || { echo "❌ Error: Failed to apply manifests."; exit 1; }

echo "=================================================================="
echo "✅ Argo Rollouts Blue/Green Example deployed successfully on AKS!"
echo ""
echo "Next Steps:"
echo "1. Watch the rollout: kubectl argo rollouts get rollout rollout-payment-service --watch"
echo "2. Trigger a secret rotation in Key Vault:"
echo "   az keyvault secret set --vault-name ${KEYVAULT_NAME} --name 'payment-db-password' --value 'NewPaymentPassword2026_Rotated!'"
echo "=================================================================="
