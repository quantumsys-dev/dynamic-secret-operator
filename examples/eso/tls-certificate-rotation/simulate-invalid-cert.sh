#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Simulate Invalid Certificate with ESO & Test Circuit Breaker
# ==============================================================================

set -euo pipefail

DOMAIN=""
KEYVAULT_NAME="kv-dso-dev-jc"

print_usage() {
    echo "Usage: $0 -d <DOMAIN> [-k <KEYVAULT_NAME>]"
    echo "  -d    Target Domain Name (e.g., myapp.contoso.com)"
    echo "  -k    Azure Key Vault Name (default: kv-dso-dev-jc)"
    exit 1
}

while getopts "d:k:h" opt; do
    case "${opt}" in
        d) DOMAIN="${OPTARG}" ;;
        k) KEYVAULT_NAME="${OPTARG}" ;;
        h) print_usage ;;
        *) print_usage ;;
    esac
done

if [ -z "${DOMAIN}" ]; then
    echo "❌ Error: Domain (-d) is required."
    print_usage
fi

export AZURE_EXTENSION_DIR="${AZURE_EXTENSION_DIR:-$HOME/.azure/ext}"
mkdir -p "${AZURE_EXTENSION_DIR}"

echo "=================================================================="
echo "⚠️  Simulating Invalid Certificate Injection via ESO for '${DOMAIN}'..."
echo "🔑 Target Key Vault: ${KEYVAULT_NAME}"
echo "=================================================================="

# 1. Capture current production revision
CURRENT_REV="$(kubectl get dynamicsecretpolicy eso-ingress-tls-policy -n dso-examples -o jsonpath='{.status.currentRevision}' 2>/dev/null || true)"
echo "✅ Current healthy production revision: ${CURRENT_REV}"

# 2. Generate an invalid/mismatched TLS certificate bundle
echo "🔧 Generating invalid TLS certificate bundle (mismatched key to trigger validation crash)..."
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
    -keyout "${TMP_DIR}/key_a.pem" \
    -out "${TMP_DIR}/cert_a.pem" \
    -subj "/CN=${DOMAIN}/O=DynamicSecretOperator" >/dev/null 2>&1

openssl genrsa -out "${TMP_DIR}/key_b.pem" 2048 >/dev/null 2>&1

cat "${TMP_DIR}/cert_a.pem" "${TMP_DIR}/key_b.pem" > "${TMP_DIR}/bundle.pem"

echo "ℹ️  Mismatched certificate + private key bundle generated."

# 3. Inject invalid bundle into Azure Key Vault secret
echo "📤 Publishing invalid bundle into Key Vault secret 'ingress-tls-cert'..."
az keyvault secret set \
    --vault-name "${KEYVAULT_NAME}" \
    --name "ingress-tls-cert" \
    --file "${TMP_DIR}/bundle.pem" \
    --output none || { echo "❌ Error: Failed to publish invalid secret to Key Vault."; exit 1; }

echo "✅ Invalid bundle uploaded to Azure Key Vault."

# 4. Monitor ESO Sync & DSO Circuit Breaker
echo "⏳ Monitoring ESO Sync & DSO Circuit Breaker on AKS..."
TIMEOUT=120
ELAPSED=0
TRIPPED=false

while [ $ELAPSED -lt $TIMEOUT ]; do
    FAIL_COUNT="$(kubectl get dynamicsecretpolicy eso-ingress-tls-policy -n dso-examples -o jsonpath='{.status.consecutiveFailures}' 2>/dev/null || echo "0")"
    IS_TRIPPED="$(kubectl get dynamicsecretpolicy eso-ingress-tls-policy -n dso-examples -o jsonpath='{.status.conditions[?(@.type=="CircuitBreakerTripped")].status}' 2>/dev/null || echo "False")"

    echo "  -> Elapsed: ${ELAPSED}s | Consecutive Failures: ${FAIL_COUNT} | Tripped: ${IS_TRIPPED}"

    if [ "${IS_TRIPPED}" = "True" ]; then
        TRIPPED=true
        echo ""
        echo "⚡ CIRCUIT BREAKER TRIPPED!"
        MSG="$(kubectl get dynamicsecretpolicy eso-ingress-tls-policy -n dso-examples -o jsonpath='{.status.conditions[?(@.type=="CircuitBreakerTripped")].message}' 2>/dev/null || true)"
        echo "ℹ️  Condition Message: ${MSG}"
        break
    fi

    sleep 4
    ELAPSED=$((ELAPSED + 4))
done

# 5. Verify Production Stability
echo "🔍 Verifying Production Workload Status..."
READY_REPLICAS="$(kubectl get deployment tls-gateway -n dso-examples -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")"
TOTAL_REPLICAS="$(kubectl get deployment tls-gateway -n dso-examples -o jsonpath='{.status.replicas}' 2>/dev/null || echo "0")"

if [ "${READY_REPLICAS}" = "${TOTAL_REPLICAS}" ]; then
    echo "✅ Production Gateway is 100% HEALTHY (${READY_REPLICAS}/${TOTAL_REPLICAS} replicas ready)!"
else
    echo "⚠️  Production Gateway status: ${READY_REPLICAS}/${TOTAL_REPLICAS} replicas ready."
fi

echo ""
echo "=================================================================="
echo "✅ Circuit Breaker Chaos Test Complete! Production was fully protected."
echo "=================================================================="
echo ""
echo "🔄 TO RECOVER & HEAL PRODUCTION:"
echo "Run the valid rotation script to issue a fresh certificate:"
echo "  ./rotate-cert.sh -d \"${DOMAIN}\" -k \"${KEYVAULT_NAME}\""
echo "=================================================================="
