# Google Cloud Platform (GCP) Secret Manager Provider Guide

> [!WARNING]
> **Status: 🟡 Under Active Development (Roadmap v0.3.0)**
>
> Native direct event-driven ingestion for Google Cloud Secret Manager (via Cloud Pub/Sub) is currently being developed.
> 
> **For production GCP workloads today:** You can achieve automated canary rollout, probe validation, and rollback right now by using the **[Universal Multi-Cloud Provider via External Secrets Operator (ESO)](eso.md)**. See the [ESO GCP Secret Manager integration](#recommended-production-alternative-today-eso-mode) section below.

---

## 1. Architectural Model (Target v0.3.0)

Once released, DSO's native GCP provider will deliver real-time, event-driven secret rotation without polling:

```mermaid
sequenceDiagram
    autonumber
    actor SecAdmin as Security Admin / CI-CD
    participant GSM as GCP Secret Manager
    participant PubSub as Cloud Pub/Sub
    participant DSO as DSO Controller (GKE)
    participant Workload as Target Workload (App)

    SecAdmin->>GSM: AddSecretVersion / Update Secret
    GSM->>PubSub: Publish Event Notification
    PubSub->>DSO: Deliver via StreamingPull (Workload Identity)
    DSO->>GSM: AccessSecretVersion via GCP Client Libraries
    DSO->>DSO: Materialize Immutable Revision Secret
    DSO->>DSO: Launch Isolated Canary + Synthetic Probes
    DSO->>Workload: Zero-Downtime Rollout & Settle
    DSO->>PubSub: Acknowledge (ACK) Message
```

---

## 2. Planned Infrastructure Prerequisites (v0.3.0)

When native GCP support lands, the setup will require:
1. **Google Kubernetes Engine (GKE) Cluster** with Workload Identity enabled.
2. **Google Cloud Secret Manager Secret** configured with event notifications publishing to a Cloud Pub/Sub topic:
   ```bash
   gcloud secrets update <SECRET_NAME> \
     --add-topics="projects/<PROJECT_ID>/topics/<TOPIC_NAME>"
   ```
3. **Cloud Pub/Sub Subscription** (e.g., `projects/<PROJECT_ID>/subscriptions/dso-vault-events-sub`).
4. **Google Service Account (GSA)** bound to DSO's Kubernetes Service Account (`dso-system:dso-dynamic-secret-operator`) with roles:
   - `roles/secretmanager.secretAccessor`
   - `roles/pubsub.subscriber`

---

## 3. Planned Helm Installation (v0.3.0 Preview)

*(Note: Target syntax for the upcoming v0.3.0 release)*

### PowerShell (Windows)
```powershell
# Note: Native GCP provider is currently under development (Roadmap v0.3.0)
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

### Bash (Linux / macOS)
```bash
# Note: Native GCP provider is currently under development (Roadmap v0.3.0)
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

---

## 4. Planned DynamicSecretPolicy CRD (v0.3.0)

When native GCP push ingestion lands in v0.3.0, you can bind secrets directly via `source.type: GCPSecretManager`. Below are real-world policy patterns demonstrating various probe types:

### Pattern A: Ephemeral Batch Job Probe (Cloud Memorystore Redis)
*Uses a "Bring Your Own Container" Job probe running `redis-cli PING` to verify rotated Memorystore Redis credentials:*

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: gcp-redis-cache-policy
  namespace: production
spec:
  source:
    type: "GCPSecretManager"
    gcpSecretManager:
      secretId: "projects/my-project/secrets/memorystore-redis-auth"
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
                        REDIS_PASS=$(cat /secrets/auth/password)
                        redis-cli -h 10.0.0.5 -a "$REDIS_PASS" PING | grep PONG
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

### Pattern B: Web Microservice with HTTP Health Probe
*Asserts that the candidate microservice starts successfully and responds with HTTP 200 before routing live production traffic:*

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: gcp-payment-api-policy
  namespace: production
spec:
  source:
    type: "GCPSecretManager"
    gcpSecretManager:
      secretId: "projects/my-project/secrets/payment-api-keys"
  workloadSelector:
    kind: "Deployment"
    name: "payment-api"
  targetRef:
    volumeName: "api-tokens"
  validationProbes:
    - type: "HTTP"
      endpoint: "http://payment-api.production.svc.cluster.local:8080/healthz"
      path: "/healthz"
      expectedStatus: 200
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

### Pattern C: Cloud SQL Relational Database Probe
*Validates database credential rotation against Cloud SQL PostgreSQL/MySQL using a live `SELECT 1` query with automatic credential sanitization:*

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: gcp-cloudsql-db-policy
  namespace: production
spec:
  source:
    type: "GCPSecretManager"
    gcpSecretManager:
      secretId: "projects/my-project/secrets/cloudsql-db-password"
  workloadSelector:
    kind: "Deployment"
    name: "user-service"
  targetRef:
    volumeName: "db-credentials"
  validationProbes:
    - type: "PostgreSQL" # Also supports "MySQL"
      endpoint: "cloudsql-proxy.production.svc.cluster.local:5432"
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

### Pattern D: Ingress Gateway with TLS Certificate Handshake Probe
*Intercepts rotated TLS certificates and verifies live TLS handshake and SHA-256 thumbprint matching:*

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: gcp-ingress-tls-policy
  namespace: ingress-system
spec:
  source:
    type: "GCPSecretManager"
    gcpSecretManager:
      secretId: "projects/my-project/secrets/wildcard-ingress-tls"
  workloadSelector:
    kind: "Deployment"
    name: "ingress-nginx-controller"
  validationProbes:
    - type: "TLS"
      endpoint: "edge-gateway.ingress-system.svc.cluster.local:443"
      thumbprint: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
      queryTimeout: 10
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 2
```

---

## 5. Recommended Production Alternative Today: ESO Mode

To run dynamic secret rotation on GCP **today in production**, use DSO's **External Secrets Operator (ESO)** integration:

1. Deploy ESO on your GKE cluster:
   ```bash
   helm repo add external-secrets https://charts.external-secrets.io
   helm repo update
   helm install external-secrets external-secrets/external-secrets \
     --namespace external-secrets \
     --create-namespace \
     --set installCRDs=true
   ```

2. Deploy DSO in decoupled ESO mode (requiring **no cloud credentials** inside DSO):
   ```bash
   helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
     --namespace dso-system \
     --create-namespace \
     --set mode=eso
   ```

3. Configure an ESO `SecretStore` authenticating to GCP Secret Manager via Workload Identity and an `ExternalSecret` with the watch label `dso.quantumsys.dev/managed: "watch"`:
   ```yaml
   apiVersion: external-secrets.io/v1beta1
   kind: ExternalSecret
   metadata:
     name: gcp-db-secret
     namespace: production
   spec:
     refreshInterval: 1m
     secretStoreRef:
       name: gcp-secret-store
       kind: SecretStore
     target:
       name: gcp-synced-db-pass
       template:
         metadata:
           labels:
             dso.quantumsys.dev/managed: "watch"
     data:
       - secretKey: password
         remoteRef:
           key: payment-db-password
   ```

4. Create a `DynamicSecretPolicy` pointing to `k8sSecret.name: "gcp-synced-db-pass"`.

For complete details, see the [ESO Provider Guide](eso.md) and [Operating Modes Guide](../operating-modes.md).
