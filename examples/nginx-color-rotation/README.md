# Nginx Color Rotation Example (Local kind & Argo CD)

This example demonstrates end-to-end **Dynamic Secret Operator (DSO)** functionality on a local [`kind`](https://kind.sigs.k8s.io/) Kubernetes cluster with **Argo CD GitOps drift auto-patching**.

When you update a secret (such as a background hex color) in Azure Key Vault, DSO:
1. Receives an instant event notification via Azure Service Bus (Peek-Lock).
2. Materializes a versioned, immutable Kubernetes Secret revision.
3. Provisions an isolated 1-replica **Canary Deployment** and **NetworkPolicy**.
4. Validates health and promotes the production workload.
5. **GitOps Auto-Patching:** Automatically patches the parent Argo CD `Application` (`spec.ignoreDifferences`) so that Argo CD's Self-Heal does not roll back the in-cluster mutation!

---

## 🛠️ Prerequisites

- **kind**: `kind v0.20.0+` ([installation guide](https://kind.sigs.k8s.io/docs/user/quick-start/#installation))
- **kubectl**: Kubernetes CLI ([installation guide](https://kubernetes.io/docs/tasks/tools/))
- **Go**: `go1.26+` ([installation guide](https://go.dev/doc/install))
- **Azure CLI**: `az` CLI ([installation guide](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli))
- **Azure Service Principal**: With `Key Vault Secrets User` and `Azure Service Bus Data Receiver` roles.

---

## 🚀 Quickstart

### Step 1: Configure Manifest Placeholders
Edit `examples/nginx-color-rotation/manifests.yaml` and update the `DynamicSecretPolicy` with your Azure Key Vault URI:

```yaml
spec:
  vaultRef:
    keyVaultURI: "https://<YOUR_KEYVAULT_NAME>.vault.azure.net"
    objectName: "database-password"
    objectType: "Secret"
```

### Step 2: Bootstrap the Local Cluster
Execute the setup script to create the `dso-local` kind cluster, install Argo CD (`v2.11.0`), and deploy the manifests:

```bash
chmod +x setup-kind.sh
./setup-kind.sh
```

### Step 3: Create Local `.env` Credentials File
Create a `.env` file at the root of the repository (this file is ignored by Git):

```bash
# Enable automatic Argo CD ignoreDifferences patching
export ARGOCD_AUTOPATCH_ENABLED="true"

# Azure Service Principal credentials
export AZURE_TENANT_ID="<YOUR_AZURE_TENANT_ID>"
export AZURE_CLIENT_ID="<YOUR_SERVICE_PRINCIPAL_CLIENT_ID>"
export AZURE_CLIENT_SECRET="<YOUR_SERVICE_PRINCIPAL_CLIENT_SECRET>"

# Azure Service Bus queue receiving Key Vault EventGrid events
export SERVICEBUS_NAMESPACE="<YOUR_SERVICEBUS_NAMESPACE>.servicebus.windows.net"
export SERVICEBUS_QUEUE_NAME="<YOUR_QUEUE_NAME>"
```

### Step 4: Run the Operator Locally
Install the CRDs and start the controller in your terminal:

```bash
make install
source .env
make run
```

---

## 🧪 Testing Live Rotation

### 1. View the Application
Port-forward the test service in a separate terminal:

```bash
kubectl port-forward svc/nginx-crypto-tracker 8080:80
```
Open **http://localhost:8080** in your browser.

### 2. Rotate the Secret in Azure Key Vault
Update the secret to a new hex color (e.g., Emerald Green `#10b981` or Crimson `#e74c3c`):

```bash
az keyvault secret set \
  --vault-name "<YOUR_KEYVAULT_NAME>" \
  --name "database-password" \
  --value "#10b981"
```

### 3. Observe the Results
- The browser page at `http://localhost:8080` smoothly updates to the new color.
- Check the Argo CD application diffing rules:
  ```bash
  kubectl get application crypto-tracker-app -n argocd -o yaml
  ```
  Notice that DSO automatically discovered the application and injected `spec.ignoreDifferences` without requiring manual Git changes!

---

## 🧹 Teardown

When you are done testing, delete the kind cluster:

```bash
kind delete cluster --name dso-local
```
