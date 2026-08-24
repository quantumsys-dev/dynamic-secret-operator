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
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestDynamicSecretPolicyReconciler_SetupWithManager(t *testing.T) {
	scheme := setupTestScheme(t)
	mgr, err := ctrl.NewManager(&rest.Config{Host: "http://127.0.0.1:6443"}, ctrl.Options{
		Scheme: scheme,
	})
	if err == nil {
		r := &DynamicSecretPolicyReconciler{
			Client: mgr.GetClient(),
			Scheme: scheme,
		}
		_ = r.SetupWithManager(mgr)
	}
}

// =========================================================================
// Ginkgo & Gomega Asynchronous Envtest Integration Suite
// =========================================================================

var _ = Describe("DynamicSecretPolicy Controller Envtest Suite", func() {
	Context("When reconciling a DynamicSecretPolicy against live API server", func() {
		const (
			policyName       = "envtest-policy"
			targetDeployName = "envtest-target-app"
			namespace        = "default"
		)

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{
			Name:      policyName,
			Namespace: namespace,
		}

		BeforeEach(func() {
			if k8sClient == nil {
				Skip("k8sClient not initialized (envtest skipped)")
			}

			// Create target dummy deployment
			targetDeploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      targetDeployName,
					Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: func() *int32 { i := int32(2); return &i }(),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": targetDeployName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": targetDeployName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: "nginx:alpine",
									Env: []corev1.EnvVar{
										{
											Name: "DATABASE_PASSWORD",
											ValueFrom: &corev1.EnvVarSource{
												SecretKeyRef: &corev1.SecretKeySelector{
													LocalObjectReference: corev1.LocalObjectReference{
														Name: "initial-secret",
													},
													Key: "db-secret",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			_ = k8sClient.Create(ctx, targetDeploy)
		})

		AfterEach(func() {
			if k8sClient == nil {
				return
			}
			policy := &secretv1alpha1.DynamicSecretPolicy{}
			if err := k8sClient.Get(ctx, typeNamespacedName, policy); err == nil {
				_ = k8sClient.Delete(ctx, policy)
			}
			targetDeploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      targetDeployName,
					Namespace: namespace,
				},
			}
			_ = k8sClient.Delete(ctx, targetDeploy)
		})

		It("should materialize immutable SecretRevision and progressively promote workload", func() {
			if k8sClient == nil {
				Skip("k8sClient not initialized")
			}

			By("Creating the DynamicSecretPolicy resource")
			policy := &secretv1alpha1.DynamicSecretPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      policyName,
					Namespace: namespace,
				},
				Spec: secretv1alpha1.DynamicSecretPolicySpec{
					VaultRef: secretv1alpha1.VaultReference{
						KeyVaultURI: "https://dso-vault.vault.azure.net",
						ObjectName:  "db-secret",
						ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
					},
					WorkloadSelector: secretv1alpha1.WorkloadSelector{
						Kind: "Deployment",
						Name: targetDeployName,
					},
					ValidationProbes: []secretv1alpha1.ValidationProbe{
						{
							Type:         secretv1alpha1.ProbeTypeHTTP,
							Endpoint:     "http://localhost:8080/health",
							QueryTimeout: 5,
						},
					},
					RollbackConfig: &secretv1alpha1.RollbackConfig{
						AutoRollback:            true,
						CircuitBreakerThreshold: 3,
					},
				},
			}
			Expect(k8sClient.Create(ctx, policy)).To(Succeed())

			By("Assertion 1: Eventually creating the immutable SecretRevision")
			Eventually(func() error {
				secrets := &corev1.SecretList{}
				if err := k8sClient.List(ctx, secrets, client.InNamespace(namespace)); err != nil {
					return err
				}
				for _, sec := range secrets.Items {
					if rev, ok := sec.Labels[LabelRevision]; ok && rev != "" {
						return nil
					}
				}
				return fmt.Errorf("no SecretRevision found with %s label", LabelRevision)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			By("Assertion 2: Eventually patching target deployment with the revision")
			Eventually(func() error {
				deploy := &appsv1.Deployment{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: targetDeployName, Namespace: namespace}, deploy); err != nil {
					return err
				}
				if deploy.Spec.Template.Annotations != nil && deploy.Spec.Template.Annotations[LabelRevision] != "" {
					return nil
				}
				return fmt.Errorf("target deployment not yet patched with revision annotation")
			}, time.Second*10, time.Millisecond*250).Should(Succeed())

			By("Assertion 3: Eventually updating the CRD status to Promoting/Completed")
			Eventually(func() error {
				p := &secretv1alpha1.DynamicSecretPolicy{}
				if err := k8sClient.Get(ctx, typeNamespacedName, p); err != nil {
					return err
				}
				if p.Status.CurrentRevision != "" {
					return nil
				}
				return fmt.Errorf("status.currentRevision is still empty")
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})
	})
})

// =========================================================================
// Deterministic Table-Driven Unit Tests (Fake Client)
// =========================================================================

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
							EnvFrom: []corev1.EnvFromSource{
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "order-service-rev-old",
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
		ProbeRunner: func(ctx context.Context, probe secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
			return nil // Mock probe success
		},
		OnSecretMaterialized: func(ctx context.Context, policyName, revision string) error {
			ackTriggered = true
			return errors.New("callback logging error")
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

	// Step 4: Validating -> Promoting (Runs mock probe, patches target deployment, cleans up canary)
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
}

func TestDynamicSecretPolicyReconciler_StateTransitions(t *testing.T) {
	scheme := setupTestScheme(t)

	t.Run("transitions from CanaryProvisioning to Validating", func(t *testing.T) {
		policy := &secretv1alpha1.DynamicSecretPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "canary-prov-policy",
				Namespace: "default",
			},
			Status: secretv1alpha1.DynamicSecretPolicyStatus{
				DesiredRevision: "rev-abc-1234",
				Conditions: []metav1.Condition{
					{
						Type:   ConditionTypeCanaryProvisioning,
						Status: metav1.ConditionTrue,
						Reason: ReasonProvisioning,
					},
				},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
			WithObjects(policy).
			Build()

		r := &DynamicSecretPolicyReconciler{
			Client: c,
			Scheme: scheme,
		}

		res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "canary-prov-policy", Namespace: "default"}})
		if err != nil {
			t.Fatalf("unexpected reconcile error: %v", err)
		}
		if !res.Requeue {
			t.Errorf("expected requeue after transitioning to Validating")
		}

		updated := &secretv1alpha1.DynamicSecretPolicy{}
		_ = c.Get(context.Background(), types.NamespacedName{Name: "canary-prov-policy", Namespace: "default"}, updated)
		if !meta.IsStatusConditionTrue(updated.Status.Conditions, ConditionTypeValidating) {
			t.Errorf("expected Validating condition to be True")
		}
	})

	t.Run("handles unhandled state gracefully by requeuing", func(t *testing.T) {
		policy := &secretv1alpha1.DynamicSecretPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "unhandled-policy",
				Namespace: "default",
			},
			Status: secretv1alpha1.DynamicSecretPolicyStatus{
				Conditions: []metav1.Condition{
					{
						Type:   "UnknownState",
						Status: metav1.ConditionTrue,
						Reason: "UnknownReason",
					},
				},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
			WithObjects(policy).
			Build()

		r := &DynamicSecretPolicyReconciler{
			Client: c,
			Scheme: scheme,
			SecretFetcher: &mockSecretFetcher{},
		}

		res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "unhandled-policy", Namespace: "default"}})
		if err != nil {
			t.Fatalf("unexpected error on unhandled state: %v", err)
		}
		if !res.Requeue {
			t.Errorf("expected requeue on unhandled state")
		}
	})
}

