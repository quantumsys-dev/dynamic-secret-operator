#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Argo Rollouts Blue/Green Example Setup
# Cluster Name: dso-rollouts-local
# ==============================================================================

set -euo pipefail

CLUSTER_NAME="dso-rollouts-local"
ARGO_ROLLOUTS_VERSION="v1.7.2"

echo "=================================================================="
echo "🚀 Setting up local kind cluster with Argo Rollouts for DSO..."
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
    kind create cluster --name "${CLUSTER_NAME}"
fi

# 3. Install Argo Rollouts Controller
echo "📦 Installing Argo Rollouts (${ARGO_ROLLOUTS_VERSION})..."
kubectl create namespace argo-rollouts --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n argo-rollouts -f "https://github.com/argoproj/argo-rollouts/releases/download/${ARGO_ROLLOUTS_VERSION}/install.yaml"

echo "⏳ Waiting for Argo Rollouts controller to become ready..."
kubectl rollout status deployment/argo-rollouts -n argo-rollouts --timeout=120s

# 4. Install DSO CRD
echo "🛠️  Installing DynamicSecretPolicy CRD..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    kubectl apply -k "${REPO_ROOT}/config/crd"
fi

# 5. Apply Example Manifests
echo "📄 Applying Argo Rollout and Policy manifests..."
kubectl apply -f "${SCRIPT_DIR}/manifests.yaml"

echo "=================================================================="
echo "✅ Argo Rollouts Blue/Green Example setup completed!"
echo "📜 Watch rollout: kubectl argo rollouts get rollout rollout-payment-service --watch"
echo "=================================================================="
