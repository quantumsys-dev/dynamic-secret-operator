#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO Fullstack DB Rotation Example
# ==============================================================================
# This multi-cloud demonstration showcases real-time rotation of database
# credentials (PostgreSQL password) synchronized by External Secrets Operator (ESO)
# and safely validated with synthetic native PostgreSQL probes and zero downtime
# via Dynamic Secret Operator (DSO).
# ==============================================================================

set -euo pipefail

NAMESPACE="dso-examples"
SECRET_STORE_NAME=""
SECRET_STORE_KIND=""
REMOTE_SECRET_NAME="db-password"

print_usage() {
    echo "Usage: $0 -s <SECRET_STORE_NAME> -k <SECRET_STORE_KIND> [-n <NAMESPACE>] [-r <REMOTE_SECRET_NAME>]"
    echo "  -s    Name of the SecretStore or ClusterSecretStore    [required]"
    echo "  -k    Kind of the store (SecretStore / ClusterSecretStore) [required]"
    echo "  -n    Target Kubernetes namespace                      [default: dso-examples]"
    echo "  -r    Remote DB secret name in your secret store       [default: db-password]"
    exit 1
}

while getopts "s:k:n:r:h" opt; do
    case "${opt}" in
        s) SECRET_STORE_NAME="${OPTARG}" ;;
        k) SECRET_STORE_KIND="${OPTARG}" ;;
        n) NAMESPACE="${OPTARG}" ;;
        r) REMOTE_SECRET_NAME="${OPTARG}" ;;
        h) print_usage ;;
        *) print_usage ;;
    esac
done

if [ -z "${SECRET_STORE_NAME}" ] || [ -z "${SECRET_STORE_KIND}" ]; then
    echo "❌ Error: -s <SECRET_STORE_NAME> and -k <SECRET_STORE_KIND> are required parameters."
    print_usage
fi

echo "=================================================================="
echo "🚀 Deploying ESO Fullstack Database Rotation Example..."
echo "=================================================================="
echo "ℹ️  Target Namespace:   ${NAMESPACE}"
echo "ℹ️  SecretStore Name:   ${SECRET_STORE_NAME}"
echo "ℹ️  SecretStore Kind:   ${SECRET_STORE_KIND}"
echo "ℹ️  Remote Secret Name: ${REMOTE_SECRET_NAME}"

# 1. Check prerequisites
command -v kubectl >/dev/null 2>&1 || { echo "❌ Error: 'kubectl' is required. Please install kubectl: https://kubernetes.io/docs/tasks/tools/"; exit 1; }

CURRENT_CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
if [ -z "${CURRENT_CONTEXT}" ]; then
    echo "❌ Error: Not connected to any Kubernetes cluster. Please configure your kubeconfig first."
    exit 1
fi
echo "✅ Connected to cluster: ${CURRENT_CONTEXT}"

# 2. Ensure target namespace exists
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
echo "✅ Namespace '${NAMESPACE}' is ready."

# 3. Create bootstrap initial secret
echo "🔒 Creating bootstrap initial secret in namespace '${NAMESPACE}'..."
kubectl create secret generic db-status-app-db-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=db-password="InitialSecretPassword123!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "✅ Bootstrap initial secret created."

# 4. Apply DynamicSecretPolicy CRD if present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
if [ -d "${REPO_ROOT}/config/crd/bases" ]; then
    kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/config/crd/bases" >/dev/null 2>&1 || true
    echo "✅ DynamicSecretPolicy CRD applied."
fi

# 5. Apply manifests
echo "📄 Applying manifests with Store '${SECRET_STORE_NAME}' (${SECRET_STORE_KIND}), Remote Key '${REMOTE_SECRET_NAME}'..."
MANIFEST_PATH="${SCRIPT_DIR}/manifests.yaml"
if [ ! -f "${MANIFEST_PATH}" ]; then
    echo "❌ Error: Manifest file not found at: ${MANIFEST_PATH}"
    exit 1
fi

