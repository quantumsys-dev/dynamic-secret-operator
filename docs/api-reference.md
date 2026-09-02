# DynamicSecretPolicy CRD API Reference

**API Version:** `dso.quantumsys.dev/v1alpha1`  
**Kind:** `DynamicSecretPolicy`  
**Scope:** `Namespaced`

The `DynamicSecretPolicy` Custom Resource Definition (CRD) configures how the **Dynamic Secret Operator (DSO)** monitors Azure Key Vault objects, materializes versioned SecretRevisions, provisions isolated canary workloads, runs validation probes, and safely promotes production workloads.

---

## 📋 Complete YAML Schema Example

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: payment-service-db-policy
  namespace: production
spec:
  # 1. Secret Source Ingestion (Required)
  source:
    type: "K8sSecret" # Options: AzureKeyVault, K8sSecret, AWSSecretsManager, GCPSecretManager, Vault
    parseJSON: false  # When true, unmarshals JSON payloads into discrete keys
    k8sSecret:
      name: "eso-synced-db-pass"

  # 2. Target Workload Selector (Required)
  workloadSelector:
    kind: "Deployment" # Options: Deployment, StatefulSet, DaemonSet, Rollout
    name: "payment-service"
    matchLabels:
      app: "payment-service"

  # 3. Target Mount / Injection Configuration (Optional)
  targetRef:
    volumeName: "db-secret-volume"
    containerName: "payment-api"
    envName: "DB_PASSWORD"

  # 4. Canary Rollout Strategy (Optional, defaulted if omitted)
  canaryStrategy:
    timeoutSeconds: 45
    trafficWeight: 10
    stepWeight: 20
    stepIntervalSeconds: 10

  # 5. Synthetic Validation Probes (Optional)
  validationProbes:
    - type: "PostgreSQL"
      endpoint: "postgres-cluster.production.svc.cluster.local:5432"
      queryTimeout: 5
    - type: "HTTP"
      endpoint: "http://127.0.0.1:8080/healthz"
      path: "/healthz"
      expectedStatus: 200
      queryTimeout: 3
    - type: "TLS"
      endpoint: "127.0.0.1:8443"
      thumbprint: "a1b2c3d4e5f6..."
      queryTimeout: 10

  # 6. Automated Rollback & Circuit Breaker (Optional, defaulted if omitted)
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

---

## 🔍 Specification Field Breakdown (`spec`)

### `spec.source` (Required)
Specifies the pluggable secret provider backend configuration.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `type` | `string` | **Yes** | The source backend type. Supported: `AzureKeyVault`, `K8sSecret`, `AWSSecretsManager`, `GCPSecretManager`, `Vault`. |
| `parseJSON` | `bool` | No | Unmarshals JSON string payloads into discrete key-value pairs in the materialized Secret (default: `false`). |
| `k8sSecret` | `K8sSecretSource` | Context | Required when `type` is `K8sSecret`. Universal multi-cloud ingestion via intermediate ESO Kubernetes secrets. |
| `azureKeyVault` | `AzureKeyVaultSource` | Context | Required when `type` is `AzureKeyVault`. Direct Azure Key Vault event-driven ingestion. |
| `awsSecretsManager`| `AWSSecretsManagerSource` | Context | Reserved for native AWS Secrets Manager + EventBridge/SQS driver. |
| `gcpSecretManager`| `GCPSecretManagerSource` | Context | Reserved for native GCP Secret Manager + Pub/Sub driver. |
| `vault` | `VaultSource` | Context | Reserved for native HashiCorp Vault webhook/engine driver. |

*(Note: `spec.vaultRef` is deprecated but preserved for backwards compatibility with v0.1.x policies).*

---

### `spec.workloadSelector` (Required)
Defines the Kubernetes workload that will receive the materialized secret revisions.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `kind` | `string` | **Yes** | Target workload kind. Supported: `Deployment`, `StatefulSet`, `DaemonSet`, `Rollout` (Argo Rollouts). |
| `name` | `string` | **Yes** | Exact name of the workload in the same namespace. |
| `matchLabels` | `map[string]string` | No | Optional label selector to verify workload identity. |

---

### `spec.targetRef` (Optional)
Specifies where inside the Pod template the secret revision should be attached.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `volumeName` | `string` | No | Name of the `volume` in `spec.template.spec.volumes` whose `secret.secretName` will be updated to the new revision hash. |
| `containerName` | `string` | No | Specific container within the pod template to target (defaults to primary container if omitted). |
| `envName` | `string` | No | Environment variable name to update with `secretKeyRef` pointing to the new secret revision. |

