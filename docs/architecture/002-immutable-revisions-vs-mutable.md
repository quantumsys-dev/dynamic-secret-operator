# ADR-002: Immutable Secret Revisions vs In-Place Mutable Updates

## Status
**Accepted**

## Context
When rotating credentials (database passwords, API keys, TLS certificates), a Kubernetes operator can either:
1. **Mutate in-place:** Overwrite the existing `corev1.Secret` resource with the newly fetched secret data.
2. **Materialize immutable revisions:** Generate a new, uniquely named, immutable `corev1.Secret` for each rotation event (e.g. `<workload>-rev-<sha256-hash>`), point target workloads to the new revision, and preserve previous revisions for rollback.

## Decision
We decided to adopt **Immutable SecretRevision Materialization**.

Each secret rotation materializes a new `corev1.Secret` structured as:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: <target-workload>-rev-<sha256-short>
  namespace: <namespace>
  labels:
    dso.quantumsys.io/managed: "true"
    dso.quantumsys.io/revision: "<sha256-short>"
  ownerReferences:
    - apiVersion: secret.quantumsys.io/v1alpha1
      kind: DynamicSecretPolicy
      name: <policy-name>
      controller: true
immutable: true
data: ...
```

### Architectural Rationale

1. **Elimination of `CrashLoopBackOff` Race Conditions:**
   - In-place mutation of a Secret immediately propagates to pods mounting the secret or re-reading filesystem files.
   - If the new secret is invalid (e.g., database credentials not yet active, invalid TLS certificate, typo in vault), existing pods running the application may immediately crash or fail health checks upon reloading, causing a total service outage.
   - Immutable revisions decouple the existing healthy production pods from newly fetched credentials until the new credentials pass strict canary validation.

2. **Deterministic, Zero-Downtime Canary Verification:**
   - With immutable revisions, the operator spins up an isolated **Canary Pod/Deployment** mounting `<workload>-rev-<new>` while production pods continue running smoothly against `<workload>-rev-<old>`.
   - Validation probes (HTTP, TLS thumbprint, DB authentication) execute against the canary in isolation.
   - Production workloads are only patched once the canary proves 100% operational.

3. **Instant, Zero-Cost Rollback:**
   - In the event of probe failure or circuit breaker trip, rolling back production merely requires repointing the deployment's volume/env references back to the previous revision secret.
   - No round-trips to Azure Key Vault or secret reconstruction are required during incident recovery.

4. **Cryptographic Auditability & Traceability:**
   - Revision hashes are derived directly from the payload SHA-256 and Key Vault version ID.
   - Security and compliance teams can inspect the exact timeline of secret revisions and correlate Kubernetes events directly with Key Vault audit logs.

## Consequences

### Positive
- **Safety:** True zero-downtime progressive delivery with zero blast radius on bad secret rotations.
- **Rollback Speed:** Near-instantaneous rollback capability without external network calls.
- **State Integrity:** Native Kubernetes `immutable: true` flag protects secrets against accidental manual tampering.

### Negative / Trade-offs
- Multiple Secret objects exist temporarily in the target namespace.
- Requires automatic garbage collection / pruning of historical SecretRevisions beyond the configured rollback history limit.
