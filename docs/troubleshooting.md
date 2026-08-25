# Dynamic Secret Operator – Enterprise Troubleshooting Guide

This guide provides operational runbooks and troubleshooting procedures for common production issues encountered when operating the **Dynamic Secret Operator (DSO)**.

---

## 📑 Quick Navigation

1. [Azure Service Bus Peek-Lock & Dead-Letter Queue (DLQ) Triage](#1-azure-service-bus-peek-lock--dead-letter-queue-dlq-triage)
2. [Circuit Breaker Tripped & Auto-Recovery](#2-circuit-breaker-tripped--auto-recovery)
3. [Argo CD GitOps Drift & Revert Loops](#3-argo-cd-gitops-drift--revert-loops)
4. [Validation Probe Failures](#4-validation-probe-failures)
5. [Monitoring Metrics & OpenTelemetry Traces](#5-monitoring-metrics--opentelemetry-traces)

---

## 1. Azure Service Bus Peek-Lock & Dead-Letter Queue (DLQ) Triage

### Symptoms
- Messages accumulate in the Service Bus Queue's Dead-Letter Queue (`$DeadLetterQueue`).
- DSO does not react to secret rotation events from Event Grid.

### Root Causes & Remediation

| Cause | Diagnostic | Remediation |
| :--- | :--- | :--- |
| **Max Delivery Count Exceeded** | `DeliveryCount > MaxDeliveryCount` (default: 10) in message properties. | Occurs if the controller crashes during processing or if the event queue was blocked. DSO uses a 2s push timeout to prevent listener stalls. Inspect DLQ messages using `az servicebus queue show`. |
| **Message Lock Expiration** | Event ingestion took longer than `LockDuration` (default: 30s). | Ensure network connectivity between AKS and Service Bus endpoint. DSO uses Peek-Lock and only ACKs messages after enqueueing into controller memory. |
| **Malformed Event Grid Schema** | Deserialization errors in operator logs. | Verify Event Grid subscription uses standard CloudEvents 1.0 or EventGridSchema with filter `Microsoft.KeyVault.SecretNewVersionCreated`. |

### Reprocessing DLQ Messages with Azure CLI
```bash
# Inspect Dead-Letter message count
az servicebus queue show \
  --resource-group "<RESOURCE_GROUP>" \
  --namespace-name "<SERVICEBUS_NAMESPACE>" \
  --name "<QUEUE_NAME>" \
  --query "countDetails.deadLetterMessageCount"

# Resubmit dead-lettered messages using Service Bus Explorer or Azure CLI tools
```

---

## 2. Circuit Breaker Tripped & Auto-Recovery

### Symptoms
- `DynamicSecretPolicy` status condition shows `CircuitBreakerTripped = True`.
- Operator logs: `circuit breaker tripped due to consecutive failures; halting reconciliations`.

### How It Works & State Machine Recovery
When consecutive rotation failures exceed `spec.rollbackConfig.circuitBreakerThreshold` (default: 3), DSO halts to prevent infinite crash loops on production workloads.

1. **Automatic Upstream Recovery (Drift Detection):**
   - You do **not** need to manually delete the pod or restart the operator.
   - When a security admin updates Azure Key Vault with a corrected secret version, DSO detects upstream version drift, automatically resets `ConsecutiveFailures` to `0`, un-trips the circuit breaker, and launches a fresh canary rollout.

2. **Manual Force Reset:**
   If you need to manually force a reset without waiting for a new Key Vault event:
   ```bash
   # Annotate the policy to trigger a manual reconciliation
   kubectl annotate dynamicsecretpolicy <POLICY_NAME> -n <NAMESPACE> dso.quantumsys.dev/reconcile-trigger="$(date +%s)" --overwrite
   ```

---

## 3. Argo CD GitOps Drift & Revert Loops

### Symptoms
- Pods restart repeatedly.
- Argo CD UI shows the `Application` flipping between `Synced` and `OutOfSync`.
- DSO updates the secret volume, but Argo CD Self-Heal immediately reverts the change back to the Git state.

### Root Cause
Argo CD `selfHeal: true` treats the live cluster volume or revision annotation update as unauthorized drift.

### Remediation
Enable automatic ignoreDifferences patching by setting:
```bash
ARGOCD_AUTOPATCH_ENABLED="true"
```
Or add explicit `ignoreDifferences` to your Argo CD `Application` spec:
```yaml
spec:
  ignoreDifferences:
    - group: apps
      kind: Deployment
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
        - /spec/template/spec/volumes
```
> 📘 **Note:** If your workload uses multiple custom volumes and you only want to ignore the DSO secret volume, use `jqPathExpressions` as detailed in [GitOps Argo CD Guide](gitops-argo-cd.md).

---

## 4. Validation Probe Failures

### Synthetic Database Probe Timeout (PostgreSQL / MySQL)
- **Check Network Policy:** Can the canary pod reach the database host on port 5432 / 3306?
- **Sanitized Errors:** DSO automatically redacts raw passwords from logs and traces. Inspect the sanitized error log:
  ```bash
  kubectl logs -n dso-system deployment/dso-dynamic-secret-operator -f | grep "probe execution failed"
  ```

### TLS Handshake Probe Failures
- **Expired Certificates:** If the rotated certificate in Azure Key Vault has a `NotBefore` in the future or `NotAfter` in the past, the TLS probe will reject it before promotion.
- **Thumbprint Mismatch:** If `spec.validationProbes[].thumbprint` is specified, verify that the SHA-256 hash matches the leaf certificate.

---

## 5. Monitoring Metrics & OpenTelemetry Traces

DSO exposes standard Prometheus metrics on `:8080/metrics` and distributes OTLP traces to your OpenTelemetry collector.

### Essential Prometheus Alerting Rules
```yaml
groups:
  - name: dso-alerts
    rules:
      - alert: DSOCircuitBreakerTripped
        expr: dso_circuit_breakers_tripped_total > 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "DSO Circuit Breaker tripped on {{ $labels.policy }}"
          description: "Multiple consecutive secret rotations failed validation. Operator halted reconciliation to protect production."

      - alert: DSORotationFailureRateHigh
        expr: rate(dso_rotations_failed_total[10m]) / rate(dso_rotations_total[10m]) > 0.2
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High secret rotation failure rate on DSO"
```
