# ESO + Dynamic Secret Operator (DSO) Integration Pattern

This example demonstrates how **External Secrets Operator (ESO)** and **Dynamic Secret Operator (DSO)** combine to provide end-to-end multi-cloud secret ingestion and safe progressive canary rotation.

## Architecture

```
 External Vault (AKV / AWS / HashiCorp)
              │
              ▼
   [External Secrets Operator]
              │
              ▼ (Syncs upstream secret)
     Kubernetes Secret (Target)
              │
              ▼ (Triggers canary validation & rollout)
   [Dynamic Secret Operator]
              │
    ┌─────────┴─────────┐
    ▼                   ▼
[Canary Pod]      [NetworkPolicy]
 (Validates via    (Zero-trust
  HTTP/DB/Job)      isolation)
    │
    ▼ (On probe success)
[Production Workload Promoted]
```

## How It Works
1. **ESO** fetches secrets from your vault (AWS Secrets Manager, Azure Key Vault, HashiCorp Vault, etc.) and synchronizes them into a Kubernetes Secret (`app-credentials`).
2. **DSO** observes the `DynamicSecretPolicy` bound to `app-credentials`.
3. When ESO rotates `app-credentials`, DSO:
   - Computes a deterministic immutable hash (`app-credentials-rev-a1b2c3d4`).
   - Launches a single-pod isolated canary deployment with locked-down `NetworkPolicy` egress.
   - Executes non-blocking synthetic probes (PostgreSQL connection check, HTTP ping, or custom Job probe).
   - If probes pass: smoothly promotes the production `Deployment` to the new secret revision.
   - If probes fail: trips the circuit breaker and retains the production workload on the last-known good secret revision without downtime!

## Manifests

See [`manifests.yaml`](manifests.yaml) for the complete reference manifest combining:
- `SecretStore` (or `ClusterSecretStore`)
- `ExternalSecret` (ESO sync resource)
- `DynamicSecretPolicy` (DSO progressive delivery resource)
- `Deployment` (Production target workload)
