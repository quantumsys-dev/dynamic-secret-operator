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

2. **Explicit Settlement, Bounded by Enqueue Rather Than Full Reconciliation (Peek-Lock):**
   - With push webhooks, if the operator pod restarts or encounters temporary API rate-limits during secret materialization, the HTTP request fails or drops, requiring complex retry policies on the sender side.
   - With Service Bus **Peek-Lock**, the operator locks the message only for the short, synchronous span of listing matching `DynamicSecretPolicy` resources and enqueueing a `GenericEvent` per match. `CompleteMessage` is called immediately after that enqueue succeeds - **not** after the rotation state machine (`RevisionPrepared` &rarr; `CanaryProvisioning` &rarr; `Validating`) actually finishes. Holding the lock across the full state machine (an earlier design) risked the lock expiring mid-rotation, causing `LockLost` errors and unbounded redelivery.
   - If the handler fails before enqueueing (e.g. the internal event channel is at capacity), the message is explicitly abandoned so it becomes available for redelivery immediately rather than waiting out the lock's natural expiry.
   - The trade-off: because the ack no longer waits for materialization, a crash between ack and actual reconciliation could in principle lose that specific event. This is bounded, not open-ended - `DynamicSecretPolicy.Status.DesiredRevision` is cleared back to empty once a rotation completes, and the reconciler treats an empty `DesiredRevision` as "re-check upstream for drift" on *any* trigger, including the manager's own periodic cache resync (`--sync-period`, default 5m). A lost event therefore self-heals within one resync interval rather than being permanently missed, at the cost of up to that interval's delay versus true event-driven near-real-time detection.

3. **Backpressure and Rate-Limiting Control:**
   - Sudden bulk secret updates in Azure Key Vault produce spikes of events. Service Bus queues act as a natural shock absorber, allowing DSO to reconcile rotations deterministically according to worker capacity without exhausting Kubernetes API server limits.

## Consequences

### Positive
- **Air-Gapped / Private Cluster Compatibility:** Works out-of-the-box in private AKS clusters without public IPs or ingress controllers.
- **Resilience:** Explicit Abandon on handler failure and a bounded cache resync backstop keep a lost or lock-expired event from staying undetected indefinitely, though delivery is best-effort rather than a strict at-least-once guarantee (see the settlement trade-off above).
- **Security:** Authenticated entirely via passwordless **Azure Workload Identity** with Azure RBAC role `Azure Service Bus Data Receiver`.

### Negative / Trade-offs
- Requires provisioning an Azure Service Bus namespace and queue alongside Event Grid subscriptions.
- Slight increase in message routing latency (~100-300ms) compared to direct HTTP webhooks, which is negligible for automated rotation lifecycles.
