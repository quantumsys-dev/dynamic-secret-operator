# Troubleshooting Microsoft Azure Key Vault & DSO

This guide provides diagnostic procedures and resolution playbooks for common issues encountered when running the **Dynamic Secret Operator (DSO)** with **Microsoft Azure Key Vault** and **Azure Service Bus**.

---

## 🔍 Diagnostic Checklist

When encountering an issue, verify these four core components first:

1. **Operator Status & Logs:**
   ```bash
   kubectl get pods -n dso-system -l app.kubernetes.io/name=dynamic-secret-operator
   kubectl logs -n dso-system deploy/dynamic-secret-operator -c manager --tail=100
   ```

2. **Policy Status & Conditions:**
   ```bash
   kubectl describe dynamicsecretpolicy <POLICY_NAME> -n <NAMESPACE>
   ```

3. **Service Bus Queue State:**
   ```bash
   az servicebus queue show \
     --resource-group <RESOURCE_GROUP> \
     --namespace-name <SERVICEBUS_NAMESPACE> \
     --name <QUEUE_NAME> \
     --query "{Active:countDetails.activeMessageCount, DeadLetter:countDetails.deadLetterMessageCount}"
   ```

4. **Workload Identity ServiceAccount Annotations:**
   ```bash
   kubectl get sa dso-dynamic-secret-operator -n dso-system -o yaml
   ```

---

## 🛑 Common Errors & Resolution Playbooks

### 1. `AADSTS70021: No matching federated identity record found`

#### Symptom:
The operator pod logs show errors acquiring Azure tokens:
```
DefaultAzureCredential: failed to acquire token: AADSTS70021: No matching federated identity record found for present_assertion
```

#### Cause:
The Workload Identity federated credential configured in Microsoft Entra ID does not exactly match the Kubernetes ServiceAccount token subject, issuer URL, or audience.

#### Resolution:
1. Verify the AKS OIDC Issuer URL:
   ```bash
   az aks show -n <AKS_NAME> -g <RG> --query "oidcIssuerProfile.issuerUrl" -o tsv
   ```
2. Verify the federated credential in Azure:
   ```bash
   az identity federated-credential show \
     --name dso-federation \
     --identity-name dso-identity \
     --resource-group <RG>
   ```
3. Ensure the properties match exactly:
   - **Issuer:** Matches `https://<region>.oic.prod-aks.azure.com/...` (including `https://` and no trailing slash).
   - **Subject:** Must be `system:serviceaccount:dso-system:dso-dynamic-secret-operator`.
   - **Audience:** Must be `api://AzureADTokenExchange`.

---

### 2. `403 Forbidden` Fetching Secret from Azure Key Vault

#### Symptom:
DSO receives the rotation event from Service Bus, but fails to retrieve the secret value:
```
failed to fetch secret from source provider "AzureKeyVault": caller is not authorized to perform action 'Microsoft.KeyVault/vaults/secrets/getSecret/action'
```

#### Cause:
The User-Assigned Managed Identity is missing the **Key Vault Secrets User** Azure RBAC role on the target Key Vault (or the vault uses legacy Access Policies instead of Azure RBAC).

#### Resolution:
1. Confirm Key Vault Permission Model:
   - In Azure Portal, navigate to **Key Vault $\to$ Access configuration**. Ensure **Azure role-based access control (RBAC)** is selected.
2. Grant the required role to the identity:
   ```bash
   IDENTITY_CLIENT_ID="$(az identity show -n dso-identity -g <RG> --query clientId -o tsv)"
   VAULT_SCOPE="$(az keyvault show -n <VAULT_NAME> -g <RG> --query id -o tsv)"

   az role assignment create \
     --role "Key Vault Secrets User" \
     --assignee "$IDENTITY_CLIENT_ID" \
     --scope "$VAULT_SCOPE"
   ```
3. Note: Role assignments can take up to 2–3 minutes to propagate through Microsoft Entra ID.

---

### 3. `401 Unauthorized` or `403 Forbidden` on Azure Service Bus Queue

#### Symptom:
The operator fails to connect to Azure Service Bus on startup:
```
failed to establish Service Bus AMQP receiver: unauthorized access to queue: ip or identity unauthorized
```

