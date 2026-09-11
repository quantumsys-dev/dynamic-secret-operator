#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy ESO NGINX Color Rotation Example
# ==============================================================================
# This visual demonstration showcases real-time web application configuration
# changes (background color rotation) driven by External Secrets Operator (ESO)
# and safely validated with zero downtime via Dynamic Secret Operator (DSO).
# ==============================================================================

set -euo pipefail

NAMESPACE="dso-examples"
SECRET_STORE_NAME=""
SECRET_STORE_KIND=""
REMOTE_SECRET_NAME="nginx-bg-color"

while getopts "n:s:k:r:h" opt; do
    case "${opt}" in
        n) NAMESPACE="${OPTARG}" ;;
        s) SECRET_STORE_NAME="${OPTARG}" ;;
        k) SECRET_STORE_KIND="${OPTARG}" ;;
        r) REMOTE_SECRET_NAME="${OPTARG}" ;;
        h) echo "Usage: $0 -s <SECRET_STORE_NAME> -k <SECRET_STORE_KIND> [-n <NAMESPACE>] [-r <REMOTE_SECRET_NAME>]"; exit 0 ;;
        *) echo "Usage: $0 -s <SECRET_STORE_NAME> -k <SECRET_STORE_KIND> [-n <NAMESPACE>] [-r <REMOTE_SECRET_NAME>]"; exit 1 ;;
    esac
done

if [ -z "${SECRET_STORE_NAME}" ] || [ -z "${SECRET_STORE_KIND}" ]; then
    echo "❌ Error: -s <SECRET_STORE_NAME> and -k <SECRET_STORE_KIND> are required parameters."
    echo "Usage: $0 -s <SECRET_STORE_NAME> -k <SECRET_STORE_KIND> [-n <NAMESPACE>] [-r <REMOTE_SECRET_NAME>]"
    exit 1
fi

echo "=================================================================="
echo "🚀 Deploying ESO NGINX Color Rotation Example..."
echo "=================================================================="
echo "ℹ️  Target Namespace:   ${NAMESPACE}"
echo "ℹ️  SecretStore Name:   ${SECRET_STORE_NAME}"
echo "ℹ️  SecretStore Kind:   ${SECRET_STORE_KIND}"
echo "ℹ️  Remote Secret Name: ${REMOTE_SECRET_NAME}"

# 1. Check prerequisites
command -v kubectl >/dev/null 2>&1 || { echo "❌ Error: 'kubectl' is required."; exit 1; }

CURRENT_CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
if [ -z "${CURRENT_CONTEXT}" ]; then
    echo "❌ Error: Not connected to any Kubernetes cluster."
    exit 1
fi
echo "✅ Connected to cluster: ${CURRENT_CONTEXT}"

# 2. Ensure target namespace exists
kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
echo "✅ Namespace '${NAMESPACE}' is ready."

# 3. Create bootstrap initial secret
if ! kubectl get secret nginx-color-app-nginx-bg-color-initial -n "${NAMESPACE}" >/dev/null 2>&1; then
    kubectl create secret generic nginx-color-app-nginx-bg-color-initial \
        --namespace "${NAMESPACE}" \
        --from-literal=nginx-bg-color="#3b82f6" \
        --dry-run=client -o yaml | kubectl apply -f - >/dev/null
    echo "✅ Bootstrap initial secret created with default color '#3b82f6' (Blue)."
else
    echo "ℹ️  Bootstrap initial secret already exists."
fi

# 4. Apply DynamicSecretPolicy CRD if present
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
if [ -d "${REPO_ROOT}/config/crd/bases" ]; then
    kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/config/crd/bases" >/dev/null 2>&1 || true
    echo "✅ DynamicSecretPolicy CRD applied."
fi

