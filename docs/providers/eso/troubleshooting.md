# Troubleshooting ESO & DSO (Universal Multi-Cloud)

This guide provides diagnostic procedures and resolution playbooks for common issues encountered when running the **Dynamic Secret Operator (DSO)** in **Decoupled ESO Mode** alongside the **External Secrets Operator (ESO)**.

---

## 🔍 Diagnostic Checklist

When troubleshooting ESO mode, verify the state of both operators:

1. **Check ESO Resource Health:**
   ```bash
   kubectl get secretstore,clustersecretstore,externalsecret -A
   kubectl describe externalsecret <SECRET_NAME> -n <NAMESPACE>
   ```

2. **Verify the Synced Secret & Label Contract:**
   ```bash
   kubectl get secret <SECRET_NAME> -n <NAMESPACE> --show-labels
   ```
   *Must include:* `dso.quantumsys.dev/managed=watch`.

3. **Check DSO Policy Health:**
   ```bash
   kubectl describe dynamicsecretpolicy <POLICY_NAME> -n <NAMESPACE>
   ```

4. **Inspect DSO Controller Logs:**
   ```bash
   kubectl logs -n dso-system deploy/dynamic-secret-operator -c manager --tail=100
   ```

---

## 🛑 Common Errors & Resolution Playbooks

### 1. Secret Updated in Cloud Vault, but DSO Never Triggers Rotation

#### Symptom:
You updated the secret in AWS Secrets Manager, GCP Secret Manager, or Azure Key Vault, but DSO does not start a canary rollout.

#### Cause 1: Missing Watch Label on Synced Secret
By design, DSO's informer cache strictly monitors secrets carrying:
```yaml
dso.quantumsys.dev/managed: "watch"
```
If the `ExternalSecret` does not declare this label inside `spec.target.template.metadata.labels`, DSO ignores the secret entirely to prevent memory bloat.

#### Resolution:
Update your `ExternalSecret` manifest:
```yaml
spec:
  target:
    name: db-credentials-synced
    template:
      metadata:
        labels:
          dso.quantumsys.dev/managed: "watch"
```
Apply the change and verify the label appears on the materialized Secret:
```bash
kubectl get secret db-credentials-synced -n <NAMESPACE> -o jsonpath='{.metadata.labels}'
```

#### Cause 2: ESO Polling Refresh Interval Delay
ESO reconciles external vaults based on `spec.refreshInterval` (e.g. `1h` or `15m`). If you just updated the vault, ESO may not have fetched the new version yet.

#### Resolution:
1. Trigger an immediate sync using the ESO CLI or annotation:
   ```bash
   kubectl annotate externalsecret <NAME> -n <NAMESPACE> force-sync=$(date +%s) --overwrite
   ```
2. Verify that the Secret resource's `.data` has changed before checking DSO.

---

### 2. `ExternalSecret` Reports `SecretSyncedError`

#### Symptom:
The `ExternalSecret` reports `Ready: False`:
```bash
kubectl get externalsecrets -n <NAMESPACE>
```
```
NAME               STORE                 REFRESH   STATUS              READY
db-secret-sync     cloud-vault-backend   1m        SecretSyncedError   False
```

#### Cause:
ESO cannot authenticate with the external vault, or the secret key does not exist in the vault.

#### Resolution:
1. Inspect the detailed error emitted by ESO:
   ```bash
   kubectl describe externalsecret <NAME> -n <NAMESPACE>
   ```
2. Common causes:
   - **`SecretStore not ready`**: The referenced `SecretStore` has invalid IAM credentials or networking blocks.
   - **`secret not found`**: The `spec.data[].remoteRef.key` does not match the secret name in the vault.
   - **`property not found`**: The secret payload in the vault is not valid JSON or lacks the requested JSON property.

---

### 3. DSO Reports `WorkloadNotFound` or `VolumeNotFound`

#### Symptom:
The `DynamicSecretPolicy` status contains errors:
```
status:
  conditions:
    - type: Ready
      status: "False"
      reason: WorkloadNotFound
      message: "target workload Deployment 'order-service' not found in namespace 'production'"
```

#### Cause:
1. The workload name or kind in `spec.workloadSelector` is mistyped, or
2. The volume name specified in `spec.targetRef.volumeName` does not exist in the workload's pod template.

#### Resolution:
1. Verify the workload exists in the same namespace:
   ```bash
   kubectl get deployment order-service -n production
   ```
2. Inspect the workload's volume definitions:
   ```bash
   kubectl get deployment order-service -n production -o jsonpath='{.spec.template.spec.volumes[*].name}'
   ```
3. Update `spec.workloadSelector` or `spec.targetRef` in your `DynamicSecretPolicy` accordingly.

---

### 4. Synthetic Probes Failing in Canary Pod

#### Symptom:
DSO triggers rotation and deploys the canary, but rolls back:
```
status:
  conditions:
    - type: ValidationSucceeded
      status: "False"
      reason: ValidationProbeFailed
      message: "database authentication failed: dial tcp 10.0.1.5:5432: i/o timeout"
```

#### Cause:
1. **NetworkPolicy Egress:** The ephemeral `NetworkPolicy` created by DSO for the canary blocks egress to the database.
2. **Bad Credentials:** The newly synced password in the vault is incorrect or the user account is locked.

#### Resolution:
1. If the database is external to the cluster, verify that the cluster allows egress to the database IP/port.
2. Verify probe credentials: if using PostgreSQL/MySQL probes with custom secret keys, specify `spec.validationProbes[].credentials.passwordKey`.
3. Check canary pod events and logs:
   ```bash
   kubectl get pods -n <NAMESPACE> -l dso.quantumsys.dev/canary=true
   ```

---

## 🔗 Related Documentation

- [ESO Provider Overview](README.md)
- [ESO Getting Started Guide](getting-started.md)
- [Operator Operating Modes](../../architecture/operating-modes.md)
