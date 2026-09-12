# Getting Started with Microsoft Azure Key Vault & DSO

This guide walks you through the end-to-end configuration required to deploy and run the **Dynamic Secret Operator (DSO)** in **Event-Driven Mode** on Azure Kubernetes Service (AKS).

---

## 1. Prerequisites

Before starting, ensure you have:
- An active **Azure Subscription** with permissions to manage Key Vault, Service Bus, and Azure RBAC.
- **Azure CLI** (`az`) installed and authenticated (`az login`).
- An **AKS Cluster** with OIDC Issuer and Workload Identity enabled.
- **Helm v3** installed locally.
- **kubectl** configured to target your AKS cluster.

---

## 2. Infrastructure Setup (Azure Cloud)

### 2.1 Enable OIDC Issuer & Workload Identity on AKS
If your AKS cluster was created without Workload Identity, enable it:

```bash
az aks update \
  --resource-group <RESOURCE_GROUP> \
  --name <AKS_CLUSTER_NAME> \
  --enable-oidc-issuer \
  --enable-workload-identity
```

Retrieve the cluster OIDC Issuer URL:
```bash
AKS_OIDC_ISSUER="$(az aks show -n <AKS_CLUSTER_NAME> -g <RESOURCE_GROUP> --query "oidcIssuerProfile.issuerUrl" -o tsv)"
```

### 2.2 Create User-Assigned Managed Identity
Create a dedicated identity for the DSO operator:

```bash
az identity create \
  --name dso-identity \
  --resource-group <RESOURCE_GROUP>

IDENTITY_CLIENT_ID="$(az identity show -n dso-identity -g <RESOURCE_GROUP> --query "clientId" -o tsv)"
```

### 2.3 Assign Azure RBAC Permissions
DSO requires two specific roles to operate under least privilege:

1. **Key Vault Secrets User** (Scoped to target Key Vault):
   ```bash
   VAULT_SCOPE="/subscriptions/<SUBSCRIPTION_ID>/resourceGroups/<RESOURCE_GROUP>/providers/Microsoft.KeyVault/vaults/<VAULT_NAME>"

   az role assignment create \
     --role "Key Vault Secrets User" \
     --assignee "$IDENTITY_CLIENT_ID" \
     --scope "$VAULT_SCOPE"
   ```

2. **Azure Service Bus Data Receiver** (Scoped to target Service Bus Namespace):
   ```bash
   SB_SCOPE="/subscriptions/<SUBSCRIPTION_ID>/resourceGroups/<RESOURCE_GROUP>/providers/Microsoft.ServiceBus/namespaces/<SERVICEBUS_NAMESPACE>"

   az role assignment create \
     --role "Azure Service Bus Data Receiver" \
     --assignee "$IDENTITY_CLIENT_ID" \
     --scope "$SB_SCOPE"
   ```

### 2.4 Establish Workload Identity Federation
Federate the Managed Identity with the operator's Kubernetes ServiceAccount (`dso-dynamic-secret-operator` in namespace `dso-system`):

```bash
az identity federated-credential create \
  --name "dso-federation" \
  --identity-name dso-identity \
  --resource-group <RESOURCE_GROUP> \
  --issuer "$AKS_OIDC_ISSUER" \
  --subject "system:serviceaccount:dso-system:dso-dynamic-secret-operator" \
  --audience "api://AzureADTokenExchange"
```

### 2.5 Configure Event Grid Subscription
Subscribe your Azure Service Bus queue to Key Vault's secret creation lifecycle events:

```bash
KEYVAULT_ID="$(az keyvault show --name <VAULT_NAME> -g <RESOURCE_GROUP> --query id -o tsv)"
QUEUE_ID="$(az servicebus queue show --namespace-name <SERVICEBUS_NAMESPACE> --name <QUEUE_NAME> -g <RESOURCE_GROUP> --query id -o tsv)"

az eventgrid event-subscription create \
  --name "dso-keyvault-rotations" \
  --source-resource-id "$KEYVAULT_ID" \
  --endpoint-type servicebusqueue \
  --endpoint "$QUEUE_ID" \
  --included-event-types Microsoft.KeyVault.SecretNewVersionCreated
```

---

## 3. Helm Installation

Deploy the DSO Helm chart configured for Azure Event-Driven Mode:

### PowerShell (Windows)
```powershell
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=event-driven `
  --set provider=azure `
  --set azure.workloadIdentity.enabled=true `
  --set azure.workloadIdentity.clientId="<MANAGED_IDENTITY_CLIENT_ID>" `
  --set azure.workloadIdentity.tenantId="<AZURE_TENANT_ID>" `
  --set azure.serviceBus.namespace="<SERVICEBUS_NAMESPACE_FQDN>" `
  --set azure.serviceBus.queueName="<QUEUE_NAME>" `
  --wait
```

### Bash (Linux / macOS)
```bash
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=event-driven \
  --set provider=azure \
  --set azure.workloadIdentity.enabled=true \
  --set azure.workloadIdentity.clientId="<MANAGED_IDENTITY_CLIENT_ID>" \
  --set azure.workloadIdentity.tenantId="<AZURE_TENANT_ID>" \
  --set azure.serviceBus.namespace="<SERVICEBUS_NAMESPACE_FQDN>" \
  --set azure.serviceBus.queueName="<QUEUE_NAME>" \
  --wait
