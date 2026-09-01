#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="dso-examples"
echo "Deploying ESO TLS Certificate Rotation Example..."

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f manifests.yaml -n "${NAMESPACE}"

echo "Deployment complete. Test HTTPS gateway:"
echo "kubectl get svc tls-gateway -n ${NAMESPACE}"
