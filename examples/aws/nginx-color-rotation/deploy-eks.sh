#!/usr/bin/env bash
# ==============================================================================
# Dynamic Secret Operator (DSO) – Deploy Nginx Color Rotation Example on EKS
# ==============================================================================

set -euo pipefail

KEYVAULT_NAME=""

print_usage() {
    echo "Usage: $0 -k <KEYVAULT_NAME>"
    echo "  -k    Name of the AWS Secrets Manager (e.g., kv-dso-dev)"
    exit 1
}

while getopts "k:h" opt; do
    case "${opt}" in
        k) KEYVAULT_NAME="${OPTARG}" ;;
        h) print_usage ;;
        *) print_usage ;;
    esac
done

if [ -z "${KEYVAULT_NAME}" ]; then
    echo "❌ Error: Secrets Manager name is required."
    print_usage
fi

echo "=================================================================="
echo "🚀 Deploying Nginx Color Rotation Example to EKS Cluster..."
echo "🔑 Target Secrets Manager: ${KEYVAULT_NAME}"
echo "=================================================================="

# 1. Check prerequisites
command -v kubectl >/dev/null 2>&1 || { echo "❌ Error: 'kubectl' is required."; exit 1; }
command -v az >/dev/null 2>&1 || { echo "❌ Error: 'az' AWS CLI is required."; exit 1; }

# 2. Check cluster connection
CURRENT_CONTEXT="$(kubectl config current-context 2>/dev/null || true)"
if [ -z "${CURRENT_CONTEXT}" ]; then
    echo "❌ Error: Not connected to any Kubernetes cluster. Please run 'az EKS get-credentials' first."
    exit 1
fi
echo "☸️  Using Kubernetes Context: ${CURRENT_CONTEXT}"

# 3. Verify Secrets Manager accessibility
echo "🔑 Verifying access to AWS Secrets Manager '${KEYVAULT_NAME}'..."
if ! aws secretsmanager describe-secret --secret-id "${KEYVAULT_NAME}" >/dev/null 2>&1; then
    echo "❌ Error: Unable to access Secrets Manager '${KEYVAULT_NAME}'. Please verify the name and your AWS permissions."
    exit 1
fi
echo "✅ Secrets Manager '${KEYVAULT_NAME}' verified."

# 4. Seed initial secret in AWS Secrets Manager if not exists
echo "🔑 Checking secret 'nginx-bg-color' in AWS Secrets Manager '${KEYVAULT_NAME}'..."
if ! aws secretsmanager get-secret-value --secret-id "${KEYVAULT_NAME}" --secret-id "nginx-bg-color" >/dev/null 2>&1; then
    echo "ℹ️  Creating initial secret 'nginx-bg-color' in Secrets Manager..."
    aws secretsmanager put-secret-value \
        --secret-id "${KEYVAULT_NAME}" \
        --secret-id "nginx-bg-color" \
        --secret-string "#3b82f6" \
        --output none || { echo "❌ Error: Failed to create secret 'nginx-bg-color' in Secrets Manager '${KEYVAULT_NAME}'."; exit 1; }
    echo "✅ Initial secret 'nginx-bg-color' seeded in Secrets Manager."
else
    echo "ℹ️  Secret 'nginx-bg-color' already exists in Secrets Manager."
fi

# 5. Ensure target namespace exists and install DSO CRD
echo "📦 Ensuring namespace 'dso-examples' exists..."
kubectl create namespace dso-examples --dry-run=client -o yaml | kubectl apply -f - >/dev/null

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

if [ -d "${REPO_ROOT}/config/crd/bases" ]; then
    echo "🛠️  Applying DynamicSecretPolicy CRD..."
    kubectl apply --server-side --force-conflicts -f "${REPO_ROOT}/config/crd/bases" || { echo "❌ Error: Failed to apply DynamicSecretPolicy CRD."; exit 1; }
    echo "✅ CRD applied."
fi

