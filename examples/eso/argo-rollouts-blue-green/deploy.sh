#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="dso-examples"
echo "Deploying ESO + Argo Rollouts Blue/Green Progressive Secret Delivery Example..."

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f manifests.yaml -n "${NAMESPACE}"

echo "Deployment submitted successfully. Monitor rollout with:"
echo "kubectl argo rollouts get rollout rollout-payment-service -n ${NAMESPACE} --watch"
