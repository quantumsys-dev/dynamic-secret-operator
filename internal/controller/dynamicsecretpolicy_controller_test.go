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

package controller

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys/dynamic-secret-operator/internal/azure"
)

type mockSecretFetcher struct {
	getSecretFunc func(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error)
}

func (m *mockSecretFetcher) GetSecret(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
	if m.getSecretFunc != nil {
		return m.getSecretFunc(ctx, vaultURI, secretName, version)
	}
	return &azure.SecretPayload{
		Value:   []byte("test-db-password-super-secret"),
		Version: "v100",
		ID:      fmt.Sprintf("%s/secrets/%s/v100", vaultURI, secretName),
	}, nil
}

func setupTestScheme(t *testing.T) *runtime.Scheme {
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("failed to add clientgo scheme: %v", err)
	}
	if err := secretv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add secretv1alpha1 scheme: %v", err)
	}
	return s
}

func TestDynamicSecretPolicyReconciler_SecretMaterializationAndProgression(t *testing.T) {
	scheme := setupTestScheme(t)

	targetDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "order-service",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "order-service:1.0.0",
							Env: []corev1.EnvVar{
								{
									Name: "DB_PASS",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "order-service-rev-old",
											},
											Key: "db-pass",
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
									SecretName: "order-service-rev-old",
								},
							},
						},
					},
				},
			},
		},
	}

	canaryDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "order-service-canary",
			Namespace: "default",
		},
	}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "order-policy",
			Namespace: "default",
			UID:       types.UID("12345-67890"),
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://my-vault.vault.azure.net",
				ObjectName:  "db-pass",
				ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "order-service",
			},
			ValidationProbes: []secretv1alpha1.ValidationProbe{
				{Type: secretv1alpha1.ProbeTypePostgreSQL},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy, targetDeployment, canaryDeploy).
		Build()

	ackTriggered := false
	reconciler := &DynamicSecretPolicyReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		SecretFetcher: &mockSecretFetcher{},
		OnSecretMaterialized: func(ctx context.Context, policyName, revision string) error {
			ackTriggered = true
			return nil
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "order-policy",
			Namespace: "default",
		},
	}

	ctx := context.Background()

	// Step 1: Initial state -> Materialize Secret and Transition to RevisionPrepared
	res, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 1 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 1 to requeue for next state")
	}
	if !ackTriggered {
		t.Errorf("expected post-materialization callback to be triggered")
	}

	updated := &secretv1alpha1.DynamicSecretPolicy{}
	if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get policy after step 1: %v", err)
	}
	if !meta.IsStatusConditionTrue(updated.Status.Conditions, ConditionTypeRevisionPrepared) {
		t.Errorf("expected RevisionPrepared condition to be True after step 1")
	}
	desiredRevision := updated.Status.DesiredRevision
	if desiredRevision == "" {
		t.Errorf("expected DesiredRevision to be populated")
	}

	// Verify that the secret was materialized in Kubernetes
	secretList := &corev1.SecretList{}
	if err := fakeClient.List(ctx, secretList); err != nil {
		t.Fatalf("failed to list secrets: %v", err)
	}
	if len(secretList.Items) != 1 {
		t.Fatalf("expected 1 materialized secret, got %d", len(secretList.Items))
	}
	createdSecret := secretList.Items[0]
	if createdSecret.Labels[LabelRevision] != desiredRevision {
		t.Errorf("expected secret revision label %q, got %q", desiredRevision, createdSecret.Labels[LabelRevision])
	}
	if len(createdSecret.OwnerReferences) == 0 {
		t.Errorf("expected controller owner reference on created secret")
	}

	// Step 2: RevisionPrepared -> CanaryProvisioning (NetworkPolicy creation)
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 2 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 2 to requeue")
	}

	netpolList := &networkingv1.NetworkPolicyList{}
	if err := fakeClient.List(ctx, netpolList); err != nil {
		t.Fatalf("failed to list network policies: %v", err)
	}
	if len(netpolList.Items) != 1 {
		t.Fatalf("expected 1 canary network policy, got %d", len(netpolList.Items))
	}
	createdNetpol := netpolList.Items[0]
	if createdNetpol.Name != "order-service-canary-netpol" {
		t.Errorf("expected netpol name 'order-service-canary-netpol', got %q", createdNetpol.Name)
	}
	if len(createdNetpol.OwnerReferences) == 0 {
		t.Errorf("expected controller owner reference on network policy")
	}

	// Step 3: CanaryProvisioning -> Validating
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 3 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 3 to requeue")
	}

	// Step 4: Validating -> Promoting (Patches target deployment and cleans up canary)
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 4 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 4 to requeue")
	}

	// Assert target deployment was patched
	patchedDeploy := &appsv1.Deployment{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "order-service", Namespace: "default"}, patchedDeploy); err != nil {
		t.Fatalf("failed to get patched target deployment: %v", err)
	}
	expectedSecretName := fmt.Sprintf("order-service-rev-%s", desiredRevision)
	if patchedDeploy.Spec.Template.Spec.Volumes[0].Secret.SecretName != expectedSecretName {
		t.Errorf("expected volume secretName %q, got %q", expectedSecretName, patchedDeploy.Spec.Template.Spec.Volumes[0].Secret.SecretName)
	}
	if patchedDeploy.Spec.Template.Spec.Containers[0].Env[0].ValueFrom.SecretKeyRef.Name != expectedSecretName {
		t.Errorf("expected env secret ref %q, got %q", expectedSecretName, patchedDeploy.Spec.Template.Spec.Containers[0].Env[0].ValueFrom.SecretKeyRef.Name)
	}
	if patchedDeploy.Spec.Template.Annotations[LabelRevision] != desiredRevision {
		t.Errorf("expected pod template revision annotation %q, got %q", desiredRevision, patchedDeploy.Spec.Template.Annotations[LabelRevision])
	}

	// Assert Canary resources were destroyed
	if err := fakeClient.List(ctx, netpolList); err == nil && len(netpolList.Items) != 0 {
		t.Errorf("expected 0 canary network policies after cleanup, got %d", len(netpolList.Items))
	}

	// Assert status update
	if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get policy: %v", err)
	}
	if updated.Status.CurrentRevision != desiredRevision {
		t.Errorf("expected CurrentRevision to be %q, got %q", desiredRevision, updated.Status.CurrentRevision)
	}
	if updated.Status.DesiredRevision != "" {
		t.Errorf("expected DesiredRevision to be empty after promotion, got %q", updated.Status.DesiredRevision)
	}

	// Step 5: Promoting -> Terminal state
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 5 reconcile failed: %v", err)
	}
	if res.Requeue {
		t.Errorf("expected step 5 (terminal) not to requeue")
	}

	// Step 6: Test Idempotency (reconcile second policy when secret already exists)
	secondPolicy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "second-policy",
			Namespace: "default",
			UID:       types.UID("second-uid-9999"),
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://my-vault.vault.azure.net",
				ObjectName:  "db-pass",
				ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "order-service",
			},
		},
	}
	if err := fakeClient.Create(ctx, secondPolicy); err != nil {
		t.Fatalf("failed to create second policy: %v", err)
	}

	secondReq := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "second-policy",
			Namespace: "default",
		},
	}
	res2, err := reconciler.Reconcile(ctx, secondReq)
	if err != nil {
		t.Fatalf("expected idempotent reconciliation when secret already exists, got err: %v", err)
	}
	if !res2.Requeue {
		t.Errorf("expected requeue on successful materialization, got: %v", res2)
	}
}

