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

package canary

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

// LabelRevision is the standard label attached to revision secrets.
const LabelRevision = "dso.quantumsys.dev/revision"

// BuildCanaryDeployment constructs an isolated 1-replica canary Deployment cloned from the
// target production deployment, mounting the newly materialized secret revision.
func BuildCanaryDeployment(targetDeploy *appsv1.Deployment, policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) *appsv1.Deployment {
	targetName := targetDeploy.Name
	canaryName := fmt.Sprintf("%s-canary", targetName)
	managedPrefix := fmt.Sprintf("%s-rev-", targetName)

	canaryDeploy := targetDeploy.DeepCopy()
	canaryDeploy.ResourceVersion = ""
	canaryDeploy.UID = ""
	canaryDeploy.Name = canaryName
	canaryDeploy.Namespace = policy.Namespace

	// 1. Force single canary replica for ephemeral testing
	replicas := int32(1)
	canaryDeploy.Spec.Replicas = &replicas

	// 2. Strictly overwrite labels and Pod template selector with isolated canary labels
	// to prevent production Kubernetes Services from routing live user traffic to the canary pod.
	canaryLabels := map[string]string{
		LabelCanary:         "true",
		LabelTargetWorkload: targetName,
	}

	canaryDeploy.Labels = canaryLabels
	canaryDeploy.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: canaryLabels,
	}
	canaryDeploy.Spec.Template.Labels = map[string]string{
		LabelCanary:         "true",
		LabelTargetWorkload: targetName,
	}

	if canaryDeploy.Spec.Template.Annotations == nil {
		canaryDeploy.Spec.Template.Annotations = make(map[string]string)
	}
	canaryDeploy.Spec.Template.Annotations[LabelRevision] = policy.Status.DesiredRevision

	// 3. Update volume mounts referencing operator-managed secrets
	for i := range canaryDeploy.Spec.Template.Spec.Volumes {
		vol := &canaryDeploy.Spec.Template.Spec.Volumes[i]
		if vol.Secret != nil {
			if strings.HasPrefix(vol.Secret.SecretName, managedPrefix) ||
				(policy.Status.CurrentRevision == "" && vol.Name == policy.Spec.VaultRef.ObjectName) {
				vol.Secret.SecretName = newSecretName
			}
		}
	}

	// 4. Update container environment secret references (targeted replacement)
	for cIdx := range canaryDeploy.Spec.Template.Spec.Containers {
		container := &canaryDeploy.Spec.Template.Spec.Containers[cIdx]

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
					(policy.Status.CurrentRevision == "" && envFrom.SecretRef.Name == policy.Spec.VaultRef.ObjectName) ||
					(policy.Status.CurrentRevision != "" && envFrom.SecretRef.Name == fmt.Sprintf("%s-rev-%s", targetName, policy.Status.CurrentRevision)) {
					envFrom.SecretRef.Name = newSecretName
				}
			}
		}
	}

	return canaryDeploy
}
