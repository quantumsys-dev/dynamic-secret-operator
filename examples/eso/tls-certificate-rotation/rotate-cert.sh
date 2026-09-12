#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Trigger Valid ESO TLS Certificate Rotation
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
echo "🚀 Triggering Valid TLS Certificate Rotation via ESO for '${DOMAIN}'..."
echo "🔑 Azure Key Vault: ${KEYVAULT_NAME}"
echo "=================================================================="

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
POLICY_TEMPLATE="${SCRIPT_DIR}/certificate-policy.json"
TMP_POLICY="$(mktemp)"
trap 'rm -f "${TMP_POLICY}"' EXIT

sed "s/\${DOMAIN}/${DOMAIN}/g" "${POLICY_TEMPLATE}" > "${TMP_POLICY}"

CURRENT_ACCOUNT="$(az account show --query "user.name" -o tsv 2>/dev/null || true)"
CURRENT_USER_ID="$(az ad signed-in-user show --query id -o tsv 2>/dev/null || true)"
if [ -z "${CURRENT_USER_ID}" ] && [ -n "${CURRENT_ACCOUNT}" ]; then
    CURRENT_USER_ID="$(az ad user show --id "${CURRENT_ACCOUNT}" --query id -o tsv 2>/dev/null || true)"
    if [ -z "${CURRENT_USER_ID}" ]; then
        CURRENT_USER_ID="$(az ad sp show --id "${CURRENT_ACCOUNT}" --query id -o tsv 2>/dev/null || true)"
    fi
fi

KV_ID="$(az keyvault show --name "${KEYVAULT_NAME}" --query id -o tsv 2>/dev/null || true)"
if [ -n "${KV_ID}" ]; then
    HAS_CERT_ROLE=false
    if [ -n "${CURRENT_USER_ID}" ]; then
        if az role assignment list --assignee "${CURRENT_USER_ID}" --scope "${KV_ID}" --role "Key Vault Certificates Officer" --query "[0].id" -o tsv 2>/dev/null | grep -q .; then
            HAS_CERT_ROLE=true
        fi
    fi
    if [ "${HAS_CERT_ROLE}" != "true" ] && [ -n "${CURRENT_ACCOUNT}" ]; then
        if az role assignment list --assignee "${CURRENT_ACCOUNT}" --scope "${KV_ID}" --role "Key Vault Certificates Officer" --query "[0].id" -o tsv 2>/dev/null | grep -q .; then
            HAS_CERT_ROLE=true
        fi
    fi

    if [ "${HAS_CERT_ROLE}" != "true" ]; then
        echo "ℹ️  Assigning 'Key Vault Certificates Officer' role to caller..."
        USER_TYPE="$(az account show --query "user.type" -o tsv 2>/dev/null || true)"
        ASSIGNEE_TYPE="User"
        if [ "${USER_TYPE}" = "servicePrincipal" ]; then
            ASSIGNEE_TYPE="ServicePrincipal"
        fi

        ROLE_CREATED=false
        if [ -n "${CURRENT_USER_ID}" ]; then
            if az role assignment create --role "Key Vault Certificates Officer" --assignee-object-id "${CURRENT_USER_ID}" --assignee-principal-type "${ASSIGNEE_TYPE}" --scope "${KV_ID}" --output none 2>/dev/null; then
                ROLE_CREATED=true
            fi
        fi
        if [ "${ROLE_CREATED}" != "true" ] && [ -n "${CURRENT_ACCOUNT}" ]; then
            if az role assignment create --role "Key Vault Certificates Officer" --assignee "${CURRENT_ACCOUNT}" --scope "${KV_ID}" --output none 2>/dev/null; then
                ROLE_CREATED=true
            fi
        fi

        if [ "${ROLE_CREATED}" = "true" ]; then
            echo "✅ Role 'Key Vault Certificates Officer' automatically assigned to caller."
            echo "⏳ Awaiting initial RBAC propagation (10s)..."
            sleep 10
        fi
    fi
fi

echo "ℹ️  Creating new certificate version in Azure Key Vault..."
CREATE_SUCCESS=false
for attempt in 1 2 3 4 5 6; do
    if az keyvault certificate create \
        --vault-name "${KEYVAULT_NAME}" \
        --name "ingress-tls-cert" \
        --policy "@${TMP_POLICY}" \
        --output none 2>&1; then
        CREATE_SUCCESS=true
        break
    fi
    echo "⏳ Waiting for Azure RBAC propagation... (attempt ${attempt}/6)"
    sleep 10
done

if [ "${CREATE_SUCCESS}" != "true" ]; then
    echo "❌ Error: Failed to rotate certificate in Key Vault."
    exit 1
fi

echo "✅ New certificate version generated in Azure Key Vault!"
echo ""
echo "📡 Data Flow: Key Vault -> ESO Sync -> Intermediate K8s Secret -> DSO Watch -> Canary -> Promotion"
echo ""
echo "Watching DSO Policy Status in AKS:"
kubectl get dynamicsecretpolicy eso-ingress-tls-policy -n dso-examples -w
