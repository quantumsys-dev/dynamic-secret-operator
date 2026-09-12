# Google Cloud Platform (GCP) Secret Manager Provider

The **Dynamic Secret Operator (DSO)** supports secret rotation for **Google Cloud Secret Manager** workloads running on Google Kubernetes Engine (GKE) and hybrid GCP environments.

> [!NOTE]
> **Current Support Status:**
> - 🟢 **Production Ready Today:** **[Universal ESO Mode](../eso/README.md)** (Decoupled multi-cloud architecture utilizing CNCF External Secrets Operator with GKE Workload Identity).
> - 🟡 **Under Active Development (Roadmap v0.3.0):** Native event-driven push ingestion using Cloud Pub/Sub event notifications and streaming pull.

---

## 🧭 Provider Documentation Navigation

| Document | Description |
| :--- | :--- |
| 📖 **[Getting Started Guide](getting-started.md)** | Step-by-step setup for GCP workloads: Production deployment via ESO mode today, and preview configuration for native Secret Manager + Pub/Sub. |
| 🔧 **[Troubleshooting Guide](troubleshooting.md)** | Diagnostic playbooks, GKE Workload Identity binding issues, Pub/Sub permissions, Secret Manager topic configurations, and ESO GCP sync errors. |

---

## 🏗️ Architectural Model (Native v0.3.0 Target)

Once released in v0.3.0, DSO's native GCP provider will deliver real-time, event-driven secret rotation without continuous polling:

```mermaid
sequenceDiagram
    autonumber
    actor SecAdmin as Security Admin / CI-CD
    participant GSM as GCP Secret Manager
    participant PubSub as Cloud Pub/Sub Topic & Sub
    participant DSO as DSO Controller (GKE)
    participant Canary as Isolated Canary Sandbox
    participant Workload as Target Workload (App)

    SecAdmin->>GSM: AddSecretVersion / Update Secret
    GSM->>PubSub: Publish Event Notification
    PubSub->>DSO: Deliver via StreamingPull (Workload Identity)
    DSO->>GSM: AccessSecretVersion via GCP Client Libraries
    DSO->>DSO: Materialize Immutable SecretRevision
    DSO->>Canary: Deploy Ephemeral Canary + Network Sandbox
    DSO->>Canary: Run Synthetic Probes (DB/HTTP/TLS/Job)
    Canary-->>DSO: Health Check Succeeded ✅
    DSO->>Workload: Zero-Downtime Rollout & Settle
    DSO->>PubSub: Acknowledge (ACK) Message
```

---

## ⚖️ Architectural Options for GCP Environments

| Dimension | Option A: ESO Mode (Production Ready Today) | Option B: Native Event-Driven (Roadmap v0.3.0) |
| :--- | :--- | :--- |
| **Status** | 🟢 **Production Ready** | 🟡 **In Development** |
| **Ingestion Type** | Level-triggered drift detection via ESO | Push-accelerated event stream via Cloud Pub/Sub |
| **Latency** | Governed by ESO `refreshInterval` or webhook | Sub-second (< 500ms from Secret Manager commit) |
| **DSO Cloud Credentials** | **None** (100% Kubernetes RBAC only) | GKE Workload Identity (`secretAccessor`, `subscriber`) |
| **Infrastructure Setup** | Minimal (Standard ESO Helm Chart) | Pub/Sub Topic, Subscription, GSA IAM Bindings |
| **Recommendation** | **Recommended for all GCP production clusters today** | For organizations requiring sub-second rotation latency |

---

## 📂 Related Documentation & Resources

- [GCP Getting Started Guide](getting-started.md)
- [GCP Troubleshooting Guide](troubleshooting.md)
- [Universal Multi-Cloud via ESO Provider Guide](../eso/README.md)
- [Operator Operating Modes Guide](../../architecture/operating-modes.md)