func TestDynamicSecretPolicyReconciler_RollbackCleanup(t *testing.T) {
	scheme := setupTestScheme(t)

	canaryDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "order-service-canary",
			Namespace: "default",
		},
	}

	canaryNetpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "order-service-canary-netpol",
			Namespace: "default",
		},
	}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollback-policy",
			Namespace: "default",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "order-service",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "failedrev123",
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeRolledBack,
					Status: metav1.ConditionTrue,
					Reason: ReasonRolledBack,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy, canaryDeploy, canaryNetpol).
		Build()

	reconciler := &DynamicSecretPolicyReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		SecretFetcher: &mockSecretFetcher{},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "rollback-policy",
			Namespace: "default",
		},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("expected clean exit from rollback state, got: %v", err)
	}
	if res.Requeue {
		t.Errorf("expected no requeue for terminal rollback state")
	}

	// Verify canary netpol is deleted
	netpolList := &networkingv1.NetworkPolicyList{}
	if err := fakeClient.List(context.Background(), netpolList); err == nil && len(netpolList.Items) != 0 {
		t.Errorf("expected canary netpol to be destroyed on rollback, found %d", len(netpolList.Items))
	}
}

func TestDynamicSecretPolicyReconciler_NotFoundIgnored(t *testing.T) {
	scheme := setupTestScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &DynamicSecretPolicyReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		SecretFetcher: &mockSecretFetcher{},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "non-existent",
			Namespace: "default",
		},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("expected nil error for not found resource, got: %v", err)
	}
	if res.Requeue {
		t.Errorf("expected no requeue for not found resource")
	}
}
