# Security Architecture & Threat Model

This document outlines the security architecture, threat model, and defense-in-depth principles implemented across the **Dynamic Secret Operator (DSO)**.

---

## 1. Executive Summary & Security Posture

The Dynamic Secret Operator is designed from the ground up under the principle of **Zero Trust**. It automates secret rotation and progressive canary verification on Kubernetes without ever storing static credentials, exposing cluster-wide secrets, or relying on false security assumptions.

```mermaid
flowchart TD
    subgraph AzureCloud ["☁️ Azure Cloud"]
        AKV["Azure Key Vault"]
        ASB["Azure Service Bus (Peek-Lock)"]
        MI["User-Assigned Managed Identity"]
    end

    subgraph KubernetesCluster ["☸️ Kubernetes Cluster"]
        subgraph DSOSystem ["dso-system Namespace"]
            DSO["⚙️ Dynamic Secret Operator<br/>(Chainguard Distroless / Non-Root)"]
        end

        subgraph ManagedWorkloads ["Target Workload Namespace"]
            SEC["🔒 Immutable SecretRevision<br/>(dso.quantumsys.dev/managed: 'true')"]
            CANARY["🐤 Ephemeral Canary Workload"]
            NETPOL["🛡️ Strict NetworkPolicy"]
            PROD["🚀 Production Workload"]
        end
    end

    MI -.->|"OIDC Federated Token"| DSO
    AKV -->|"Secret Payload (mTLS)"| DSO
    ASB -->|"Rotation Event"| DSO
    DSO -->|"Materialize Revision"| SEC
    DSO -->|"Deploy & Validate"| CANARY
    CANARY --- NETPOL
    DSO -->|"Zero-Downtime Patch"| PROD
```

---

## 2. Memory Lifecycle & Go Runtime Threat Model

### The Myth of Manual Heap Zeroization in Garbage-Collected Languages
In systems programming languages like C/Rust, developers can use volatile memory operations (`explicit_bzero` / `sodium_memzero`) to zero memory prior to deallocation. In managed runtime languages like **Go**:
1. **Immutable Strings:** Converting secret payloads (`[]byte`) to strings (e.g., for DSN connection strings, URLs, or HTTP headers) copies the bytes into immutable heap buffers managed entirely by the Go runtime allocator.
2. **Garbage Collector Lifecycle:** The Go GC moves and copies memory blocks during stack growth and allocation routines. Even if a specific `[]byte` slice is zeroed, previous copies, compiler optimizations, or string conversions may remain in unmanaged garbage regions until overwritten by future allocations.
3. **False Sense of Security:** Attempting manual memory zeroing (`ZeroBytes`) in application-level Go code creates an architectural anti-pattern and a false sense of security while adding cognitive overhead.

### Real Defense-in-Depth Memory Controls
Rather than relying on un-guaranteed in-memory zeroing, DSO implements concrete operating-system and container-level boundaries:

| Security Control | Implementation & Enforcement |
| :--- | :--- |
| **Strict Process Isolation** | The operator runs in dedicated namespaces (`dso-system`) isolated from tenant workloads. |
| **Non-Root Execution** | Container runs strictly as unprivileged user (`runAsNonRoot: true`, `runAsUser: 65534 / nobody`, `fsGroup: 65534`). |
| **Read-Only Root Filesystem** | `readOnlyRootFilesystem: true` prevents an attacker from writing executables or dumping memory artifacts to disk. |
| **Privilege Escalation Prevention** | `allowPrivilegeEscalation: false` and `capabilities.drop: ["ALL"]` prevent Linux capability exploits. |
| **Seccomp Sandboxing** | `seccompProfile.type: RuntimeDefault` restricts system calls available to the container runtime. |
| **Disabled Memory Dumps** | Core dumps and ptrace attachments are blocked at the host and container level. |
| **Zero Logging of Secret Payloads** | Payloads are strictly excluded from log streams, traces, events, and metrics. |

---

## 3. Secret Ingestion & Cache Isolation

### Blast-Radius Minimization
By default, Kubernetes operators that reconcile `Secret` resources risk caching all secrets cluster-wide in memory. DSO eliminates this blast radius via **scoped controller-runtime caching**:

```go
// cmd/main.go
secretCacheRequirement, _ := labels.NewRequirement(
    "dso.quantumsys.dev/managed", selection.In, []string{"true", "watch"},
)
Cache: cache.Options{
    ByObject: map[client.Object]cache.ByObject{
        &corev1.Secret{}: {
            Label: labels.NewSelector().Add(*secretCacheRequirement),
        },
    },
}
```

