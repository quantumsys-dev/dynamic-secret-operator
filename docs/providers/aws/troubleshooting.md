# Troubleshooting AWS Secrets Manager & DSO

This guide provides diagnostic procedures and resolution playbooks for common issues encountered when rotating secrets from **AWS Secrets Manager** with the **Dynamic Secret Operator (DSO)** on Amazon EKS.

---

## 🔍 Diagnostic Checklist

When troubleshooting an AWS deployment, inspect the following resources:

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

4. **AWS IRSA Token Projection:**
   ```bash
   kubectl exec -it -n dso-system deploy/dynamic-secret-operator -- env | grep -E "AWS_ROLE_ARN|AWS_WEB_IDENTITY_TOKEN_FILE"
   ```

---

## 🛑 Common Errors & Resolution Playbooks

### 1. `AccessDeniedException` on AWS Secrets Manager

#### Symptom:
Logs show access denied when attempting to fetch secrets:
```
failed to fetch secret from source provider "AWSSecretsManager": AccessDeniedException: User: arn:aws:sts::<ACCOUNT_ID>:assumed-role/dso-role is not authorized to perform: secretsmanager:GetSecretValue on resource: arn:aws:secretsmanager:...
```

#### Cause:
The IAM Role lacks the `secretsmanager:GetSecretValue` permission, or the Secret has a Resource-based policy that denies the assumed role.

#### Resolution:
1. Verify the IAM policy attached to the role:
   ```json
   {
     "Effect": "Allow",
     "Action": [
       "secretsmanager:GetSecretValue",
       "secretsmanager:DescribeSecret"
     ],
     "Resource": "arn:aws:secretsmanager:<REGION>:<ACCOUNT_ID>:secret:<SECRET_PREFIX>/*"
   }
   ```
2. If the secret is encrypted with a customer-managed KMS key (CMK), the role must also have:
   ```json
   {
     "Effect": "Allow",
     "Action": ["kms:Decrypt"],
     "Resource": "arn:aws:kms:<REGION>:<ACCOUNT_ID>:key/<KEY_ID>"
   }
   ```

---

### 2. IRSA Trust Relationship / `sts:AssumeRoleWithWebIdentity` Failure

#### Symptom:
Operator pod fails to authenticate with AWS STS:
```
InvalidIdentityToken: OpenIDConnect provider's HTTPS certificate has changed or token is expired
```

#### Cause:
The IAM Role's trust policy does not match the EKS cluster's OIDC issuer URL, or the ServiceAccount name/namespace does not match.

#### Resolution:
1. Retrieve the cluster OIDC provider URL:
   ```bash
   aws eks describe-cluster --name <CLUSTER_NAME> --query "cluster.identity.oidc.issuer" --output text
   ```
   *(Example: `https://oidc.eks.us-east-1.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE`)*
2. Check the Trust Relationship on the IAM Role:
   ```json
   {
     "Version": "2012-10-17",
     "Statement": [
       {
         "Effect": "Allow",
         "Principal": {
           "Federated": "arn:aws:iam::<ACCOUNT_ID>:oidc-provider/oidc.eks.<REGION>.amazonaws.com/id/<OIDC_ID>"
         },
         "Action": "sts:AssumeRoleWithWebIdentity",
         "Condition": {
           "StringEquals": {
             "oidc.eks.<REGION>.amazonaws.com/id/<OIDC_ID>:sub": "system:serviceaccount:dso-system:dso-dynamic-secret-operator",
             "oidc.eks.<REGION>.amazonaws.com/id/<OIDC_ID>:aud": "sts.amazonaws.com"
           }
         }
       }
     ]
   }
   ```

---

### 3. SQS Permissions Failure (Native Event-Driven Mode)

#### Symptom:
Operator fails to read rotation event messages from Amazon SQS:
```
failed to receive messages from SQS queue: AccessDenied: Access to the given resource is forbidden
```

#### Cause:
The IAM Role lacks SQS consumer permissions on the specified queue.

#### Resolution:
Add the required SQS actions to the IAM role policy:
```json
{
  "Effect": "Allow",
  "Action": [
    "sqs:ReceiveMessage",
    "sqs:DeleteMessage",
    "sqs:GetQueueAttributes",
    "sqs:ChangeMessageVisibility"
  ],
  "Resource": "arn:aws:sqs:<REGION>:<ACCOUNT_ID>:<QUEUE_NAME>"
}
```

---

### 4. EventBridge Rotation Events Not Reaching SQS

#### Symptom:
Secrets are rotated in AWS Secrets Manager, but the SQS queue remains empty:
```bash
aws sqs get-queue-attributes --queue-url <QUEUE_URL> --attribute-names ApproximateNumberOfMessages
```

#### Cause:
The SQS queue policy does not grant permissions for Amazon EventBridge to publish messages, or the EventBridge rule pattern is too restrictive.

#### Resolution:
1. Ensure the SQS Queue Policy allows EventBridge (`events.amazonaws.com`):
   ```json
   {
     "Sid": "AWSEvents-dso-rule",
     "Effect": "Allow",
     "Principal": {
       "Service": "events.amazonaws.com"
     },
     "Action": "sqs:SendMessage",
     "Resource": "arn:aws:sqs:<REGION>:<ACCOUNT_ID>:<QUEUE_NAME>",
     "Condition": {
       "ArnEquals": {
         "aws:SourceArn": "arn:aws:events:<REGION>:<ACCOUNT_ID>:rule/<RULE_NAME>"
       }
     }
   }
   ```
2. Test the EventBridge rule in the AWS Console with a sample event.

---

### 5. In ESO Mode: `ExternalSecret` Reports `SecretSyncedError`

#### Symptom:
The `ExternalSecret` resource is stuck with `Ready: False`:
```bash
kubectl get externalsecrets -n production
```
```
NAME                          STORE                 REFRESH   STATUS              READY
aurora-db-credentials-sync    aws-secrets-backend   1m        SecretSyncedError   False
```

#### Cause:
The secret name in `spec.data[].remoteRef.key` does not exist in AWS Secrets Manager, or the region configured in `SecretStore` is incorrect.

#### Resolution:
1. Inspect the detailed error message:
   ```bash
   kubectl describe externalsecret aurora-db-credentials-sync -n production
   ```
2. Confirm the secret exists in the specified AWS region:
   ```bash
   aws secretsmanager describe-secret --secret-id <SECRET_NAME> --region <REGION>
   ```

---

## 🔗 Related Documentation

- [AWS Provider Overview](README.md)
- [AWS Getting Started Guide](getting-started.md)
- [Universal Multi-Cloud via ESO Guide](../eso/README.md)
