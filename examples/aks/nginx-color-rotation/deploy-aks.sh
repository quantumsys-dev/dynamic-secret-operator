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

# 3. Verify Key Vault accessibility
echo "🔑 Verifying access to Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault show --name "${KEYVAULT_NAME}" >/dev/null 2>&1; then
    echo "❌ Error: Unable to access Key Vault '${KEYVAULT_NAME}'. Please verify the name and your Azure permissions."
    exit 1
fi
echo "✅ Key Vault '${KEYVAULT_NAME}' verified."

# 4. Seed initial secret in Azure Key Vault if not exists
echo "🔑 Checking secret 'nginx-bg-color' in Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault secret show --vault-name "${KEYVAULT_NAME}" --name "nginx-bg-color" >/dev/null 2>&1; then
    echo "ℹ️  Creating initial secret 'nginx-bg-color' in Key Vault..."
    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "nginx-bg-color" \
        --value "#3b82f6" \
        --output none || { echo "❌ Error: Failed to create secret 'nginx-bg-color' in Key Vault '${KEYVAULT_NAME}'."; exit 1; }
    echo "✅ Initial secret 'nginx-bg-color' seeded in Key Vault."
else
    echo "ℹ️  Secret 'nginx-bg-color' already exists in Key Vault."
fi

# 5. Install DSO CRD if not present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd/bases" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/config/crd/bases" || { echo "❌ Error: Failed to apply DynamicSecretPolicy CRD."; exit 1; }
    echo "✅ CRD applied."
fi

# 6. Apply manifests with Key Vault replacement
echo "📄 Deploying Nginx Color App and DynamicSecretPolicy manifests..."
if [ ! -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    echo "❌ Error: Manifest file not found at ${SCRIPT_DIR}/manifests.yaml"
    exit 1
fi
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f - || { echo "❌ Error: Failed to apply manifests."; exit 1; }

echo "⏳ Waiting for Nginx Color App deployment to be ready..."
kubectl rollout status deployment/nginx-color-app --timeout=120s || { echo "❌ Error: Deployment rollout failed or timed out."; exit 1; }

echo "=================================================================="
echo "✅ Nginx Color Rotation Example deployed successfully on AKS!"
echo "=================================================================="
echo ""
echo "📋 STEP-BY-STEP VERIFICATION GUIDE:"
echo "------------------------------------------------------------------"
echo ""
echo "1️⃣ Access the Nginx Web App:"
echo "   - Public URL (LoadBalancer):"
echo "     kubectl get svc nginx-color-app"
echo "     (Open http://<EXTERNAL-IP> in your browser)"
echo ""
echo "   - Fallback (Port-Forward):"
echo "     kubectl port-forward svc/nginx-color-app 8080:80"
echo "     (Open http://localhost:8080)"
echo ""
echo "2️⃣ Monitor DSO and Workload in Real Time (in a separate terminal):"
echo "   - Watch DSO State Machine & Conditions:"
echo "     kubectl get dynamicsecretpolicy aks-nginx-color-policy -w"
echo ""
echo "   - Watch Pod Rollout & Canary Lifecycle:"
echo "     kubectl get pods -l app=nginx-color-app -w"
echo ""
echo "   - Stream Operator Logs:"
echo "     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f"
echo ""
echo "3️⃣ Trigger a Secret Rotation in Azure Key Vault:"
echo "   az keyvault secret set --vault-name ${KEYVAULT_NAME} --name 'nginx-bg-color' --value '#10b981'"
echo ""
echo "4️⃣ Observe Zero-Downtime Promotion:"
echo "   - Key Vault publishes SecretNewVersionCreated event to Azure Service Bus."
echo "   - DSO triggers Canary Provisioning, runs synthetic HTTP /health probe."
echo "   - Target Deployment 'nginx-color-app' is promoted to the new color with zero downtime!"
echo "   - Refresh your browser to see the background change from Blue (#3b82f6) to Green (#10b981)!"
echo ""
echo "5️⃣ Test Circuit Breaker & Auto-Rollback (Optional):"
echo "   - Inject an invalid value that fails health checks:"
echo "     az keyvault secret set --vault-name ${KEYVAULT_NAME} --name 'nginx-bg-color' --value 'INVALID_COLOR'"
echo "   - Watch DSO Canary fail synthetic probes and automatically abort rollout without affecting live traffic!"
echo "=================================================================="

