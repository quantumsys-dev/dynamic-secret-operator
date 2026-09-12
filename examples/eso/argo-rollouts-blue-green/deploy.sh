#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO Argo Rollouts Blue/Green Example
# ==============================================================================
# This multi-cloud demonstration showcases automated zero-downtime Blue/Green
# secret delivery with Argo Rollouts driven by External Secrets Operator (ESO)
# and Dynamic Secret Operator (DSO).
# ==============================================================================

set -euo pipefail

NAMESPACE="dso-examples"
SECRET_STORE_NAME=""
SECRET_STORE_KIND=""
REMOTE_SECRET_NAME="payment-db-password"
ARGO_ROLLOUTS_VERSION="v1.7.2"

print_usage() {
    echo "Usage: $0 -s <SECRET_STORE_NAME> -k <SECRET_STORE_KIND> [-n <NAMESPACE>] [-r <REMOTE_SECRET_NAME>] [-v <ARGO_VERSION>]"
    echo "  -s    Name of the SecretStore or ClusterSecretStore    [required]"
    echo "  -k    Kind of the store (SecretStore / ClusterSecretStore) [required]"
    echo "  -n    Target Kubernetes namespace                      [default: dso-examples]"
    echo "  -r    Remote secret name in your secret store          [default: payment-db-password]"
    echo "  -v    Argo Rollouts controller version                 [default: v1.7.2]"
    exit 1
}

while getopts "s:k:n:r:v:h" opt; do
    case "${opt}" in
        s) SECRET_STORE_NAME="${OPTARG}" ;;
        k) SECRET_STORE_KIND="${OPTARG}" ;;
        n) NAMESPACE="${OPTARG}" ;;
        r) REMOTE_SECRET_NAME="${OPTARG}" ;;
        v) ARGO_ROLLOUTS_VERSION="${OPTARG}" ;;
        h) print_usage ;;
        *) print_usage ;;
    esac
done

if [ -z "${SECRET_STORE_NAME}" ] || [ -z "${SECRET_STORE_KIND}" ]; then
    echo "❌ Error: -s <SECRET_STORE_NAME> and -k <SECRET_STORE_KIND> are required parameters."
    print_usage
fi

echo "=================================================================="
echo "🚀 Deploying ESO + Argo Rollouts Blue/Green Example..."
echo "=================================================================="
echo "ℹ️  Target Namespace:    ${NAMESPACE}"
echo "ℹ️  SecretStore Name:    ${SECRET_STORE_NAME}"
echo "ℹ️  SecretStore Kind:    ${SECRET_STORE_KIND}"
echo "ℹ️  Remote Secret Name:  ${REMOTE_SECRET_NAME}"
echo "ℹ️  Argo Rollouts Ver:   ${ARGO_ROLLOUTS_VERSION}"

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

# 3. Check and install Argo Rollouts controller if needed
if ! kubectl get crd rollouts.argoproj.io >/dev/null 2>&1; then
    echo "ℹ️  Argo Rollouts CRD not found. Installing Argo Rollouts (${ARGO_ROLLOUTS_VERSION})..."
    kubectl create namespace argo-rollouts --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    kubectl apply -n argo-rollouts -f "https://github.com/argoproj/argo-rollouts/releases/download/${ARGO_ROLLOUTS_VERSION}/install.yaml"
    echo "⏳ Waiting for Argo Rollouts controller deployment to become ready..."
    kubectl rollout status deployment/argo-rollouts -n argo-rollouts --timeout=120s
    echo "✅ Argo Rollouts controller (${ARGO_ROLLOUTS_VERSION}) installed and ready."
else
    echo "✅ Argo Rollouts is already installed in the cluster."
fi

# 4. Create bootstrap initial secret
echo "🔒 Creating bootstrap initial secret in namespace '${NAMESPACE}'..."
kubectl create secret generic rollout-payment-service-payment-db-password-initial \
    --namespace "${NAMESPACE}" \
    --from-literal=payment-db-password="initial-database-password-v1" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
