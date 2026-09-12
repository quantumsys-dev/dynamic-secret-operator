# GitOps Integration: Managing Argo CD Drift with DSO

When managing Kubernetes workloads with **Argo CD** (specifically with `selfHeal: true` and `automated: prune` enabled), Argo CD continuously reconciles the live cluster state against the Git repository.

Because the **Dynamic Secret Operator (DSO)** updates secret volumes, container environment variables, and revision annotations directly in-cluster upon secret rotation, Argo CD can detect these mutations as "drift" and immediately revert them back to the Git state. This creates an **infinite reconciliation loop** (DSO promotes new secret -> Argo CD reverts to Git -> Pod restarts with old credentials).

This guide explains how to configure `ignoreDifferences` in your Argo CD manifests to allow DSO to manage runtime secret rotation seamlessly while retaining full GitOps governance over application code, container images, replicas, and infrastructure specifications.

---

## ⚙️ Automatic vs. Declarative Drift Management

DSO provides two approaches to manage Argo CD diffing:

### 1. Declarative GitOps Manifests (Default & Recommended for App-of-Apps)
Explicitly declare fine-grained `jqPathExpressions` in your Git repository's `Application` manifest or globally in the `argocd-cm` ConfigMap.

> [!WARNING]
> #### 🛑 Critical: App-of-Apps & Strict Declarative GitOps Notice
> If your organization uses the **Argo CD App-of-Apps pattern** or **ApplicationSets** (where `Application` CR manifests themselves are committed to Git and continuously reconciled by a root Argo CD application), **DO NOT enable automatic in-cluster patching**.
>
> If `ARGOCD_AUTOPATCH_ENABLED="true"` is enabled in an App-of-Apps environment, DSO's in-cluster patches to the parent `Application` CR will be immediately detected by the root App-of-Apps controller as external drift. Argo CD will revert the `Application` CR back to its Git definition, resulting in an infinite patch-revert reconciliation war between DSO and Argo CD.
>
> **Best Practice for App-of-Apps:**
> Keep `ARGOCD_AUTOPATCH_ENABLED="false"` (the default). Commit the `ignoreDifferences` blocks directly into your Git repository's `Application` YAML manifests (or define them globally in the `argocd-cm` ConfigMap).

### 2. Automatic In-Cluster Patching (`ARGOCD_AUTOPATCH_ENABLED="true"`, Opt-In)
When explicitly enabled in standalone Application environments, DSO automatically:
- Discovers the parent Argo CD `Application` via standard tracking metadata (`argocd.argoproj.io/tracking-id`, `argocd.argoproj.io/instance`, or `app.kubernetes.io/instance`).
- Injects standard JSON Pointer for the revision annotation (`/spec/template/metadata/annotations/dso.quantumsys.dev~1revision`).
- Injects fine-grained JQ path expressions targeting only the DSO-mutated revision secrets:
  ```jq
  .spec.template.spec.volumes[] | select(.secret.secretName | startswith("<appName>-"))
  .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name | startswith("<appName>-"))
  ```
- Retries with exponential backoff on Kubernetes resource version conflicts (`409 Conflict`).

To enable automatic in-cluster patching via Helm:
```yaml
argoCD:
  autoPatch: true
```
*(Or set environment variable `ARGOCD_AUTOPATCH_ENABLED="true"` on the operator deployment).*

---

## 🛠️ Declarative `Application` Configuration

