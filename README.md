# Dynamic Secret Operator (DSO)

[![CI/CD Release](https://github.com/quantumsys-dev/dynamic-secret-operator/actions/workflows/release.yaml/badge.svg)](https://github.com/quantumsys-dev/dynamic-secret-operator/actions)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Lint Status](https://img.shields.io/badge/Lint-golangci--lint-blueviolet.svg)](https://golangci-lint.run/)
[![Security: Chainguard](https://img.shields.io/badge/Base_Image-Chainguard_Distroless-success.svg)](https://chainguard.dev)
[![Supply Chain: Cosign](https://img.shields.io/badge/Signed_by-Cosign_OIDC-blueviolet.svg)](https://sigstore.dev)
[![Pod Security: Restricted](https://img.shields.io/badge/PSA-Restricted_Compliant-green.svg)](https://kubernetes.io/docs/concepts/security/pod-security-standards/)

**Dynamic Secret Operator (DSO)** is a production-grade, enterprise Kubernetes operator engineered for **zero-downtime, progressive secret and certificate rotations**. 

Backed by **Azure Key Vault**, **Azure Workload Identity**, and **Event-Driven Azure Service Bus Peek-Lock**, DSO eliminates `CrashLoopBackOff` outages during credential rotations through immutable SecretRevisions, isolated canary verification probes, circuit-breaker backoffs, and automated promotion.

---

## 🏛️ Architecture Overview

```mermaid
flowchart TD
    subgraph Azure Cloud ["☁️ Azure Cloud (Zero-Trust)"]
        AKV["🔑 Azure Key Vault<br/>(Secrets, Keys, TLS Certs)"]
        AEG["⚡ Azure Event Grid<br/>(Rotation Event Subscription)"]
        ASB["📬 Azure Service Bus Queue<br/>(Peek-Lock Delivery)"]
        AKV -->|"SecretNewVersionCreated"| AEG
        AEG -->|"Push Event"| ASB
    end

    subgraph K8s Cluster ["☸️ Kubernetes Cluster"]
        subgraph DSO Namespace ["dso-system"]
            DSO["⚙️ Dynamic Secret Operator<br/>(Azure Workload Identity)"]
            OTEL["📊 OpenTelemetry & Prometheus<br/>(:8080/metrics)"]
        end

        subgraph App Namespace ["Application Namespace"]
            CRD["📄 DynamicSecretPolicy<br/>(CRD Instance)"]
            SEC_NEW["🔒 Immutable SecretRevision<br/>(app-rev-f5a6b7)"]
            CANARY["🐤 Canary Pod & NetworkPolicy<br/>(Network-Isolated Testing)"]
            PROBES["🩺 Validation Probes<br/>(HTTP / TLS / PostgreSQL / MySQL / Job)"]
            PROD["🚀 Production Workload<br/>(Zero-Downtime Rollover)"]
        end
    end

    ASB -.->|"Outbound Pull (Peek-Lock)"| DSO
    DSO -->|"1. Fetch Secret via Workload Identity"| AKV
    CRD -->|"Defines target & probes"| DSO
    DSO -->|"2. Materialize Immutable Secret"| SEC_NEW
    DSO -->|"3. Spin up Isolated Canary"| CANARY
    CANARY -->|"4. Mounts New Secret"| SEC_NEW
    DSO -->|"5. Execute Anti-Leakage Probes"| PROBES
    PROBES -->|"Validate"| CANARY
    DSO -->|"6. Progressive Patch & Promote"| PROD
    DSO -->|"7. Teardown Canary & Settle Message"| CANARY
    DSO -->|"Emit Spans & Metrics"| OTEL
```

---

## ✨ Key Enterprise Capabilities

| Feature | Description |
| :--- | :--- |
| **Zero-Trust Passwordless Auth** | Integrates exclusively with **Azure Workload Identity** using projected federated tokens. No static credentials, client secrets, or long-lived keys. |
| **Immutable SecretRevisions** | Materializes cryptographically hashed, immutable Kubernetes Secrets (`<workload>-rev-<sha256>`), preventing in-place race conditions. |
| **Progressive Canary Validation** | Spins up isolated canary workloads with strict `NetworkPolicy` ingress rules and executes synthetic validation probes before touching production. |
| **Comprehensive Probe Engine** | Built-in probes for **HTTP**, **TLS** (certificate expiration and thumbprint matching), **PostgreSQL**, and **MySQL** (`SELECT 1`). |
| **Extensible Job-Based Probes** | "Bring Your Own Container" (`type: Job`) lets users supply a standard `batch/v1.JobTemplateSpec` (e.g., `redis:alpine`, `kafka-consumer`, custom scripts). The operator creates the Job ephemerally in the target namespace, substitutes `{{REVISION_SECRET_NAME}}` as a placeholder, monitors completion, captures failure logs into CRD Conditions, and auto-cleans up — with zero driver bloat in the operator binary. |
| **Anti-Leakage Error Sanitization** | Intercepts all database and transport errors, stripping passwords, tokens, and raw DSNs before emitting logs or OpenTelemetry spans. |
| **In-Memory Zeroization** | Sensitive byte buffers and secret payloads are zeroed out in RAM (`ZeroBytes`) immediately after materialization. |
| **Circuit Breaker & Backoff** | Exponential backoff and threshold-based circuit breaker halts retry storms and preserves intact production workloads on bad credential updates. |
| **Supply Chain Security** | Built on zero-CVE **Chainguard Static Distroless**, cryptographically signed keylessly via **Sigstore / Cosign OIDC**, with attached **SPDX SBOMs**. |

---

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

---

## 🚀 Quick Start Guide

### 1. Install via Helm

**PowerShell (Windows):**
```powershell
helm install dso ./deploy/helm/dso `
  --namespace dso-system `
  --create-namespace `
  --set azure.workloadIdentity.clientId="<MANAGED_IDENTITY_CLIENT_ID>" `
  --set azure.workloadIdentity.tenantId="<AZURE_TENANT_ID>" `
  --set azure.serviceBus.namespace="<SERVICEBUS_NAMESPACE_FQDN>" `
  --set azure.serviceBus.queueName="<QUEUE_NAME>"
```

**Bash (Linux / macOS):**
```bash
helm install dso ./deploy/helm/dso \
  --namespace dso-system \
  --create-namespace \
  --set azure.workloadIdentity.clientId="<MANAGED_IDENTITY_CLIENT_ID>" \
  --set azure.workloadIdentity.tenantId="<AZURE_TENANT_ID>" \
  --set azure.serviceBus.namespace="<SERVICEBUS_NAMESPACE_FQDN>" \
  --set azure.serviceBus.queueName="<QUEUE_NAME>"
```

### 2. Define a `DynamicSecretPolicy`

Create a policy linking your Azure Key Vault secret to your target Kubernetes `Deployment`:

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: order-service-secret-policy
  namespace: production
spec:
  vaultRef:
    keyVaultURI: "https://my-prod-vault.vault.azure.net"
    objectName: "database-password"
    objectType: "Secret"
  workloadSelector:
    kind: "Deployment"
    name: "order-service"
  validationProbes:
    - type: "PostgreSQL"
      endpoint: "postgres-cluster.production.svc.cluster.local:5432"
      queryTimeout: 5
    - type: "HTTP"
      endpoint: "http://localhost:8080/health"
      queryTimeout: 3
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

Apply the policy:
```bash
kubectl apply -f order-service-policy.yaml
```

---

## 📊 Observability & Metrics

DSO exposes Prometheus metrics on `:8080/metrics`:

| Metric | Type | Description |
| :--- | :--- | :--- |
| `dso_rotations_total` | Counter | Total number of secret rotations initiated by policy and namespace. |
| `dso_rotations_failed` | Counter | Total number of secret rotations that failed validation or promotion. |
| `dso_circuit_breakers_tripped` | Counter | Count of circuit breakers tripped due to consecutive failure thresholds. |
| `dso_probe_duration_seconds` | Histogram | Validation probe execution latency partitioned by `probe_type`. |

---

## 📖 Documentation & Guides

- [**API Reference (`DynamicSecretPolicy`)**](docs/api-reference.md): Complete field-by-field schema breakdown, defaults, and Kyverno policy-as-code templates.
- [**Troubleshooting & Runbooks**](docs/troubleshooting.md): Operational guide for Service Bus DLQ handling, circuit breaker recovery, probe failures, and metrics.
- [**GitOps & Argo CD Integration**](docs/gitops-argo-cd.md): Preventing drift reconciliation conflicts with `ignoreDifferences` and `ARGOCD_AUTOPATCH_ENABLED`.

---

## 💡 Production & Enterprise Examples

Explore our comprehensive reference architecture examples for both local testing and live cloud deployment:

### 💻 Local (`kind`) Examples
- [**Fullstack Database Rotation**](examples/local/fullstack-db-rotation/): Interactive web dashboard demonstrating live PostgreSQL zero-downtime credential rotations.
- [**TLS Certificate Rotation**](examples/local/tls-certificate-rotation/): Automated Azure Key Vault certificate rotation with native `kubernetes.io/tls` secret mapping and TLS probe validation.
- [**Argo Rollouts Blue/Green**](examples/local/argo-rollouts-blue-green/): Progressive Blue/Green delivery with immutable SecretRevisions and automated preview validation cutover.
- [**Nginx Color Canary Rollout**](examples/local/nginx-color-rotation/): Visualizing canary rollout transitions and Argo CD GitOps drift auto-patching.

### ☁️ Azure Kubernetes Service (AKS) Examples
- [**AKS Fullstack DB Rotation**](examples/aks/fullstack-db-rotation/): Live AKS cluster integration with Azure Key Vault, Service Bus, and Workload Identity.
- [**AKS Argo Rollouts Blue/Green**](examples/aks/argo-rollouts-blue-green/): Live AKS Blue/Green promotion triggered by Azure Key Vault rotations.
- [**AKS TLS Certificate Rotation**](examples/aks/tls-certificate-rotation/): Live AKS TLS Gateway with Azure Key Vault SSL certificate auto-parsing.
- [**AKS Nginx Color Canary**](examples/aks/nginx-color-rotation/): Live AKS Canary rollout with Argo CD `ignoreDifferences` auto-patching.

---

## 📚 Architecture Decision Records (ADRs)

Deep-dive design rationale is documented in our ADR repository:
- [**ADR-001: Azure Service Bus Peek-Lock vs Event Grid Webhooks**](docs/architecture/001-asb-peek-lock-vs-webhooks.md)
- [**ADR-002: Immutable Secret Revisions vs In-Place Mutable Updates**](docs/architecture/002-immutable-revisions-vs-mutable.md)

---

## 🛡️ Security & Vulnerability Reporting

This project enforces strict **Zero-Trust Security**. 
- Base images are refreshed automatically against Chainguard Static Distroless.
- All release artifacts are signed via Sigstore Cosign with attached SPDX SBOMs.
- To report a security vulnerability, please refer to our [Security Policy](SECURITY.md).

---

## 📄 License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.
