#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="dso-examples"
echo "Deploying ESO Job-Based Redis Probe Example..."

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f manifests.yaml -n "${NAMESPACE}"

echo "Deployment complete. Monitor Redis logs:"
echo "kubectl logs -l app=redis-consumer -n ${NAMESPACE} -f"
