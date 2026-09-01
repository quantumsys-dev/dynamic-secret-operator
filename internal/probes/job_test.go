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

package probes

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

func TestInjectRevisionSecretEnv(t *testing.T) {
	spec := &corev1.PodSpec{
		InitContainers: []corev1.Container{
			{
				Name:  "init-setup",
				Image: "busybox:latest",
				Env: []corev1.EnvVar{
					{Name: "CUSTOM_VAR", Value: "custom-val"},
				},
			},
		},
		Containers: []corev1.Container{
			{
				Name:  "validator",
				Image: "redis:alpine",
				Env: []corev1.EnvVar{
					{Name: "EXISTING_KEY", Value: "existing-val"},
					{Name: EnvRevisionSecretName, Value: "stale-revision"},
				},
			},
		},
	}

	revisionSecretName := "payments-db-credentials-rev-5"
	injectRevisionSecretEnv(spec, revisionSecretName)

	// Verify init container has injected env var
	initFound := false
	for _, env := range spec.InitContainers[0].Env {
		if env.Name == EnvRevisionSecretName {
			initFound = true
			if env.Value != revisionSecretName {
				t.Errorf("expected initContainer %s=%s, got %s", EnvRevisionSecretName, revisionSecretName, env.Value)
			}
		}
	}
	if !initFound {
		t.Errorf("expected %s to be injected into initContainer", EnvRevisionSecretName)
	}

	// Verify container had stale value replaced and existing env preserved
	containerFound := false
	customFound := false
	for _, env := range spec.Containers[0].Env {
		if env.Name == EnvRevisionSecretName {
			containerFound = true
			if env.Value != revisionSecretName {
				t.Errorf("expected container %s=%s, got %s", EnvRevisionSecretName, revisionSecretName, env.Value)
			}
		}
		if env.Name == "EXISTING_KEY" && env.Value == "existing-val" {
			customFound = true
		}
	}
	if !containerFound {
		t.Errorf("expected %s to be injected into container", EnvRevisionSecretName)
	}
	if !customFound {
		t.Errorf("expected EXISTING_KEY to be preserved in container")
	}
}

func TestBuildProbeJob(t *testing.T) {
	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redis-policy",
			Namespace: "prod",
			UID:       "uid-12345",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "payment-api",
			},
			VaultRef: secretv1alpha1.VaultReference{
				ObjectName: "redis-secret",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "3",
		},
	}

	jobProbeSpec := &secretv1alpha1.JobProbeSpec{
		JobTemplate: batchv1.JobTemplateSpec{
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						RestartPolicy: corev1.RestartPolicyNever,
						Containers: []corev1.Container{
							{
								Name:  "probe",
								Image: "redis:alpine",
								Command: []string{
									"sh", "-c", "echo $(DSO_REVISION_SECRET_NAME)",
								},
							},
						},
					},
				},
			},
		},
	}

	revisionSecretName := "payment-api-redis-secret-rev-3"
	job := buildProbeJob(policy, jobProbeSpec, revisionSecretName)

	if job == nil {
		t.Fatalf("expected non-nil Job")
	}
	if job.Namespace != "prod" {
		t.Errorf("expected namespace prod, got %s", job.Namespace)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("expected backoffLimit 0, got %v", job.Spec.BackoffLimit)
	}
	if len(job.OwnerReferences) == 0 || job.OwnerReferences[0].Name != "redis-policy" {
		t.Errorf("expected ownerReference to redis-policy")
	}

	// Verify env var injection
	injected := false
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == EnvRevisionSecretName && env.Value == revisionSecretName {
			injected = true
			break
		}
	}
	if !injected {
		t.Errorf("expected %s=%s in job container env", EnvRevisionSecretName, revisionSecretName)
	}
}