func TestDynamicSecretPolicyReconciler_PromotingTerminalState(t *testing.T) {
	scheme := setupTestScheme(t)

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "promoting-policy",
			Namespace: "default",
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			CurrentRevision: "rev-done-1234",
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypePromoting,
					Status: metav1.ConditionTrue,
					Reason: ReasonCompleted,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy).
		Build()

	r := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "promoting-policy", Namespace: "default"}})
	if err != nil {
		t.Fatalf("expected nil error on promoting terminal state")
	}
	if res.Requeue {
		t.Errorf("expected no requeue for terminal state")
	}
}

func TestDynamicSecretPolicyReconciler_MaterializationErrorPaths(t *testing.T) {
	scheme := setupTestScheme(t)

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "error-policy",
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

	t.Run("fails when SecretFetcher is nil", func(t *testing.T) {
		r := &DynamicSecretPolicyReconciler{
			Client: fakeClient,
			Scheme: scheme,
		}
		_, err := r.materializeSecretRevision(context.Background(), policy)
		if err == nil {
			t.Fatalf("expected error when SecretFetcher is nil")
		}
	})

	t.Run("fails when SecretFetcher returns error", func(t *testing.T) {
		r := &DynamicSecretPolicyReconciler{
			Client: fakeClient,
			Scheme: scheme,
			SecretFetcher: &mockSecretFetcher{
				getSecretFunc: func(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
					return nil, errors.New("vault 403 forbidden")
				},
			},
		}
		_, err := r.materializeSecretRevision(context.Background(), policy)
		if err == nil {
			t.Fatalf("expected error when fetcher fails")
		}
	})

	t.Run("reconciler handles materialization error cleanly", func(t *testing.T) {
		r := &DynamicSecretPolicyReconciler{
			Client: fakeClient,
			Scheme: scheme,
			SecretFetcher: &mockSecretFetcher{
				getSecretFunc: func(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
					return nil, errors.New("vault error")
				},
			},
		}
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "error-policy", Namespace: "default"}})
		if err == nil {
			t.Fatalf("expected error from Reconcile when materialization fails")
		}
	})

	t.Run("handles existing secret revision idempotently", func(t *testing.T) {
		existingSec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "order-service-rev-f5a6b7c8d9e0",
				Namespace: "default",
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(existingSec).
			Build()

		r := &DynamicSecretPolicyReconciler{
			Client: c,
			Scheme: scheme,
			SecretFetcher: &mockSecretFetcher{
				getSecretFunc: func(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
					return &azure.SecretPayload{
						Value:   []byte("idempotent-test"),
						Version: "v1",
					}, nil
				},
			},
		}
		rev, err := r.materializeSecretRevision(context.Background(), policy)
		if err != nil {
			t.Fatalf("expected idempotent success on existing secret, got: %v", err)
		}
		if rev == "" {
			t.Errorf("expected non-empty revision")
		}
	})
}

