#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO Multi-Secret Rotation Example
# ==============================================================================
# This multi-cloud demonstration showcases real-time rotation of multiple
# independent secrets (PostgreSQL, Redis, Payment API Key) driven by External
# Secrets Operator (ESO) and safely validated with zero downtime via
# Dynamic Secret Operator (DSO).
# ==============================================================================

set -euo pipefail

NAMESPACE="dso-examples"
SECRET_STORE_NAME=""
SECRET_STORE_KIND=""
REMOTE_DB_SECRET_NAME="db-password"
REMOTE_REDIS_SECRET_NAME="redis-auth-token"
REMOTE_PAYMENT_SECRET_NAME="payment-api-key"

print_usage() {
    echo "Usage: $0 -s <SECRET_STORE_NAME> -k <SECRET_STORE_KIND> [-n <NAMESPACE>] [-d <DB_SECRET>] [-r <REDIS_SECRET>] [-p <PAYMENT_SECRET>]"
    echo "  -s    Name of the SecretStore or ClusterSecretStore    [required]"
    echo "  -k    Kind of the store (SecretStore / ClusterSecretStore) [required]"
    echo "  -n    Target Kubernetes namespace                      [default: dso-examples]"
    echo "  -d    Remote DB secret name in your secret store       [default: db-password]"
    echo "  -r    Remote Redis secret name in your secret store    [default: redis-auth-token]"
    echo "  -p    Remote Payment secret name in your secret store  [default: payment-api-key]"
    exit 1
}

while getopts "s:k:n:d:r:p:h" opt; do
    case "${opt}" in
        s) SECRET_STORE_NAME="${OPTARG}" ;;
        k) SECRET_STORE_KIND="${OPTARG}" ;;
        n) NAMESPACE="${OPTARG}" ;;
        d) REMOTE_DB_SECRET_NAME="${OPTARG}" ;;
        r) REMOTE_REDIS_SECRET_NAME="${OPTARG}" ;;
        p) REMOTE_PAYMENT_SECRET_NAME="${OPTARG}" ;;
        h) print_usage ;;
        *) print_usage ;;
    esac
done

if [ -z "${SECRET_STORE_NAME}" ] || [ -z "${SECRET_STORE_KIND}" ]; then
    echo "❌ Error: -s <SECRET_STORE_NAME> and -k <SECRET_STORE_KIND> are required parameters."
    print_usage
fi

echo "=================================================================="
echo "🚀 Deploying ESO Multi-Secret Rotation Example..."
echo "=================================================================="
echo "ℹ️  Target Namespace:            ${NAMESPACE}"
echo "ℹ️  SecretStore Name:            ${SECRET_STORE_NAME}"
echo "ℹ️  SecretStore Kind:            ${SECRET_STORE_KIND}"
echo "ℹ️  Remote DB Secret Key:        ${REMOTE_DB_SECRET_NAME}"
echo "ℹ️  Remote Redis Secret Key:     ${REMOTE_REDIS_SECRET_NAME}"
echo "ℹ️  Remote Payment Secret Key:   ${REMOTE_PAYMENT_SECRET_NAME}"

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
kubectl create secret generic multi-secret-app-db-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=db-password="InitialPsqlPass123!" \
    --from-literal=db-user="postgres" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

kubectl create secret generic multi-secret-app-redis-auth-token-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=redis-auth-token="InitialRedisToken456!" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null

kubectl create secret generic multi-secret-app-payment-api-key-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=payment-api-key="sk_live_pay_9876543210" \
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
echo "📄 Applying manifests with Store '${SECRET_STORE_NAME}' (${SECRET_STORE_KIND})..."
MANIFEST_PATH="${SCRIPT_DIR}/manifests.yaml"
if [ ! -f "${MANIFEST_PATH}" ]; then
    echo "❌ Error: Manifest file not found at: ${MANIFEST_PATH}"
    exit 1
fi

sed -e "s/\${SECRET_STORE_NAME}/${SECRET_STORE_NAME}/g" \
    -e "s/\${SECRET_STORE_KIND}/${SECRET_STORE_KIND}/g" \
    -e "s/\${REMOTE_DB_SECRET_NAME}/${REMOTE_DB_SECRET_NAME}/g" \
    -e "s/\${REMOTE_REDIS_SECRET_NAME}/${REMOTE_REDIS_SECRET_NAME}/g" \
    -e "s/\${REMOTE_PAYMENT_SECRET_NAME}/${REMOTE_PAYMENT_SECRET_NAME}/g" \
    "${MANIFEST_PATH}" | sed "s/namespace: dso-examples/namespace: ${NAMESPACE}/g" | kubectl apply -f - -n "${NAMESPACE}"

