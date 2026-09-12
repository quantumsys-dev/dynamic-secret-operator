# DynamicSecretPolicy CRD API Reference

**API Version:** `dso.quantumsys.dev/v1alpha1`  
**Kind:** `DynamicSecretPolicy`  
**Scope:** `Namespaced`

The `DynamicSecretPolicy` Custom Resource Definition (CRD) configures how the **Dynamic Secret Operator (DSO)** monitors secret sources (Azure Key Vault, External Secrets Operator / Kubernetes Secrets, AWS Secrets Manager, GCP Secret Manager, HashiCorp Vault), materializes immutable versioned `SecretRevision` objects, provisions isolated canary sandboxes, runs synthetic validation probes, and safely promotes production workloads with automated rollback and circuit breaker protection.

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
  # Pluggable backends: AzureKeyVault, K8sSecret (ESO synergy), AWSSecretsManager, GCPSecretManager, Vault
  source:
    type: "K8sSecret"
    parseJSON: false  # When true, unmarshals JSON payloads into discrete keys
    k8sSecret:
      name: "eso-synced-db-pass"

  # 2. Target Workload Selector (Required)
  workloadSelector:
    kind: "Deployment" # Options: Deployment, StatefulSet, DaemonSet, Rollout
    name: "payment-service"

  # 3. Target Mount / Injection Configuration (Optional)
  targetRef:
    volumeName: "db-secret-volume"
    containerName: "payment-api"
    envName: "DB_PASSWORD"

  # 4. Network Policy Isolation Engine (Optional, default: Standard)
  networkPolicy:
    provider: "Standard" # Options: Standard (networking.k8s.io/v1), Cilium (cilium.io/v2 eBPF)

  # 5. Canary Rollout Strategy (Optional, defaulted if omitted)
  canaryStrategy:
    timeoutSeconds: 30 # Duration to wait for canary health and probe validation

  # 6. Synthetic Validation Probes (Optional)
  validationProbes:
    - type: "PostgreSQL"
      endpoint: "postgres-cluster.production.svc.cluster.local:5432/appdb"
      queryTimeout: 5
      credentials:
        passwordKey: "db-password"
        usernameKey: "db-user"
    - type: "HTTP"
      endpoint: "http://127.0.0.1:8080/healthz"
      queryTimeout: 3
    - type: "TLS"
      endpoint: "127.0.0.1:8443"
      queryTimeout: 10
    - type: "Job"
      job:
        timeoutSeconds: 60
        jobTemplate:
          spec:
            backoffLimit: 0
            template:
              spec:
                restartPolicy: Never
                containers:
                  - name: validator
                    image: redis:7-alpine
                    command:
                      - /bin/sh
                      - -c
                      - "redis-cli -h redis-master -a $(DSO_REVISION_SECRET_NAME) PING"

  # 7. Automated Rollback & Circuit Breaker (Optional, defaulted if omitted)
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

---

## 🔍 Specification Field Breakdown (`spec`)

### `spec.source` (Required)
Specifies the pluggable secret provider backend configuration. Exactly one provider configuration matching `spec.source.type` must be provided.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `type` | `string` | **Yes** | The source backend type. Enum: `AzureKeyVault`, `K8sSecret`, `AWSSecretsManager`, `GCPSecretManager`, `Vault`. |
| `parseJSON` | `bool` | No | Unmarshals JSON string payloads into discrete key-value pairs in the materialized Secret (default: `false`). |
| `k8sSecret` | `K8sSecretSource` | Context | Required when `type: K8sSecret`. Universal multi-cloud ingestion via intermediate Kubernetes secrets (e.g. ESO synergy). |
| `azureKeyVault` | `AzureKeyVaultSource` | Context | Required when `type: AzureKeyVault`. Direct Azure Key Vault event-driven ingestion. |
| `awsSecretsManager`| `AWSSecretsManagerSource` | Context | Required when `type: AWSSecretsManager`. AWS Secrets Manager driver. |
| `gcpSecretManager`| `GCPSecretManagerSource` | Context | Required when `type: GCPSecretManager`. Google Cloud Secret Manager driver. |
| `vault` | `VaultSource` | Context | Required when `type: Vault`. HashiCorp Vault driver. |

*(Note: `spec.vaultRef` is deprecated but preserved for backwards compatibility with v0.1.x policies).*

#### `spec.source.k8sSecret`
| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `name` | `string` | **Yes** | Name of the intermediate source secret synchronized by ESO or external tools in the same namespace. |

#### `spec.source.azureKeyVault`
| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `keyVaultURI` | `string` | **Yes** | URI of the Azure Key Vault (must match Azure Key Vault domain format). |
| `objectName` | `string` | **Yes** | Name of the secret, certificate, or key within Key Vault. |
| `objectType` | `string` | No | Object type: `Secret` (default), `Certificate`, or `Key`. |

#### `spec.source.awsSecretsManager`
| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `secretID` | `string` | **Yes** | The ARN or friendly name of the AWS secret. |
| `region` | `string` | No | The AWS region (e.g., `us-east-1`). |

#### `spec.source.gcpSecretManager`
| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `secretID` | `string` | **Yes** | The resource path (`projects/*/secrets/*`) of the GCP secret. |

