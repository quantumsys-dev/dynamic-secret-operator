# Amazon Web Services (AWS) Secrets Manager Provider

The **Dynamic Secret Operator (DSO)** supports secret rotation for **AWS Secrets Manager** workloads running on Amazon Elastic Kubernetes Service (EKS) and hybrid AWS environments.

> [!NOTE]
> **Current Support Status:**
> - 🟢 **Production Ready Today:** **[Universal ESO Mode](../eso/README.md)** (Decoupled multi-cloud architecture utilizing CNCF External Secrets Operator with AWS IRSA).
> - 🟡 **Under Active Development (Roadmap v0.3.0):** Native event-driven push ingestion using Amazon EventBridge and Amazon SQS queues.

---

## 🧭 Provider Documentation Navigation

| Document | Description |
| :--- | :--- |
| 📖 **[Getting Started Guide](getting-started.md)** | Step-by-step setup for AWS workloads: Production deployment via ESO mode today, and preview configuration for native EventBridge + SQS. |
| 🔧 **[Troubleshooting Guide](troubleshooting.md)** | Diagnostic playbooks, AWS IAM/IRSA permission errors, SQS queue inspection, EventBridge rule verification, and ESO AWS syncing issues. |

---

## 🏗️ Architectural Model (Native v0.3.0 Target)

Once released in v0.3.0, DSO's native AWS provider will deliver real-time, event-driven secret rotation without continuous polling:

```mermaid
sequenceDiagram
    autonumber
    actor SecAdmin as Security Admin / CI-CD
    participant SM as AWS Secrets Manager
    participant EB as Amazon EventBridge
    participant SQS as Amazon SQS Queue
    participant DSO as DSO Controller (EKS)
    participant Canary as Isolated Canary Sandbox
    participant Workload as Target Workload (App)

    SecAdmin->>SM: PutSecretValue / Update Secret
    SM->>EB: Emit "Secrets Manager Secret Rotation Succeeded"
    EB->>SQS: Forward Event to SQS Queue
    SQS->>DSO: ReceiveMessage via AWS IRSA / EKS Pod Identity
    DSO->>SM: Fetch Payload via AWS SDK v2
    DSO->>DSO: Materialize Immutable SecretRevision
    DSO->>Canary: Deploy Ephemeral Canary + Network Sandbox
    DSO->>Canary: Run Synthetic Probes (DB/HTTP/TLS/Job)
    Canary-->>DSO: Health Check Succeeded ✅
    DSO->>Workload: Zero-Downtime Rollout & Settle
    DSO->>SQS: DeleteMessage / ACK
```

---

## ⚖️ Architectural Options for AWS Environments

| Dimension | Option A: ESO Mode (Production Ready Today) | Option B: Native Event-Driven (Roadmap v0.3.0) |
| :--- | :--- | :--- |
| **Status** | 🟢 **Production Ready** | 🟡 **In Development** |
| **Ingestion Type** | Level-triggered drift detection via ESO | Push-accelerated event stream via SQS |
| **Latency** | Governed by ESO `refreshInterval` or webhook | Sub-second (< 500ms from Secrets Manager commit) |
| **DSO Cloud Credentials** | **None** (100% Kubernetes RBAC only) | AWS IRSA / EKS Pod Identity (`secretsmanager:*`, `sqs:*`) |
| **Infrastructure Setup** | Minimal (Standard ESO Helm Chart) | EventBridge Rule, SQS Queue, IAM Policy |
| **Recommendation** | **Recommended for all AWS production clusters today** | For organizations requiring sub-second rotation latency |

---

## 📂 Related Documentation & Resources

- [AWS Getting Started Guide](getting-started.md)
- [AWS Troubleshooting Guide](troubleshooting.md)
- [Universal Multi-Cloud via ESO Provider Guide](../eso/README.md)
- [Operator Operating Modes Guide](../../architecture/operating-modes.md)
