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
    DSO -->|Patch Rollout Spec, then Poll Status.Phase| Rollout["Argo Rollouts<br/>(Blue/Green Shift + native AnalysisRuns)"]
    Rollout -->|Phase=Healthy| DSO
```

DSO does not provision its own canary sandbox for Rollout targets: patching the Rollout's pod
template is what triggers Argo's own progressive delivery, so DSO instead relies entirely on
Argo's own blue/green strategy (`autoPromotionEnabled` here, or any configured `AnalysisRun`s)
for validation, polling `Rollout.Status.Phase` until it reports `Healthy` before finalizing the
promotion.

---

## Deploying

```bash
chmod +x deploy.sh
./deploy.sh
```