func TestDynamicSecretPolicyReconciler_PromoteErrorPath(t *testing.T) {
	scheme := setupTestScheme(t)

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "promote-err-policy",
			Namespace: "default",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "missing-target-service",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "rev-err-1234",
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeValidating,
					Status: metav1.ConditionTrue,
					Reason: ReasonProbesRunning,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy).
		Build()

	r := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	t.Run("promoteTargetWorkload returns error", func(t *testing.T) {
		err := r.promoteTargetWorkload(context.Background(), policy)
		if err == nil {
			t.Fatalf("expected error when target deployment does not exist")
		}
	})

	t.Run("reconciler returns error on promotion failure", func(t *testing.T) {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "promote-err-policy", Namespace: "default"}})
		if err == nil {
			t.Fatalf("expected reconcile error on promotion failure")
		}
	})
}

func TestDynamicSecretPolicyReconciler_ValidationProbesExecution(t *testing.T) {
	scheme := setupTestScheme(t)

	t.Run("returns nil immediately when no probes configured", func(t *testing.T) {
		policy := &secretv1alpha1.DynamicSecretPolicy{}
		r := &DynamicSecretPolicyReconciler{}
		if err := r.runValidationProbes(context.Background(), policy); err != nil {
			t.Fatalf("expected nil error for empty probes")
		}
	})

	t.Run("executes default probe runner", func(t *testing.T) {
		policy := &secretv1alpha1.DynamicSecretPolicy{
			Spec: secretv1alpha1.DynamicSecretPolicySpec{
				ValidationProbes: []secretv1alpha1.ValidationProbe{
					{
						Type:     secretv1alpha1.ProbeTypeHTTP,
						Endpoint: "", // Will fail validation cleanly
					},
				},
			},
		}
		r := &DynamicSecretPolicyReconciler{
			Scheme: scheme,
		}
		err := r.runValidationProbes(context.Background(), policy)
		if err == nil {
			t.Fatalf("expected error from empty probe endpoint")
		}
	})
}

