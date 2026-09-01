# GitOps Integration: Managing Argo CD Drift with DSO

When managing Kubernetes workloads with **Argo CD** (specifically with `selfHeal: true` and `automated: prune` enabled), Argo CD continuously reconciles the live cluster state against the Git repository.

Because the **Dynamic Secret Operator (DSO)** updates secret volumes, environment variables, and revision annotations directly in-cluster upon secret rotation, Argo CD can detect these mutations as "drift" and immediately revert them back to the Git state. This creates an **infinite reconciliation loop** (DSO updates -> Argo CD reverts -> Pod restarts).

This guide explains how to configure `ignoreDifferences` in your Argo CD `Application` manifests to allow DSO to manage runtime secret rotation seamlessly while retaining full GitOps governance for application code, replicas, and infrastructure specs.

---

## ⚙️ Automatic vs Manual Drift Management

> [!WARNING]
> ### 🛑 Critical: App-of-Apps & Strict Declarative GitOps Notice
> If your organization uses the **Argo CD App-of-Apps pattern** or **ApplicationSets** (where the `Application` CRD manifests themselves are committed to Git and continuously reconciled by a root Argo CD application), **DO NOT enable automatic in-cluster patching**.
>
> If `ARGOCD_AUTOPATCH_ENABLED="true"` is enabled in an App-of-Apps environment, DSO's in-cluster patches to the parent `Application` CR will be immediately detected by the root App-of-Apps controller as external drift. Argo CD will revert the `Application` CR back to its Git definition, resulting in an infinite patch-revert reconciliation war between DSO and Argo CD.
>
> **Best Practice for App-of-Apps:**
> Keep `ARGOCD_AUTOPATCH_ENABLED="false"` (the default). Commit the `ignoreDifferences` or fine-grained `jqPathExpressions` blocks directly into your Git repository's `Application` YAML manifests (or define them globally in the `argocd-cm` ConfigMap).

DSO provides two methods to manage Argo CD diffing:

1. **Declarative GitOps Manifests (Default & Recommended for App-of-Apps):**
   Explicitly declare `ignoreDifferences` or fine-grained `jqPathExpressions` in your Git repository's `Application` manifest as detailed below. This prevents any out-of-band cluster mutation drift.

2. **Automatic In-Cluster Patching (`ARGOCD_AUTOPATCH_ENABLED="true"`, Opt-In):**
   When explicitly enabled in standalone Application environments, the operator discovers the parent Argo CD `Application` via tracking labels (`app.kubernetes.io/instance` or `argocd.argoproj.io/tracking-id`) and automatically injects standard JSON Pointers (`/spec/template/metadata/annotations/dso.quantumsys.dev~1revision` and `/spec/template/spec/volumes`) into the Application's `spec.ignoreDifferences`.
   > ⚠️ **Scope Notice:** Automatic patching ignores the `/spec/template/spec/volumes` array as a whole. If your application relies on GitOps diff enforcement for non-DSO volumes (e.g. ConfigMaps or PVCs) on the same Pod, define fine-grained `jqPathExpressions` manually.

---

## The Solution: `ignoreDifferences` Configuration

Argo CD provides the [`ignoreDifferences`](https://argo-cd.readthedocs.io/en/stable/user-guide/diffing/) feature to instruct the diffing engine to ignore specific fields mutated by in-cluster controllers.

### Complete Argo CD `Application` Example

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

  # Enable automated sync with Self-Heal
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true

  # Instruct Argo CD to ignore DSO-managed mutations
  ignoreDifferences:
    # 1. Ignore DSO Revision Annotations on Pod Templates
    - group: apps
      kind: Deployment
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
        - /spec/template/spec/volumes

    # 2. Support for StatefulSets
    - group: apps
      kind: StatefulSet
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
        - /spec/template/spec/volumes

    # 3. Support for DaemonSets
    - group: apps
      kind: DaemonSet
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
        - /spec/template/spec/volumes

    # 4. Support for Argo Rollouts
    - group: argoproj.io
      kind: Rollout
      jsonPointers:
        - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
        - /spec/template/spec/volumes
```

---

## Fine-Grained JQ Path Ignoring (Recommended for Multi-Volume Pods)

If your workloads mount multiple volumes (e.g., config maps, persistent volumes, TLS certificates) and you only want to ignore the specific operator-managed secret volumes without ignoring all volumes, use **JQ Path Expressions**:

```yaml
spec:
  ignoreDifferences:
    # Ignore DSO secret revision annotations across all supported workloads
    - group: apps
      kind: Deployment
      jqPathExpressions:
        - .spec.template.metadata.annotations["dso.quantumsys.dev/revision"]
        - .spec.template.spec.volumes[] | select(.secret.secretName | startswith("order-service-rev-"))
        - .spec.template.spec.containers[].env[] | select(.valueFrom.secretKeyRef.name | startswith("order-service-rev-"))
        - .spec.template.spec.containers[].envFrom[] | select(.secretRef.name | startswith("order-service-rev-"))

    - group: argoproj.io
      kind: Rollout
      jqPathExpressions:
        - .spec.template.metadata.annotations["dso.quantumsys.dev/revision"]
        - .spec.template.spec.volumes[] | select(.secret.secretName | startswith("order-service-rev-"))
        - .spec.template.spec.containers[].env[] | select(.valueFrom.secretKeyRef.name | startswith("order-service-rev-"))
        - .spec.template.spec.containers[].envFrom[] | select(.secretRef.name | startswith("order-service-rev-"))
```

---

## System-Level (Global) Ignore Differences

For cluster administrators managing hundreds of applications, you can configure these diffing rules globally in the `argocd-cm` ConfigMap instead of modifying every individual `Application` CR:

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
  resource.customizations.ignoreDifferences.apps_Deployment: |
    jsonPointers:
      - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision

  resource.customizations.ignoreDifferences.apps_StatefulSet: |
    jsonPointers:
      - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision

  resource.customizations.ignoreDifferences.apps_DaemonSet: |
    jsonPointers:
      - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision

  resource.customizations.ignoreDifferences.argoproj.io_Rollout: |
    jsonPointers:
      - /spec/template/metadata/annotations/dso.quantumsys.dev~1revision
```

---

## Summary of Benefits

With this configuration:
* **GitOps Compliance:** Git remains the single source of truth for application versions, images, environment configs, replicas, and policies.
* **Zero-Downtime Secret Rotation:** DSO safely validates secrets via canary delivery and updates running workloads in-cluster without interference from Argo CD Self-Heal.
* **Deterministic Rollback:** In the event of an upstream secret failure, DSO's circuit breaker halts rollout and retains the last known good revision without triggering Git sync conflicts.
