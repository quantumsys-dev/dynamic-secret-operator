#!/usr/bin/env bash
# ==============================================================================
# Fullstack Database Auto-Rotation Example – Local kind Setup Script
# Cluster Name: dso-local
# ==============================================================================

set -euo pipefail

CLUSTER_NAME="dso-local"
IMAGE_TAG="ghcr.io/quantumsys-dev/fullstack-db-rotation-demo:latest"

echo "=================================================================="
echo "🚀 Initializing Fullstack DB Rotation PoC on local kind..."
echo "=================================================================="

# 1. Check prerequisites
command -v kind >/dev/null 2>&1 || { echo "❌ Error: 'kind' is required. https://kind.sigs.k8s.io/"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "❌ Error: 'kubectl' is required. https://kubernetes.io/docs/tasks/tools/"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "❌ Error: 'docker' is required."; exit 1; }

# 2. Create kind cluster if it doesn't already exist
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    echo "ℹ️  kind cluster '${CLUSTER_NAME}' already exists. Switching context..."
    kubectl config use-context "kind-${CLUSTER_NAME}"
else
    echo "📦 Creating kind cluster '${CLUSTER_NAME}'..."
    cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 80
    hostPort: 8080
    protocol: TCP
  - containerPort: 5432
    hostPort: 5432
    protocol: TCP
EOF
fi

# 3. Build & Load Backend Docker Image into kind
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

echo "🐳 Building local Go backend Docker image (${IMAGE_TAG})..."
docker build -t "${IMAGE_TAG}" "${SCRIPT_DIR}"

echo "📥 Loading image into kind cluster '${CLUSTER_NAME}'..."
kind load docker-image "${IMAGE_TAG}" --name "${CLUSTER_NAME}"

# 4. Install DSO CRDs
echo "🛠️  Installing DynamicSecretPolicy CRD..."
if [ -d "${REPO_ROOT}/config/crd" ]; then
    kubectl apply -k "${REPO_ROOT}/config/crd"
fi

# 5. Apply Workload & Database Manifests
echo "📄 Deploying PostgreSQL, Go Backend, and DSO Policy..."
if [ -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    kubectl apply -f "${SCRIPT_DIR}/manifests.yaml"
fi

echo "⏳ Waiting for PostgreSQL to be ready..."
kubectl wait --for=condition=ready pod -l app=postgres --timeout=120s || true

echo "=================================================================="
echo "✅ Fullstack PoC Environment Initialized!"
echo ""
echo "Next Steps:"
echo "1. Configure your local .env file (see README.md)."
echo "2. Start the operator locally: make install && source .env && make run"
echo "3. Port-forward the web dashboard: kubectl port-forward svc/db-status-app 8080:80"
echo "4. Open http://localhost:8080 to observe live database health and 5-min auto-rotations!"
echo "=================================================================="