1. **Label Stamping:** Every secret materialized by DSO is explicitly stamped with:
   - `dso.quantumsys.dev/managed: "true"`
   - `dso.quantumsys.dev/policy: <policy-name>`
   - `dso.quantumsys.dev/revision: <hash>`
   - `dso.quantumsys.dev/target-workload: <workload-name>`
2. **Cache Restriction:** The operator's informer only requests and watches secrets bearing `dso.quantumsys.dev/managed: "true"` (secrets DSO owns) or `dso.quantumsys.dev/managed: "watch"` (externally owned source secrets DSO only observes, e.g. an ESO sync target - see [ADR-003](architecture/003-decoupling-secret-ingestion-eso.md)).
3. **Protection of Third-Party Secrets:** The operator never receives or caches `ServiceAccount` tokens, cluster TLS certificates, Helm release secrets, or unrelated tenant data.

### RBAC Is a Separate Boundary From the Cache
The cache restriction above only limits what the operator's own in-process informer *holds in memory* - it is a client-side filter, not an API-server-enforced boundary. The `ClusterRole` the operator's ServiceAccount holds still grants `get`/`list`/`watch` on the `secrets` resource across **all** namespaces, because Kubernetes RBAC has no concept of "restrict to secrets with this label" - RBAC rules can only be scoped by API group, resource, verb, and (optionally) an explicit `resourceNames` list, none of which can express a label selector. This means that if the DSO pod itself were compromised (e.g. via a dependency vulnerability or container escape), the attacker's stolen ServiceAccount token could call the Kubernetes API directly to read **any** secret in the cluster, including unrelated `kube-system` TLS certificates and other workloads' credentials - the cache scoping above would not stop them, since it is bypassed entirely by talking to the API server directly rather than through the operator's cache.

There is no way to close this gap with RBAC alone while still supporting cluster-wide secret rotation from a single Deployment. For high-security or multi-tenant clusters, deploy DSO with `rbac.scope: "Namespaced"` and an explicit `rbac.watchNamespaces` list in the Helm chart's `values.yaml`. This grants a `Role`/`RoleBinding` per listed namespace instead of a cluster-wide `ClusterRole`, **and** passes the same namespace list to the operator via `--watch-namespaces`, which sets `cache.Options.DefaultNamespaces` so the manager's own List/Watch calls are scoped identically to the RBAC grant - eliminating the ability to read secrets outside those namespaces even if the pod is compromised, rather than just narrowing the RBAC grant while the manager still (harmlessly, but noisily) attempts to watch cluster-wide.

---

## 4. Authentication & Identity Architecture

### Passwordless Azure Workload Identity
DSO completely eliminates static credentials:
- **Projected ServiceAccount Tokens:** Kubernetes projects short-lived OIDC tokens (`/var/run/secrets/azure/tokens/azure-identity-token`).
- **Federated Credential Exchange:** The Azure SDK exchanges the token with Microsoft Entra ID (Azure AD) for short-lived Key Vault access tokens.
- **No Long-Lived Secrets:** No Azure client secrets, certificates, or static service principal credentials are stored in git, Helm values, or cluster storage.

---

## 5. Canary Sandboxing & Blast-Radius Mitigation

When a secret rotation occurs:
1. **Immutable Secret Revision:** DSO materializes a unique secret `<workload>-<secret>-rev-<hash>`, leaving active production secrets untouched.
2. **Ephemeral Canary Deployment:** DSO launches a 1-replica isolated canary workload.
3. **Zero-Trust NetworkPolicy:** An ephemeral `NetworkPolicy` is applied to isolate the canary pod from internal cluster traffic while allowing only the required synthetic validation probe egress.
4. **Validation Probes:** Synthetic probes (PostgreSQL, MySQL, HTTP, TLS, or Job-based) validate authentication before any production modification.
5. **Automatic Teardown:** Upon success or failure, the canary deployment and network policy are deleted immediately.

---

## 6. Error Sanitization & Anti-Leakage Controls

Database drivers and HTTP clients frequently embed credentials in raw connection errors (e.g., `postgres://user:password@host/db`).

DSO uses automated regex sanitization across all probe runners:
- Strips passwords, connection strings, Basic Auth headers, and Bearer tokens before errors are passed to OpenTelemetry spans, Kubernetes Events, or CRD Conditions.
- For Job-based probes, stdout/stderr is captured with length limits and sanitized before status surface.

---

## 7. Supply Chain & Container Hardening

- **Base Image:** Built on zero-CVE **Chainguard Static Distroless** (`cgr.dev/chainguard/static:latest`).
- **Cryptographic Signing:** Images are signed keylessly with **Cosign** using GitHub Actions OIDC identity.
- **SBOM Generation:** Software Bill of Materials (SPDX JSON format) is attached and published with every release.
- **Security Scanning:** Continuous vulnerability scanning in CI with **Trivy** and **Govulncheck**.
