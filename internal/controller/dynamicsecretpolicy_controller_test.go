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

	corev1 "k8s.io/api/core/v1"
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
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy).
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
	if updated.Status.DesiredRevision == "" {
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
	if createdSecret.Labels[LabelRevision] != updated.Status.DesiredRevision {
		t.Errorf("expected secret revision label %q, got %q", updated.Status.DesiredRevision, createdSecret.Labels[LabelRevision])
	}
	if len(createdSecret.OwnerReferences) == 0 {
		t.Errorf("expected controller owner reference on created secret")
	}

	// Step 2: Test Idempotency (reconcile when secret already exists)
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
