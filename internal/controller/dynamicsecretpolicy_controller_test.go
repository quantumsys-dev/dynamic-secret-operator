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
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

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

func TestDynamicSecretPolicyReconciler_StateMachineProgression(t *testing.T) {
	scheme := setupTestScheme(t)

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "order-policy",
			Namespace: "default",
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

	reconciler := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "order-policy",
			Namespace: "default",
		},
	}

	ctx := context.Background()

	// Step 1: Initial state -> RevisionPrepared
	res, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 1 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 1 to requeue for next state")
	}

	updated := &secretv1alpha1.DynamicSecretPolicy{}
	if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get policy after step 1: %v", err)
	}
	if !meta.IsStatusConditionTrue(updated.Status.Conditions, ConditionTypeRevisionPrepared) {
		t.Errorf("expected RevisionPrepared condition to be True after step 1")
	}

	// Step 2: RevisionPrepared -> CanaryProvisioning
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 2 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 2 to requeue")
	}

	if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get policy after step 2: %v", err)
	}
	if !meta.IsStatusConditionTrue(updated.Status.Conditions, ConditionTypeCanaryProvisioning) {
		t.Errorf("expected CanaryProvisioning condition to be True after step 2")
	}

	// Step 3: CanaryProvisioning -> Validating
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 3 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 3 to requeue")
	}

	if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get policy after step 3: %v", err)
	}
	if !meta.IsStatusConditionTrue(updated.Status.Conditions, ConditionTypeValidating) {
		t.Errorf("expected Validating condition to be True after step 3")
	}

	// Step 4: Validating -> Promoting
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 4 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 4 to requeue")
	}

	if err := fakeClient.Get(ctx, req.NamespacedName, updated); err != nil {
		t.Fatalf("failed to get policy after step 4: %v", err)
	}
	if !meta.IsStatusConditionTrue(updated.Status.Conditions, ConditionTypePromoting) {
		t.Errorf("expected Promoting condition to be True after step 4")
	}

	// Step 5: Terminal state -> Promoting completed, no requeue
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 5 reconcile failed: %v", err)
	}
	if res.Requeue {
		t.Errorf("expected step 5 (terminal state) NOT to requeue")
	}
}

func TestDynamicSecretPolicyReconciler_NotFoundIgnored(t *testing.T) {
	scheme := setupTestScheme(t)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
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
