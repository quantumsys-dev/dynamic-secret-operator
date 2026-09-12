# Observability & Metrics Architecture

The **Dynamic Secret Operator (DSO)** provides enterprise-grade observability natively instrumented with **Prometheus** metrics and **OpenTelemetry** distributed tracing. 

This document details how to access the metrics endpoint, the complete catalog of exposed metrics, cardinality controls, and production alerting rules.

---

## 1. Accessing DSO Metrics

The operator exposes Prometheus metrics over HTTP at `/metrics` (configured by default on port `8080`).

### 1.1 Quick Inspection via Port-Forwarding
To inspect live metrics from your local workstation:

```bash
# Port-forward the operator metrics port
kubectl port-forward -n dso-system deploy/dynamic-secret-operator 8080:8080

# In another terminal, query the Prometheus metrics endpoint
curl -s http://localhost:8080/metrics
```

### 1.2 Automated Scraping with Prometheus Operator (ServiceMonitor)
If your cluster runs **Prometheus Operator** (e.g. `kube-prometheus-stack`), enable the built-in `ServiceMonitor` via Helm in `values.yaml`:

```yaml
metrics:
  port: 8080
  serviceMonitor:
    enabled: true
    interval: 30s
    scrapeTimeout: 10s
    labels:
      release: prometheus-stack
```

Or deploy a standalone `ServiceMonitor` manifest:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: dynamic-secret-operator
  namespace: dso-system
  labels:
    app.kubernetes.io/name: dynamic-secret-operator
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: dynamic-secret-operator
  endpoints:
    - port: metrics
      path: /metrics
      interval: 30s
      scrapeTimeout: 10s
```

### 1.3 Standard Prometheus Pod Annotations
If your monitoring agent uses standard scrape annotations, ensure the operator deployment template contains:

```yaml
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"
    prometheus.io/path: "/metrics"
