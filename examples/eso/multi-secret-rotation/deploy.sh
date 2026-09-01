#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="dso-examples"
echo "Deploying ESO Multi-Secret Rotation Example..."

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f manifests.yaml -n "${NAMESPACE}"

echo "Deployment complete. Check dashboard service:"
echo "kubectl get svc multi-secret-app -n ${NAMESPACE}"
