# ADR-003: Decoupling Secret Ingestion & Standardizing on External Secrets Operator (ESO)

## Status
**Accepted**

## Context
Secret synchronization into Kubernetes has standardized across the cloud-native ecosystem around the CNCF project **External Secrets Operator (ESO)**. ESO natively integrates with over 30 external secret management systems, including Azure Key Vault, AWS Secrets Manager, Google Cloud Secret Manager, HashiCorp Vault, 1Password, and CyberArk.

Earlier versions of `dynamic-secret-operator` (DSO) included built-in Azure Key Vault API clients and Azure Workload Identity token fetchers. While functional on Azure AKS, embedding proprietary cloud fetchers directly into DSO introduces significant architectural trade-offs:
1. **Total Addressable Market (TAM) & Multi-Cloud Limits:** Tightly coupling DSO to Azure Key Vault prevents adoption in AWS (EKS), GCP (GKE), hybrid, on-premises, and multi-cloud Kubernetes clusters.
2. **Duplication of CNCF Standards:** ESO is the established standard for *fetching and materializing* secrets from external vaults. Re-implementing vault drivers in DSO duplicates existing ecosystem functionality and creates substantial maintenance overhead.
3. **Privilege Separation & Zero-Trust:** When DSO fetches secrets directly, the operator pod itself must hold broad IAM permissions across external vaults. In contrast, separating ingestion (ESO) from rollout validation (DSO) adheres to the principle of least privilege.

## Decision
We decouple secret *ingestion* from secret *delivery* and establish DSO's primary role as the **CNCF Progressive Secret Delivery & Canary Rollout Controller**.

```
  ┌─────────────────────────────────────────────────────────┐
  │                 Ingestion Layer (ESO)                   │
  │  Azure Key Vault / AWS Secrets Mgr / HashiCorp Vault    │
  └───────────────────────────┬─────────────────────────────┘
                              │ Syncs & updates
                              ▼
  ┌─────────────────────────────────────────────────────────┐
  │              Kubernetes Secret (Sync Target)            │
  │              e.g., "orders-db-secret"                   │
  └───────────────────────────┬─────────────────────────────┘
                              │ Triggers rotation
                              ▼
  ┌─────────────────────────────────────────────────────────┐
  │        Dynamic Secret Operator (Validation & Canary)     │
  │                                                         │
  │  1. Materializes immutable revision:                    │
  │     "orders-api-orders-db-secret-rev-a1b2c3d4"          │
  │  2. Provisions isolated Canary workload                 │
  │  3. Applies zero-trust Canary NetworkPolicy             │
  │  4. Runs Async Probes (HTTP, TLS, MySQL, Postgres, Job) │
  │  5. Progressive Workload Promotion (Deployment/Rollout)  │
  │  6. GitOps Drift Healing (Argo CD ignoreDifferences)    │
  │  7. Automated Rollback & Circuit Breaking on failure    │
  └─────────────────────────────────────────────────────────┘
```

### Strategic Separation of Concerns
1. **External Secrets Operator (ESO):**
   - **Responsibility:** Ingestion, vault authentication, secret transformation, and synchronization into raw Kubernetes `Secret` resources.
2. **Dynamic Secret Operator (DSO):**
   - **Responsibility:** Safe progressive delivery, canary workload isolation, synthetic health probing, zero-trust network egress sandboxing, automated promotion, circuit breaking, and GitOps self-healing.

### Architectural Modes

#### Mode 1: ESO-Native / Kubernetes Secret Lifecycle (Primary CNCF Standard)
- DSO observes target Kubernetes Secrets (or `ExternalSecret` status).
- When ESO updates a synced secret with a new payload, DSO detects the hash drift, derives an immutable revision, and executes the progressive canary rollout.
- **Zero Cloud IAM Overhead:** DSO requires zero cloud provider credentials.
- **Required label:** to keep the manager's Secret cache scoped to only the secrets DSO cares
  about (rather than every Secret in the cluster), the ESO sync target must carry the label
  `dso.quantumsys.dev/managed: "watch"` — for example via `target.template.metadata.labels` on
  the `ExternalSecret`. Without this label, DSO's cache never observes the secret and rotation
  detection silently never triggers. This label is distinct from `dso.quantumsys.dev/managed:
  "true"`, which marks revision secrets DSO itself creates and owns.

#### Mode 2: Push-Accelerated Hybrid Adapter (Azure Service Bus / EventGrid)
- Retained as an optional event-driven adapter for environments where polling intervals are unacceptable and sub-second push notifications from Azure EventGrid &rarr; Service Bus are required.

## Consequences

### Positive
- **Universal Multi-Cloud Compatibility:** Operates seamlessly across AKS, EKS, GKE, and on-premises OpenShift/Kind clusters alongside ESO or HashiCorp Vault.
- **Enhanced Security:** DSO operates entirely on standard Kubernetes RBAC without requiring cloud IAM / Workload Identity credentials for secret fetching.
- **Laser-Focused Value Proposition:** DSO focuses exclusively on progressive canary validation, ephemeral probe execution, zero-trust network sandboxing, and automated rollback.

### Negative / Trade-offs
- Teams using ESO configure two lightweight CRDs (`ExternalSecret` for vault syncing + `DynamicSecretPolicy` for progressive canary validation).