echo "✅ Bootstrap initial secret created."

# 5. Apply DynamicSecretPolicy CRD if present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
if [ -d "${REPO_ROOT}/config/crd/bases" ]; then
    kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/config/crd/bases" >/dev/null 2>&1 || true
    echo "✅ DynamicSecretPolicy CRD applied."
fi

# 6. Apply manifests
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
    -e "s/payment-service-active\.dso-examples\.svc\.cluster\.local/payment-service-active.${NAMESPACE}.svc.cluster.local/g" \
    "${MANIFEST_PATH}" | kubectl apply -f - -n "${NAMESPACE}"

echo "✅ Manifests applied successfully."

echo ""
echo "=================================================================="
echo "🎉 ESO + ARGO ROLLOUTS BLUE/GREEN EXAMPLE DEPLOYED SUCCESSFULLY!"
echo "=================================================================="
cat <<EOF

📋 Deployed Configuration in '${NAMESPACE}':
------------------------------------------------------------------
SecretStore Reference:   ${SECRET_STORE_NAME} (${SECRET_STORE_KIND})
ExternalSecret:          payment-db-password-eso
Intermediate Secret:     payment-db-password-synced
Workload Rollout:        rollout-payment-service (Argo Rollout)
Active Service:          payment-service-active (LoadBalancer)
Preview Service:         payment-service-preview (ClusterIP)
DynamicSecretPolicy:     eso-rollout-payment-policy

🌐 How to Access the Live Active Payment Service:
------------------------------------------------------------------
Option 1: Using Port-Forward (Immediate):
  kubectl port-forward svc/payment-service-active 8080:80 -n ${NAMESPACE}
  Open in your browser or curl: http://localhost:8080

Option 2: Using LoadBalancer External IP:
  kubectl get svc payment-service-active -n ${NAMESPACE} -w

🔍 STEP-BY-STEP VERIFICATION & ROTATION GUIDE:
------------------------------------------------------------------

1️⃣ Monitor Argo Rollouts & DSO in Real Time (in separate terminals):
   - Watch Argo Rollouts Blue/Green State & ReplicaSets:
     kubectl argo rollouts get rollout rollout-payment-service -n ${NAMESPACE} --watch
     (or standard kubectl: kubectl get pods -n ${NAMESPACE} -l app=payment-service -w)

   - Watch DynamicSecretPolicy State Machine:
     kubectl get dynamicsecretpolicy eso-rollout-payment-policy -n ${NAMESPACE} -w

   - Watch ESO Secret Synchronization:
     kubectl get externalsecrets -n ${NAMESPACE} -w

2️⃣ Execute Safe Blue/Green Secret Rotation:
   🔹 Step 2.1: Update secret '${REMOTE_SECRET_NAME}' in your Secret Provider (Vault, AWS, GCP, Azure, etc.):
      Set the secret '${REMOTE_SECRET_NAME}' value to 'NewPaymentPassword2026_Rotated!'

   🔹 Step 2.2: Observe Progressive Delivery & Zero-Downtime Shift:
      1. ESO synchronizes the new secret to 'payment-db-password-synced'.
      2. DSO creates candidate revision and updates the Rollout template.
      3. Argo Rollouts spins up the new Green ReplicaSet alongside Blue.
      4. DSO validates preview pods using configured validation probes.
      5. Once Healthy, Argo Rollouts performs an atomic cutover of active traffic from Blue to Green.
      6. The old Blue ReplicaSet is gracefully scaled down after the scaleDownDelaySeconds window.
      7. Active service experiences zero dropped connections or 5xx errors!

3️⃣ Test Invalid Secret & Circuit Breaker Protection:
   - Update secret '${REMOTE_SECRET_NAME}' in your provider with an invalid value.
   - ESO synchronizes the intermediate secret.
   - DSO detects the change and attempts validation.
   - If the candidate revision fails probe validation, rollout promotion is aborted!
   - Active traffic remains securely pointed to the healthy Blue ReplicaSet with 100% uptime.
==================================================================
EOF
