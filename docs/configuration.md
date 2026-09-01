# Dynamic Secret Operator – Configuration & Enterprise Tuning Guide

This document outlines operator runtime flags, Helm configuration values, and enterprise high-throughput concurrency tuning guidelines for the **Dynamic Secret Operator (DSO)**.

---

## ⚙️ CLI Flags & Operator Arguments

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--event-buffer-size` | `1000` | Buffer capacity of the internal event channel bridging Azure Service Bus messages to the controller reconciliation watch queue. |
| `--metrics-bind-address` | `:8080` | The address the Prometheus metrics endpoint binds to. |
| `--health-probe-bind-address` | `:8081` | The address the health (`/healthz`) and readiness (`/readyz`) probes bind to. |
| `--leader-elect` | `true` | Enables high-availability leader election for controller manager instances. |
| `--metrics-secure` | `false` | Enables TLS encryption on the metrics serving endpoint. |
| `--enable-http2` | `false` | Enables HTTP/2 protocol support on the metrics server. |

---

## 🎛️ Helm Values Configuration

In your `values.yaml` or Helm deployment command, configure operator parameters under the `controller` and `metrics` blocks:

```yaml
controller:
  # Buffer capacity for the internal event channel bridging Azure Service Bus to the controller watch queue.
  # Increase for enterprise clusters experiencing high-frequency batch secret rotations.
  eventBufferSize: 1000

metrics:
  port: 8080
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
  prometheusRule:
    enabled: true
```

---

## 🚀 High-Throughput Enterprise Tuning Guidance

### 1. Handling Mass Batch Rotations (>100 Secrets Simultaneously)
During bulk rotation events (such as disaster recovery drills, mass compliance credential renewals, or automated vault-wide updates), Azure Service Bus can deliver hundreds of `SecretNewVersionCreated` events in bursts.

- **Default Capacity:** The default `eventBufferSize` is set to `1000` to buffer massive concurrent bursts without dropping or NACKing messages.
- **Enterprise Recommendations:** For enterprise environments with >1,000 managed policies, configure `controller.eventBufferSize: 2500` or higher to prevent backpressure timeouts:
  ```bash
  helm upgrade dso ./deploy/helm/dso \
    --namespace dso-system \
    --set controller.eventBufferSize=2500
  ```

### 2. Backpressure & Transactional Ack Mechanics
- The operator ingests events via Azure Service Bus **Peek-Lock** mode.
- If the event buffer is temporarily saturated, the ingestion handler applies backpressure with a 2-second timeout before NACKing the message, allowing Azure Service Bus to redeliver the event with exponential backoff rather than losing rotation events.
