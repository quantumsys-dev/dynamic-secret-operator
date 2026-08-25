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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

// LabelRevision is the standard label attached to revision secrets.
const LabelRevision = "dso.quantumsys.dev/revision"

// BuildCanaryFromTemplate constructs an isolated 1-replica canary Deployment cloned from
// any workload's PodTemplateSpec (Deployment, StatefulSet, DaemonSet), mounting the newly
// materialized secret revision and isolating traffic.
func BuildCanaryFromTemplate(
	targetName string,
	podTemplate *corev1.PodTemplateSpec,
	policy *secretv1alpha1.DynamicSecretPolicy,
	newSecretName string,
) *appsv1.Deployment {
	canaryName := fmt.Sprintf("%s-canary", targetName)
	objectName := policy.Spec.VaultRef.ObjectName
	managedPrefix := fmt.Sprintf("%s-%s-rev-", targetName, objectName)
	legacyPrefix := fmt.Sprintf("%s-rev-", targetName)
	canaryTemplate := podTemplate.DeepCopy()

	// 1. Force single canary replica for ephemeral testing
	replicas := int32(1)

	// 2. Strictly overwrite labels and Pod template selector with isolated canary labels
	// to prevent production Kubernetes Services from routing live user traffic to the canary pod.
	canaryLabels := map[string]string{
		LabelCanary:         "true",
		LabelTargetWorkload: targetName,
	}

	canaryTemplate.Labels = map[string]string{
		LabelCanary:         "true",
		LabelTargetWorkload: targetName,
	}

	if canaryTemplate.Annotations == nil {
		canaryTemplate.Annotations = make(map[string]string)
	}
	canaryTemplate.Annotations[LabelRevision] = policy.Status.DesiredRevision

	var targetVolumeName string
	var targetEnvName string
	var targetContainerName string
	if policy.Spec.TargetRef != nil {
		targetVolumeName = policy.Spec.TargetRef.VolumeName
		targetEnvName = policy.Spec.TargetRef.EnvName
		targetContainerName = policy.Spec.TargetRef.ContainerName
	}

	// 3. Update volume mounts referencing operator-managed secrets
	if targetVolumeName != "" {
		for i := range canaryTemplate.Spec.Volumes {
			vol := &canaryTemplate.Spec.Volumes[i]
			if vol.Name == targetVolumeName {
				if vol.Secret == nil {
					vol.Secret = &corev1.SecretVolumeSource{}
				}
				vol.Secret.SecretName = newSecretName
			}
		}
	} else if targetEnvName == "" {
		for i := range canaryTemplate.Spec.Volumes {
			vol := &canaryTemplate.Spec.Volumes[i]
			if vol.Secret != nil {
				sName := vol.Secret.SecretName
				if strings.HasPrefix(sName, managedPrefix) ||
					sName == objectName ||
					sName == fmt.Sprintf("%s-%s", targetName, objectName) ||
					sName == fmt.Sprintf("%s-%s-secret", targetName, objectName) ||
					sName == fmt.Sprintf("%s-secret", targetName) ||
					vol.Name == objectName ||
					vol.Name == fmt.Sprintf("%s-volume", objectName) ||
					(strings.HasPrefix(sName, legacyPrefix) && (policy.Spec.TargetRef == nil || policy.Spec.TargetRef.VolumeName == "")) {
					vol.Secret.SecretName = newSecretName
				}
			}
		}
	}

	// 4. Update container environment secret references (targeted replacement)
	mutateContainers := func(containers []corev1.Container) {
		for cIdx := range containers {
			container := &containers[cIdx]

			if targetContainerName != "" && container.Name != targetContainerName {
				continue
			}

			if targetEnvName != "" {
				found := false
				for eIdx := range container.Env {
					envVar := &container.Env[eIdx]
					if envVar.Name == targetEnvName {
						found = true
						envVar.Value = ""
						envVar.ValueFrom = &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: newSecretName,
								},
								Key: objectName,
							},
						}
					}
				}
				if !found && (targetContainerName == "" || container.Name == targetContainerName) {
					container.Env = append(container.Env, corev1.EnvVar{
						Name: targetEnvName,
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: newSecretName,
								},
								Key: objectName,
							},
						},
					})
				}
			} else {
				for eIdx := range container.Env {
					envVar := &container.Env[eIdx]
					if envVar.ValueFrom != nil && envVar.ValueFrom.SecretKeyRef != nil {
						ref := envVar.ValueFrom.SecretKeyRef
						if strings.HasPrefix(ref.Name, managedPrefix) ||
							ref.Key == objectName ||
							ref.Name == objectName ||
							ref.Name == fmt.Sprintf("%s-%s", targetName, objectName) ||
							(policy.Status.CurrentRevision != "" && ref.Name == fmt.Sprintf("%s-%s-rev-%s", targetName, objectName, policy.Status.CurrentRevision)) ||
							(policy.Status.CurrentRevision != "" && ref.Name == fmt.Sprintf("%s-rev-%s", targetName, policy.Status.CurrentRevision)) {
							ref.Name = newSecretName
						}
					}
				}

				for efIdx := range container.EnvFrom {
					envFrom := &container.EnvFrom[efIdx]
					if envFrom.SecretRef != nil {
						if strings.HasPrefix(envFrom.SecretRef.Name, managedPrefix) ||
							envFrom.SecretRef.Name == objectName ||
							envFrom.SecretRef.Name == fmt.Sprintf("%s-%s", targetName, objectName) ||
							(policy.Status.CurrentRevision != "" && envFrom.SecretRef.Name == fmt.Sprintf("%s-%s-rev-%s", targetName, objectName, policy.Status.CurrentRevision)) ||
							(policy.Status.CurrentRevision != "" && envFrom.SecretRef.Name == fmt.Sprintf("%s-rev-%s", targetName, policy.Status.CurrentRevision)) {
							envFrom.SecretRef.Name = newSecretName
						}
					}
				}
			}
		}
	}

	mutateContainers(canaryTemplate.Spec.Containers)
	mutateContainers(canaryTemplate.Spec.InitContainers)

	canaryDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      canaryName,
			Namespace: policy.Namespace,
			Labels:    canaryLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: canaryLabels,
			},
			Template: *canaryTemplate,
		},
	}

	return canaryDeploy
}

// BuildCanaryDeployment constructs an isolated 1-replica canary Deployment cloned from the
// target production deployment, mounting the newly materialized secret revision.
func BuildCanaryDeployment(targetDeploy *appsv1.Deployment, policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) *appsv1.Deployment {
	return BuildCanaryFromTemplate(targetDeploy.Name, &targetDeploy.Spec.Template, policy, newSecretName)
}
