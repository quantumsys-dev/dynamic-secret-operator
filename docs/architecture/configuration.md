# Dynamic Secret Operator – Configuration & Enterprise Tuning Guide

This document outlines operator runtime flags, environment variables, Helm configuration values, and enterprise high-throughput concurrency tuning guidelines for the **Dynamic Secret Operator (DSO)**.

---

## ⚙️ CLI Flags & Operator Arguments

The operator binary accepts the following command-line flags (defined in `cmd/main.go`):

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--mode` | `event-driven` | Operating mode: `event-driven` (push-based cloud message queues) or `eso` (universal multi-cloud decoupled mode). Can be overridden via `DSO_MODE` env var. |
| `--provider` | `azure` | Target secret provider backend when `--mode=event-driven`: `azure`, `aws`, or `gcp` (ignored when `--mode=eso`). Can be overridden via `DSO_PROVIDER` env var. |
| `--event-buffer-size` | `1000` | Buffer capacity of the internal event channel bridging cloud message queues (Azure Service Bus, AWS SQS, GCP Pub/Sub) to the controller reconciliation watch queue. |
| `--max-concurrent-reconciles` | `5` | Maximum number of concurrent reconciliation workers executing policy evaluations, canary rollouts, and validation probes simultaneously. |
| `--sync-period` | `5m` | Full resync period for the controller manager cache. Bounds maximum drift detection latency against source backends if external message queue events are delayed or lost. |
| `--watch-namespaces` | `""` | Comma-separated list of namespaces to restrict the operator cache and informers to (matches `rbac.scope: Namespaced`). Leave empty (default) to watch all namespaces cluster-wide with a ClusterRole. |
| `--metrics-bind-address` | `:8080` | The network address and port the Prometheus metrics server binds to. |
| `--metrics-secure` | `false` | Enables TLS encryption on the Prometheus metrics endpoint. |
| `--health-probe-bind-address` | `:8081` | The network address and port the health (`/healthz`) and readiness (`/readyz`) probes bind to. |
| `--leader-elect` | `true` | Enables high-availability leader election for controller manager instances across multiple replicas. |
| `--zap-log-level` | `info` | Zap logger verbosity level: `debug`, `info`, `warn`, `error`. |
| `--zap-encoder` | `json` | Log format encoder: `json` (production default) or `console`. |

---

## 🎛️ Helm Values Configuration

In your `values.yaml` or Helm deployment command, configure operator parameters:

```yaml
# Operating mode: "event-driven" or "eso"
mode: "event-driven"

# Secret provider backend (required only when mode: "event-driven"): "azure", "aws", or "gcp"
# In "eso" mode, provider is not required.
provider: "azure"

# Role-Based Access Control (RBAC) scoping
rbac:
  create: true
  # "Cluster" (default: ClusterRole for all namespaces) or "Namespaced" (Role limited to target namespaces)
  scope: "Cluster"
  # List of namespaces when scope is "Namespaced" (e.g. ["production", "payments"])
  watchNamespaces: []

# Controller concurrency and buffer tuning
controller:
  # Buffer capacity for the internal event channel bridging cloud message queues to the watch queue
  eventBufferSize: 1000
  # Maximum concurrent reconciliation workers for high-throughput policy processing
  maxConcurrentReconciles: 5
  # Periodic cache resync interval to ensure zero-drift reconciliation
  syncPeriod: "5m"

# Argo CD GitOps integration
argoCD:
  # Automatically discover and patch parent Argo CD Application ignoreDifferences
  # to eliminate Self-Heal drift loops on operator-managed secret revisions.
  autoPatch: false

# Prometheus Metrics & Alerting
metrics:
  port: 8080
  serviceMonitor:
    enabled: false
    interval: 30s
    scrapeTimeout: 10s
    labels: {}
  prometheusRule:
    enabled: false
    labels: {}
```

---

## 🚀 High-Throughput Enterprise Tuning Guidance

### 1. Handling Mass Batch Rotations (>100 Secrets Simultaneously)
During bulk rotation events (such as disaster recovery drills, mass compliance credential renewals, or automated vault-wide updates), cloud message queues (Azure Service Bus, Amazon SQS, Google Cloud Pub/Sub) can deliver hundreds of secret rotation events in bursts.

- **Event Buffer Capacity (`controller.eventBufferSize`):**
  - **Default:** `1000` events in memory.
  - **Enterprise Recommendation:** For enterprise environments with >1,000 managed policies, configure `controller.eventBufferSize: 2500` or higher to prevent queue backpressure timeouts:
    ```bash
    helm upgrade dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
      --namespace dso-system \
      --set controller.eventBufferSize=2500
    ```

- **Reconciliation Concurrency (`controller.maxConcurrentReconciles`):**
  - **Default:** `5` concurrent workers.
  - **Enterprise Recommendation:** When managing hundreds of distinct microservices rotating concurrently, increase `maxConcurrentReconciles` to `15`–`30` to avoid serialization bottlenecks in probe executions and rollout progressions:
    ```bash
    helm upgrade dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
      --namespace dso-system \
      --set controller.maxConcurrentReconciles=20
    ```

---

### 2. Backpressure & Transactional Ack Mechanics
- The operator ingests events via cloud-native reliable messaging (e.g., Azure Service Bus **Peek-Lock**, Amazon SQS **Visibility Timeout**, GCP Pub/Sub **StreamingPull**).
- If the event buffer is temporarily saturated, the ingestion handler applies backpressure with a 2-second timeout before NACKing the message, allowing cloud message brokers to redeliver the event with exponential backoff rather than losing rotation events.
- Once the controller processes the event and materializes the new `SecretRevision`, it marks the message as completed/deleted in the broker.

---

### 3. Drift Resilience & Sync Period (`controller.syncPeriod`)
- In addition to event-driven triggers, the controller executes a full reconciliation sweep governed by `syncPeriod` (default: `5m`).
- This guarantees that if an external cloud message is ever dropped, deleted, or blocked by a transient network issue, the operator detects the drift within 5 minutes, checks the source vault, and initiates the progressive canary rollout.

---

### 4. Least-Privilege Multi-Tenant Scoping (`rbac.scope`)
- In enterprise shared clusters or regulated environments (PCI-DSS, HIPAA), granting cluster-wide secret read/write permissions via `ClusterRole` may violate security policies.
- By setting `rbac.scope: Namespaced` and providing `rbac.watchNamespaces: ["team-a", "team-b"]`, the operator:
  - Generates namespaced `Role` and `RoleBinding` objects strictly in the designated namespaces.
  - Passes `--watch-namespaces=team-a,team-b` to the manager, restricting its client-go cache and informers to those namespaces only.
  - Operates completely without cluster-wide RBAC privileges.

---

## 🔗 Related Resources

- [Observability & Metrics Reference](metrics.md)
- [Universal Architecture Troubleshooting Guide](troubleshooting.md)
- [Operating Modes: ESO vs. Event-Driven](operating-modes.md)
- [GitOps Integration: Managing Argo CD Drift](gitops-argo-cd.md)
- [Security Architecture & Threat Model](security.md)
