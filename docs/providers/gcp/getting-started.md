# Getting Started with GCP Secret Manager & DSO

This guide explains how to configure and deploy the **Dynamic Secret Operator (DSO)** for workloads consuming secrets from **Google Cloud Secret Manager** on Google Kubernetes Engine (GKE).

Two implementation tracks are available:
1. **[Track 1: Production Ready Today (ESO Mode)](#track-1-production-ready-today-eso-mode)** *(Recommended)*
2. **[Track 2: Preview of Native Event-Driven Ingestion (Roadmap v0.3.0)](#track-2-preview-native-event-driven-mode-v030)**

---

## Track 1: Production Ready Today (ESO Mode)

In this decoupled model, the CNCF [External Secrets Operator (ESO)](https://external-secrets.io/) synchronizes credentials from GCP Secret Manager into an intermediate Kubernetes Secret, while DSO manages progressive canary validation, synthetic probes, and zero-downtime workload updates without needing any GCP IAM permissions.

### Step 1: Configure GKE Workload Identity for ESO
1. Create a Google Service Account (GSA):
   ```bash
   gcloud iam service-accounts create eso-service-account \
     --display-name="External Secrets Operator" \
     --project=<PROJECT_ID>
   ```

2. Grant the GSA access to GCP Secret Manager:
   ```bash
   gcloud projects add-iam-policy-binding <PROJECT_ID> \
     --member="serviceAccount:eso-service-account@<PROJECT_ID>.iam.gserviceaccount.com" \
     --role="roles/secretmanager.secretAccessor"
   ```

3. Allow the Kubernetes ServiceAccount to impersonate the GSA:
   ```bash
   gcloud iam service-accounts add-iam-policy-binding \
     eso-service-account@<PROJECT_ID>.iam.gserviceaccount.com \
     --role="roles/iam.workloadIdentityUser" \
     --member="serviceAccount:<PROJECT_ID>.svc.id.goog[external-secrets/external-secrets]"
   ```

### Step 2: Install External Secrets Operator (ESO)
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

### Step 3: Install DSO in ESO Mode
Deploy DSO with `--set mode=eso`. Because ingestion is handled by ESO, DSO requires **zero cloud credentials**:

#### PowerShell (Windows)
```powershell
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=eso `
  --wait
```

#### Bash (Linux / macOS)
```bash
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=eso \
  --wait
```

### Step 4: Create SecretStore & ExternalSecret with Watch Label
```yaml
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: gcp-secrets-backend
  namespace: production
spec:
  provider:
    gcpsm:
      projectID: <PROJECT_ID>
      auth:
        workloadIdentity:
          serviceAccountRef:
            name: external-secrets
---
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: cloud-sql-credentials-sync
  namespace: production
spec:
  refreshInterval: 1m
  secretStoreRef:
    name: gcp-secrets-backend
    kind: SecretStore
  target:
    name: cloud-sql-credentials-synced
    template:
      metadata:
        labels:
          # Mandatory label for DSO discovery
          dso.quantumsys.dev/managed: "watch"
  data:
    - secretKey: password
      remoteRef:
        key: production-db-password
```

### Step 5: Declare DynamicSecretPolicy
Bind the synced secret to your target workload:

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: cloud-sql-policy
  namespace: production
spec:
  source:
    type: K8sSecret
    k8sSecret:
      name: cloud-sql-credentials-synced
  workloadSelector:
    kind: Deployment
    name: customer-service
  targetRef:
    volumeName: db-secret-volume
  validationProbes:
    - type: PostgreSQL
      endpoint: "10.128.0.5:5432"
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

---

## Track 2: Preview: Native Event-Driven Mode (v0.3.0)

> [!WARNING]
> Native direct event-driven ingestion for GCP is currently in active development for **Release v0.3.0**. The instructions below show the planned configuration.

### 1. Infrastructure Architecture
- **Secret Manager Notifications:** Configured to push events to a Cloud Pub/Sub topic:
  ```bash
  gcloud secrets update <SECRET_NAME> \
    --add-topics="projects/<PROJECT_ID>/topics/dso-vault-events"
  ```
- **Cloud Pub/Sub Subscription:** `projects/<PROJECT_ID>/subscriptions/dso-vault-events-sub`.
- **GKE Workload Identity:** Grants DSO roles `roles/secretmanager.secretAccessor` and `roles/pubsub.subscriber`.

### 2. Preview Helm Installation
Deploy DSO configured with GCP native event-driven parameters:

#### PowerShell (Windows)
```powershell
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=event-driven `
  --set provider=gcp `
  --set gcp.enabled=true `
  --set gcp.workloadIdentity.serviceAccount="dso-sa@<PROJECT_ID>.iam.gserviceaccount.com" `
  --set gcp.pubsub.subscription="projects/<PROJECT_ID>/subscriptions/dso-vault-events-sub" `
  --wait
```

#### Bash (Linux / macOS)
```bash
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=event-driven \
  --set provider=gcp \
  --set gcp.enabled=true \
  --set gcp.workloadIdentity.serviceAccount="dso-sa@<PROJECT_ID>.iam.gserviceaccount.com" \
  --set gcp.pubsub.subscription="projects/<PROJECT_ID>/subscriptions/dso-vault-events-sub" \
  --wait
```

### 3. Native DynamicSecretPolicy Syntax
```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: gcp-payment-policy
  namespace: production
spec:
  source:
    type: GCPSecretManager
    gcpSecretManager:
      secretId: "projects/<PROJECT_ID>/secrets/payment-db-password"
  workloadSelector:
    kind: Deployment
    name: payment-service
  targetRef:
    volumeName: db-secret-volume
  validationProbes:
    - type: PostgreSQL
      endpoint: "10.128.0.5:5432"
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

---

## 🔗 Related Documentation

- [GCP Provider Overview](README.md)
- [GCP Troubleshooting Guide](troubleshooting.md)
- [Universal Multi-Cloud via ESO Guide](../eso/README.md)
