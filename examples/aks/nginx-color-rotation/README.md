# AKS Production Example: Nginx Color Canary Rotation & Argo CD GitOps Integration

This example demonstrates end-to-end automated secret rotation on **Azure Kubernetes Service (AKS)** with **Canary Rollouts** and **Argo CD GitOps Drift Protection**.

---

## 🏗️ Architecture on AKS

```mermaid
flowchart TD
    subgraph AzureCloud ["☁️ Azure Cloud"]
        AKV["🔑 Azure Key Vault<br/>(Secret: nginx-bg-color)"]
        EG["⚡ Event Grid System Topic"]
        ASB["📨 Azure Service Bus<br/>(Queue: dso-vault-events)"]
    end

    subgraph AKS ["☸️ Azure Kubernetes Service (AKS) Cluster"]
        subgraph DSOSystem ["dso-system Namespace"]
            DSO["⚙️ Dynamic Secret Operator<br/>(ARGOCD_AUTOPATCH_ENABLED=true)"]
            PROBE["🩺 HTTP Validation Probe"]
        end

        subgraph GitOps ["argocd Namespace"]
            ARGOCD["🐙 Argo CD Application Controller<br/>(Self-Heal Active)"]
        end

        subgraph ProductionWorkload ["default Namespace"]
            CANARY["🐤 1-Replica Canary Pod<br/>(NetworkPolicy Isolated)"]
            PROD["🚀 Production Nginx Gateway<br/>(Rolling Update)"]
            SEC["🔒 Immutable SecretRevision"]
        end
    end

    AKV -->|"1. Secret Rotated (e.g. #3b82f6)"| EG
    EG -->|"2. Forward Event"| ASB
    ASB -->|"3. Peek-Lock Event"| DSO
    DSO -->|"4. Materialize Revision"| SEC
    DSO -->|"5. Provision Isolated Canary"| CANARY
    DSO -->|"6. HTTP Validation Probe"| PROBE
    PROBE -->|"Verify Status 200"| CANARY
    DSO -->|"7. Auto-Patch ignoreDifferences"| ARGOCD
    DSO -->|"8. Promote Workload"| PROD
    PROD -->|"Mounts"| SEC
```

---

## 💡 How It Works on AKS

1. **Event Ingestion:** Azure Key Vault notifies Azure Service Bus via Event Grid when `nginx-bg-color` updates.
2. **Canary Verification:** DSO provisions an isolated 1-replica canary deployment with strict `NetworkPolicy` to validate the secret before touching live workloads.
3. **Argo CD Drift Reconciliation:** When `ARGOCD_AUTOPATCH_ENABLED="true"` is set, DSO automatically patches `spec.ignoreDifferences` on the parent Argo CD `Application`, preventing Argo CD Self-Heal from rolling back the in-cluster secret mutation.
4. **Production Promotion:** Once validated, DSO promotes the production Nginx deployment with zero downtime.

---

## 🛠️ Prerequisites

- Provisioned Azure infrastructure (Run `setup-azure-resources.ps1` at repository root).
- `kubectl` authenticated to your AKS cluster (`az aks get-credentials --resource-group <RG> --name <CLUSTER_NAME>`).
- Azure CLI (`az`) logged in.

---

## 🚀 Quickstart Deployment

### Step 1: Deploy Nginx Color Rotation Example on AKS
Execute the deployment script providing your Azure Key Vault name:

**PowerShell (Windows):**
```powershell
.\deploy-aks.ps1 -KeyVaultName "kv-dso-dev"
```

**Bash (Linux / WSL / macOS):**
```bash
chmod +x deploy-aks.sh
./deploy-aks.sh -k kv-dso-dev
```

### Step 2: Access the Nginx Gateway
Forward the gateway port:

```bash
kubectl port-forward svc/nginx-color-app 8080:80
```

Open [http://localhost:8080](http://localhost:8080) to observe the active background color.

### Step 3: Trigger a Secret Rotation in Key Vault
Update the background color secret in Azure Key Vault:

```bash
az keyvault secret set \
  --vault-name kv-dso-dev \
  --name "nginx-bg-color" \
  --value "#10b981"
```

Refresh your browser to see the new color active without application downtime or Argo CD drift conflicts.
