#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Multi-Secret Rotation Demo on AKS
# ==============================================================================

set -euo pipefail

KEYVAULT_NAME=""
NAMESPACE="multi-secret-demo"

print_usage() {
    echo "Usage: $0 -k <KEYVAULT_NAME> [-n <NAMESPACE>]"
    echo "  -k    Name of the Azure Key Vault (e.g., kv-dso-dev)   [required]"
    echo "  -n    Target Kubernetes namespace                        [default: multi-secret-demo]"
    exit 1
}

while getopts "k:n:h" opt; do
    case "${opt}" in
        k) KEYVAULT_NAME="${OPTARG}" ;;
        n) NAMESPACE="${OPTARG}" ;;
        h) print_usage ;;
        *) print_usage ;;
    esac
done

if [ -z "${KEYVAULT_NAME}" ]; then
    echo "❌ Error: Key Vault name is required."
    print_usage
fi

echo "=================================================================="
echo "🚀 Deploying Multi-Secret Microservice Example to AKS Cluster..."
echo "🔑 Target Key Vault : ${KEYVAULT_NAME}"
echo "📦 Target Namespace : ${NAMESPACE}"
echo "=================================================================="

# 1. Check prerequisites
command -v kubectl >/dev/null 2>&1 || { echo "❌ Error: 'kubectl' is required."; exit 1; }
command -v az     >/dev/null 2>&1 || { echo "❌ Error: 'az' Azure CLI is required."; exit 1; }

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

# 4. Ensure target namespace exists
echo "📦 Ensuring namespace '${NAMESPACE}' exists..."
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null || { echo "❌ Error: Failed to create namespace '${NAMESPACE}'."; exit 1; }
echo "✅ Namespace '${NAMESPACE}' ready."

# 5. Seed all 3 secrets in Azure Key Vault
declare -A SECRETS=(
    ["db-password"]="InitialPsqlPass123!"
    ["redis-auth-token"]="InitialRedisToken456!"
    ["payment-api-key"]="sk_live_pay_9876543210"
)

echo "🔑 Checking & seeding initial secrets in Azure Key Vault '${KEYVAULT_NAME}'..."
for SECRET_NAME in "${!SECRETS[@]}"; do
    SECRET_VALUE="${SECRETS[$SECRET_NAME]}"
    if ! az keyvault secret show --vault-name "${KEYVAULT_NAME}" --name "${SECRET_NAME}" >/dev/null 2>&1; then
        echo "ℹ️  Secret '${SECRET_NAME}' not found. Creating in Key Vault..."
        az keyvault secret set \
            --vault-name "${KEYVAULT_NAME}" \
            --name "${SECRET_NAME}" \
            --value "${SECRET_VALUE}" \
            --output none || { echo "❌ Error: Failed to create secret '${SECRET_NAME}' in Key Vault '${KEYVAULT_NAME}'."; exit 1; }
        echo "✅ Secret '${SECRET_NAME}' created in Key Vault."
    else
        echo "ℹ️  Secret '${SECRET_NAME}' already exists in Key Vault."
    fi
done

# 6. Create initial bootstrap secrets in Kubernetes
echo "🔒 Creating bootstrap secrets in namespace '${NAMESPACE}'..."
kubectl create secret generic multi-secret-app-db-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=db-password="InitialPsqlPass123!" \
    --from-literal=db-user="postgres" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null || { echo "❌ Error: Failed to create db-password bootstrap secret."; exit 1; }

kubectl create secret generic multi-secret-app-redis-auth-token-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=redis-auth-token="InitialRedisToken456!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null || { echo "❌ Error: Failed to create redis-auth-token bootstrap secret."; exit 1; }

kubectl create secret generic multi-secret-app-payment-api-key-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=payment-api-key="sk_live_pay_9876543210" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null || { echo "❌ Error: Failed to create payment-api-key bootstrap secret."; exit 1; }

echo "✅ Bootstrap Kubernetes secrets created."

# 7. Apply DynamicSecretPolicy CRD (if repo root is available)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD from repo..."
    kubectl apply -k "${REPO_ROOT}/config/crd" >/dev/null || { echo "❌ Error: Failed to apply CRD."; exit 1; }
    echo "✅ CRD applied."
fi

# 8. Apply manifests with Key Vault name substitution
echo "📄 Deploying Multi-Secret workloads and DynamicSecretPolicy manifests..."
if [ ! -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    echo "❌ Error: Manifest file not found at ${SCRIPT_DIR}/manifests.yaml"
    exit 1
fi
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" "${SCRIPT_DIR}/manifests.yaml" \
  | kubectl apply -f - || { echo "❌ Error: Failed to apply manifests."; exit 1; }
echo "✅ All manifests deployed successfully!"

# 9. Wait for pods to roll out
echo "⏳ Waiting for deployments in '${NAMESPACE}' to be ready..."
kubectl rollout status deployment/postgres -n "${NAMESPACE}" --timeout=120s || { echo "❌ Error: PostgreSQL rollout failed or timed out."; exit 1; }
kubectl rollout status deployment/redis -n "${NAMESPACE}" --timeout=120s || { echo "❌ Error: Redis rollout failed or timed out."; exit 1; }
kubectl rollout status deployment/payment-gateway -n "${NAMESPACE}" --timeout=120s || { echo "❌ Error: Payment Gateway rollout failed or timed out."; exit 1; }
kubectl rollout status deployment/multi-secret-app -n "${NAMESPACE}" --timeout=180s || { echo "❌ Error: Multi-Secret App rollout failed or timed out."; exit 1; }

echo "=================================================================="
echo "🎉 MULTI-SECRET MICROSERVICE DEPLOYED SUCCESSFULLY!"
echo "=================================================================="
echo ""
echo "📊 Access the Multi-Secret Dashboard:"
echo "   kubectl port-forward svc/multi-secret-app 8080:80 -n ${NAMESPACE}"
echo ""
echo "   Then open in your browser: http://localhost:8080"
echo ""
echo "🔄 Test Independent Secret Rotations:"
echo ""
echo "1️⃣ Rotate PostgreSQL Database Password:"
echo "   az keyvault secret set --vault-name '${KEYVAULT_NAME}' --name 'db-password' --value 'NewRotatedPsqlPass999!'"
echo ""
echo "2️⃣ Rotate Redis Auth Token:"
echo "   az keyvault secret set --vault-name '${KEYVAULT_NAME}' --name 'redis-auth-token' --value 'NewRotatedRedisToken888!'"
echo ""
echo "3️⃣ Rotate Payment API Key:"
echo "   az keyvault secret set --vault-name '${KEYVAULT_NAME}' --name 'payment-api-key' --value 'sk_live_pay_new_777777'"
echo "=================================================================="
