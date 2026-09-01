# ESO + Argo Rollouts: Blue/Green Progressive Secret Delivery

This example demonstrates how **Dynamic Secret Operator (DSO)** pairs seamlessly with **External Secrets Operator (ESO)** to drive automated zero-downtime Blue/Green deployments in **Argo Rollouts**.

---

## Architecture Flow

```mermaid
graph LR
    Vault["External Vault<br/>(AWS/GCP/Vault/Azure)"] -->|Poll / Sync| ESO["External Secrets Operator<br/>(SecretStore + ExternalSecret)"]
    ESO -->|Writes Intermediate| SyncSec["K8s Secret<br/>(payment-db-password-synced)"]
    SyncSec -.->|Watch / Ingest| DSO["Dynamic Secret Operator<br/>(DynamicSecretPolicy)"]
    DSO -->|Materialize Immutable Revision| RevSec["Secret Revision<br/>(rollout-payment-service-rev-xyz)"]
    DSO -->|Canary Sandbox & Probes| Canary["Ephemeral Canary Pod"]
    DSO -->|Promote Live| Rollout["Argo Rollouts<br/>(Blue/Green Shift)"]
```

---

## Deploying

```bash
chmod +x deploy.sh
./deploy.sh
```
