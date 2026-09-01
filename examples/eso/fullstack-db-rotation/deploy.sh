#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="dso-examples"
echo "Deploying ESO Fullstack Database Rotation PoC..."

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f manifests.yaml -n "${NAMESPACE}"

echo "Deployment complete. Check UI service:"
echo "kubectl get svc db-status-app -n ${NAMESPACE}"
