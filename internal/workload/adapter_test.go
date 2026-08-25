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
		{kind: "CronJob", expectKind: "", expectError: true},
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

func TestDeploymentAdapter_Lifecycle(t *testing.T) {
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
	if adapter.Kind() != KindDeployment {
		t.Errorf("expected kind Deployment, got %s", adapter.Kind())
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
	canary := adapter.BuildCanary(policy, "web-app-database-password-rev-rev123")
	if canary.Name != "web-app-canary" {
		t.Errorf("expected canary name 'web-app-canary', got %q", canary.Name)
	}
	if canary.Spec.Template.Spec.Volumes[0].Secret.SecretName != "web-app-database-password-rev-rev123" {
		t.Errorf("expected canary volume secret 'web-app-database-password-rev-rev123', got %q",
			canary.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}

	// Promote
	if err := adapter.Promote(ctx, client, policy, "web-app-database-password-rev-rev123"); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	updated := &appsv1.Deployment{}
	if err := client.Get(ctx, types.NamespacedName{Name: "web-app", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get updated deployment: %v", err)
	}
	if updated.Spec.Template.Spec.Volumes[0].Secret.SecretName != "web-app-database-password-rev-rev123" {
		t.Errorf("expected promoted volume secret 'web-app-database-password-rev-rev123', got %q",
			updated.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
}

func TestStatefulSetAdapter_Lifecycle(t *testing.T) {
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

	// BuildCanary produces an ephemeral 1-replica Deployment without PVCs
	canary := adapter.BuildCanary(policy, "db-cluster-database-password-rev-rev999")
	if canary.Name != "db-cluster-canary" {
		t.Errorf("expected canary name 'db-cluster-canary', got %q", canary.Name)
	}
	if canary.Spec.Template.Spec.Volumes[0].Secret.SecretName != "db-cluster-database-password-rev-rev999" {
		t.Errorf("expected canary volume secret 'db-cluster-database-password-rev-rev999', got %q",
			canary.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}

	// Promote
	if err := adapter.Promote(ctx, client, policy, "db-cluster-database-password-rev-rev999"); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	updated := &appsv1.StatefulSet{}
	if err := client.Get(ctx, types.NamespacedName{Name: "db-cluster", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get updated statefulset: %v", err)
	}
	if updated.Spec.Template.Spec.Volumes[0].Secret.SecretName != "db-cluster-database-password-rev-rev999" {
		t.Errorf("expected promoted volume secret 'db-cluster-database-password-rev-rev999', got %q",
			updated.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
}

func TestDaemonSetAdapter_Lifecycle(t *testing.T) {
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

	// BuildCanary produces an isolated 1-replica Deployment
	canary := adapter.BuildCanary(policy, "node-agent-database-password-rev-rev555")
	if canary.Name != "node-agent-canary" {
		t.Errorf("expected canary name 'node-agent-canary', got %q", canary.Name)
	}
	if canary.Spec.Template.Spec.Volumes[0].Secret.SecretName != "node-agent-database-password-rev-rev555" {
		t.Errorf("expected canary volume secret 'node-agent-database-password-rev-rev555', got %q",
			canary.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}

	// Promote
	if err := adapter.Promote(ctx, client, policy, "node-agent-database-password-rev-rev555"); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	updated := &appsv1.DaemonSet{}
	if err := client.Get(ctx, types.NamespacedName{Name: "node-agent", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get updated daemonset: %v", err)
	}
	if updated.Spec.Template.Spec.Volumes[0].Secret.SecretName != "node-agent-database-password-rev-rev555" {
		t.Errorf("expected promoted volume secret 'node-agent-database-password-rev-rev555', got %q",
			updated.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
}

func TestRolloutAdapter_Lifecycle(t *testing.T) {
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
	canary := adapter.BuildCanary(policy, "canary-service-database-password-rev-rev777")
	if canary.Name != "canary-service-canary" {
		t.Errorf("expected canary name 'canary-service-canary', got %q", canary.Name)
	}
	if canary.Spec.Template.Spec.Volumes[0].Secret.SecretName != "canary-service-database-password-rev-rev777" {
		t.Errorf("expected canary volume secret 'canary-service-database-password-rev-rev777', got %q",
			canary.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}

	// Promote
	if err := adapter.Promote(ctx, client, policy, "canary-service-database-password-rev-rev777"); err != nil {
		t.Fatalf("Promote failed: %v", err)
	}

	updated := &argorolloutsv1alpha1.Rollout{}
	if err := client.Get(ctx, types.NamespacedName{Name: "canary-service", Namespace: "default"}, updated); err != nil {
		t.Fatalf("failed to get updated rollout: %v", err)
	}
	if updated.Spec.Template.Spec.Volumes[0].Secret.SecretName != "canary-service-database-password-rev-rev777" {
		t.Errorf("expected promoted volume secret 'canary-service-database-password-rev-rev777', got %q",
			updated.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
}

func TestMutatePodTemplateSpec_MultiSecretAndExplicitTargetRef(t *testing.T) {
	// Pod with 3 secrets: DB password, Redis token, and unmanaged TLS certificate
	tpl := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Env: []corev1.EnvVar{
						{Name: "EXISTING_VAR", Value: "plain"},
						{Name: "REDIS_TOKEN", Value: "initial-token"},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "tls-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "my-tls-cert-secret",
						},
					},
				},
				{
					Name: "db-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "initial-db-secret",
						},
					},
				},
				{
					Name: "redis-volume",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "initial-redis-secret",
						},
					},
				},
			},
		},
	}

	// 1. Policy for Database Password with explicit VolumeName: "db-volume"
	dbPolicy := &secretv1alpha1.DynamicSecretPolicy{
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				ObjectName: "database-password",
			},
			TargetRef: &secretv1alpha1.TargetRef{
				VolumeName: "db-volume",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "dbrev1",
		},
	}

	MutatePodTemplateSpec(tpl, "order-service", dbPolicy, "order-service-database-password-rev-dbrev1")

	// Verify db-volume was mutated
	if tpl.Spec.Volumes[1].Secret.SecretName != "order-service-database-password-rev-dbrev1" {
		t.Errorf("expected db-volume mutated to 'order-service-database-password-rev-dbrev1', got %q",
			tpl.Spec.Volumes[1].Secret.SecretName)
	}
	// Verify TLS and Redis volumes were completely untouched!
	if tpl.Spec.Volumes[0].Secret.SecretName != "my-tls-cert-secret" {
		t.Errorf("expected tls-volume untouched, got %q", tpl.Spec.Volumes[0].Secret.SecretName)
	}
	if tpl.Spec.Volumes[2].Secret.SecretName != "initial-redis-secret" {
		t.Errorf("expected redis-volume untouched, got %q", tpl.Spec.Volumes[2].Secret.SecretName)
	}

	// 2. Policy for Redis Token with explicit EnvName: "REDIS_TOKEN"
	redisPolicy := &secretv1alpha1.DynamicSecretPolicy{
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				ObjectName: "redis-token",
			},
			TargetRef: &secretv1alpha1.TargetRef{
				EnvName: "REDIS_TOKEN",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "redisrev2",
		},
	}

	MutatePodTemplateSpec(tpl, "order-service", redisPolicy, "order-service-redis-token-rev-redisrev2")

	// Verify REDIS_TOKEN was updated to SecretKeyRef
	appContainer := tpl.Spec.Containers[0]
	var redisEnv *corev1.EnvVar
	for i := range appContainer.Env {
		if appContainer.Env[i].Name == "REDIS_TOKEN" {
			redisEnv = &appContainer.Env[i]
		}
	}
	if redisEnv == nil || redisEnv.ValueFrom == nil || redisEnv.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected REDIS_TOKEN to have SecretKeyRef, got %v", redisEnv)
	}
	if redisEnv.ValueFrom.SecretKeyRef.Name != "order-service-redis-token-rev-redisrev2" {
		t.Errorf("expected REDIS_TOKEN secret name 'order-service-redis-token-rev-redisrev2', got %q",
			redisEnv.ValueFrom.SecretKeyRef.Name)
	}
	if redisEnv.ValueFrom.SecretKeyRef.Key != "redis-token" {
		t.Errorf("expected secret key 'redis-token', got %q", redisEnv.ValueFrom.SecretKeyRef.Key)
	}

	// Verify EXISTING_VAR untouched
	if appContainer.Env[0].Value != "plain" {
		t.Errorf("expected EXISTING_VAR untouched, got %q", appContainer.Env[0].Value)
	}
}

func TestMutatePodTemplateSpec_ImplicitEnv_PreservesUnrelatedSecretsWithSameKey(t *testing.T) {
	tpl := &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"app": "order-service"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "nginx:alpine",
					Env: []corev1.EnvVar{
						{
							// Target secret managed by operator
							Name: "DB_PASSWORD",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "order-service-password",
									},
									Key: "password",
								},
							},
						},
						{
							// Unrelated third-party secret that also happens to have Key: "password"
							Name: "REDIS_PASSWORD",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "third-party-redis-credentials",
									},
									Key: "password",
								},
							},
						},
					},
				},
			},
		},
	}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				ObjectName: "password",
			},
			// No explicit TargetRef -> uses implicit matching
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "rev99",
		},
	}

	MutatePodTemplateSpec(tpl, "order-service", policy, "order-service-password-rev-rev99")

	appContainer := tpl.Spec.Containers[0]
	var dbEnv, redisEnv *corev1.EnvVar
	for i := range appContainer.Env {
		if appContainer.Env[i].Name == "DB_PASSWORD" {
			dbEnv = &appContainer.Env[i]
		}
		if appContainer.Env[i].Name == "REDIS_PASSWORD" {
			redisEnv = &appContainer.Env[i]
		}
	}

	if dbEnv == nil || dbEnv.ValueFrom == nil || dbEnv.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected DB_PASSWORD to have SecretKeyRef, got %v", dbEnv)
	}
	if dbEnv.ValueFrom.SecretKeyRef.Name != "order-service-password-rev-rev99" {
		t.Errorf("expected DB_PASSWORD secret name 'order-service-password-rev-rev99', got %q",
			dbEnv.ValueFrom.SecretKeyRef.Name)
	}

	if redisEnv == nil || redisEnv.ValueFrom == nil || redisEnv.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected REDIS_PASSWORD to have SecretKeyRef, got %v", redisEnv)
	}
	// Crucial check: REDIS_PASSWORD must remain pointed at third-party-redis-credentials, NOT overwritten!
	if redisEnv.ValueFrom.SecretKeyRef.Name != "third-party-redis-credentials" {
		t.Errorf("CRITICAL: Unrelated third-party secret was overwritten! Expected 'third-party-redis-credentials', got %q",
			redisEnv.ValueFrom.SecretKeyRef.Name)
	}
}