#### `spec.source.vault`
| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `path` | `string` | **Yes** | The Vault secret path (e.g., `secret/data/payment`). |

---

### `spec.workloadSelector` (Required)
Defines the Kubernetes workload that will receive the materialized secret revisions.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `kind` | `string` | **Yes** | Target workload kind. Enum: `Deployment`, `StatefulSet`, `DaemonSet`, `Rollout` (Argo Rollouts). |
| `name` | `string` | **Yes** | Exact name of the workload in the same namespace. |

---

### `spec.targetRef` (Optional)
Specifies where inside the Pod template the secret revision should be attached to avoid ambiguous mutations.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `volumeName` | `string` | No | Name of the `volume` in `spec.template.spec.volumes` whose `secret.secretName` will be updated to the new revision secret. |
| `containerName` | `string` | No | Target container name when `envName` is used. If empty, matches across all containers and initContainers. |
| `envName` | `string` | No | Environment variable name to update with `valueFrom.secretKeyRef.name` pointing to the new secret revision. |

---

### `spec.networkPolicy` (Optional)
Configures the network isolation engine for canary workloads.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `provider` | `string` | `Standard` | Network policy engine. Enum: `Standard` (generates `networking.k8s.io/v1.NetworkPolicy`), `Cilium` (generates `cilium.io/v2.CiliumNetworkPolicy` for eBPF and Hubble flow telemetry). |

---

### `spec.canaryStrategy` (Optional)
Configures the isolated canary validation phase timing.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `timeoutSeconds` | `int32` | `30` | Maximum time allowed (in seconds, must be > 0) for canary pod startup and probe validation before declaring a failure. |

---

### `spec.validationProbes[]` (Optional)
Synthetic health and connectivity probes executed against the canary pod before promoting production.

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `type` | `string` | **Yes** | Probe engine type. Enum: `TLS`, `PostgreSQL`, `MySQL`, `HTTP`, `Job`. |
| `endpoint` | `string` | Context | Host and port to connect to (e.g. `http://127.0.0.1:8080/healthz`, `postgres-db:5432/appdb`). Required for network probes; not used for `Job` probes. |
| `queryTimeout` | `int32` | No | Probe timeout in seconds (minimum `1`, default: `15` for HTTP, `5` for DB/TLS). Not used for `Job` probes. |
| `credentials` | `ProbeCredentials` | No | Explicit credential key mappings for database probes (`PostgreSQL`, `MySQL`). |
| `job` | `JobProbeSpec` | Context | Job probe specification. **Required** when `type: Job`. |

#### `spec.validationProbes[].credentials` — `ProbeCredentials`
Maps specific keys within the materialized secret to database credentials:

| Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `passwordKey` | `string` | No | Key in the secret holding the password. Falls back to well-known conventions (`password`, `pass`, `POSTGRES_PASSWORD`, etc.) or single-value secret data. |
| `usernameKey` | `string` | No | Key in the secret holding the username (default: `postgres` or endpoint username). |
| `databaseKey` | `string` | No | Key in the secret holding the database name (default: endpoint database or `appdb`). |

#### `spec.validationProbes[].job` — `JobProbeSpec`
Configures the ephemeral `batch/v1.Job` launched by the operator as a validation probe:

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `timeoutSeconds` | `*int32` | `60` | Maximum seconds to wait for the Job to reach a terminal state. If exceeded, the Job is deleted and the probe is marked failed. |
| `jobTemplate` | `batchv1.JobTemplateSpec` | — | Standard Kubernetes `batch/v1` Job template. The operator sets `backoffLimit: 0` if unset. |

> **Secret Name Injection**: The operator automatically injects the `DSO_REVISION_SECRET_NAME` environment variable into all `initContainers` and `containers` in the `jobTemplate`. Containers reference `$(DSO_REVISION_SECRET_NAME)` in container `command`, `args`, or environment variables to dynamically consume candidate credentials.

> **Lifecycle**: The probe Job is owned by the `DynamicSecretPolicy` and is automatically deleted after completion (success or failure), preventing resource leaks.

---

### `spec.rollbackConfig` (Optional)
Controls error handling, automatic rollbacks, and circuit breaker trip thresholds.

| Field | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `autoRollback` | `bool` | `true` | When `true`, automatically restores the last known good secret revision on canary validation failure. |
| `circuitBreakerThreshold` | `int32` | `3` | Number of consecutive failed rotation attempts (minimum `1`) before tripping the circuit breaker and halting reconciliation. |

---

## 📊 Status Field Breakdown (`status`)

| Field | Type | Description |
| :--- | :--- | :--- |
| `currentRevision` | `string` | Hash of the currently active and promoted SecretRevision in production. |
| `desiredRevision` | `string` | Hash of the target SecretRevision currently being validated or rolled out. |
| `consecutiveFailures` | `int32` | Number of consecutive rotation failures encountered. |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes status conditions: `RevisionPrepared`, `CanaryProvisioning`, `Validating`, `RolloutProgressing`, `Promoting`, `RolledBack`, `CircuitBreakerTripped`. |

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