sed -e "s/\${SECRET_STORE_NAME}/${SECRET_STORE_NAME}/g" \
    -e "s/\${SECRET_STORE_KIND}/${SECRET_STORE_KIND}/g" \
    -e "s/\${REMOTE_SECRET_NAME}/${REMOTE_SECRET_NAME}/g" \
    -e "s/namespace: dso-examples/namespace: ${NAMESPACE}/g" \
    -e "s/postgres\.dso-examples\.svc\.cluster\.local/postgres.${NAMESPACE}.svc.cluster.local/g" \
    "${MANIFEST_PATH}" | kubectl apply -f - -n "${NAMESPACE}"

echo "✅ Manifests applied successfully."

# 6. Wait for rollout
echo "⏳ Waiting for deployments in '${NAMESPACE}' to be ready..."
kubectl rollout status deployment/postgres -n "${NAMESPACE}" --timeout=120s
kubectl rollout status deployment/db-status-app -n "${NAMESPACE}" --timeout=180s
echo "✅ All database workloads are running and ready."

echo ""
echo "=================================================================="
echo "🎉 ESO FULLSTACK DATABASE ROTATION EXAMPLE DEPLOYED SUCCESSFULLY!"
echo "=================================================================="
cat <<EOF

📋 Deployed Configuration in '${NAMESPACE}':
------------------------------------------------------------------
SecretStore Reference:   ${SECRET_STORE_NAME} (${SECRET_STORE_KIND})
ExternalSecret:          db-password-eso
Intermediate Secret:     db-password-synced
Workload Deployment:     db-status-app
Database Deployment:     postgres
Validation Probe:        PostgreSQL (native synthetic query)
DynamicSecretPolicy:     eso-database-password-policy

🌐 How to View the Live Database Dashboard:
------------------------------------------------------------------
Option 1: Using Port-Forward (Immediate):
  kubectl port-forward svc/db-status-app 8080:80 -n ${NAMESPACE}
  Open in your browser at: http://localhost:8080

Option 2: Using LoadBalancer External IP:
  kubectl get svc db-status-app -n ${NAMESPACE} -w

🔍 STEP-BY-STEP VERIFICATION & ROTATION GUIDE:
------------------------------------------------------------------

1️⃣ Monitor Database Status & DSO Policy (in separate terminals):
   - Watch DynamicSecretPolicy State Machine:
     kubectl get dynamicsecretpolicy eso-database-password-policy -n ${NAMESPACE} -w

   - Watch Pod Rollout:
     kubectl get pods -n ${NAMESPACE} -l app=db-status-app -w

   - Watch ESO Secret Synchronization:
     kubectl get externalsecrets -n ${NAMESPACE} -w

2️⃣ Execute Safe PostgreSQL Database Credential Rotation:
   🔹 Step 2.1: Update password inside running PostgreSQL (simulating DBA/Rotation Engine):
      kubectl exec deployment/postgres -n ${NAMESPACE} -- psql -U postgres -d appdb -c "ALTER USER postgres WITH PASSWORD 'NewSecret2026_Rotated!';"

   🔹 Step 2.2: Update secret '${REMOTE_SECRET_NAME}' in your Secret Provider (Vault, AWS, GCP, Azure, etc.):
      Set the secret '${REMOTE_SECRET_NAME}' value to 'NewSecret2026_Rotated!'

   🔹 Step 2.3: Observe Autonomous Validation & Zero Downtime:
      1. ESO synchronizes the new secret to 'db-password-synced'.
      2. DSO creates candidate revision and provisions an isolated Canary Pod.
      3. DSO executes native PostgreSQL probe (CONNECT + SELECT query).
      4. Upon probe success, DSO safely promotes 'db-status-app' with rolling update.
      5. The live web dashboard reflects the new password hint seamlessly with ZERO connection errors!

3️⃣ Test Invalid Secret & Circuit Breaker Protection:
   - Update secret '${REMOTE_SECRET_NAME}' in your provider with an invalid value (e.g. 'WrongPassword999!') WITHOUT updating PostgreSQL.
   - ESO synchronizes the intermediate secret.
   - DSO launches Canary and runs the PostgreSQL probe which fails authentication.
   - DSO immediately rejects the canary revision, surfaces the failure condition, and trips the circuit breaker!
   - The production dashboard remains untouched on the valid password with 100% uptime.
==================================================================
EOF