func TestDynamicSecretPolicyReconciler_CircuitBreakerTripsOnConsecutiveFailures(t *testing.T) {
	scheme := setupTestScheme(t)

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cb-policy",
			Namespace: "default",
			UID:       types.UID("cb-uid-1234"),
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://my-vault.vault.azure.net",
				ObjectName:  "db-pass",
				ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "payment-api",
			},
			ValidationProbes: []secretv1alpha1.ValidationProbe{
				{Type: secretv1alpha1.ProbeTypePostgreSQL},
			},
			RollbackConfig: &secretv1alpha1.RollbackConfig{
				CircuitBreakerThreshold: 3,
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "badrevision12",
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeValidating,
					Status: metav1.ConditionTrue,
					Reason: ReasonProbesRunning,
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy).
		Build()

	reconciler := &DynamicSecretPolicyReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		SecretFetcher: &mockSecretFetcher{},
		ProbeRunner: func(ctx context.Context, probe secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
			return errors.New("database connection refused")
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "cb-policy",
			Namespace: "default",
		},
	}

	ctx := context.Background()

	// Failure 1: Under threshold -> returns error for backoff
	_, err := reconciler.Reconcile(ctx, req)
	if err == nil {
		t.Fatalf("expected error from failed probe on attempt 1")
	}

	updated := &secretv1alpha1.DynamicSecretPolicy{}
	_ = fakeClient.Get(ctx, req.NamespacedName, updated)
	if updated.Status.ConsecutiveFailures != 1 {
		t.Errorf("expected ConsecutiveFailures=1, got %d", updated.Status.ConsecutiveFailures)
	}

	// Failure 2: Under threshold -> returns error for backoff
	_, err = reconciler.Reconcile(ctx, req)
	if err == nil {
		t.Fatalf("expected error from failed probe on attempt 2")
	}
	_ = fakeClient.Get(ctx, req.NamespacedName, updated)
	if updated.Status.ConsecutiveFailures != 2 {
		t.Errorf("expected ConsecutiveFailures=2, got %d", updated.Status.ConsecutiveFailures)
	}

	// Failure 3: Threshold reached (3) -> Circuit breaker trips, returns nil error & no requeue
	res, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("expected nil error on circuit breaker trip, got: %v", err)
	}
	if res.Requeue {
		t.Errorf("expected no requeue once circuit breaker trips")
	}

	_ = fakeClient.Get(ctx, req.NamespacedName, updated)
	if updated.Status.ConsecutiveFailures != 3 {
		t.Errorf("expected ConsecutiveFailures=3, got %d", updated.Status.ConsecutiveFailures)
	}
	if !meta.IsStatusConditionTrue(updated.Status.Conditions, ConditionTypeCircuitBreakerTripped) {
		t.Errorf("expected CircuitBreakerTripped condition to be True")
	}

	// Step 4: Next reconcile iteration is immediately halted by Circuit Breaker check
	res2, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("expected nil error from halted circuit breaker, got: %v", err)
	}
	if res2.Requeue {
		t.Errorf("expected no requeue from halted circuit breaker")
	}
}