echo "✅ Manifests applied successfully."

# 6. Wait for rollout
echo "⏳ Waiting for deployments in '${NAMESPACE}' to be ready..."
kubectl rollout status deployment/postgres -n "${NAMESPACE}" --timeout=120s
kubectl rollout status deployment/redis -n "${NAMESPACE}" --timeout=120s
kubectl rollout status deployment/payment-gateway -n "${NAMESPACE}" --timeout=120s
kubectl rollout status deployment/multi-secret-app -n "${NAMESPACE}" --timeout=180s
echo "✅ All deployments are running and ready."

echo ""
echo "=================================================================="
echo "🎉 ESO MULTI-SECRET ROTATION EXAMPLE DEPLOYED SUCCESSFULLY!"
echo "=================================================================="
cat <<EOF

📋 Deployed Configuration in '${NAMESPACE}':
------------------------------------------------------------------
SecretStore Reference:   ${SECRET_STORE_NAME} (${SECRET_STORE_KIND})
ExternalSecrets:         db-password-eso, redis-auth-token-eso, payment-api-key-eso
Intermediate Secrets:    db-password-synced, redis-auth-token-synced, payment-api-key-synced
Workload Deployment:     multi-secret-app
DynamicSecretPolicies:   multi-secret-db-policy, multi-secret-redis-policy, multi-secret-payment-policy

🌐 How to View the Live Microservice Dashboard:
------------------------------------------------------------------
Option 1: Using Port-Forward (Immediate):
  kubectl port-forward svc/multi-secret-app 8080:80 -n ${NAMESPACE}
  Open in your browser at: http://localhost:8080

Option 2: Using LoadBalancer External IP:
  kubectl get svc multi-secret-app -n ${NAMESPACE} -w

🔄 External Secrets & Safe Rotation Walkthrough:
------------------------------------------------------------------
1. Create Initial Secrets in your Secret Provider (Vault, AWS, GCP, Azure, etc.):
   - DB Password ('${REMOTE_DB_SECRET_NAME}'):       'InitialPsqlPass123!'
   - Redis Token ('${REMOTE_REDIS_SECRET_NAME}'):    'InitialRedisToken456!'
   - Payment Key ('${REMOTE_PAYMENT_SECRET_NAME}'):  'sk_live_pay_9876543210'

2. Monitor ExternalSecrets and DynamicSecretPolicies:
   Watch ESO synchronize secrets from your provider:
     kubectl get externalsecrets -n ${NAMESPACE} -w

   Watch DSO validate canaries and promote secrets independently:
     kubectl get dynamicsecretpolicies -n ${NAMESPACE} -w
     kubectl get pods -n ${NAMESPACE} -w

3. Test Independent Secret Rotations:

   1. Rotate PostgreSQL Database Password:
      a) Update Postgres user password in cluster:
         kubectl exec deployment/postgres -n ${NAMESPACE} -- psql -U postgres -d appdb -c "ALTER USER postgres WITH PASSWORD 'NewRotatedPsqlPass999!';"
      b) Update secret '${REMOTE_DB_SECRET_NAME}' in your secret provider to 'NewRotatedPsqlPass999!'
      -> ESO polls and syncs db-password-synced.
      -> DSO launches Canary and validates native PostgreSQL probe.
      -> Workload rolling update mutates only 'db-secret-volume'.

   2. Rotate Redis Auth Token:
      a) Update Redis password in cluster:
         kubectl exec deployment/redis -n ${NAMESPACE} -- redis-cli -a InitialRedisToken456! CONFIG SET requirepass "NewRotatedRedisToken888!"
      b) Update secret '${REMOTE_REDIS_SECRET_NAME}' in your secret provider to 'NewRotatedRedisToken888!'
      -> ESO polls and syncs redis-auth-token-synced.
      -> DSO launches Canary and validates Redis connection probe.
      -> Workload rolling update mutates only 'redis-secret-volume'.

   3. Rotate Payment API Gateway Key:
      Update secret '${REMOTE_PAYMENT_SECRET_NAME}' in your secret provider to 'sk_live_pay_new_777777'
      -> ESO polls and syncs payment-api-key-synced.
      -> DSO launches Canary and validates Payment API HTTP probe.
      -> Workload rolling update mutates only 'payment-secret-volume'.

4. Test Invalid Secret and Circuit Breaker Protection:
   Update secret '${REMOTE_PAYMENT_SECRET_NAME}' in your secret provider with an invalid value.
   -> ESO syncs to intermediate secret.
   -> DSO Canary probe fails HTTP health check, rejects rollout, and trips circuit breaker!
   -> The production workload remains completely healthy on the last valid secret.
==================================================================
EOF
