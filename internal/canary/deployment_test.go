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
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

func TestBuildCanaryDeployment(t *testing.T) {
	replicas := int32(3)
	targetDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "payment-service",
			Namespace:       "prod",
			ResourceVersion: "12345",
			UID:             "abc-123",
			Labels: map[string]string{
				"app":  "payment-service",
				"tier": "backend",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":  "payment-service",
					"tier": "backend",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":  "payment-service",
						"tier": "backend",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "payment:2.0.0",
							Env: []corev1.EnvVar{
								{
									Name: "DB_PASS",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "payment-service-rev-old",
											},
											Key: "db-pass",
										},
									},
								},
								{
									Name: "DATADOG_KEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "datadog-secret",
											},
											Key: "api-key",
										},
									},
								},
							},
							EnvFrom: []corev1.EnvFromSource{
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "payment-service-rev-old",
										},
									},
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "managed-vol",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "payment-service-rev-old",
								},
							},
						},
						{
							Name: "tls-vol",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "tls-secret",
								},
							},
						},
					},
				},
			},
		},
	}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payment-policy",
			Namespace: "prod",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				ObjectName: "db-pass",
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Name: "payment-service",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "newrev1234",
			CurrentRevision: "old",
		},
	}

	newSecretName := "payment-service-rev-newrev1234"
	canaryDeploy := BuildCanaryDeployment(targetDeploy, policy, newSecretName)

	if canaryDeploy.Name != "payment-service-canary" {
		t.Errorf("expected canary name 'payment-service-canary', got %q", canaryDeploy.Name)
	}
	if canaryDeploy.Namespace != "prod" {
		t.Errorf("expected canary namespace 'prod', got %q", canaryDeploy.Namespace)
	}
	if canaryDeploy.ResourceVersion != "" || canaryDeploy.UID != "" {
		t.Errorf("expected metadata UID and ResourceVersion to be cleared for creation")
	}
	if *canaryDeploy.Spec.Replicas != 1 {
		t.Errorf("expected canary replicas to be forced to 1, got %d", *canaryDeploy.Spec.Replicas)
	}
	if canaryDeploy.Labels[LabelCanary] != "true" || canaryDeploy.Labels[LabelTargetWorkload] != "payment-service" {
		t.Errorf("expected canary labels, got %v", canaryDeploy.Labels)
	}

	// Assert Pod Template labels are strictly isolated (no inherited production app labels)
	tplLabels := canaryDeploy.Spec.Template.Labels
	if tplLabels[LabelCanary] != "true" || tplLabels[LabelTargetWorkload] != "payment-service" {
		t.Errorf("expected canary pod template labels, got %v", tplLabels)
	}
	if _, exists := tplLabels["app"]; exists {
		t.Errorf("CRITICAL SECURITY / TRAFFIC BLEED BUG: inherited production label 'app' found on canary pod template: %v", tplLabels)
	}
	if _, exists := tplLabels["tier"]; exists {
		t.Errorf("CRITICAL SECURITY / TRAFFIC BLEED BUG: inherited production label 'tier' found on canary pod template: %v", tplLabels)
	}

	if canaryDeploy.Spec.Template.Annotations[LabelRevision] != "newrev1234" {
		t.Errorf("expected revision annotation 'newrev1234', got %q", canaryDeploy.Spec.Template.Annotations[LabelRevision])
	}

	// Verify targeted secret replacement in canary
	c := canaryDeploy.Spec.Template.Spec.Containers[0]
	if c.Env[0].ValueFrom.SecretKeyRef.Name != newSecretName {
		t.Errorf("expected DB_PASS secret name %q, got %q", newSecretName, c.Env[0].ValueFrom.SecretKeyRef.Name)
	}
	if c.Env[1].ValueFrom.SecretKeyRef.Name != "datadog-secret" {
		t.Errorf("expected unmanaged DATADOG_KEY secret to remain 'datadog-secret', got %q", c.Env[1].ValueFrom.SecretKeyRef.Name)
	}
	if c.EnvFrom[0].SecretRef.Name != newSecretName {
		t.Errorf("expected EnvFrom secret %q, got %q", newSecretName, c.EnvFrom[0].SecretRef.Name)
	}

	vols := canaryDeploy.Spec.Template.Spec.Volumes
	if vols[0].Secret.SecretName != newSecretName {
		t.Errorf("expected managed volume secret %q, got %q", newSecretName, vols[0].Secret.SecretName)
	}
	if vols[1].Secret.SecretName != "tls-secret" {
		t.Errorf("expected unmanaged TLS volume secret to remain 'tls-secret', got %q", vols[1].Secret.SecretName)
	}
}
