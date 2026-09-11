#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO Job-Based Redis Probe Example
# ==============================================================================
# This multi-cloud demonstration showcases real-time rotation of custom protocol
# secrets (Redis AUTH) synchronized by External Secrets Operator (ESO) and safely
# validated using an ephemeral batch/v1.Job probe via Dynamic Secret Operator (DSO).
# ==============================================================================

set -euo pipefail

NAMESPACE="dso-examples"
SECRET_STORE_NAME=""
SECRET_STORE_KIND=""
REMOTE_SECRET_NAME="redis-auth-password"

print_usage() {
    echo "Usage: $0 -s <SECRET_STORE_NAME> -k <SECRET_STORE_KIND> [-n <NAMESPACE>] [-r <REMOTE_SECRET_NAME>]"
    echo "  -s    Name of the SecretStore or ClusterSecretStore    [required]"
    echo "  -k    Kind of the store (SecretStore / ClusterSecretStore) [required]"
    echo "  -n    Target Kubernetes namespace                      [default: dso-examples]"
    echo "  -r    Remote Redis secret name in your secret store    [default: redis-auth-password]"
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
echo "🚀 Deploying ESO Job-Based Redis Probe Example..."
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

# 3. Create bootstrap initial secrets
echo "🔒 Creating bootstrap initial secrets in namespace '${NAMESPACE}'..."
kubectl create secret generic redis-master-redis-auth-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=redis-auth-password="InitialRedisPassword123!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

kubectl create secret generic redis-consumer-redis-auth-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=redis-auth-password="InitialRedisPassword123!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "✅ Bootstrap initial secrets created."

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
    -e "s/redis-master\.dso-examples\.svc\.cluster\.local/redis-master.${NAMESPACE}.svc.cluster.local/g" \
    "${MANIFEST_PATH}" | kubectl apply -f - -n "${NAMESPACE}"

echo "✅ Manifests applied successfully."

# 6. Wait for rollout
echo "⏳ Waiting for Redis deployments in '${NAMESPACE}' to be ready..."
kubectl rollout status deployment/redis-master -n "${NAMESPACE}" --timeout=120s
kubectl rollout status deployment/redis-consumer -n "${NAMESPACE}" --timeout=120s
echo "✅ All Redis deployments are running and ready."

echo ""
echo "=================================================================="
echo "🎉 ESO JOB-BASED REDIS PROBE EXAMPLE DEPLOYED SUCCESSFULLY!"
echo "=================================================================="
cat <<EOF

📋 Deployed Configuration in '${NAMESPACE}':
------------------------------------------------------------------
SecretStore Reference:   ${SECRET_STORE_NAME} (${SECRET_STORE_KIND})
ExternalSecret:          redis-auth-password-eso
Intermediate Secret:     redis-auth-password-synced
Workload Deployment:     redis-consumer
Target Env Var:          REDIS_AUTH_PASSWORD
Validation Probe:        batch/v1.Job (redis-cli PING)
DynamicSecretPolicy:     redis-cache-rotation

🔍 STEP-BY-STEP VERIFICATION & ROTATION GUIDE:
------------------------------------------------------------------

1️⃣ Tail Redis Consumer Logs (in a dedicated terminal):
   kubectl logs -l app=redis-consumer -n ${NAMESPACE} -f
   (Observe continuous heartbeat logs: 'redis-ping=PONG')

2️⃣ Monitor DSO Policy & Ephemeral Validation Jobs (in separate terminals):
   - Watch Ephemeral Probe Job Lifecycle:
     kubectl get jobs -n ${NAMESPACE} -w

   - Watch DynamicSecretPolicy State Machine:
     kubectl get dynamicsecretpolicy redis-cache-rotation -n ${NAMESPACE} -w

   - Watch ESO Secret Synchronization:
     kubectl get externalsecrets -n ${NAMESPACE} -w

3️⃣ Execute Safe Redis AUTH Password Rotation:
   🔹 Step 3.1: Update password inside running Redis Master:
      kubectl exec deployment/redis-master -n ${NAMESPACE} -- redis-cli -a InitialRedisPassword123! --no-auth-warning CONFIG SET requirepass "RotatedRedisPassword456!"

   🔹 Step 3.2: Update secret '${REMOTE_SECRET_NAME}' in your Secret Provider (Vault, AWS, GCP, Azure, etc.):
      Set the secret '${REMOTE_SECRET_NAME}' value to 'RotatedRedisPassword456!'

   🔹 Step 3.3: Observe Autonomous Validation & Zero Downtime:
      1. ESO synchronizes the new secret to 'redis-auth-password-synced'.
      2. DSO creates candidate revision and spawns an ephemeral Job probe.
      3. The probe runs 'redis-cli -h redis-master -p 6379 -a <new-secret> PING'.
      4. Upon receiving 'PONG' (exit code 0), DSO rolls out 'redis-consumer' with the new password.
      5. Ephemeral Job probe is cleaned up automatically.
      6. Consumer logs show continuous 'PONG' with zero failed connections!

4️⃣ Test Invalid Secret & Circuit Breaker Protection:
   - Update secret '${REMOTE_SECRET_NAME}' in your provider with an invalid value (e.g. 'BadPassword999!').
   - ESO synchronizes the intermediate secret.
   - DSO launches the ephemeral Job probe which fails AUTH against Redis master.
   - DSO immediately rejects the canary revision, logs the probe failure, and trips the circuit breaker!
   - The production 'redis-consumer' workload remains untouched on the valid password with 100% uptime.
==================================================================
EOF
