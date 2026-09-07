<div align="center">

# 🔐 Dynamic Secret Operator (DSO)

**Zero-Downtime, Progressive Secret & Certificate Rotations for Enterprise Kubernetes**

[![CI/CD Release](https://github.com/quantumsys-dev/dynamic-secret-operator/actions/workflows/release.yaml/badge.svg)](https://github.com/quantumsys-dev/dynamic-secret-operator/actions)
[![SLSA 3](https://slsa.dev/images/gh-badge-level3.svg)](https://slsa.dev)
[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go)](https://go.dev/)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.28+-326ce5?logo=kubernetes)](https://kubernetes.io)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Security: Chainguard](https://img.shields.io/badge/Base_Image-Chainguard_Distroless-success.svg)](https://chainguard.dev)
[![Supply Chain: Cosign](https://img.shields.io/badge/Signed_by-Cosign_OIDC-blueviolet.svg)](https://sigstore.dev)
[![Pod Security: Restricted](https://img.shields.io/badge/PSA-Restricted_Compliant-green.svg)](https://kubernetes.io/docs/concepts/security/pod-security-standards/)

</div>

---

## 📖 Executive Summary

**Dynamic Secret Operator (DSO)** is a production-grade Kubernetes operator engineered to eliminate `CrashLoopBackOff` outages during credential rotations across multi-cloud environments. 

Traditional secret management tools mutate secrets *in-place*, instantly crashing downstream pods if a rotated database credential, API key, or TLS certificate is malformed, not yet active, or fails handshakes. DSO solves this by adopting **ADR-002: Immutable Revisions**. 

Featuring an **extensible, provider-agnostic source abstraction layer**, DSO supports:
- **Event-Driven Multi-Cloud Ingestion** supporting AWS, GCP, and Azure via cloud-native message queues (Amazon SQS, Google Cloud Pub/Sub, Azure Service Bus) & Zero-Trust Federated Workload Identity.
- **Universal Multi-Cloud Synergy with External Secrets Operator (ESO)** for AWS Secrets Manager, Google Cloud Secret Manager, HashiCorp Vault, and Akeyless.
- **eBPF Canary Isolation with CiliumNetworkPolicy** and Hubble packet telemetry.
- **Supply Chain Security** with SLSA Level 3 build provenance, keyless Cosign OIDC signing, and SPDX SBOMs.

## 🏗️ Architecture at a Glance

DSO shifts secret rotation from a risky "push and pray" operation to a safe, event-driven Progressive Delivery pipeline.

```mermaid
flowchart TD
    subgraph MultiCloud ["☁️ Multi-Cloud Secret Backends & Ingestion Architectures"]
        direction TB

        subgraph ModeESO ["ESO Mode: Universal Multi-Cloud Ingestion (ESO-Native)"]
            VAULT_ALL["AWS Secrets Manager / GCP Secret Manager / Vault / Key Vault"]
            ESO["External Secrets Operator<br/>(SecretStore + ExternalSecret)"]
            SYNC_SEC["Intermediate Secret<br/>(dso.quantumsys.dev/managed: watch)"]
            VAULT_ALL -->|"Sync (Drift / Polling / Webhook)"| ESO
            ESO -->|"Writes Synced Secret"| SYNC_SEC
        end

        subgraph ModeEventDriven ["Event-Driven Mode: Universal Direct Ingestion (Multi-Cloud Push)"]
            direction LR
            subgraph CloudVaults ["Cloud Vaults & Event Routers"]
                AKV["Azure Key Vault<br/>+ Event Grid"]
                AWS_SM["AWS Secrets Manager<br/>+ EventBridge / SNS"]
                GCP_SM["GCP Secret Manager<br/>+ Cloud Pub/Sub"]
                VAULT_EVT["HashiCorp Vault<br/>+ Event Streams"]
            end

            subgraph CloudQueues ["Reliable Message Queues & Streaming"]
                ASB["Azure Service Bus Queue<br/>(Peek-Lock Delivery)"]
                SQS["Amazon SQS Queue<br/>(Ack/Nack Delivery)"]
                PUBSUB["GCP Pub/Sub<br/>(Streaming Subscription)"]
                KAFKA["Kafka / NATS Broker<br/>(Event Streaming)"]
            end

            AKV -->|"SecretNewVersionCreated"| ASB
            AWS_SM -->|"Rotation Event"| SQS
            GCP_SM -->|"Secret Version Add"| PUBSUB
            VAULT_EVT -->|"Audit / Event Stream"| KAFKA
        end
    end

    subgraph K8sCluster ["☸️ Kubernetes Cluster Architecture"]
        subgraph DSO_System ["dso-system Namespace"]
            DSO["⚙️ Dynamic Secret Operator<br/>(Pluggable Event Handlers & Providers)"]
            OTEL["📊 OpenTelemetry & Prometheus<br/>(:8080/metrics)"]
        end

        subgraph AppNamespace ["Target Application Namespace"]
            DSP["📄 DynamicSecretPolicy<br/>(Declarative Strategy & Probes)"]
            REV_SEC["🔒 Immutable SecretRevision<br/>(app-rev-a1b2c3d4)"]
            CANARY["🐤 Ephemeral Canary Pod<br/>(+ NetPol / Cilium eBPF Sandbox)"]
            PROBES["🩺 Synthetic Validation Probes<br/>(HTTP / TLS / PG / MySQL / Job)"]
            PROD["🚀 Production Workload<br/>(Deployment / StatefulSet / Rollout)"]
        end
    end

    %% Ingestion Flows
    SYNC_SEC -.->|"spec.source.k8sSecret (Watch / Informer)"| DSO
    CloudQueues -.->|"spec.source.* (Zero-Polling Push Stream)"| DSO
    DSO -.->|"Fetch Payload via Cloud Identity (Workload Identity / IRSA)"| MultiCloud

    %% Progressive Delivery State Machine
    DSP -->|"1. Reconcile Policy"| DSO
    DSO -->|"2. Materialize Revision"| REV_SEC
    DSO -->|"3. Provision Isolated Sandbox"| CANARY
    CANARY -->|"Mounts"| REV_SEC
    DSO -->|"4. Execute Synthetic Probes"| PROBES
    PROBES -->|"Validate Real Traffic"| CANARY
    DSO -->|"5. Zero-Downtime Rollover & GitOps Patch"| PROD
```

---

### 🔄 Two Architectural Ingestion Modes: ESO Mode vs. Event-Driven Mode

DSO provides two distinct, enterprise-grade ingestion models designed to fit any cloud topology:

#### 1. ESO Mode: Universal Multi-Cloud Ingestion (ESO-Native / Decoupled)
- **Concept:** Operates alongside the CNCF standard **External Secrets Operator (ESO)** to synchronize secrets from 30+ external secret stores (AWS Secrets Manager, Google Secret Manager, HashiCorp Vault, Azure Key Vault, Akeyless, CyberArk) into intermediate Kubernetes Secrets.
- **How it Works:** When an upstream secret updates, ESO refreshes the Kubernetes Secret carrying the label `dso.quantumsys.dev/managed: "watch"`. DSO observes the hash drift through standard controller-runtime informers and initiates the progressive canary rollout.
- **Zero Cloud IAM Overhead:** DSO requires **zero** cloud IAM permissions in this mode, running purely on native Kubernetes RBAC.

#### 2. Event-Driven Mode: Universal Direct Ingestion (Multi-Cloud Push-Accelerated)
- **Concept:** Sub-second, reactive push notifications triggered directly by cloud event brokers and queue streams. Rather than relying on periodic polling intervals, upstream secret rotations immediately push notification events to DSO.
- **Multi-Cloud Architecture:** Event-driven ingestion is an architectural pattern supported across **all major cloud providers and hybrid infrastructures**:
  - 🟦 **Microsoft Azure:** `Azure Key Vault` &rarr; `Azure Event Grid` &rarr; `Azure Service Bus Queue` (with Peek-Lock delivery and Azure Workload Identity).
  - 🟧 **Amazon Web Services (AWS):** `AWS Secrets Manager` &rarr; `Amazon EventBridge / SNS` &rarr; `Amazon SQS Queue` (with message visibility timeouts and AWS IAM Roles for Service Accounts - IRSA / EKS Pod Identity).
  - 🟥 **Google Cloud Platform (GCP):** `Google Cloud Secret Manager` &rarr; `Cloud Pub/Sub Topic & Subscription` (with streaming pull and GCP Workload Identity Federation).
  - 🟪 **HashiCorp Vault & Hybrid/On-Prem:** `Vault Event Streams / Audit Webhooks` &rarr; `Apache Kafka / NATS / Event Broker` (with mutual TLS / SPIFFE identities).
- **Sub-Second Latency & Zero Polling:** Eliminates API rate-limit throttling and polling delays, ensuring rotation events trigger canary validation within milliseconds of upstream modification.

---

## ✨ Key Enterprise Capabilities

| Feature | Description |
| :--- | :--- |
| **Pluggable Provider Architecture** | Extensible source backend abstraction (`source.Provider`) supporting native **Event-Driven Multi-Cloud Push** (Azure Key Vault, AWS Secrets Manager, GCP Secret Manager, Vault) and **Universal ESO-Native** intermediate watch. |
| **Zero-Trust Passwordless Auth** | Integrates natively with cloud federated identities: **Azure Workload Identity**, **AWS IAM Roles for Service Accounts (IRSA) / EKS Pod Identity**, and **GCP Workload Identity**. No static credentials, client secrets, or long-lived tokens. |
| **Immutable SecretRevisions** | Materializes cryptographically hashed, immutable Kubernetes Secrets (`<workload>-rev-<sha256>`), preventing in-place race conditions. |
| **Progressive Canary Validation** | Spins up isolated canary workloads with strict `NetworkPolicy` or eBPF `CiliumNetworkPolicy` ingress rules and executes synthetic validation probes before touching production. |
| **eBPF & Hubble Observability** | Optional native generation of `cilium.io/v2.CiliumNetworkPolicy` for granular L3/L4/L7 egress sandboxing and real-time Hubble packet telemetry. |
| **Comprehensive Probe Engine** | Built-in probes for **HTTP**, **TLS** (certificate expiration and thumbprint matching), **PostgreSQL**, and **MySQL** (`SELECT 1`). |
| **Extensible Job-Based Probes** | "Bring Your Own Container" (`type: Job`) lets users supply a standard `batch/v1.JobTemplateSpec` (e.g., `redis:alpine`, `kafka-consumer`, custom scripts). The operator creates the Job ephemerally in the target namespace, automatically injects `DSO_REVISION_SECRET_NAME` into container environments, monitors completion, captures failure logs into CRD Conditions, and auto-cleans up — with zero driver bloat in the operator binary. |
| **Anti-Leakage Error Sanitization** | Intercepts all database and transport errors, stripping passwords, tokens, and raw DSNs before emitting logs or OpenTelemetry spans. |
| **Scoped Secret Ingestion** | Restricts controller-runtime caches to operator-managed secrets (`dso.quantumsys.dev/managed`), isolating cluster secrets and minimizing memory exposure. |
| **Circuit Breaker & Backoff** | Exponential backoff and threshold-based circuit breaker halts retry storms and preserves intact production workloads on bad credential updates. |
| **Supply Chain Security & SLSA L3** | Built on zero-CVE **Chainguard Static Distroless**, cryptographically signed keylessly via **Sigstore / Cosign OIDC**, with attached **SPDX SBOMs** and verifiable **SLSA Level 3 Build Provenance**. |

*   **🛡️ Immutable Revisions (ADR-002):** Eliminates in-place mutation drift. Rotations generate unique, immutable SecretRevisions (`<workload>-rev-<sha256>`). Production pods are entirely shielded from bad credentials until the new revision passes all canary tests.
*   **🩺 Comprehensive Validation Probes:** Ship with confidence using built-in synthetic probes for **PostgreSQL**, **MySQL** (executing `SELECT 1`), **HTTP/S**, and **TLS** (validating certificate expiration and SHA-256 thumbprint matching).
*   **📜 Native `kubernetes.io/tls` Auto-Parsing:** No more manual scripting. DSO automatically intercepts Key Vault Certificate payloads (PEM/PKCS#12), splits them into `tls.crt` and `tls.key`, and creates native `kubernetes.io/tls` Secrets ready for immediate consumption by Nginx Ingress, Istio, or Gateway API.
*   **🐙 GitOps & Argo CD Harmony:** In-cluster mutations typically cause infinite reconciliation loops with Argo CD's Self-Heal. DSO automatically calculates and injects safe `ignoreDifferences` JSON Pointers into parent Argo CD `Application` resources, utilizing `RetryOnConflict` to avoid 409 Conflict storms during concurrent updates.
*   **♻️ Enterprise Resiliency & Etcd GC:** 
    *   **Circuit Breakers:** Tracks consecutive failures and halts reconciliation to prevent cascading cluster damage. Supports automatic drift-recovery the moment an upstream admin fixes the secret in Key Vault.
    *   **Sliding Window GC:** Automatically garbage-collects orphaned SecretRevisions in `etcd`, keeping only the `Current` and `Desired` revisions to prevent API server bloat.
    *   **Backpressure Handling:** Leverages reliable queue delivery (e.g. Azure Service Bus Peek-Lock, AWS SQS visibility timeouts, GCP Pub/Sub nacks) with explicit timeout context NACKs to ensure rotation events are safely preserved during cluster CPU/Queue saturation.
*   **🚥 Native Rollout Compatibility:** Works natively with standard Kubernetes `Deployment`, `StatefulSet`, and `DaemonSet` resources, as well as native support for **Argo Rollouts (Blue/Green)** for advanced traffic shifting.

## 🔐 Azure Prerequisites & Infrastructure Setup

You can provision all required Azure resources (Resource Group, Key Vault, Service Bus, Event Grid, AKS, and Workload Identity Federation) automatically or manually:

### Automated Provisioning (PowerShell)
Execute the infrastructure provisioner script with minimal-cost SKUs (Free Tier AKS, Standard Key Vault, Basic Service Bus):

```powershell
.\setup-azure-resources.ps1 -ResourceGroupName "rg-dso-dev" -Location "eastus"
```

---

### Manual Azure RBAC Setup

#### 1. Assign Azure RBAC Roles
Assign the Managed Identity permissions on your Key Vault and Service Bus namespace:

```bash
# 1. Key Vault Secrets User (Read-only secret retrieval)
az role assignment create \
  --role "Key Vault Secrets User" \
  --assignee-object-id "<MANAGED_IDENTITY_OBJECT_ID>" \
  --assignee-principal-type "ServicePrincipal" \
  --scope "/subscriptions/<SUB_ID>/resourceGroups/<RG>/providers/Microsoft.KeyVault/vaults/<VAULT_NAME>"

# 2. Azure Service Bus Data Receiver (Peek-Lock message consumption)
az role assignment create \
  --role "Azure Service Bus Data Receiver" \
  --assignee-object-id "<MANAGED_IDENTITY_OBJECT_ID>" \
  --assignee-principal-type "ServicePrincipal" \
  --scope "/subscriptions/<SUB_ID>/resourceGroups/<RG>/providers/Microsoft.ServiceBus/namespaces/<SERVICEBUS_NAME>"
```

#### 2. Establish Workload Identity Federation
```bash
az identity federated-credential create \
  --name "dso-federated-credential" \
  --identity-name "<MANAGED_IDENTITY_NAME>" \
  --resource-group "<RG>" \
  --issuer "<AKS_OIDC_ISSUER_URL>" \
  --subject "system:serviceaccount:dso-system:dso-dynamic-secret-operator" \
  --audience "api://AzureADTokenExchange"
```

### 3. Deploy DSO Operator via Helm

Deploy DSO into your cluster using the installation configuration suited for your cloud provider or operating mode:

> 📖 **Provider Installation Guides:**  
> - 🟢 **[Microsoft Azure Key Vault Guide](docs/providers/azure.md)** *(Production Ready)*
> - 🟢 **[Universal Multi-Cloud via ESO Guide](docs/providers/eso.md)** *(Production Ready)*
> - 🟡 **[Amazon Web Services (AWS) Guide](docs/providers/aws.md)** *(In Development – Roadmap v0.3)*
> - 🟡 **[Google Cloud Platform (GCP) Guide](docs/providers/gcp.md)** *(In Development – Roadmap v0.3)*

#### Option 1: Microsoft Azure (🟢 Production Ready)
*Event-driven via Azure Key Vault, Azure Event Grid, Azure Service Bus, and Azure Workload Identity.*

**PowerShell (Windows):**
```powershell
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=event-driven `
  --set provider=azure `
  --set azure.workloadIdentity.enabled=true `
  --set azure.workloadIdentity.clientId="<MANAGED_IDENTITY_CLIENT_ID>" `
  --set azure.workloadIdentity.tenantId="<AZURE_TENANT_ID>" `
  --set azure.serviceBus.namespace="<SERVICEBUS_NAMESPACE_FQDN>" `
  --set azure.serviceBus.queueName="<QUEUE_NAME>" `
  --wait
```

**Bash (Linux / macOS):**
```bash
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=event-driven \
  --set provider=azure \
  --set azure.workloadIdentity.enabled=true \
  --set azure.workloadIdentity.clientId="<MANAGED_IDENTITY_CLIENT_ID>" \
  --set azure.workloadIdentity.tenantId="<AZURE_TENANT_ID>" \
  --set azure.serviceBus.namespace="<SERVICEBUS_NAMESPACE_FQDN>" \
  --set azure.serviceBus.queueName="<QUEUE_NAME>" \
  --wait
```

#### Option 2: Amazon Web Services (AWS) (🟡 In Development – Roadmap v0.3)
> [!NOTE]
> Native AWS event-driven ingestion (EventBridge $\to$ SQS) is currently under active development.
> For production AWS clusters today, use **Option 4 (Universal Multi-Cloud via ESO)** below. See also [AWS Provider Guide](docs/providers/aws.md).

**PowerShell (Windows):**
```powershell
# Note: Native AWS provider is currently under development (Roadmap v0.3.0)
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=event-driven `
  --set provider=aws `
  --set aws.enabled=true `
  --set aws.roleArn="arn:aws:iam::<ACCOUNT_ID>:role/dso-secret-operator-role" `
  --set aws.sqs.queueUrl="https://sqs.<REGION>.amazonaws.com/<ACCOUNT_ID>/dso-vault-events" `
  --set aws.region="<REGION>" `
  --wait
```

**Bash (Linux / macOS):**
```bash
# Note: Native AWS provider is currently under development (Roadmap v0.3.0)
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=event-driven \
  --set provider=aws \
  --set aws.enabled=true \
  --set aws.roleArn="arn:aws:iam::<ACCOUNT_ID>:role/dso-secret-operator-role" \
  --set aws.sqs.queueUrl="https://sqs.<REGION>.amazonaws.com/<ACCOUNT_ID>/dso-vault-events" \
  --set aws.region="<REGION>" \
  --wait
```

#### Option 3: Google Cloud Platform (GCP) (🟡 In Development – Roadmap v0.3)
> [!NOTE]
> Native GCP event-driven ingestion (Secret Manager $\to$ Pub/Sub) is currently under active development.
> For production GCP clusters today, use **Option 4 (Universal Multi-Cloud via ESO)** below. See also [GCP Provider Guide](docs/providers/gcp.md).

**PowerShell (Windows):**
```powershell
# Note: Native GCP provider is currently under development (Roadmap v0.3.0)
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=event-driven `
  --set provider=gcp `
  --set gcp.enabled=true `
  --set gcp.workloadIdentity.serviceAccount="dso-sa@<PROJECT_ID>.iam.gserviceaccount.com" `
  --set gcp.pubsub.subscription="projects/<PROJECT_ID>/subscriptions/dso-vault-events-sub" `
  --wait
```

**Bash (Linux / macOS):**
```bash
# Note: Native GCP provider is currently under development (Roadmap v0.3.0)
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=event-driven \
  --set provider=gcp \
  --set gcp.enabled=true \
  --set gcp.workloadIdentity.serviceAccount="dso-sa@<PROJECT_ID>.iam.gserviceaccount.com" \
  --set gcp.pubsub.subscription="projects/<PROJECT_ID>/subscriptions/dso-vault-events-sub" \
  --wait
```

#### Option 4: Universal Multi-Cloud via External Secrets Operator (ESO) (🟢 Production Ready)
*Recommended for AWS, GCP, HashiCorp Vault, Azure, or hybrid clusters today. Simply set `mode=eso` (requires no cloud credentials or provider parameter inside DSO).*  
*(See the [ESO Universal Provider Guide](docs/providers/eso.md) for full prerequisites and setup).*

**PowerShell (Windows):**
```powershell
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=eso `
  --wait
```

**Bash (Linux / macOS):**
```bash
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=eso \
  --wait
```

## 📖 CRD API Reference Summary

The `DynamicSecretPolicy` CRD is your declarative interface for secret management.

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: payment-db-policy
  namespace: production
spec:
  # 1. External Vault Identity
  vaultRef:
    keyVaultURI: "https://my-prod-vault.vault.azure.net"
    objectName: "payment-db-credentials"
    objectType: "Secret" # Options: Secret, Certificate, Key

  # 2. Target Workload to Promote
  workloadSelector:
    kind: "Deployment" # Options: Deployment, StatefulSet, DaemonSet, Rollout
    name: "payment-service"

  # 3. Explicit Injection Boundaries (Optional)
  targetRef:
    volumeName: "db-secret-volume"

  # 4. Synthetic Validation Probes
  validationProbes:
    - type: "PostgreSQL"
      endpoint: "postgres.production.svc.cluster.local:5432"
      queryTimeout: 5

  # 5. Circuit Breaker Configuration
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```
*For the complete specification, default behaviors, and Kyverno Policy-as-Code examples, view the [Full API Reference](docs/api-reference.md).*

## 🗺️ Future Roadmap: Multi-Cloud Expansion

DSO's core progressive delivery engine—the state machine, immutable SecretRevisions, isolated canary sandboxing, and synthetic validation probes—is entirely cloud-agnostic.

- **✅ Phase 1 (Production Ready - v0.2.x):**
  - **ESO Mode (Universal Multi-Cloud):** Seamless progressive delivery for 30+ secret backends via External Secrets Operator across EKS, GKE, AKS, and bare-metal Kubernetes.
  - **Event-Driven Mode (Azure):** Native sub-second push ingestion via Azure Key Vault, Event Grid, and Service Bus Peek-Lock queues with Azure Workload Identity.
- **🚀 Phase 2 (Roadmap - v0.3.x):**
  - **Event-Driven Mode (AWS):** Native push adapter for AWS Secrets Manager via Amazon EventBridge / SNS & Amazon SQS using AWS IAM Roles for Service Accounts (IRSA) and EKS Pod Identity.
  - **Event-Driven Mode (Google Cloud):** Native push adapter for Google Cloud Secret Manager via Cloud Pub/Sub using GCP Workload Identity Federation.
  - **Event-Driven Mode (HashiCorp Vault & On-Prem):** Native push adapter for Vault Event Streams and Audit Webhooks via Kafka / NATS / CloudEvents. 

---

## 💡 Examples

Explore our comprehensive reference architecture examples for testing:

### ☁️ Azure Kubernetes Service (AKS) Examples (`examples/azure`)
- [**Azure Multi-Secret Rotation**](examples/azure/multi-secret-rotation/): Multi-secret workload auto-rotation consuming PostgreSQL, Redis, and Payment API keys with dedicated validation probes.
- [**Azure Fullstack DB Rotation**](examples/azure/fullstack-db-rotation/): Live AKS cluster integration with Azure Key Vault, Service Bus, and Workload Identity.
- [**Azure Job-Based Redis Probe**](examples/azure/job-based-redis-probe/): Ephemeral Batch Job probe running custom CLI validation scripts against rotated Redis cache tokens.
- [**Azure Argo Rollouts Blue/Green**](examples/azure/argo-rollouts-blue-green/): Live AKS Blue/Green promotion triggered by Azure Key Vault rotations.
- [**Azure TLS Certificate Rotation**](examples/azure/tls-certificate-rotation/): Live AKS TLS Gateway with Azure Key Vault SSL certificate auto-parsing.
- [**Azure Nginx Color Canary**](examples/azure/nginx-color-rotation/): Live AKS Canary rollout with Argo CD `ignoreDifferences` auto-patching.

### 🌐 External Secrets Operator (ESO) Multi-Cloud Examples (`examples/eso`)
- [**ESO Argo Rollouts Blue/Green**](examples/eso/argo-rollouts-blue-green/): Decoupled Blue/Green rollout triggered by synced Kubernetes secrets.
- [**ESO TLS Certificate Rotation**](examples/eso/tls-certificate-rotation/): Decoupled TLS ingress certificate rotation with synthetic TLS handshake verification.
- [**ESO Nginx Color Canary**](examples/eso/nginx-color-rotation/): Canary rollout with synthetic Job hex color verification and Argo CD drift protection.
- [**ESO Fullstack DB Rotation**](examples/eso/fullstack-db-rotation/): Zero-downtime PostgreSQL credential rollover via synced secrets.
- [**ESO Job-Based Redis Probe**](examples/eso/job-based-redis-probe/): Ephemeral validation Job running `redis-cli PING` on synced secret update.
- [**ESO Multi-Secret Rotation**](examples/eso/multi-secret-rotation/): Multi-secret microservice with independent validation probes per volume.
- [**ESO Cilium Hubble Observability**](examples/eso/cilium-hubble-observability/): eBPF-based L3/L4/L7 egress network sandboxing and telemetry.
- [**ESO Circuit Breaker & Rollback**](examples/eso/circuit-breaker-rollback/): Automatic circuit breaker tripping and rollback on invalid secrets.

---

## 📚 Documentation & Architecture Decision Records (ADRs)

For comprehensive details on enterprise integration, architecture, and operation:

- [Getting Started Guide (5-Minute Quickstart)](docs/getting-started.md)
- [Operating Modes Guide (ESO Mode vs Event-Driven Mode)](docs/operating-modes.md)
- [API Reference](docs/api-reference.md)
- [Configuration & Enterprise Tuning](docs/configuration.md)
- [Troubleshooting & Runbooks](docs/troubleshooting.md)
- [GitOps: Argo CD Self-Heal Integration](docs/gitops-argo-cd.md)
- [Security & Threat Model](docs/security.md)
- [Pluggable Providers Overview](docs/providers/overview.md)
  - [Microsoft Azure Key Vault Guide (Production Ready)](docs/providers/azure.md)
  - [Universal Multi-Cloud via ESO Guide (Production Ready)](docs/providers/eso.md)
  - [AWS Secrets Manager Guide (In Development)](docs/providers/aws.md)
  - [Google Cloud Secret Manager Guide (In Development)](docs/providers/gcp.md)

**Architecture Decision Records (ADRs):**
- [ADR-001: Azure Service Bus Peek-Lock vs Webhooks](docs/architecture/001-asb-peek-lock-vs-webhooks.md)
- [ADR-002: Immutable Revisions vs Mutable Updates](docs/architecture/002-immutable-revisions-vs-mutable.md)
- [ADR-003: Decoupling Secret Ingestion & ESO Standard](docs/architecture/003-decoupling-secret-ingestion-eso.md)

We actively welcome community contributions, PRs, and provider plugin development to help make DSO the universal standard for progressive secret delivery across all major cloud providers.

---

<div align="center">
  <b>Built with 🩵 by the QuantumSys Architecture Team.</b><br>
  For runtime flags and enterprise concurrency tuning, see the <a href="docs/configuration.md">Configuration Guide</a>.<br>
  For operational runbooks and DLQ management, refer to the <a href="docs/troubleshooting.md">Troubleshooting Guide</a>.<br>
  To report vulnerabilities, please read our <a href="SECURITY.md">Security Policy</a>.
</div>
