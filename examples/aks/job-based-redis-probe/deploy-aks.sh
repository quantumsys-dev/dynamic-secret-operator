#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Job-Based Redis Probe Example on AKS
# ==============================================================================

set -euo pipefail

KEYVAULT_NAME=""
NAMESPACE="dso-examples"

print_usage() {
    echo "Usage: $0 -k <KEYVAULT_NAME> [-n <NAMESPACE>]"
    echo "  -k    Name of the Azure Key Vault (e.g., kv-dso-dev)   [required]"
    echo "  -n    Target Kubernetes namespace                        [default: dso-examples]"
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

# 5. Seed initial Redis AUTH password in Azure Key Vault
echo "🔑 Checking secret 'redis-auth-password' in Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault secret show --vault-name "${KEYVAULT_NAME}" --name "redis-auth-password" >/dev/null 2>&1; then
    echo "ℹ️  Secret 'redis-auth-password' not found. Creating initial secret..."
    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "redis-auth-password" \
        --value "InitialRedisPassword123!" \
        --output none || { echo "❌ Error: Failed to create secret 'redis-auth-password' in Key Vault '${KEYVAULT_NAME}'."; exit 1; }
    echo "✅ Initial secret 'redis-auth-password' seeded in Key Vault."
else
    echo "ℹ️  Secret 'redis-auth-password' already exists in Key Vault."
fi

# 6. Create bootstrap secrets so pods can start before DSO materializes the first revision.
echo "🔒 Creating bootstrap secrets in namespace '${NAMESPACE}'..."

kubectl create secret generic redis-master-redis-auth-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=redis-auth-password="InitialRedisPassword123!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null || { echo "❌ Error: Failed to create redis-master bootstrap secret."; exit 1; }

kubectl create secret generic redis-consumer-redis-auth-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=redis-auth-password="InitialRedisPassword123!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null || { echo "❌ Error: Failed to create redis-consumer bootstrap secret."; exit 1; }

echo "✅ Bootstrap secrets created."

# 7. Apply DynamicSecretPolicy CRD (if running from the repo)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd/bases" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/config/crd/bases" >/dev/null || { echo "❌ Error: Failed to apply DynamicSecretPolicy CRD."; exit 1; }
    echo "✅ CRD applied."
fi

# 8. Apply manifests with Key Vault substitution
echo "📄 Deploying Redis workloads and DynamicSecretPolicy manifests..."
if [ ! -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    echo "❌ Error: Manifest file not found at ${SCRIPT_DIR}/manifests.yaml"
    exit 1
fi
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g; s/\${NAMESPACE}/${NAMESPACE}/g" "${SCRIPT_DIR}/manifests.yaml" \
  | kubectl apply -n "${NAMESPACE}" -f - || { echo "❌ Error: Failed to apply manifests."; exit 1; }

# 9. Wait for workloads to become ready
echo "⏳ Waiting for redis-master to be ready..."
kubectl rollout status deployment/redis-master -n "${NAMESPACE}" --timeout=120s || { echo "❌ Error: Redis master rollout failed or timed out."; exit 1; }

echo "⏳ Waiting for redis-consumer to be ready..."
kubectl rollout status deployment/redis-consumer -n "${NAMESPACE}" --timeout=120s || { echo "❌ Error: Redis consumer rollout failed or timed out."; exit 1; }

echo "=================================================================="
echo "✅ Job-Based Redis Probe Example deployed successfully on AKS!"
echo "=================================================================="
echo ""
echo "📋 STEP-BY-STEP VERIFICATION GUIDE:"
echo "------------------------------------------------------------------"
echo ""
echo "1️⃣ Tail Redis Consumer Logs (in a dedicated terminal):"
echo "   kubectl logs -n ${NAMESPACE} -l app=redis-consumer -f"
echo "   (Observe continuous PING/PONG heartbeats with current AUTH password)"
echo ""
echo "2️⃣ Monitor DSO Policy & Ephemeral Validation Jobs (in separate terminals):"
echo "   - Watch Ephemeral Probe Job Lifecycle:"
echo "     kubectl get jobs -n ${NAMESPACE} -w"
echo ""
echo "   - Watch DynamicSecretPolicy State Machine:"
echo "     kubectl get dynamicsecretpolicy redis-cache-rotation -n ${NAMESPACE} -w"
echo ""
echo "   - Stream Operator Logs:"
echo "     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f"
echo ""
echo "3️⃣ Execute Redis AUTH Password Rotation:"
echo "   🔹 Step 3.1: Retrieve current active password:"
echo "      CURRENT_PASS=\$(az keyvault secret show --vault-name ${KEYVAULT_NAME} --name 'redis-auth-password' --query value -o tsv)"
echo ""
echo "   🔹 Step 3.2: Update password inside running Redis Master:"
echo "      kubectl exec deployment/redis-master -n ${NAMESPACE} -- redis-cli -a \"\${CURRENT_PASS}\" CONFIG SET requirepass 'RotatedRedisPassword456!'"
echo ""
echo "   🔹 Step 3.3: Update secret in Azure Key Vault:"
echo "      az keyvault secret set --vault-name ${KEYVAULT_NAME} --name 'redis-auth-password' --value 'RotatedRedisPassword456!'"
echo ""
echo "4️⃣ Observe Job-Based Validation & Zero-Downtime Promotion:"
echo "   - Key Vault emits SecretNewVersionCreated event to Azure Service Bus."
echo "   - DSO launches an ephemeral Kubernetes Job with 'redis:alpine' image."
echo "   - The Job executes 'redis-cli -a <new-password> ping' against the master."
echo "   - Upon successful PING validation (exit 0), DSO promotes 'redis-consumer'."
echo "   - The ephemeral validation Job is automatically cleaned up."
echo "   - Consumer logs reflect continuous zero-downtime PONG responses with no dropped connections!"
echo "=================================================================="
