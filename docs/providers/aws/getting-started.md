# Getting Started with AWS Secrets Manager & DSO

This guide explains how to configure and deploy the **Dynamic Secret Operator (DSO)** for workloads consuming secrets from **AWS Secrets Manager** on Amazon Elastic Kubernetes Service (EKS).

Two implementation tracks are available:
1. **[Track 1: ESO Mode](#track-1-eso-mode)** *(Decoupled)*
2. **[Track 2: Native Event-Driven Mode](#track-2-native-event-driven-mode)** *(Recommended)*

---

## Track 1: ESO Mode

In this decoupled model, the CNCF [External Secrets Operator (ESO)](https://external-secrets.io/) synchronizes credentials from AWS Secrets Manager into an intermediate Kubernetes Secret, while DSO manages progressive canary validation, synthetic probes, and zero-downtime workload updates without needing any AWS IAM permissions.

### Step 1: Configure AWS IAM for ESO
Create an IAM Role for Service Accounts (IRSA) for the ESO controller:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "secretsmanager:GetResourcePolicy",
        "secretsmanager:GetSecretValue",
        "secretsmanager:DescribeSecret",
        "secretsmanager:ListSecretVersionIds"
      ],
      "Resource": ["arn:aws:secretsmanager:<REGION>:<ACCOUNT_ID>:secret:production/*"]
    }
  ]
}
```

Bind this role to the `external-secrets` ServiceAccount in namespace `external-secrets`.

### Step 2: Install External Secrets Operator (ESO)
```bash
helm repo add external-secrets https://charts.external-secrets.io
helm repo update

helm install external-secrets \
  external-secrets/external-secrets \
  --namespace external-secrets \
  --create-namespace \
  --set installCRDs=true \
  --wait
```

### Step 3: Install DSO in ESO Mode
Deploy DSO with `--set mode=eso`. Because ingestion is handled by ESO, DSO requires **zero cloud credentials**:

#### PowerShell (Windows)
```powershell
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator `
  --namespace dso-system `
  --create-namespace `
  --set mode=eso `
  --wait
```

#### Bash (Linux / macOS)
```bash
helm install dso oci://ghcr.io/quantumsys-dev/charts/dynamic-secret-operator \
  --namespace dso-system \
  --create-namespace \
  --set mode=eso \
  --wait
```

### Step 4: Create SecretStore & ExternalSecret with Watch Label
Define a `SecretStore` referencing AWS Secrets Manager, and declare an `ExternalSecret` with the mandatory `dso.quantumsys.dev/managed: "watch"` label:

```yaml
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: aws-secrets-backend
  namespace: production
spec:
  provider:
    aws:
      service: SecretsManager
      region: us-east-1
      auth:
        jwt:
          serviceAccountRef:
            name: external-secrets
---
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: aurora-db-credentials-sync
  namespace: production
spec:
  refreshInterval: 1m
  secretStoreRef:
    name: aws-secrets-backend
    kind: SecretStore
  target:
    name: aurora-db-credentials-synced
    template:
      metadata:
        labels:
          # Mandatory label for DSO discovery
          dso.quantumsys.dev/managed: "watch"
  data:
    - secretKey: password
      remoteRef:
        key: production/aurora-db
        property: password
```

### Step 5: Declare DynamicSecretPolicy
Bind the synced secret to your target workload:

```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: aurora-db-policy
  namespace: production
spec:
  source:
    type: K8sSecret
    k8sSecret:
      name: aurora-db-credentials-synced
  workloadSelector:
    kind: Deployment
    name: order-service
  targetRef:
    volumeName: db-secret-volume
  validationProbes:
    - type: PostgreSQL
      endpoint: "aurora-cluster.production.rds.amazonaws.com:5432"
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

---

## Track 2: Native Event-Driven Mode

### 1. Infrastructure Architecture
- **Amazon EventBridge Rule:** Filters for AWS Secrets Manager rotation events and delivers to an Amazon SQS queue:
  ```json
  {
    "source": ["aws.secretsmanager"],
    "detail-type": [
      "AWS Secrets Manager Secret Rotation",
      "AWS Service Event via CloudTrail"
    ],
    "detail": {
      "eventName": ["PutSecretValue", "UpdateSecretVersionStage"]
    }
  }
  ```
- **Amazon SQS Queue:** Holds event messages for consumption with visibility timeout. 
  - **Note (Visibility Timeout):** Set your SQS Default Visibility Timeout to **>= 2 minutes**. If it is set too low (e.g., 30s), SQS may redeliver the rotation event while the DSO is still running the canary health checks.
  - **Note (Resource Policy):** The queue must have a Resource-Based Policy allowing EventBridge to send messages to it:
  ```json
  {
    "Effect": "Allow",
    "Principal": { "Service": "events.amazonaws.com" },
    "Action": "sqs:SendMessage",
    "Resource": "arn:aws:sqs:<REGION>:<ACCOUNT_ID>:<QUEUE_NAME>"
  }
  ```
- **OpenTelemetry Context Propagation:** To properly link distributed tracing spans, ensure that your EventBridge rule is configured to pass `traceparent` attributes through to the SQS message attributes. Without this, DSO will start a new root trace for every event.
- **AWS IRSA:** Grants DSO permissions to `secretsmanager:GetSecretValue` and `sqs:ReceiveMessage/DeleteMessage/ChangeMessageVisibility`.

### 2. Event-Driven Mode Installation
Deploy DSO configured with AWS native event-driven parameters:

#### PowerShell (Windows)
```powershell
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

#### Bash (Linux / macOS)
```bash
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

### 3. Native DynamicSecretPolicy Syntax
```yaml
apiVersion: dso.quantumsys.dev/v1alpha1
kind: DynamicSecretPolicy
metadata:
  name: aws-order-policy
  namespace: production
spec:
  source:
    type: AWSSecretsManager
    awsSecretsManager:
      secretID: "arn:aws:secretsmanager:us-east-1:123456789012:secret:production/order-db"
  workloadSelector:
    kind: Deployment
    name: order-service
  targetRef:
    volumeName: db-secret-volume
  validationProbes:
    - type: PostgreSQL
      endpoint: "aurora-cluster.production.rds.amazonaws.com:5432"
      queryTimeout: 5
  rollbackConfig:
    autoRollback: true
    circuitBreakerThreshold: 3
```

---

## 🔗 Related Documentation

- [AWS Provider Overview](README.md)
- [AWS Troubleshooting Guide](troubleshooting.md)
- [Universal Multi-Cloud via ESO Guide](../eso/README.md)
