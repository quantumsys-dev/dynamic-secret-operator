#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – TLS Certificate Rotation Example Setup
# Cluster Name: dso-tls-local
# ==============================================================================

set -euo pipefail

CLUSTER_NAME="dso-tls-local"

echo "=================================================================="
echo "🔒 Setting up local kind cluster for DSO TLS Rotation Example..."
echo "=================================================================="

# 1. Check prerequisites
command -v kind >/dev/null 2>&1 || { echo "❌ Error: 'kind' is required. Please install kind: https://kind.sigs.k8s.io/"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "❌ Error: 'kubectl' is required. Please install kubectl: https://kubernetes.io/docs/tasks/tools/"; exit 1; }
command -v openssl >/dev/null 2>&1 || { echo "❌ Error: 'openssl' is required."; exit 1; }

# 2. Create kind cluster if it doesn't already exist
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    echo "ℹ️  kind cluster '${CLUSTER_NAME}' already exists. Switching context..."
    kubectl config use-context "kind-${CLUSTER_NAME}"
else
    echo "📦 Creating kind cluster '${CLUSTER_NAME}' with port 8443 mapped..."
    cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 8443
    hostPort: 8443
    protocol: TCP
EOF
fi

# 3. Generate initial self-signed demo TLS certificate
echo "🔑 Generating self-signed demo TLS certificate..."
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TEMP_DIR}"' EXIT

openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
    -keyout "${TEMP_DIR}/tls.key" \
    -out "${TEMP_DIR}/tls.crt" \
    -subj "/CN=localhost/O=QuantumSys/OU=Security" >/dev/null 2>&1

kubectl create secret tls tls-gateway-ingress-tls-cert-initial \
    --cert="${TEMP_DIR}/tls.crt" \
    --key="${TEMP_DIR}/tls.key" \
    --dry-run=client -o yaml | kubectl apply -f -

# 4. Install DSO CRD
echo "🛠️  Installing DynamicSecretPolicy CRD..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    kubectl apply -k "${REPO_ROOT}/config/crd"
fi

# 5. Apply Example Manifests
echo "📄 Applying TLS Gateway manifests..."
kubectl apply -f "${SCRIPT_DIR}/manifests.yaml"

echo "=================================================================="
echo "✅ TLS Rotation Example setup completed!"
echo "🌐 Verify HTTPS Endpoint: curl -k https://localhost:8443"
echo "=================================================================="
