#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Sidecar Proxy Reloader Example Setup
# Cluster Name: dso-sidecar-local
# ==============================================================================

set -euo pipefail

CLUSTER_NAME="dso-sidecar-local"

echo "=================================================================="
echo "🔄 Setting up local kind cluster for DSO Sidecar Reloader Example..."
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

# 3. Install DSO CRD
echo "🛠️  Installing DynamicSecretPolicy CRD..."
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd" ]; then
    kubectl apply -k "${REPO_ROOT}/config/crd"
fi

# 4. Apply Example Manifests
echo "📄 Applying Sidecar Reloader manifests..."
kubectl apply -f "${SCRIPT_DIR}/manifests.yaml"

echo "=================================================================="
echo "✅ Sidecar Reloader Example setup completed!"
echo "📜 Watch sidecar logs: kubectl logs -f deployment/sidecar-api-service -c secret-reloader"
echo "=================================================================="
