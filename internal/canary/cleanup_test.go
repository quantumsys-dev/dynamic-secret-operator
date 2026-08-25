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
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

func TestCleanupCanaryResources(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = secretv1alpha1.AddToScheme(scheme)

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-dsp",
			Namespace: "production",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "orders-api",
			},
		},
	}

	canaryDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-canary",
			Namespace: "production",
		},
	}

	canaryNetpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-canary-netpol",
			Namespace: "production",
		},
	}

	t.Run("successfully deletes existing canary resources", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(canaryDeploy, canaryNetpol).
			Build()

		ctx := context.Background()
		if err := CleanupCanaryResources(ctx, fakeClient, policy); err != nil {
			t.Fatalf("expected successful cleanup, got error: %v", err)
		}

		// Verify deletion
		deployCheck := &appsv1.Deployment{}
		err := fakeClient.Get(ctx, types.NamespacedName{Name: "orders-api-canary", Namespace: "production"}, deployCheck)
		if err == nil {
			t.Errorf("expected canary deployment to be deleted, but it was found")
		}

		netpolList := &networkingv1.NetworkPolicyList{}
		if err := fakeClient.List(ctx, netpolList); err != nil || len(netpolList.Items) != 0 {
			t.Errorf("expected 0 network policies after cleanup, got %d", len(netpolList.Items))
		}
	})

	t.Run("is idempotent when resources are already deleted", func(t *testing.T) {
		emptyClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		ctx := context.Background()
		if err := CleanupCanaryResources(ctx, emptyClient, policy); err != nil {
			t.Fatalf("expected no error when cleaning up non-existent resources, got: %v", err)
		}
	})
}
