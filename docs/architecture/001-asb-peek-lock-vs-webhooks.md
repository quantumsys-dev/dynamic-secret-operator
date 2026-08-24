# ADR-001: Azure Service Bus Peek-Lock vs Event Grid Webhooks

## Status
**Accepted**

## Context
When secrets or certificates rotate inside Azure Key Vault, the `dynamic-secret-operator` (DSO) must be notified in near real-time to trigger progressive canary validation and seamless workload promotion.

Azure provides two primary integration patterns for Key Vault rotation events:
1. **Push-based Event Grid Webhooks:** Azure Event Grid posts HTTP notifications directly to a publicly exposed ingress endpoint or webhook hosted within the Kubernetes cluster.
2. **Pull-based Azure Service Bus Queue (with Event Grid routing):** Azure Event Grid routes `Microsoft.KeyVault.SecretNewVersionCreated` events into an Azure Service Bus queue. DSO connects via outbound TCP/AMQP and pulls messages using the **Peek-Lock** pattern.

## Decision
We decided to adopt the **Pull-based Azure Service Bus Queue with Peek-Lock** pattern.

### Architectural Rationale
1. **Zero-Trust Network Ingress Posture:**
   - Exposing an inbound HTTP webhook endpoint requires a public IP, DNS record, TLS certificate management, and ingress controller or load balancer rules.
   - Many enterprise AKS environments run in **private clusters** with egress-only firewalls. A pull-based model requires **zero inbound network ports**—the operator connects outbound via standard HTTPS/AMQP to Azure Service Bus over Azure Private Link or TLS.

2. **At-Least-Once Delivery & Explicit Settlement (Peek-Lock):**
   - With push webhooks, if the operator pod restarts or encounters temporary API rate-limits during secret materialization, the HTTP request fails or drops, requiring complex retry policies on the sender side.
   - With Service Bus **Peek-Lock**, the operator locks the message while executing the rotation state machine (`RevisionPrepared` &rarr; `CanaryProvisioning` &rarr; `Validating`).
   - The message is only settled (`CompleteMessage`) once the secret has been materialized and validated in cluster. If validation or materialization encounters an unrecoverable failure, the message is released or routed to the Dead-Letter Queue (DLQ) with audit metadata.

3. **Backpressure and Rate-Limiting Control:**
   - Sudden bulk secret updates in Azure Key Vault produce spikes of events. Service Bus queues act as a natural shock absorber, allowing DSO to reconcile rotations deterministically according to worker capacity without exhausting Kubernetes API server limits.

## Consequences

### Positive
- **Air-Gapped / Private Cluster Compatibility:** Works out-of-the-box in private AKS clusters without public IPs or ingress controllers.
- **Resilience:** Guaranteed at-least-once event delivery with lock renewals and Dead-Letter Queue (DLQ) support.
- **Security:** Authenticated entirely via passwordless **Azure Workload Identity** with Azure RBAC role `Azure Service Bus Data Receiver`.

### Negative / Trade-offs
- Requires provisioning an Azure Service Bus namespace and queue alongside Event Grid subscriptions.
- Slight increase in message routing latency (~100-300ms) compared to direct HTTP webhooks, which is negligible for automated rotation lifecycles.