func TestDynamicSecretPolicyReconciler_CircuitBreakerResetOnNewRevision(t *testing.T) {
	scheme := setupTestScheme(t)

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "reset-policy",
			Namespace: "default",
			UID:       types.UID("reset-uid-5678"),
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://my-vault.vault.azure.net",
				ObjectName:  "db-pass",
				ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "auth-service",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			ConsecutiveFailures: 5,
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeCircuitBreakerTripped,
					Status: metav1.ConditionTrue,
					Reason: ReasonValidationThresholdExceeded,
				},
			},
		},
	}

	// Clear conditions to simulate a newly triggered revision from state ""
	policy.Status.Conditions = nil

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy).
		Build()

	reconciler := &DynamicSecretPolicyReconciler{
		Client:        fakeClient,
		Scheme:        scheme,
		SecretFetcher: &mockSecretFetcher{},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "reset-policy",
			Namespace: "default",
		},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected requeue after materializing new revision")
	}

	updated := &secretv1alpha1.DynamicSecretPolicy{}
	_ = fakeClient.Get(context.Background(), req.NamespacedName, updated)
	if updated.Status.ConsecutiveFailures != 0 {
		t.Errorf("expected ConsecutiveFailures to reset to 0, got %d", updated.Status.ConsecutiveFailures)
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

func TestDynamicSecretPolicyReconciler_PreservesUnrelatedSecretsDuringPromotion(t *testing.T) {
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
								{
									Name: "DATADOG_API_KEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "datadog-secret",
											},
											Key: "api-key",
										},
									},
								},
								{
									Name: "STRIPE_KEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "stripe-credentials",
											},
											Key: "secret-key",
										},
									},
								},
							},
							EnvFrom: []corev1.EnvFromSource{
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "order-service-rev-old",
										},
									},
								},
								{
									SecretRef: &corev1.SecretEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "third-party-configs",
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
									SecretName: "order-service-rev-old",
								},
							},
						},
						{
							Name: "tls-certificates",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "ingress-tls-cert",
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
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			DesiredRevision: "newrev9988",
			CurrentRevision: "old",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy, targetDeployment).
		Build()

	r := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	err := r.promoteTargetWorkload(context.Background(), policy)
	if err != nil {
		t.Fatalf("promoteTargetWorkload failed: %v", err)
	}

	updatedDeploy := &appsv1.Deployment{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "order-service", Namespace: "default"}, updatedDeploy); err != nil {
		t.Fatalf("failed to get updated deployment: %v", err)
	}

	expectedManagedSecret := "order-service-rev-newrev9988"

	// 1. Assert managed env var was updated
	c := updatedDeploy.Spec.Template.Spec.Containers[0]
	if c.Env[0].ValueFrom.SecretKeyRef.Name != expectedManagedSecret {
		t.Errorf("expected DB_PASS secret name %q, got %q", expectedManagedSecret, c.Env[0].ValueFrom.SecretKeyRef.Name)
	}

	// 2. Assert UNRELATED env vars were NOT modified
	if c.Env[1].ValueFrom.SecretKeyRef.Name != "datadog-secret" {
		t.Errorf("CRITICAL BUG: DATADOG_API_KEY secret name was corrupted: got %q, expected 'datadog-secret'", c.Env[1].ValueFrom.SecretKeyRef.Name)
	}
	if c.Env[2].ValueFrom.SecretKeyRef.Name != "stripe-credentials" {
		t.Errorf("CRITICAL BUG: STRIPE_KEY secret name was corrupted: got %q, expected 'stripe-credentials'", c.Env[2].ValueFrom.SecretKeyRef.Name)
	}

	// 3. Assert EnvFrom: managed is updated, unmanaged is untouched
	if c.EnvFrom[0].SecretRef.Name != expectedManagedSecret {
		t.Errorf("expected managed EnvFrom %q, got %q", expectedManagedSecret, c.EnvFrom[0].SecretRef.Name)
	}
	if c.EnvFrom[1].SecretRef.Name != "third-party-configs" {
		t.Errorf("CRITICAL BUG: third-party-configs EnvFrom was corrupted: got %q", c.EnvFrom[1].SecretRef.Name)
	}

	// 4. Assert Volumes: managed is updated, TLS/unmanaged is untouched
	vols := updatedDeploy.Spec.Template.Spec.Volumes
	if vols[0].Secret.SecretName != expectedManagedSecret {
		t.Errorf("expected managed volume %q, got %q", expectedManagedSecret, vols[0].Secret.SecretName)
	}
	if vols[1].Secret.SecretName != "ingress-tls-cert" {
		t.Errorf("CRITICAL BUG: ingress-tls-cert volume was corrupted: got %q, expected 'ingress-tls-cert'", vols[1].Secret.SecretName)
	}
}