#### Cause:
The Managed Identity lacks the **Azure Service Bus Data Receiver** role on the Service Bus namespace or queue, or network firewall rules on Service Bus block AKS egress.

#### Resolution:
1. Assign the role at the namespace level:
   ```bash
   SB_SCOPE="$(az servicebus namespace show -n <SERVICEBUS_NAMESPACE> -g <RG> --query id -o tsv)"

   az role assignment create \
     --role "Azure Service Bus Data Receiver" \
     --assignee "$IDENTITY_CLIENT_ID" \
     --scope "$SB_SCOPE"
   ```
2. Check Service Bus Networking:
   - If Service Bus has **Selected Networks** enabled, ensure your AKS cluster's egress IP or virtual network subnet is added to allowed networks.

---

### 4. Messages Landing in Service Bus Dead-Letter Queue (DLQ)

#### Symptom:
`deadLetterMessageCount` is increasing, and secrets are not rotating in the cluster:
```bash
az servicebus queue show -n <QUEUE> --namespace-name <NS> -g <RG> --query countDetails.deadLetterMessageCount
```

#### Cause:
1. Event Grid is sending events in an unexpected schema, or
2. The secret named in the event does not correspond to any active `DynamicSecretPolicy`, or
3. Consecutive validation failures exceeded the maximum retry delivery count (`MaxDeliveryCount`).

#### Resolution:
1. Inspect the Dead-Letter Queue reason via Azure CLI:
   ```bash
   az servicebus queue message peek \
     --namespace-name <SERVICEBUS_NAMESPACE> \
     --name <QUEUE_NAME> \
     --queue-type deadletter \
     --resource-group <RG>
   ```
2. Check `DeadLetterReason` in message headers.
3. Ensure the Event Grid subscription specifies `--included-event-types Microsoft.KeyVault.SecretNewVersionCreated`.

---

### 5. Operator Pod Stuck in `ContainerCreating` (Workload Identity)

#### Symptom:
The DSO pod does not start, and `kubectl describe pod` shows:
```
Warning  FailedMount  MountVolume.SetUp failed for volume "azure-identity-token" : serviceaccount token projection failure
```

#### Cause:
The AKS cluster does not have the Workload Identity webhook mutating controller active, or the ServiceAccount is missing the required client ID annotation.

#### Resolution:
1. Verify the ServiceAccount has the annotation:
   ```bash
   kubectl get sa dso-dynamic-secret-operator -n dso-system -o yaml
   ```
   Must contain:
   ```yaml
   metadata:
     annotations:
       azure.workload.identity/client-id: "<MANAGED_IDENTITY_CLIENT_ID>"
   ```
2. Ensure pod template carries the label:
   ```yaml
   metadata:
     labels:
       azure.workload.identity/use: "true"
   ```

---

### 6. Synthetic Probes Failing in Canary Pod

#### Symptom:
The policy transitions to `ValidationProbeFailed`, and production workload is preserved:
```
status:
  conditions:
    - type: ValidationSucceeded
      status: "False"
      reason: ValidationProbeFailed
      message: "database authentication failed: dial tcp 10.0.1.5:5432: i/o timeout"
```

#### Cause:
The ephemeral `NetworkPolicy` created by DSO for the canary sandbox blocks egress traffic to the target endpoint, or the database credentials in Key Vault are invalid.

#### Resolution:
1. **NetworkPolicy Egress:**
   - If the target database is external to the cluster (e.g. Azure Database for PostgreSQL Flexible Server), verify that your cluster network policies permit egress to the external IP/FQDN and port.
2. **Credential Verification:**
   - Test the rotated credentials manually against the database to confirm Key Vault has the correct password.
3. **Database Key Mappings:**
   - If the secret JSON has custom keys (e.g. `pg_pass`), ensure `spec.validationProbes[].credentials.passwordKey` is configured on the `DynamicSecretPolicy`.

---

## 🔗 Related Documentation

- [Azure Provider Overview](README.md)
- [Azure Getting Started Guide](getting-started.md)
- [Operator Configuration Guide](../../architecture/configuration.md)
