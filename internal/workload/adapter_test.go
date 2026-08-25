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
	"testing"

	argorolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = appsv1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = secretv1alpha1.AddToScheme(s)
	_ = argorolloutsv1alpha1.AddToScheme(s)
	return s
}

func testPodTemplate(targetName, oldSecretName string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"app": targetName},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "nginx:alpine",
					Env: []corev1.EnvVar{
						{
							Name: "DB_PASS",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: oldSecretName,
									},
									Key: "database-password",
								},
							},
						},
					},
					EnvFrom: []corev1.EnvFromSource{
						{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: oldSecretName,
								},
							},
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "secret-vol",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: oldSecretName,
						},
					},
				},
			},
		},
	}
}

func TestNewAdapter(t *testing.T) {
	tests := []struct {
		kind        string
		expectKind  string
		expectError bool
	}{
		{kind: "Deployment", expectKind: KindDeployment, expectError: false},
		{kind: "", expectKind: KindDeployment, expectError: false},
		{kind: "StatefulSet", expectKind: KindStatefulSet, expectError: false},
		{kind: "DaemonSet", expectKind: KindDaemonSet, expectError: false},
		{kind: "Rollout", expectKind: KindRollout, expectError: false},
		{kind: "Job", expectError: true},
		{kind: "CronJob", expectError: true},
	}

	for _, tt := range tests {
		adapter, err := NewAdapter(tt.kind)
		if tt.expectError {
			if err == nil {
				t.Errorf("expected error for kind %q, got nil", tt.kind)
			}
		} else {
			if err != nil {
				t.Errorf("unexpected error for kind %q: %v", tt.kind, err)
			}
			if adapter.Kind() != tt.expectKind {
				t.Errorf("expected kind %q, got %q", tt.expectKind, adapter.Kind())
			}
		}
	}
}

