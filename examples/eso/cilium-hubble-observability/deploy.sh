#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="dso-cilium-demo"
echo "Deploying DSO Cilium eBPF & Hubble Observability Demo..."

kubectl apply -f manifests.yaml

echo "Waiting for services to become ready..."
kubectl rollout status deployment/mysql-db -n "${NAMESPACE}" --timeout=90s
kubectl rollout status deployment/payment-processor -n "${NAMESPACE}" --timeout=60s

echo "DSO Cilium Observability Demo is ready!"
echo "Check policy status: kubectl get dsp payment-processor-policy -n ${NAMESPACE}"
