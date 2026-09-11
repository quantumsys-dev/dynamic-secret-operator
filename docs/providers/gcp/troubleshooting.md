# Troubleshooting GCP Secret Manager & DSO

This guide provides diagnostic procedures and resolution playbooks for common issues encountered when rotating secrets from **Google Cloud Secret Manager** with the **Dynamic Secret Operator (DSO)** on Google Kubernetes Engine (GKE).

---

## 🔍 Diagnostic Checklist

When troubleshooting a GCP deployment, inspect the following resources:

1. **Operator Status & Logs:**
   ```bash
   kubectl get pods -n dso-system -l app.kubernetes.io/name=dynamic-secret-operator
   kubectl logs -n dso-system deploy/dynamic-secret-operator -c manager --tail=100
   ```

2. **Policy Status & Conditions:**
   ```bash
   kubectl describe dynamicsecretpolicy <POLICY_NAME> -n <NAMESPACE>
   ```

3. **ExternalSecret & SecretStore Status (if using ESO Mode):**
   ```bash
   kubectl get secretstore,externalsecret -n <NAMESPACE>
   kubectl describe externalsecret <SECRET_NAME> -n <NAMESPACE>
   ```

4. **GKE Workload Identity ServiceAccount Annotations:**
   ```bash
   kubectl get sa -n dso-system dso-dynamic-secret-operator -o yaml
   ```

---

## 🛑 Common Errors & Resolution Playbooks

### 1. `PERMISSION_DENIED` on GCP Secret Manager

#### Symptom:
Operator logs report authentication/authorization failure when accessing a secret:
```
failed to fetch secret from source provider "GCPSecretManager": rpc error: code = PermissionDenied desc = Permission 'secretmanager.versions.access' denied on resource 'projects/.../secrets/.../versions/latest'
```

#### Cause:
The Google Service Account (GSA) bound to the Kubernetes ServiceAccount does not have the `roles/secretmanager.secretAccessor` role, or the secret name/project ID is incorrect.

#### Resolution:
1. Confirm the GSA has the required role on the project or secret:
   ```bash
   gcloud projects add-iam-policy-binding <PROJECT_ID> \
     --member="serviceAccount:<GSA_EMAIL>" \
     --role="roles/secretmanager.secretAccessor"
   ```
2. Verify that the secret exists and contains at least one enabled version:
   ```bash
   gcloud secrets versions list <SECRET_NAME> --project=<PROJECT_ID>
   ```

---

### 2. GKE Workload Identity Impersonation Failure

#### Symptom:
The pod fails to authenticate with GCP APIs, falling back to Compute Engine default credentials or reporting:
```
google: could not find default credentials or GSA token exchange failed
```

#### Cause:
The Kubernetes ServiceAccount (KSA) is missing the annotation linking it to the GSA, or the GSA lacks the `roles/iam.workloadIdentityUser` binding.

#### Resolution:
1. Ensure the KSA has the proper annotation:
   ```bash
   kubectl annotate serviceaccount -n dso-system dso-dynamic-secret-operator \
     iam.gke.io/gcp-service-account=<GSA_NAME>@<PROJECT_ID>.iam.gserviceaccount.com --overwrite
   ```
2. Ensure the GSA allows the KSA to impersonate it:
   ```bash
   gcloud iam service-accounts add-iam-policy-binding \
     <GSA_NAME>@<PROJECT_ID>.iam.gserviceaccount.com \
     --role="roles/iam.workloadIdentityUser" \
     --member="serviceAccount:<PROJECT_ID>.svc.id.goog[dso-system/dso-dynamic-secret-operator]"
   ```

---

### 3. Cloud Pub/Sub Subscription Permissions (Native Event-Driven Mode)

#### Symptom:
DSO fails to start streaming pull against Cloud Pub/Sub:
```
failed to create Pub/Sub subscriber: rpc error: code = PermissionDenied desc = User not authorized to pull from subscription
```

#### Cause:
The GSA lacks the `roles/pubsub.subscriber` role on the target Cloud Pub/Sub subscription.

#### Resolution:
Grant the subscriber role on the subscription:
```bash
gcloud pubsub subscriptions add-iam-policy-binding <SUBSCRIPTION_NAME> \
  --member="serviceAccount:<GSA_EMAIL>" \
  --role="roles/pubsub.subscriber" \
  --project=<PROJECT_ID>
```

---

### 4. Secret Manager Pub/Sub Notifications Not Triggering

#### Symptom:
Secret versions are added to Secret Manager, but no messages arrive on the Pub/Sub topic:
```bash
gcloud pubsub subscriptions seek <SUBSCRIPTION_NAME> --time=now
```

#### Cause:
The Secret Manager service agent for your project lacks permission to publish to the Cloud Pub/Sub topic.

#### Resolution:
1. Retrieve your project's Secret Manager Service Agent email:
   ```bash
   SM_SERVICE_ACCOUNT="service-$(gcloud projects describe <PROJECT_ID> --format='value(projectNumber)')@gcp-sa-secretmanager.iam.gserviceaccount.com"
   ```
2. Grant `roles/pubsub.publisher` on the Pub/Sub topic:
   ```bash
   gcloud pubsub topics add-iam-policy-binding <TOPIC_NAME> \
     --member="serviceAccount:$SM_SERVICE_ACCOUNT" \
     --role="roles/pubsub.publisher" \
     --project=<PROJECT_ID>
   ```

---

### 5. In ESO Mode: `ExternalSecret` Reports `SecretSyncedError`

#### Symptom:
`ExternalSecret` status shows:
```
Status: SecretSyncedError
Reason: ProviderConfigError: could not fetch secret: secret not found
```

#### Cause:
The secret key in `spec.data[].remoteRef.key` does not match the exact Secret ID in GCP Secret Manager, or project ID is missing in `SecretStore`.

#### Resolution:
1. Inspect the detailed error:
   ```bash
   kubectl describe externalsecret <SECRET_NAME> -n <NAMESPACE>
   ```
2. Verify that `spec.provider.gcpsm.projectID` in your `SecretStore` is set correctly.

---

## 🔗 Related Documentation

- [GCP Provider Overview](README.md)
- [GCP Getting Started Guide](getting-started.md)
- [Universal Multi-Cloud via ESO Guide](../eso/README.md)