```

---

## 4. Declaring a DynamicSecretPolicy

To bind an Azure Key Vault secret to a workload, declare a `DynamicSecretPolicy` with `source.type: AzureKeyVault`:

### Pattern A: Relational Database (PostgreSQL / MySQL Probe)
Performs live connection tests (`SELECT 1`) against the candidate secret before updating the production workload:

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: payment-db-policy
  namespace: production
spec:
  source:
    type: "AzureKeyVault"
    azureKeyVault:
      keyVaultURI: "https://my-prod-vault.vault.azure.net"
      objectName: "payment-db-password"
      objectType: "Secret" # Options: Secret, Certificate, Key
  workloadSelector:
    kind: "Deployment"
    name: "payment-service"
  targetRef:
    volumeName: "db-secret-volume"
  validationProbes:
    - type: "PostgreSQL"
      endpoint: "postgres.production.svc.cluster.local:5432"
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

### Pattern B: Ingress TLS Certificate & Handshake Probe
Automatically intercepts Key Vault Certificates, splits them into `tls.crt` and `tls.key`, and tests live TLS handshakes:

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: edge-gateway-tls-policy
  namespace: production
spec:
  source:
    type: "AzureKeyVault"
    azureKeyVault:
      keyVaultURI: "https://my-prod-vault.vault.azure.net"
      objectName: "wildcard-prod-cert"
      objectType: "Certificate"
  workloadSelector:
    kind: "Deployment"
    name: "ingress-nginx-controller"
  targetRef:
    volumeName: "tls-cert-volume"
  validationProbes:
    - type: "TLS"
      endpoint: "edge-gateway.production.svc.cluster.local:8443"
      queryTimeout: 10
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 2
```

### Pattern C: Microservice with HTTP Health Probe
Issues HTTP status checks against the isolated canary pod:

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: auth-service-policy
  namespace: production
spec:
  source:
    type: "AzureKeyVault"
    azureKeyVault:
      keyVaultURI: "https://my-prod-vault.vault.azure.net"
      objectName: "auth-jwt-secret"
      objectType: "Secret"
  workloadSelector:
    kind: "Deployment"
    name: "auth-service"
  targetRef:
    volumeName: "jwt-volume"
  validationProbes:
    - type: "HTTP"
      endpoint: "http://auth-service.production.svc.cluster.local:8080/healthz"
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

### Pattern D: "Bring Your Own Container" Job Probe (Azure Cache for Redis)
Executes a Kubernetes Job with custom protocol validators (e.g. `redis-cli PING`), injecting candidate credentials via `$(DSO_REVISION_SECRET_NAME)`:

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: azure-redis-cache-policy
  namespace: production
spec:
  source:
    type: "AzureKeyVault"
    azureKeyVault:
      keyVaultURI: "https://my-prod-vault.vault.azure.net"
      objectName: "azure-redis-primary-key"
      objectType: "Secret"
  workloadSelector:
    kind: "Deployment"
    name: "session-worker"
  validationProbes:
    - type: "Job"
      job:
        timeoutSeconds: 30
        jobTemplate:
          spec:
            template:
              spec:
                containers:
                  - name: redis-tester
                    image: redis:7-alpine
                    command: ["/bin/sh", "-c"]
                    args:
                      - |
                        REDIS_PASS=$(cat /secrets/auth/azure-redis-primary-key)
                        redis-cli -h my-redis.redis.cache.windows.net -p 6380 --tls -a "$REDIS_PASS" PING | grep PONG
                    volumeMounts:
                      - name: test-secret
                        mountPath: /secrets/auth
                volumes:
                  - name: test-secret
                    secret:
                      secretName: $(DSO_REVISION_SECRET_NAME)
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

---

## 5. End-to-End Verification

1. **Verify Operator Log Readiness:**
   ```bash
   kubectl logs -n dso-system deploy/dynamic-secret-operator -f
   ```
   Ensure you see the message indicating active queue listening:
   `Listening for Azure Service Bus rotation events on queue <QUEUE_NAME>`.

2. **Trigger a Secret Rotation in Key Vault:**
   ```bash
   az keyvault secret set \
     --vault-name <VAULT_NAME> \
     --name "payment-db-password" \
     --value "my-new-secure-password-v2"
   ```

3. **Monitor the Reconciliation & Canary Rollout:**
   ```bash
   kubectl get dynamicsecretpolicies -n production -w
   ```
   You will observe the policy transition through:
   `RevisionPrepared` $\to$ `CanaryProvisioning` $\to$ `Validating` $\to$ `Promoting` $\to$ `PromotionCompleted`.

4. **Verify Workload State:**
   Inspect the workload to confirm it has rolled over cleanly to the new secret revision:
   ```bash
   kubectl get pods -n production -l app=payment-service
   ```

---

## 🔗 Next Steps & Troubleshooting

- See the **[Azure Troubleshooting Guide](troubleshooting.md)** for resolving common authentication, RBAC, and Service Bus queue issues.
- Browse ready-to-deploy examples in the **[Azure Examples Directory](../../../examples/azure/)**.
