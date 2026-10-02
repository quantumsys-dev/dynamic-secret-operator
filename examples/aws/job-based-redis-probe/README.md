# EKS Production Example: Job-Based Redis AUTH Rotation (BYOC Probe)

This example demonstrates **zero-downtime Redis AUTH password rotation** on **AWS Kubernetes Service (EKS)** using DSO's extensible **Job-based validation probe** (`type: Job`).

Instead of a built-in driver, DSO spins up an ephemeral `redis:alpine` Kubernetes **Job** in the target namespace to validate the new credentials with `redis-cli PING` — with zero driver code compiled into the operator binary.

---

## 🏗️ Architecture on EKS

```mermaid
flowchart TD
    subgraph AWSCloud ["☁️ AWS Cloud"]
        ASM["🔑 AWS Secrets Manager\n(Secret: redis-auth-password)"]
        EG["⚡ Event Grid System Topic"]
        ASB["📨 AWS Service Bus\n(Queue: dso-vault-events)"]
    end

    subgraph EKS ["☸️ AWS Kubernetes Service (EKS) Cluster"]
        subgraph DSOSystem ["dso-system Namespace"]
            DSO["⚙️ Dynamic Secret Operator\n(AWS Workload Identity)"]
        end

        subgraph ProductionNS ["dso-examples Namespace"]
            SEC["🔒 Immutable SecretRevision\n(redis-consumer-redis-auth-password-rev-XXXX)"]
            REDIS["🗄️ redis-master\n(Deployment)"]
            APP["📦 redis-consumer\n(Deployment)"]
            CANARY["🐤 Canary Pod\n(NetworkPolicy Isolated)"]
            JOB["🧪 Ephemeral Probe Job\n(redis:7-alpine)\n(auto-deleted on completion)"]
        end
    end

    ASM -->|"1. Secret Rotated"| EG
    EG -->|"2. Forward Event"| ASB
    ASB -->|"3. Peek-Lock Event"| DSO
    DSO -->|"4. Materialize Revision"| SEC
    DSO -->|"5. Provision Isolated Canary"| CANARY
    DSO -->|"6. Create Probe Job\n(DSO_REVISION_SECRET_NAME injected)"| JOB
    JOB -->|"7. redis-cli PING via new secret"| REDIS
    DSO -->|"8. Promote on PING success"| APP
    APP -->|"Mounts"| SEC
```

---

## 💡 How It Works

1. **Event Ingestion:** AWS Secrets Manager notifies AWS Service Bus via Event Grid when `redis-auth-password` is rotated.
2. **Secret Materialization:** DSO fetches the new password and materializes an immutable `Secret` in the `dso-examples` namespace (e.g., `redis-consumer-redis-auth-password-rev-a1b2c3`).
3. **Canary Provisioning:** DSO spins up an isolated 1-replica canary pod with strict `NetworkPolicy`.
4. **Job Probe:** DSO creates an ephemeral `batch/v1.Job` using the `redis:7-alpine` image. The operator automatically injects the `DSO_REVISION_SECRET_NAME` environment variable into container environments with the actual new secret name. The probe container references `$(DSO_REVISION_SECRET_NAME)` or its materialized secret to run `redis-cli PING` and validate connectivity.
5. **Pass / Fail:** If `PING` returns `PONG`, DSO promotes `redis-consumer` to use the new secret (zero downtime rolling update). If it fails, DSO captures the container logs, surfaces them as a `Condition` on the `DynamicSecretPolicy`, and (if configured) rolls back.
6. **Cleanup:** The probe Job is **always deleted** immediately after completion — success or failure — preventing resource accumulation.

---

## 🛠️ Prerequisites

- Provisioned AWS infrastructure (run `setup-AWS-resources.ps1` at repository root).
- `kubectl` authenticated to your EKS cluster (`az EKS get-credentials --resource-group <RG> --secret-id <CLUSTER_NAME>`).
- AWS CLI (`az`) logged in with access to the Secrets Manager.
- DSO operator deployed in the cluster (`dso-system` namespace).

---

## 🚀 Quickstart Deployment

### Step 1: Deploy the Redis Example on EKS

**PowerShell (Windows):**
```powershell
.\deploy-EKS.ps1 -SecretId "kv-dso-dev"
```

**Bash (Linux / WSL / macOS):**
```bash
chmod +x deploy-EKS.sh
./deploy-EKS.sh -k kv-dso-dev
```

Both scripts will:
- Create the `dso-examples` namespace
- Seed the initial `redis-auth-password` secret in Secrets Manager
- Create bootstrap secrets so pods start before DSO's first rotation
- Apply `manifests.yaml` (Redis Deployment, Consumer Deployment, Service, DynamicSecretPolicy)
- Wait for both deployments to reach `Ready`

### Step 2: Observe the Consumer App

Tail the redis-consumer logs to see live PING results:

```bash
kubectl logs -n dso-examples -l app=redis-consumer -f
```

### Step 3: Trigger a Redis AUTH Password Rotation

#### 3.1 Update Password in Redis Master
Simulate the backend credential update:

**PowerShell (Windows):**
```powershell
$CurrentPass = (aws secretsmanager get-secret-value --secret-id kv-dso-dev --secret-id "redis-auth-password" --query value -o tsv)
kubectl exec deployment/redis-master -n dso-examples -- redis-cli -a $CurrentPass CONFIG SET requirepass "RotatedRedisPassword456!"
```

**Bash (Linux / WSL / macOS):**
```bash
CURRENT_PASS=$(aws secretsmanager get-secret-value --secret-id kv-dso-dev --secret-id "redis-auth-password" --query value -o tsv)
kubectl exec deployment/redis-master -n dso-examples -- redis-cli -a "${CURRENT_PASS}" CONFIG SET requirepass 'RotatedRedisPassword456!'
```

#### 3.2 Update Secret in AWS Secrets Manager
Simulate the Secret Manager event notification:

```bash
aws secretsmanager put-secret-value \
  --secret-id kv-dso-dev \
  --secret-id "redis-auth-password" \
  --secret-string "RotatedRedisPassword456!"
```

### Step 4: Watch DSO in Action

```bash
# Watch the DynamicSecretPolicy conditions in real-time
kubectl get dynamicsecretpolicy redis-cache-rotation -n dso-examples -w

# Watch the ephemeral probe Job appear and disappear
kubectl get jobs -n dso-examples -w

# Inspect policy conditions in detail (includes probe Job failure logs if any)
kubectl describe dynamicsecretpolicy redis-cache-rotation -n dso-examples
```

---

## 📁 Files

| File | Description |
|---|---|
| `manifests.yaml` | Redis master + consumer Deployments, Service, and DynamicSecretPolicy with Job probe |
| `deploy-EKS.ps1` | PowerShell deployment script (Windows / AWS Cloud Shell) |
| `deploy-EKS.sh`  | Bash deployment script (Linux / WSL / macOS) |
| `README.md`      | This guide |

---

## 🔐 Security Notes

- The probe Job runs as `runAsNonRoot: true`, `runAsUser: 65534` (nobody), with all Linux capabilities dropped.
- `readOnlyRootFilesystem: true` prevents any writes inside the validation container.
- `backoffLimit: 0` ensures the Job fails fast without retrying.
- The probe Job is **always deleted** after execution via a deferred cleanup in the operator, leaving no orphaned resources in the cluster.
- No Redis driver is compiled into the operator binary — the entire validation is containerized and ephemeral.
