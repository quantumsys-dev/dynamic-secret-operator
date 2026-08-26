#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Job-Based Redis Probe Example on AKS
# ==============================================================================

set -euo pipefail

KEYVAULT_NAME=""
NAMESPACE="production"

print_usage() {
    echo "Usage: $0 -k <KEYVAULT_NAME> [-n <NAMESPACE>]"
    echo "  -k    Name of the Azure Key Vault (e.g., kv-dso-dev)   [required]"
    echo "  -n    Target Kubernetes namespace                        [default: production]"
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
echo "🚀 Deploying Job-Based Redis Probe Example to AKS Cluster..."
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

# 3. Ensure target namespace exists
echo "📦 Ensuring namespace '${NAMESPACE}' exists..."
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
echo "✅ Namespace '${NAMESPACE}' ready."

# 4. Seed initial Redis AUTH password in Azure Key Vault
echo "🔑 Checking secret 'redis-auth-password' in Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault secret show --vault-name "${KEYVAULT_NAME}" --name "redis-auth-password" >/dev/null 2>&1; then
    echo "ℹ️  Secret 'redis-auth-password' not found. Creating initial secret..."
    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "redis-auth-password" \
        --value "InitialRedisPassword123!" \
        --output none
    echo "✅ Initial secret 'redis-auth-password' seeded in Key Vault."
else
    echo "ℹ️  Secret 'redis-auth-password' already exists in Key Vault."
fi

# 5. Create bootstrap secrets so pods can start before DSO materializes the first revision.
echo "🔒 Creating bootstrap secrets in namespace '${NAMESPACE}'..."

kubectl create secret generic redis-master-redis-auth-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=redis-auth-password="InitialRedisPassword123!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

kubectl create secret generic redis-consumer-redis-auth-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=redis-auth-password="InitialRedisPassword123!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "✅ Bootstrap secrets created."

# 6. Apply DynamicSecretPolicy CRD (if running from the repo)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply -k "${REPO_ROOT}/config/crd" >/dev/null
    echo "✅ CRD applied."
fi

# 7. Apply manifests with Key Vault substitution
echo "📄 Deploying Redis workloads and DynamicSecretPolicy manifests..."
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" "${SCRIPT_DIR}/manifests.yaml" \
  | kubectl apply -n "${NAMESPACE}" -f -

# 8. Wait for workloads to become ready
echo "⏳ Waiting for redis-master to be ready..."
kubectl rollout status deployment/redis-master -n "${NAMESPACE}" --timeout=120s

echo "⏳ Waiting for redis-consumer to be ready..."
kubectl rollout status deployment/redis-consumer -n "${NAMESPACE}" --timeout=120s

echo "=================================================================="
echo "✅ Job-Based Redis Probe Example deployed successfully on AKS!"
echo ""
echo "Next Steps:"
echo "1. Tail the redis-consumer logs:"
echo "   kubectl logs -n ${NAMESPACE} -l app=redis-consumer -f"
echo ""
echo "2. Watch the DynamicSecretPolicy conditions:"
echo "   kubectl get dynamicsecretpolicy redis-cache-rotation -n ${NAMESPACE} -w"
echo ""
echo "3. Trigger a Redis AUTH rotation in Azure Key Vault:"
echo "   az keyvault secret set --vault-name ${KEYVAULT_NAME} --name 'redis-auth-password' --value 'RotatedRedisPassword456!'"
echo ""
echo "4. Observe DSO launch an ephemeral probe Job:"
echo "   kubectl get jobs -n ${NAMESPACE} -w"
echo "=================================================================="