```

---

## 2. Exposed Metrics Catalog

DSO metrics are registered directly into controller-runtime's global Prometheus registry (`sigs.k8s.io/controller-runtime/pkg/metrics`).

### 2.1 Operator Domain Metrics (`dso_*`)

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `dso_rotations_total` | Counter | `namespace` | Total number of dynamic secret rotation progression cycles initiated. Increments whenever a secret drift is detected and reconciliation begins. |
| `dso_rotations_failed_total` | Counter | `namespace` | Total number of failed validation probe cycles. Increments when candidate credentials fail synthetic checks (HTTP, TLS, DB, Job). |
| `dso_circuit_breakers_tripped_total` | Counter | `namespace` | Total number of times a policy's consecutive failures reached `circuitBreakerThreshold`, halting further automated updates to protect production. |
| `dso_probe_duration_seconds` | Histogram | `namespace`, `probe_type` | Latency distribution of synthetic validation probe execution in seconds. Buckets: `[0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]`.<br/>`probe_type` values: `HTTP`, `TLS`, `PostgreSQL`, `MySQL`, `Job`. |
| `dso_servicebus_messages_total` | Counter | `status` | Total number of cloud message queue events processed in Event-Driven mode. Partitioned by `status`: `ack` (completed), `nack` (abandoned/retried), `dlq` (dead-lettered). |
| `dso_keyvault_fetch_latency_seconds` | Histogram | `vault_name`, `status` | Latency distribution in seconds for fetching secrets/certificates from upstream vaults. Partitioned by `status`: `success`, `error`. |

---

### 2.2 Controller-Runtime Engine Metrics

Because DSO is built on the official Kubernetes `controller-runtime`, it automatically surfaces standard reconciliation queue and worker metrics:

| Metric Name | Type | Labels | Description |
| :--- | :--- | :--- | :--- |
| `controller_runtime_reconcile_total` | Counter | `controller`, `result` | Total number of reconciliations per controller. `result="success"` or `result="error"`. |
| `controller_runtime_reconcile_errors_total` | Counter | `controller` | Total number of reconciliation errors encountered. |
| `controller_runtime_reconcile_time_seconds` | Histogram | `controller` | Latency of the reconciliation loop from queue dequeue to completion. |
| `workqueue_depth` | Gauge | `name` | Current depth of the work queue (number of policies waiting to be processed). |
| `workqueue_adds_total` | Counter | `name` | Total number of items added to the work queue. |
| `workqueue_queue_duration_seconds` | Histogram | `name` | How long an item spent sitting in the work queue before processing started. |

---

### 2.3 Go Runtime & Process Metrics

Standard runtime health metrics are also exposed for monitoring memory, CPU, and goroutines:

* `go_goroutines`: Current number of active goroutines (useful for detecting goroutine leaks in event listeners).
* `process_resident_memory_bytes`: Physical memory (RSS) consumed by the operator binary.
* `process_cpu_seconds_total`: Total user and system CPU time spent in seconds.
* `go_gc_duration_seconds`: A summary of GC invocation durations.

---

## 3. High-Cardinality Protection & Security

### 3.1 Strict Label Cardinality Governance
In Kubernetes clusters running hundreds of microservices, unbounded label cardinality can degrade or crash Prometheus scrapers. DSO enforces strict cardinality boundaries:
* **No Dynamic Secret Names in Metrics:** Metrics such as `dso_keyvault_fetch_latency_seconds` and `dso_rotations_total` partition by `namespace`, `vault_name`, and `probe_type`, but **never** by `secret_name` or `revision_hash`.
* **Predictable Dimensionality:** Because revision hashes (`<workload>-rev-<sha256>`) change on every rotation, omitting them from Prometheus labels guarantees that time series counts remain bounded and stable over time.

### 3.2 Secret Leakage Immunity
Under the Zero Trust threat model, **no secret values, passwords, connection strings, or authorization headers** are ever passed into Prometheus labels, metric descriptions, or OpenTelemetry span attributes.

---

## 4. Production Prometheus Alerting Rules

Below are production-ready Prometheus alerting rules to include in your cluster monitoring manifests:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: dso-alerts
  namespace: dso-system
  labels:
    role: alert-rules
spec:
  groups:
    - name: dynamic-secret-operator.rules
      rules:
        # 1. Alert when a Circuit Breaker trips (Critical)
        - alert: DSOCircuitBreakerTripped
          expr: increase(dso_circuit_breakers_tripped_total[5m]) > 0
          for: 1m
          labels:
            severity: critical
          annotations:
            summary: "DSO Circuit Breaker tripped in namespace {{ $labels.namespace }}"
            description: "A DynamicSecretPolicy in namespace {{ $labels.namespace }} exceeded its consecutive failure threshold. Secret rotation halted to protect production workloads."

        # 2. Alert on high rotation failure rate (Warning)
        - alert: DSOHighRotationFailureRate
          expr: |
            (
              sum(rate(dso_rotations_failed_total[10m])) by (namespace)
              /
              sum(rate(dso_rotations_total[10m])) by (namespace)
            ) > 0.3
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "High secret rotation failure rate in namespace {{ $labels.namespace }}"
            description: "Over 30% of secret rotation validation probes are failing in namespace {{ $labels.namespace }} over the last 10 minutes."

        # 3. Alert on reconciliation backlog / worker queue stall
        - alert: DSOReconcileQueueStalled
          expr: workqueue_depth{name="dynamicsecretpolicy"} > 25
          for: 10m
          labels:
            severity: warning
          annotations:
            summary: "DSO reconciliation workqueue backlog high"
            description: "The controller workqueue depth has exceeded 25 items for more than 10 minutes. Check controller CPU limits or increase --max-concurrent-reconciles."

        # 4. Alert on high validation probe duration
        - alert: DSOValidationProbeLatencyHigh
          expr: histogram_quantile(0.95, sum(rate(dso_probe_duration_seconds_bucket[5m])) by (le, probe_type)) > 20
          for: 5m
          labels:
            severity: warning
          annotations:
            summary: "Synthetic validation probe p95 latency > 20s for probe type {{ $labels.probe_type }}"
            description: "Validation probes of type {{ $labels.probe_type }} are taking abnormally long to validate candidate canary workloads."
```

---

## 5. Distributed Tracing with OpenTelemetry

In addition to Prometheus metrics, DSO is instrumented with the **OpenTelemetry Go SDK** (`go.opentelemetry.io/otel`).

* **Tracer Scope:** `github.com/quantumsys-dev/dynamic-secret-operator`
* **Context Propagation:** Adheres to **W3C TraceContext** and **W3C Baggage** specifications (`traceparent`, `tracestate`).
* **Root Spans Emitted:**
  - `ExecuteProbe`: Measures end-to-end execution of synthetic probes (HTTP, TLS, Database, Job).
  - `MaterializeSecretRevision`: Tracks cryptographic hashing and secret creation.
  - `DeployCanary` / `TeardownCanary`: Measures sandbox orchestration lifecycle.

Traces can be directed to any OTLP-compatible backend (Jaeger, Grafana Tempo, Azure Monitor / Application Insights, Datadog) by configuring standard OpenTelemetry environment variables on the operator container:

```yaml
env:
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: "http://opentelemetry-collector.monitoring.svc:4317"
  - name: OTEL_SERVICE_NAME
    value: "dynamic-secret-operator"
```

---

## 🔗 Related Resources

- [Operator Configuration Reference](configuration.md)
- [Enterprise Troubleshooting Guide](troubleshooting.md)
- [Security Architecture & Threat Model](security.md)