Argo CD provides the [`ignoreDifferences`](https://argo-cd.readthedocs.io/en/stable/user-guide/diffing/) feature to instruct its diffing engine to ignore specific fields mutated by in-cluster controllers.

### Recommended: Fine-Grained JQ Path Ignoring

Using fine-grained `jqPathExpressions` ensures that Argo CD only ignores volumes and environment variables that point to DSO-generated revision secrets (`<target>-<secret>-rev-<hash>`), while continuing to enforce strict GitOps drift detection for all other volumes (ConfigMaps, PVCs, TLS certificates, static secrets):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: order-service-app
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/my-org/gitops-repo.git
    targetRevision: HEAD
    path: apps/order-service
  destination:
    server: https://kubernetes.default.svc
    namespace: production

  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true

  # Fine-grained ignoreDifferences scoped strictly to DSO mutations
  ignoreDifferences:
    # 1. Deployments
    - group: apps
      kind: Deployment
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
      jqPathExpressions:
        # Ignore only volumes referencing DSO-managed revision secrets
        - .spec.template.spec.volumes[] | select(.secret.secretName != null and (.secret.secretName | test("-rev-[0-9a-f]{12}$")))
        # Ignore container env vars referencing DSO-managed revision secrets
        - .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
        # Ignore envFrom referencing DSO-managed revision secrets
        - .spec.template.spec.containers[].envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
        # Ignore initContainer env vars referencing DSO-managed revision secrets (e.g. db migrations)
        - .spec.template.spec.initContainers[]?.env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.initContainers[]?.envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))

    # 2. StatefulSets
    - group: apps
      kind: StatefulSet
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
      jqPathExpressions:
        - .spec.template.spec.volumes[] | select(.secret.secretName != null and (.secret.secretName | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.containers[].envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.initContainers[]?.env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.initContainers[]?.envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))

    # 3. DaemonSets
    - group: apps
      kind: DaemonSet
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
      jqPathExpressions:
        - .spec.template.spec.volumes[] | select(.secret.secretName != null and (.secret.secretName | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.containers[].envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.initContainers[]?.env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.initContainers[]?.envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))

    # 4. Argo Rollouts (Blue/Green & Canary)
    - group: argoproj.io
      kind: Rollout
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
      jqPathExpressions:
        - .spec.template.spec.volumes[] | select(.secret.secretName != null and (.secret.secretName | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.containers[].envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.initContainers[]?.env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
        - .spec.template.spec.initContainers[]?.envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
```

---

## 🌐 System-Level (Global) Ignore Differences in `argocd-cm`

For cluster administrators managing hundreds of applications, configuring diffing rules globally in the `argocd-cm` ConfigMap applies DSO rules cluster-wide without modifying individual `Application` manifests:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-cm
  namespace: argocd
  labels:
    app.kubernetes.io/name: argocd-cm
    app.kubernetes.io/part-of: argocd
data:
  # 1. Inform Argo CD diffing engine that Rollouts use standard core/v1/PodSpec schemas
  resource.customizations.knownTypeFields.argoproj.io_Rollout: |
    - field: spec.template.spec
      type: core/v1/PodSpec

  # 2. Global ignore differences for Deployments
  resource.customizations.ignoreDifferences.apps_Deployment: |
    jsonPointers:
      - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
    jqPathExpressions:
      - .spec.template.spec.volumes[] | select(.secret.secretName != null and (.secret.secretName | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.containers[].envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.initContainers[]?.env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.initContainers[]?.envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))

  # 3. Global ignore differences for StatefulSets
  resource.customizations.ignoreDifferences.apps_StatefulSet: |
    jsonPointers:
      - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
    jqPathExpressions:
      - .spec.template.spec.volumes[] | select(.secret.secretName != null and (.secret.secretName | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.containers[].envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.initContainers[]?.env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.initContainers[]?.envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))

  # 4. Global ignore differences for DaemonSets
  resource.customizations.ignoreDifferences.apps_DaemonSet: |
    jsonPointers:
      - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
    jqPathExpressions:
      - .spec.template.spec.volumes[] | select(.secret.secretName != null and (.secret.secretName | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.containers[].envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.initContainers[]?.env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.initContainers[]?.envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))

  # 5. Global ignore differences for Argo Rollouts
  resource.customizations.ignoreDifferences.argoproj.io_Rollout: |
    jsonPointers:
      - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
    jqPathExpressions:
      - .spec.template.spec.volumes[] | select(.secret.secretName != null and (.secret.secretName | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.containers[].env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.containers[].envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.initContainers[]?.env[]? | select(.valueFrom.secretKeyRef.name != null and (.valueFrom.secretKeyRef.name | test("-rev-[0-9a-f]{12}$")))
      - .spec.template.spec.initContainers[]?.envFrom[]? | select(.secretRef.name != null and (.secretRef.name | test("-rev-[0-9a-f]{12}$")))
```

---

## 🔒 RBAC Requirements for Automatic Patching

If you choose **Option 2 (Automatic In-Cluster Patching)** via `ARGOCD_AUTOPATCH_ENABLED="true"`:

1. **Helm Installation:** The Helm chart automatically generates the required RBAC rules when `argoCD.autoPatch: true` is configured in `values.yaml`.
2. **Manual/Kustomize Installation:** Ensure the operator's `ClusterRole` (or namespace-scoped `Role` in the Argo CD namespace) grants the following permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: dso-argocd-autopatch
rules:
  - apiGroups:
      - argoproj.io
    resources:
      - applications
    verbs:
      - get
      - list
      - patch
      - update
```

---

## 🔍 Verification & Troubleshooting

### 1. Verify Ignored Differences in Argo CD CLI
To confirm Argo CD is successfully ignoring DSO runtime rotations:

```bash
# Check if the application reports "Synced" status even after secret rotation
argocd app get order-service-app

# Inspect differences - should return empty if all DSO mutations are properly ignored
argocd app diff order-service-app
```

### 2. Verify Workload In-Cluster Annotations
Ensure the workload has received the DSO revision annotation:

```bash
kubectl get deployment order-service -n production -o jsonpath='{.spec.template.metadata.annotations.dso\.quantumsys\.dev/revision}'
```

### 3. Check DSO Operator Logs for Argo CD Integration
When automatic patching is active, verify that DSO discovered and reconciled the parent `Application`:

```bash
kubectl logs -n dso-system deploy/dynamic-secret-operator | grep -i "argocd"
```
*Expected log output:*
```json
{"level":"info","ts":"2026-09-11T19:40:00Z","logger":"controllers.DynamicSecretPolicy","msg":"successfully updated Argo CD Application ignoreDifferences for DSO","integration":"argocd","application":"order-service-app","group":"apps","kind":"Deployment"}
```

---

## 🎯 Summary of Benefits

With this configuration:
* **True GitOps Governance:** Git remains the authoritative source of truth for deployments, pod specifications, CPU/memory limits, images, and non-secret volumes.
* **Zero Self-Heal Interference:** DSO safely validates secrets via isolated canary sandboxes and rotates running workloads in-cluster without Argo CD triggering rolling undo operations.
* **No Blind Spots:** Fine-grained regex JQ path expressions (`test("-rev-[0-9a-f]{12}$")`) isolate only DSO-generated secrets, ensuring changes to ConfigMaps, PersistentVolumeClaims, and other application volumes committed to Git continue to be detected and reconciled immediately.
* **Support for Enterprise Scenarios:** Works across standard Deployments, StatefulSets, DaemonSets, and advanced progressive delivery with Argo Rollouts.
