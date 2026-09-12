# Getting Started with ESO & DSO (Universal Multi-Cloud)

This guide walks you through configuring and deploying the **Dynamic Secret Operator (DSO)** in **Decoupled ESO Mode** using the CNCF [External Secrets Operator (ESO)](https://external-secrets.io/).

---

## 1. Prerequisites

Before starting, ensure you have:
- A Kubernetes cluster (v1.26+) running on any cloud provider or on-premises.
- **kubectl** configured to target your cluster.
- **Helm v3** installed locally.
- Access to an external secret vault (e.g. AWS Secrets Manager, GCP Secret Manager, Azure Key Vault, HashiCorp Vault).

---

## 2. Step 1: Install External Secrets Operator (ESO)

Add the official ESO chart repository and install the operator:

```bash
helm repo add external-secrets https://charts.external-secrets.io
helm repo update

helm install external-secrets \
  external-secrets/external-secrets \
  --namespace external-secrets \
  --create-namespace \
  --set installCRDs=true \
  --wait
```

---

## 3. Step 2: Install DSO in ESO Mode

Install DSO with `--set mode=eso`. In this mode, DSO operates strictly on native Kubernetes RBAC and requires **zero cloud credentials**:

### PowerShell (Windows)
```powershell
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=eso `
  --wait
```

### Bash (Linux / macOS)
```bash
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=eso \
  --wait
```

---

## 4. Step 3: Configure SecretStore & ExternalSecret

### 4.1 Define SecretStore
Configure a `SecretStore` (or cluster-wide `ClusterSecretStore`) connecting ESO to your external vault:

```yaml
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: cloud-vault-backend
  namespace: production
spec:
  provider:
    # Example: Azure Key Vault, AWS Secrets Manager, GCP Secret Manager, or HashiCorp Vault
    azurekv:
      vaultUrl: "https://my-prod-vault.vault.azure.net"
      authType: ManagedIdentity
```

### 4.2 Define ExternalSecret with Mandatory Watch Label
Create an `ExternalSecret` that synchronizes credentials into an intermediate secret. You **must** attach the label `dso.quantumsys.dev/managed: "watch"` in `spec.target.template.metadata.labels`:

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: database-credentials-sync
  namespace: production
spec:
  refreshInterval: 1m
  secretStoreRef:
    name: cloud-vault-backend
    kind: SecretStore
  target:
    name: db-credentials-synced
    creationPolicy: Owner
    template:
      metadata:
        labels:
          # CRITICAL: Required for DSO to discover and watch this secret
          dso.quantumsys.dev/managed: "watch"
  data:
    - secretKey: password
      remoteRef:
        key: production-db-password
```

---

## 5. Step 4: Declare DynamicSecretPolicy

Create a `DynamicSecretPolicy` in the same namespace binding the intermediate synced secret (`source.type: K8sSecret`) to your target workload:

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: db-rotation-policy
  namespace: production
spec:
  source:
    type: K8sSecret
    k8sSecret:
      name: db-credentials-synced
  workloadSelector:
    kind: Deployment
    name: order-service
  targetRef:
    volumeName: db-secret-volume
  validationProbes:
    - type: PostgreSQL
      endpoint: "postgres.production.svc.cluster.local:5432"
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

---

## 6. Step 5: End-to-End Verification

1. **Verify ESO Synchronized the Intermediate Secret:**
   ```bash
   kubectl get externalsecrets -n production
   kubectl get secret db-credentials-synced -n production --show-labels
   ```
   Confirm the label `dso.quantumsys.dev/managed=watch` is present.

2. **Verify DSO Policy Status:**
   ```bash
   kubectl get dynamicsecretpolicies -n production
   ```
   The policy should report `PromotionCompleted: True` (or `CanaryProvisioning` during initial startup).

3. **Simulate a Secret Rotation:**
   Update the secret in your cloud vault (or modify the value directly in ESO to test):
   ```bash
   # Within 1m (or configured refreshInterval), ESO writes the new value to db-credentials-synced.
   # DSO detects the hash drift immediately via its informer, deploys the canary,
   # verifies the synthetic probe, and promotes the deployment with zero downtime!
   kubectl get dynamicsecretpolicy db-rotation-policy -n production -w
   ```

---

## 🔗 Next Steps & Troubleshooting

- See the **[ESO Troubleshooting Guide](troubleshooting.md)** for resolving common synchronization delays and label discovery issues.
- Explore full multi-secret, database, and progressive delivery examples in the **[ESO Examples Directory](../../../examples/eso/)**.