# 5. Apply manifests
echo "📄 Applying manifests with Store '${SECRET_STORE_NAME}' (${SECRET_STORE_KIND}), Remote Key '${REMOTE_SECRET_NAME}'..."
if [ "${NAMESPACE}" != "dso-examples" ]; then
    sed -e "s/\${SECRET_STORE_NAME}/${SECRET_STORE_NAME}/g" \
        -e "s/\${SECRET_STORE_KIND}/${SECRET_STORE_KIND}/g" \
        -e "s/\${REMOTE_SECRET_NAME}/${REMOTE_SECRET_NAME}/g" \
        -e "s/namespace: dso-examples/namespace: ${NAMESPACE}/g" \
        "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f - -n "${NAMESPACE}"
else
    sed -e "s/\${SECRET_STORE_NAME}/${SECRET_STORE_NAME}/g" \
        -e "s/\${SECRET_STORE_KIND}/${SECRET_STORE_KIND}/g" \
        -e "s/\${REMOTE_SECRET_NAME}/${REMOTE_SECRET_NAME}/g" \
        "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f - -n "${NAMESPACE}"
fi
echo "✅ Manifests applied successfully."

# 6. Wait for rollout
echo "⏳ Waiting for deployment rollout..."
kubectl rollout status deployment/nginx-color-app -n "${NAMESPACE}" --timeout=120s
echo "✅ Deployment 'nginx-color-app' is running and ready."

echo ""
echo "=================================================================="
echo "🎉 ESO NGINX COLOR ROTATION EXAMPLE DEPLOYED SUCCESSFULLY!"
echo "=================================================================="
cat <<EOF

📋 Deployed Configuration in '${NAMESPACE}':
------------------------------------------------------------------
Deployment:           nginx-color-app
Service:              nginx-color-app (LoadBalancer)
SecretStore:          ${SECRET_STORE_NAME} (${SECRET_STORE_KIND})
ExternalSecret:       nginx-bg-color-eso
Remote Secret Name:   ${REMOTE_SECRET_NAME}
DynamicSecretPolicy:  eso-nginx-color-policy

🌐 How to View the Live Web Application:
------------------------------------------------------------------
Option 1: Using Port-Forward (Immediate):
  kubectl port-forward svc/nginx-color-app 8080:80 -n ${NAMESPACE}
  Open in your browser at: http://localhost:8080

Option 2: Using LoadBalancer External IP:
  kubectl get svc nginx-color-app -n ${NAMESPACE} -w

🔄 External Secrets & Safe Rotation Walkthrough:
------------------------------------------------------------------
1. Create Initial Secret in your Secret Provider:
   Create secret '${REMOTE_SECRET_NAME}' with an initial CSS hex color (e.g. '#3b82f6' - Blue) in your secret provider.

2. Wait for ESO Polling Interval & Verify:
   Wait for ESO to poll and sync the secret (refresh interval: 15s):
     kubectl get externalsecret nginx-bg-color-eso -n ${NAMESPACE} -w
   Verify that STATUS is 'SecretSynced' and READY is 'True'.
   Open http://localhost:8080 to see the active Blue background (#3b82f6).

3. Trigger a Valid Color Rotation (Canary Promotion):
   Update secret '${REMOTE_SECRET_NAME}' in your secret provider to a new CSS hex color (e.g. '#10b981' - Emerald Green).

   Watch ESO poll the change and DSO validate the canary:
     kubectl get dynamicsecretpolicy eso-nginx-color-policy -n ${NAMESPACE} -w
     kubectl get pods -n ${NAMESPACE} -w
   Refresh http://localhost:8080 — the background updates to Green with zero downtime!

4. Test Invalid Color & Circuit Breaker Protection:
   Update secret '${REMOTE_SECRET_NAME}' in your secret provider with an invalid CSS color (e.g. 'not-a-color').

   Watch ESO poll the update, then observe DSO run the validation probe:
     kubectl get dynamicsecretpolicy eso-nginx-color-policy -n ${NAMESPACE} -w
     kubectl get jobs -n ${NAMESPACE} -w
   DSO rejects the invalid canary revision, keeps production untouched, and trips the circuit breaker!
   Refresh http://localhost:8080 — the production workload remains running smoothly on the last valid color.
==================================================================
EOF
