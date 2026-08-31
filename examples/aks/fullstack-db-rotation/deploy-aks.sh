#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Fullstack DB Rotation Example on AKS
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
echo "🚀 Deploying Fullstack DB Rotation Example to AKS Cluster..."
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
echo "🔑 Checking secret 'db-password' in Azure Key Vault '${KEYVAULT_NAME}'..."
if ! az keyvault secret show --vault-name "${KEYVAULT_NAME}" --name "db-password" >/dev/null 2>&1; then
    echo "ℹ️  Secret 'db-password' not found. Creating initial secret in Key Vault..."
    az keyvault secret set \
        --vault-name "${KEYVAULT_NAME}" \
        --name "db-password" \
        --value "InitialSecretPassword123!" \
        --output none || { echo "❌ Error: Failed to create secret 'db-password' in Key Vault '${KEYVAULT_NAME}'."; exit 1; }
    echo "✅ Initial secret 'db-password' seeded in Key Vault."
else
    echo "ℹ️  Secret 'db-password' already exists in Key Vault."
fi

# 5. Create bootstrap secret in cluster for PostgreSQL initial startup
echo "🔒 Creating bootstrap secret in cluster for PostgreSQL initialization..."
kubectl create secret generic db-status-app-db-password-initial \
    --from-literal=db-password="InitialSecretPassword123!" \
    --dry-run=client -o yaml | kubectl apply -f - || { echo "❌ Error: Failed to create bootstrap secret in cluster."; exit 1; }

# 6. Install DSO CRD if not present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd/bases" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/config/crd/bases" || { echo "❌ Error: Failed to apply DynamicSecretPolicy CRD."; exit 1; }
    echo "✅ CRD applied."
fi

# 7. Apply manifests with Key Vault replacement
echo "📄 Deploying PostgreSQL, Web Dashboard, and DynamicSecretPolicy manifests..."
if [ ! -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    echo "❌ Error: Manifest file not found at ${SCRIPT_DIR}/manifests.yaml"
    exit 1
fi
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f - || { echo "❌ Error: Failed to apply manifests."; exit 1; }

echo "⏳ Waiting for PostgreSQL to be ready..."
kubectl rollout status deployment/postgres --timeout=120s || { echo "❌ Error: PostgreSQL rollout failed or timed out."; exit 1; }

echo "⏳ Waiting for Web Dashboard to be ready..."
kubectl rollout status deployment/db-status-app --timeout=120s || { echo "❌ Error: Web Dashboard rollout failed or timed out."; exit 1; }

echo "=================================================================="
echo "✅ Fullstack DB Rotation PoC deployed successfully on AKS!"
echo "=================================================================="
echo ""
echo "📋 STEP-BY-STEP VERIFICATION GUIDE:"
echo "------------------------------------------------------------------"
echo ""
echo "1️⃣ Open the Live PostgreSQL Status Dashboard:"
echo "   - Public URL (LoadBalancer):"
echo "     kubectl get svc db-status-app"
echo "     (Open http://<EXTERNAL-IP> in your browser)"
echo ""
echo "   - Fallback (Port-Forward):"
echo "     kubectl port-forward svc/db-status-app 8080:80"
echo "     (Open http://localhost:8080)"
echo ""
echo "2️⃣ Monitor Database Connections & DSO in Real Time (in separate terminals):"
echo "   - Watch Live Audit Log on Dashboard: The web UI displays real-time DB query status and active password hash."
echo "   - Watch DSO State Machine & Validation Conditions:"
echo "     kubectl get dynamicsecretpolicy aks-database-password-policy -w"
echo ""
echo "   - Watch Pod Rollout:"
echo "     kubectl get pods -l app=db-status-app -w"
echo ""
echo "   - Stream Operator Logs:"
echo "     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f"
echo ""
echo "3️⃣ Execute Database Credential Rotation:"
echo "   🔹 Step 3.1: Update the user password directly inside PostgreSQL (simulating DBA/Rotation Engine):"
echo "      kubectl exec deployment/postgres -- psql -U postgres -d appdb -c \"ALTER USER postgres WITH PASSWORD 'NewSecret2026_Rotated!';\""
echo ""
echo "   🔹 Step 3.2: Update the secret in Azure Key Vault:"
echo "      az keyvault secret set --vault-name ${KEYVAULT_NAME} --name 'db-password' --value 'NewSecret2026_Rotated!'"
echo ""
echo "4️⃣ Observe Zero-Downtime Database Rollover:"
echo "   - Azure Key Vault emits SecretNewVersionCreated event to Service Bus."
echo "   - DSO receives the event and provisions an isolated Canary Pod."
echo "   - DSO executes native PostgreSQL probe: runs test query against PostgreSQL with the new credentials."
echo "   - Once validated, DSO promotes 'db-status-app' with rolling update."
echo "   - The web dashboard switches to the new credential seamlessly without dropping queries!"
echo ""
echo "5️⃣ Test Circuit Breaker & Safe Abort (Optional):"
echo "   - Change Key Vault secret WITHOUT updating PostgreSQL:"
echo "     az keyvault secret set --vault-name ${KEYVAULT_NAME} --name 'db-password' --value 'WrongPassword999!'"
echo "   - DSO Canary probe will fail authentication against PostgreSQL and ABORT the rollout, keeping production safe!"
echo "=================================================================="