func TestDeploymentAdapter(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-app",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: testPodTemplate("web-app", "web-app-secret"),
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deploy).Build()
	adapter := NewDeploymentAdapter()

	// Fetch
	if err := adapter.Fetch(ctx, client, types.NamespacedName{Name: "web-app", Namespace: "default"}); err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "web-policy", Namespace: "default"},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{ObjectName: "database-password"},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "rev123",
		},
	}

	// BuildCanary
	canary := adapter.BuildCanary(policy, "web-app-rev-rev123")
	if canary.Name != "web-app-canary" {
		t.Errorf("expected canary name 'web-app-canary', got %q", canary.Name)
	}
	if canary.Spec.Template.Spec.Volumes[0].Secret.SecretName != "web-app-rev-rev123" {
		t.Errorf("expected canary volume secret 'web-app-rev-rev123', got %q",
			canary.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}

	// Promote
	if err := adapter.Promote(ctx, client, policy, "web-app-rev-rev123"); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	updated := &appsv1.Deployment{}
	if err := client.Get(ctx, types.NamespacedName{Name: "web-app", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get updated deployment: %v", err)
	}
	if updated.Spec.Template.Spec.Volumes[0].Secret.SecretName != "web-app-rev-rev123" {
		t.Errorf("expected promoted volume secret 'web-app-rev-rev123', got %q",
			updated.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
}

func TestStatefulSetAdapter(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "db-cluster",
			Namespace: "default",
		},
		Spec: appsv1.StatefulSetSpec{
			Template: testPodTemplate("db-cluster", "db-cluster-secret"),
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sts).Build()
	adapter := NewStatefulSetAdapter()

	// Fetch
	if err := adapter.Fetch(ctx, client, types.NamespacedName{Name: "db-cluster", Namespace: "default"}); err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if adapter.Kind() != KindStatefulSet {
		t.Errorf("expected kind StatefulSet, got %s", adapter.Kind())
	}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "db-policy", Namespace: "default"},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{ObjectName: "database-password"},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "rev999",
		},
	}

	// BuildCanary produces an isolated 1-replica Deployment derived from StatefulSet template
	canary := adapter.BuildCanary(policy, "db-cluster-rev-rev999")
	if canary.Name != "db-cluster-canary" {
		t.Errorf("expected canary name 'db-cluster-canary', got %q", canary.Name)
	}
	if canary.Spec.Template.Spec.Volumes[0].Secret.SecretName != "db-cluster-rev-rev999" {
		t.Errorf("expected canary volume secret 'db-cluster-rev-rev999', got %q",
			canary.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}

	// Promote
	if err := adapter.Promote(ctx, client, policy, "db-cluster-rev-rev999"); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	updated := &appsv1.StatefulSet{}
	if err := client.Get(ctx, types.NamespacedName{Name: "db-cluster", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get updated statefulset: %v", err)
	}
	if updated.Spec.Template.Spec.Volumes[0].Secret.SecretName != "db-cluster-rev-rev999" {
		t.Errorf("expected promoted volume secret 'db-cluster-rev-rev999', got %q",
			updated.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
}

func TestDaemonSetAdapter(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-agent",
			Namespace: "default",
		},
		Spec: appsv1.DaemonSetSpec{
			Template: testPodTemplate("node-agent", "node-agent-secret"),
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ds).Build()
	adapter := NewDaemonSetAdapter()

	// Fetch
	if err := adapter.Fetch(ctx, client, types.NamespacedName{Name: "node-agent", Namespace: "default"}); err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if adapter.Kind() != KindDaemonSet {
		t.Errorf("expected kind DaemonSet, got %s", adapter.Kind())
	}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-policy", Namespace: "default"},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{ObjectName: "database-password"},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "rev555",
		},
	}

	// BuildCanary produces an isolated 1-replica Deployment derived from DaemonSet template
	canary := adapter.BuildCanary(policy, "node-agent-rev-rev555")
	if canary.Name != "node-agent-canary" {
		t.Errorf("expected canary name 'node-agent-canary', got %q", canary.Name)
	}
	if canary.Spec.Template.Spec.Volumes[0].Secret.SecretName != "node-agent-rev-rev555" {
		t.Errorf("expected canary volume secret 'node-agent-rev-rev555', got %q",
			canary.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}

	// Promote
	if err := adapter.Promote(ctx, client, policy, "node-agent-rev-rev555"); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	updated := &appsv1.DaemonSet{}
	if err := client.Get(ctx, types.NamespacedName{Name: "node-agent", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get updated daemonset: %v", err)
	}
	if updated.Spec.Template.Spec.Volumes[0].Secret.SecretName != "node-agent-rev-rev555" {
		t.Errorf("expected promoted volume secret 'node-agent-rev-rev555', got %q",
			updated.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
}

func TestRolloutAdapter(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	rollout := &argorolloutsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "canary-service",
			Namespace: "default",
		},
		Spec: argorolloutsv1alpha1.RolloutSpec{
			Template: testPodTemplate("canary-service", "canary-service-secret"),
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout).Build()
	adapter := NewRolloutAdapter()

	// Fetch
	if err := adapter.Fetch(ctx, client, types.NamespacedName{Name: "canary-service", Namespace: "default"}); err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if adapter.Kind() != KindRollout {
		t.Errorf("expected kind Rollout, got %s", adapter.Kind())
	}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "canary-policy", Namespace: "default"},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{ObjectName: "database-password"},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "rev777",
		},
	}

	// BuildCanary produces an isolated 1-replica Deployment derived from Rollout template
	canary := adapter.BuildCanary(policy, "canary-service-rev-rev777")
	if canary.Name != "canary-service-canary" {
		t.Errorf("expected canary name 'canary-service-canary', got %q", canary.Name)
	}
	if canary.Spec.Template.Spec.Volumes[0].Secret.SecretName != "canary-service-rev-rev777" {
		t.Errorf("expected canary volume secret 'canary-service-rev-rev777', got %q",
			canary.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}

	// Promote
	if err := adapter.Promote(ctx, client, policy, "canary-service-rev-rev777"); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	updated := &argorolloutsv1alpha1.Rollout{}
	if err := client.Get(ctx, types.NamespacedName{Name: "canary-service", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get updated rollout: %v", err)
	}
	if updated.Spec.Template.Spec.Volumes[0].Secret.SecretName != "canary-service-rev-rev777" {
		t.Errorf("expected promoted volume secret 'canary-service-rev-rev777', got %q",
			updated.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
}