---

### `spec.canaryStrategy` (Optional)
Configures the isolated canary validation phase.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `timeoutSeconds` | `int32` | `30` | Maximum time allowed for canary pod startup and probe validation before declaring a failure. |
| `trafficWeight` | `int32` | `0` | Optional traffic routing percentage (for mesh/Argo Rollouts integrations). |
| `stepWeight` | `int32` | `0` | Stepwise traffic promotion percentage. |
| `stepIntervalSeconds`| `int32` | `0` | Interval between progressive traffic steps. |

---

### `spec.validationProbes[]` (Optional)
Synthetic health and connectivity probes executed against the canary pod before promoting production.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `type` | `string` | **Yes** | Probe engine type. Options: `HTTP`, `TLS`, `PostgreSQL`, `MySQL`, `Job`. |
| `endpoint` | `string` | Context | Host and port to connect to (e.g. `127.0.0.1:8080`, `postgres.db.svc:5432`). Not used for `Job` probes. |
| `path` | `string` | No | HTTP request path (only applicable for `HTTP` probes). |
| `expectedStatus` | `int32` | No | Expected HTTP status code (default: `200`). |
| `headers` | `map[string]string` | No | Custom HTTP request headers. |
| `queryTimeout` | `int32` | No | Timeout in seconds for probe execution (default: `5`). Not used for `Job` probes. |
| `thumbprint` | `string` | No | Expected SHA-1 or SHA-256 certificate thumbprint (for `TLS` probes). |
| `job` | `JobProbeSpec` | Context | Job probe specification. **Required** when `type` is `Job`. |

#### `spec.validationProbes[].job` — `JobProbeSpec`

Configures the ephemeral `batch/v1.Job` launched by the operator as a validation probe.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `timeoutSeconds` | `*int32` | `60` | Maximum seconds to wait for the Job to reach a terminal state. The Job is deleted and the probe fails if this duration is exceeded. |
| `jobTemplate` | `batchv1.JobTemplateSpec` | — | Standard Kubernetes `batch/v1` Job template. The operator sets `backoffLimit: 0` if unset. |

**Secret Name Injection**: The operator automatically injects the `DSO_REVISION_SECRET_NAME` environment variable into all `initContainers` and `containers` in the `jobTemplate`. Users reference `$(DSO_REVISION_SECRET_NAME)` in container `command`, `args`, or environment variable definitions to access the materialized Kubernetes `Secret` holding the new credentials.

**Lifecycle**: the probe Job is always deleted after execution (success or failure) by the operator, preventing resource leaks in the target namespace.

---

### `spec.rollbackConfig` (Optional)
Controls error handling, automatic rollbacks, and circuit breaker trip thresholds.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `autoRollback` | `bool` | `true` | When `true`, automatically restores the last known good secret revision on canary validation failure. |
| `circuitBreakerThreshold` | `int32` | `3` | Number of consecutive failed rotation attempts before tripping the circuit breaker and halting reconciliation. |
| `postRollbackScript` | `string` | `""` | Optional script or webhook trigger executed on rollback. |

---

## 📊 Status Field Breakdown (`status`)

| Field | Type | Description |
| :--- | :--- | :--- |
| `currentRevision` | `string` | Hash of the currently active and promoted SecretRevision in production. |
| `desiredRevision` | `string` | Hash of the target SecretRevision currently being validated or rolled out. |
| `consecutiveFailures` | `int32` | Number of consecutive rotation failures encountered. |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes status conditions: `RevisionPrepared`, `CanaryProvisioning`, `Validating`, `Promoting`, `RolledBack`, `CircuitBreakerTripped`. |

---

## 🛡️ Policy as Code (Kyverno / OPA Gatekeeper)

### Kyverno Validation Rule: Enforce Circuit Breaker & Auto Rollback
```yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: require-dso-circuit-breaker
spec:
  validationFailureAction: Enforce
  rules:
    - name: check-rollback-and-breaker
      match:
        any:
          - resources:
              kinds:
                - DynamicSecretPolicy
      validate:
        message: "DynamicSecretPolicy must have autoRollback: true and circuitBreakerThreshold between 1 and 5"
        pattern:
          spec:
            rollbackConfig:
              autoRollback: true
              circuitBreakerThreshold: ">0 & <=5"
```
