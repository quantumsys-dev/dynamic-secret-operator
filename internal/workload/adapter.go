/*
Copyright 2026 QuantumSys.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workload

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/canary"
)

// Supported workload kinds.
const (
	KindDeployment  = "Deployment"
	KindStatefulSet = "StatefulSet"
	KindDaemonSet   = "DaemonSet"
	KindRollout     = "Rollout"
)

// WorkloadAdapter defines the polymorphic interface for managing different Kubernetes
// workload kinds (Deployment, StatefulSet, DaemonSet) during secret rotation.
type WorkloadAdapter interface {
	// Kind returns the workload resource kind.
	Kind() string
	// Fetch retrieves the target workload from the Kubernetes API server.
	Fetch(ctx context.Context, c client.Client, key types.NamespacedName) error
	// BuildCanary constructs an isolated 1-replica canary Deployment derived from the target workload.
	BuildCanary(policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) *appsv1.Deployment
	// Promote applies the new secret revision to the target production workload.
	Promote(ctx context.Context, c client.Client, policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) error
	// TargetObject returns the underlying client.Object for owner references or status tracking.
	TargetObject() client.Object
}

// NewAdapter is a factory method resolving the appropriate WorkloadAdapter based on kind string.
func NewAdapter(kind string) (WorkloadAdapter, error) {
	switch kind {
	case KindDeployment, "":
		return NewDeploymentAdapter(), nil
	case KindStatefulSet:
		return NewStatefulSetAdapter(), nil
	case KindDaemonSet:
		return NewDaemonSetAdapter(), nil
	case KindRollout:
		return NewRolloutAdapter(), nil
	default:
		return nil, fmt.Errorf("unsupported workload kind %q: supported kinds are %s, %s, %s, %s",
			kind, KindDeployment, KindStatefulSet, KindDaemonSet, KindRollout)
	}
}

// MutatePodTemplateSpec updates annotations, volume mounts, and container environment variables
// referencing operator-managed secrets inside any PodTemplateSpec.
func MutatePodTemplateSpec(
	tpl *corev1.PodTemplateSpec,
	targetName string,
	policy *secretv1alpha1.DynamicSecretPolicy,
	newSecretName string,
) {
	managedPrefix := fmt.Sprintf("%s-rev-", targetName)

	if tpl.Annotations == nil {
		tpl.Annotations = make(map[string]string)
	}
	tpl.Annotations[canary.LabelRevision] = policy.Status.DesiredRevision

	// Update only volume mounts referencing operator-managed secrets
	for i := range tpl.Spec.Volumes {
		vol := &tpl.Spec.Volumes[i]
		if vol.Secret != nil {
			if strings.HasPrefix(vol.Secret.SecretName, managedPrefix) ||
				(policy.Status.CurrentRevision == "" && !strings.HasPrefix(vol.Secret.SecretName, managedPrefix)) ||
				vol.Secret.SecretName == fmt.Sprintf("%s-secret", targetName) {
				vol.Secret.SecretName = newSecretName
			}
		}
	}

	mutateContainers := func(containers []corev1.Container) {
		for cIdx := range containers {
			container := &containers[cIdx]

			for eIdx := range container.Env {
				envVar := &container.Env[eIdx]
				if envVar.ValueFrom != nil && envVar.ValueFrom.SecretKeyRef != nil {
					ref := envVar.ValueFrom.SecretKeyRef
					if strings.HasPrefix(ref.Name, managedPrefix) ||
						ref.Key == policy.Spec.VaultRef.ObjectName ||
						(policy.Status.CurrentRevision != "" && ref.Name == fmt.Sprintf("%s-rev-%s", targetName, policy.Status.CurrentRevision)) {
						ref.Name = newSecretName
					}
				}
			}

			for efIdx := range container.EnvFrom {
				envFrom := &container.EnvFrom[efIdx]
				if envFrom.SecretRef != nil {
					if strings.HasPrefix(envFrom.SecretRef.Name, managedPrefix) ||
						(policy.Status.CurrentRevision == "" && !strings.HasPrefix(envFrom.SecretRef.Name, managedPrefix)) ||
						(policy.Status.CurrentRevision != "" && envFrom.SecretRef.Name == fmt.Sprintf("%s-rev-%s", targetName, policy.Status.CurrentRevision)) {
						envFrom.SecretRef.Name = newSecretName
					}
				}
			}
		}
	}

	mutateContainers(tpl.Spec.Containers)
	mutateContainers(tpl.Spec.InitContainers)
}
