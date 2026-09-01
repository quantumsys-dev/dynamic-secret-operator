#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="dso-circuit-breaker-demo"
echo "Deploying DSO Circuit Breaker & Automated Rollback Demo..."

kubectl apply -f manifests.yaml

echo "Waiting for PostgreSQL and orders-api to become ready..."
kubectl rollout status deployment/postgres-db -n "${NAMESPACE}" --timeout=60s
kubectl rollout status deployment/orders-api -n "${NAMESPACE}" --timeout=60s

echo "DSO Circuit Breaker Demo is ready!"
echo "Check policy status: kubectl get dsp orders-api-policy -n ${NAMESPACE}"
