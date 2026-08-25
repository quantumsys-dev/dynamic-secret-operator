#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Local kind & Argo CD Example Setup
# Cluster Name: dso-local
# ==============================================================================

set -euo pipefail

CLUSTER_NAME="dso-local"
ARGOCD_VERSION="v2.11.0"

echo "=================================================================="
echo "🚀 Setting up local kind cluster and Argo CD for DSO example..."
echo "=================================================================="

# 1. Check prerequisites
command -v kind >/dev/null 2>&1 || { echo "❌ Error: 'kind' is required. Please install kind: https://kind.sigs.k8s.io/"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { echo "❌ Error: 'kubectl' is required. Please install kubectl: https://kubernetes.io/docs/tasks/tools/"; exit 1; }

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
  - containerPort: 443
    hostPort: 8443
    protocol: TCP
EOF
fi

# 3. Install Argo CD
echo "🐙 Installing Argo CD (${ARGOCD_VERSION})..."
kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n argocd -f "https://raw.githubusercontent.com/argoproj/argo-cd/${ARGOCD_VERSION}/manifests/install.yaml"

echo "⏳ Waiting for Argo CD server deployment to become ready..."
kubectl wait --for=condition=established --timeout=60s crd/applications.argoproj.io || true
kubectl rollout status deployment/argocd-server -n argocd --timeout=180s

# 4. Install DSO CRDs
echo "🛠️  Installing DynamicSecretPolicy CRD..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    kubectl apply -k "${REPO_ROOT}/config/crd"
fi

# 5. Apply Example Workloads
echo "📄 Deploying example workload manifests..."
if [ -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    kubectl apply -f "${SCRIPT_DIR}/manifests.yaml"
fi

echo "=================================================================="
echo "✅ Local Environment Initialized!"
echo "Cluster context: kind-${CLUSTER_NAME}"
echo ""
echo "Next Steps:"
echo "1. Configure your local .env file with your Azure credentials (see README.md)."
echo "2. Start the operator locally: make install && source .env && make run"
echo "3. Port-forward the example service: kubectl port-forward svc/nginx-color-demo 8080:80"
echo "=================================================================="