# 6. Apply manifests with Secrets Manager replacement
echo "📄 Deploying Nginx Color App and DynamicSecretPolicy manifests..."
if [ ! -f "${SCRIPT_DIR}/manifests.yaml" ]; then
    echo "❌ Error: Manifest file not found at ${SCRIPT_DIR}/manifests.yaml"
    exit 1
fi
sed "s/\${KEYVAULT_NAME}/${KEYVAULT_NAME}/g" "${SCRIPT_DIR}/manifests.yaml" | kubectl apply -f - || { echo "❌ Error: Failed to apply manifests."; exit 1; }

echo "⏳ Waiting for Nginx Color App deployment to be ready..."
kubectl rollout status deployment/nginx-color-app -n dso-examples --timeout=120s || { echo "❌ Error: Deployment rollout failed or timed out."; exit 1; }

# 8. Check and display Public LoadBalancer Service IP
echo "🔍 Checking Public LoadBalancer IP for nginx-color-app..."
EXT_IP="$(kubectl get svc nginx-color-app -n dso-examples -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)"
if [ -z "${EXT_IP}" ]; then
    echo "ℹ️  LoadBalancer Public IP is still being provisioned by AWS (status: <pending>)."
    echo "ℹ️  Run 'kubectl get svc nginx-color-app -n dso-examples -w' to view the public IP as soon as AWS assigns it."
else
    echo "✅ Public IP assigned: http://${EXT_IP}"
fi

echo "=================================================================="
echo "✅ Nginx Color Rotation Example deployed successfully on EKS!"
echo "=================================================================="
echo ""
echo "📋 STEP-BY-STEP VERIFICATION GUIDE:"
echo "------------------------------------------------------------------"
echo ""
echo "1️⃣ Access the Nginx Web App:"
echo "   - Public URL (LoadBalancer):"
echo "     kubectl get svc nginx-color-app -n dso-examples"
echo "     (Open http://<EXTERNAL-IP> in your browser)"
echo ""
echo "   - Fallback (Port-Forward):"
echo "     kubectl port-forward svc/nginx-color-app 8080:80 -n dso-examples"
echo "     (Open http://localhost:8080)"
echo ""
echo "2️⃣ Monitor DSO and Workload in Real Time (in a separate terminal):"
echo "   - Watch DSO State Machine & Conditions:"
echo "     kubectl get dynamicsecretpolicy EKS-nginx-color-policy -n dso-examples -w"
echo ""
echo "   - Watch Pod Rollout & Canary Lifecycle:"
echo "     kubectl get pods -n dso-examples -l app=nginx-color-app -w"
echo ""
echo "   - Stream Operator Logs:"
echo "     kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f"
echo ""
echo "3️⃣ Trigger a Secret Rotation in AWS Secrets Manager:"
echo "   aws secretsmanager put-secret-value --secret-id ${KEYVAULT_NAME} --secret-id 'nginx-bg-color' --secret-string '#10b981'"
echo ""
echo "4️⃣ Observe Zero-Downtime Promotion:"
echo "   - Secrets Manager publishes SecretNewVersionCreated event to AWS Service Bus."
echo "   - DSO triggers Canary Provisioning, runs synthetic Job validation probe to assert valid hex color."
echo "   - Target Deployment 'nginx-color-app' is promoted to the new color with zero downtime!"
echo "   - Refresh your browser to see the background change from Blue (#3b82f6) to Green (#10b981)!"
echo ""
echo "5️⃣ Test Circuit Breaker & Safe Abort (Optional):"
echo "   - Inject an invalid value that fails format validation:"
echo "     aws secretsmanager put-secret-value --secret-id ${KEYVAULT_NAME} --secret-id 'nginx-bg-color' --secret-string 'INVALID_COLOR'"
echo "   - Watch DSO Job probe fail hex format validation ('INVALID_COLOR' is not a valid hex code)."
echo "   - DSO aborts promotion and protects production workloads from invalid secrets."
echo "   - After reaching threshold (3 failures), DSO trips the Circuit Breaker (CircuitBreakerTripped: True)!"
echo "   - Live traffic remains 100% online on the previous stable color!"
echo "=================================================================="

