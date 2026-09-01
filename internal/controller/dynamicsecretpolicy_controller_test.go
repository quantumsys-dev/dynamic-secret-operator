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
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/azure"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/canary"
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
		WithObjects(policy, targetDeployment).
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
	if createdSecret.Labels[LabelPolicy] != policy.Name {
		t.Errorf("expected secret policy label %q, got %q", policy.Name, createdSecret.Labels[LabelPolicy])
	}
	if createdSecret.Labels[LabelManaged] != ManagedValueTrue {
		t.Errorf("expected secret managed label %q, got %q", ManagedValueTrue, createdSecret.Labels[LabelManaged])
	}
	if len(createdSecret.OwnerReferences) == 0 {
		t.Errorf("expected controller owner reference on created secret")
	}

	// Step 2: RevisionPrepared -> CanaryProvisioning (Canary Deployment and NetworkPolicy creation)
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("step 2 reconcile failed: %v", err)
	}
	if !res.Requeue {
		t.Errorf("expected step 2 to requeue")
	}

	// Verify canary deployment was created
	createdCanary := &appsv1.Deployment{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "order-service-canary", Namespace: "default"}, createdCanary); err != nil {
		t.Fatalf("failed to get created canary deployment: %v", err)
	}
	if len(createdCanary.OwnerReferences) == 0 {
		t.Errorf("expected controller owner reference on canary deployment")
	}
	if *createdCanary.Spec.Replicas != 1 {
		t.Errorf("expected 1 canary replica, got %d", *createdCanary.Spec.Replicas)
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
	expectedSecretName := fmt.Sprintf("order-service-db-pass-rev-%s", desiredRevision)
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
	deletedCanary := &appsv1.Deployment{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "order-service-canary", Namespace: "default"}, deletedCanary); !apierrors.IsNotFound(err) {
		t.Errorf("expected canary deployment to be deleted after promotion, got err: %v", err)
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
			Spec: secretv1alpha1.DynamicSecretPolicySpec{
				VaultRef: secretv1alpha1.VaultReference{
					KeyVaultURI: "https://my-vault.vault.azure.net",
					ObjectName:  "db-pass",
					ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
				},
				WorkloadSelector: secretv1alpha1.WorkloadSelector{Name: "app"},
			},
			Status: secretv1alpha1.DynamicSecretPolicyStatus{
				DesiredRevision: "unhandled-rev",
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
			Client:        c,
			Scheme:        scheme,
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

	payload := []byte("synced-secret")
	hasher := sha256.New()
	hasher.Write(payload)
	hasher.Write([]byte("v1"))
	expectedHash := fmt.Sprintf("%x", hasher.Sum(nil))[:12]

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "promoting-policy",
			Namespace: "default",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://my-vault.vault.azure.net",
				ObjectName:  "db-pass",
				ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Name: "my-app",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			CurrentRevision: expectedHash,
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
		SecretFetcher: &mockSecretFetcher{
			getSecretFunc: func(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
				return &azure.SecretPayload{
					Value:   payload,
					Version: "v1",
				}, nil
			},
		},
	}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "promoting-policy", Namespace: "default"}})
	if err != nil {
		t.Fatalf("expected nil error on promoting terminal state, got: %v", err)
	}
	if res.Requeue {
		t.Errorf("expected no requeue for in-sync terminal state")
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
		if _, err := r.reconcileValidationProbes(context.Background(), policy); err != nil {
			t.Fatalf("expected nil error for empty probes")
		}
	})

	t.Run("executes default probe runner", func(t *testing.T) {
		policy := &secretv1alpha1.DynamicSecretPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "probe-test-policy",
				Namespace: "default",
			},
			Spec: secretv1alpha1.DynamicSecretPolicySpec{
				WorkloadSelector: secretv1alpha1.WorkloadSelector{
					Name: "order-service",
				},
				ValidationProbes: []secretv1alpha1.ValidationProbe{
					{
						Type:     secretv1alpha1.ProbeTypeHTTP,
						Endpoint: "", // Will fail validation cleanly
					},
				},
			},
			Status: secretv1alpha1.DynamicSecretPolicyStatus{
				DesiredRevision: "probe123",
			},
		}
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "order-service-rev-probe123",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"password": []byte("valid-pass"),
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sec).
			Build()

		r := &DynamicSecretPolicyReconciler{
			Client: c,
			Scheme: scheme,
		}
		_, err := r.reconcileValidationProbes(context.Background(), policy)
		if err == nil {
			t.Fatalf("expected error from empty probe endpoint")
		}
	})

	t.Run("passes materialized secret.Data to ProbeRunner", func(t *testing.T) {
		policy := &secretv1alpha1.DynamicSecretPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret-data-policy",
				Namespace: "default",
			},
			Spec: secretv1alpha1.DynamicSecretPolicySpec{
				VaultRef: secretv1alpha1.VaultReference{
					ObjectName: "db-password",
					ObjectType: secretv1alpha1.VaultObjectTypeSecret,
				},
				WorkloadSelector: secretv1alpha1.WorkloadSelector{
					Name: "auth-service",
				},
				ValidationProbes: []secretv1alpha1.ValidationProbe{
					{
						Type:     secretv1alpha1.ProbeTypePostgreSQL,
						Endpoint: "postgres:5432",
					},
				},
			},
			Status: secretv1alpha1.DynamicSecretPolicyStatus{
				DesiredRevision: "rev-abc-456",
			},
		}
		secretObj := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "auth-service-db-password-rev-rev-abc-456",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"db-password": []byte("super-secret-password"),
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(secretObj).
			Build()

		var capturedValue string
		var receivedNil bool
		r := &DynamicSecretPolicyReconciler{
			Client: c,
			Scheme: scheme,
			ProbeRunner: func(ctx context.Context, probe secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
				if secretData == nil {
					receivedNil = true
				} else {
					capturedValue = string(secretData["db-password"])
				}
				return nil
			},
		}

		res, err := r.reconcileValidationProbes(context.Background(), policy)
		if err != nil {
			t.Fatalf("unexpected error running validation probes: %v", err)
		}
		if res.Requeue || res.RequeueAfter > 0 {
			t.Errorf("expected synchronous probe to complete without requeue")
		}

		if receivedNil {
			t.Fatalf("CRITICAL BUG: probe runner received nil secretData")
		}
		if capturedValue != "super-secret-password" {
			t.Errorf("expected 'super-secret-password' during probe execution, got %q", capturedValue)
		}
	})

	t.Run("creates Job asynchronously and returns RequeueAfter without blocking", func(t *testing.T) {
		policy := &secretv1alpha1.DynamicSecretPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "job-probe-policy",
				Namespace: "default",
			},
			Spec: secretv1alpha1.DynamicSecretPolicySpec{
				WorkloadSelector: secretv1alpha1.WorkloadSelector{
					Name: "worker-app",
				},
				VaultRef: secretv1alpha1.VaultReference{
					ObjectName: "api-key",
				},
				ValidationProbes: []secretv1alpha1.ValidationProbe{
					{
						Type: secretv1alpha1.ProbeTypeJob,
						Job: &secretv1alpha1.JobProbeSpec{
							JobTemplate: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											RestartPolicy: corev1.RestartPolicyNever,
											Containers: []corev1.Container{
												{
													Name:  "probe",
													Image: "busybox:latest",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			Status: secretv1alpha1.DynamicSecretPolicyStatus{
				DesiredRevision: "rev-job-789",
			},
		}

		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "worker-app-api-key-rev-rev-job-789",
				Namespace: "default",
			},
			Data: map[string][]byte{"api-key": []byte("tok123")},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(sec).
			Build()

		r := &DynamicSecretPolicyReconciler{
			Client: c,
			Scheme: scheme,
		}

		// First call: creates Job asynchronously and returns non-blocking RequeueAfter
		res, err := r.reconcileValidationProbes(context.Background(), policy)
		if err != nil {
			t.Fatalf("unexpected error creating job probe: %v", err)
		}
		if res.RequeueAfter != 2*time.Second {
			t.Errorf("expected RequeueAfter 2s for async job probe, got %v", res.RequeueAfter)
		}

		// Verify Job was created in fake client
		jobList := &batchv1.JobList{}
		if err := c.List(context.Background(), jobList, client.InNamespace("default")); err != nil {
			t.Fatalf("failed to list jobs: %v", err)
		}
		if len(jobList.Items) != 1 {
			t.Fatalf("expected 1 job created, found %d", len(jobList.Items))
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
			DesiredRevision: "000000000000",
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeValidating,
					Status: metav1.ConditionTrue,
					Reason: ReasonProbesRunning,
				},
			},
		},
	}

	hasher := sha256.New()
	hasher.Write([]byte("test-db-password-super-secret"))
	hasher.Write([]byte("v100"))
	failingRevision := fmt.Sprintf("%x", hasher.Sum(nil))[:12]
	policy.Status.DesiredRevision = failingRevision

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("payment-api-rev-%s", failingRevision),
			Namespace: "default",
		},
		Data: map[string][]byte{
			"db-pass": []byte("invalid-password"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy, secret).
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
			DesiredRevision:     "oldbrokenrev12",
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
	if meta.IsStatusConditionTrue(updated.Status.Conditions, ConditionTypeCircuitBreakerTripped) {
		t.Errorf("expected CircuitBreakerTripped condition to be removed")
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

	expectedManagedSecret := "order-service-db-pass-rev-newrev9988"

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

func TestDynamicSecretPolicyReconciler_MultipleSequentialRotations(t *testing.T) {
	scheme := setupTestScheme(t)
	ctx := context.Background()

	targetDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-service",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "user-service:1.0.0",
							Env: []corev1.EnvVar{
								{
									Name: "DB_PASS",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "user-service-rev-init",
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
							Name: "managed-vol",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "user-service-rev-init",
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
			Name:      "user-policy",
			Namespace: "default",
			UID:       types.UID("user-policy-uid"),
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://vault.azure.net",
				ObjectName:  "db-pass",
				ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "user-service",
			},
		},
	}

	currentVaultSecret := "initial-secret-value"
	currentVaultVersion := "v1"

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy, targetDeployment).
		Build()

	reconciler := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
		SecretFetcher: &mockSecretFetcher{
			getSecretFunc: func(fCtx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
				return &azure.SecretPayload{
					Value:   []byte(currentVaultSecret),
					Version: currentVaultVersion,
				}, nil
			},
		},
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "user-policy",
			Namespace: "default",
		},
	}

	// ==========================================
	// ROTATION 1: Initial Rollout of v1
	// ==========================================
	// 1. Step 1: Detects drift from "" -> Materializes v1 secret -> RevisionPrepared
	res, err := reconciler.Reconcile(ctx, req)
	if err != nil || !res.Requeue {
		t.Fatalf("Rotation 1 step 1 failed: %v (requeue: %v)", err, res.Requeue)
	}

	// 2. Step 2: RevisionPrepared -> Provisions Canary & NetworkPolicy -> CanaryProvisioning
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || !res.Requeue {
		t.Fatalf("Rotation 1 step 2 failed: %v (requeue: %v)", err, res.Requeue)
	}

	// 3. Step 3: CanaryProvisioning -> Validating
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || !res.Requeue {
		t.Fatalf("Rotation 1 step 3 failed: %v (requeue: %v)", err, res.Requeue)
	}

	// 4. Step 4: Validating -> Promotes target deployment -> Promoting completed (Requeue: true)
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || !res.Requeue {
		t.Fatalf("Rotation 1 step 4 failed: %v (requeue: %v)", err, res.Requeue)
	}

	// Verify Rotation 1 completed
	updatedPolicy := &secretv1alpha1.DynamicSecretPolicy{}
	if err := fakeClient.Get(ctx, req.NamespacedName, updatedPolicy); err != nil {
		t.Fatalf("failed to get policy: %v", err)
	}
	firstRevision := updatedPolicy.Status.CurrentRevision
	if firstRevision == "" {
		t.Fatalf("expected non-empty CurrentRevision after rotation 1")
	}

	// 5. In-Sync Terminal Verification Check (Zero Deadlock)
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || res.Requeue {
		t.Fatalf("expected idle reconcile with no requeue and no error: %v", err)
	}

	// ==========================================
	// ROTATION 2: Upstream Secret Rotated to v2
	// ==========================================
	currentVaultSecret = "rotated-secret-value-v2"
	currentVaultVersion = "v2"

	// 1. Step 1: Detects drift between firstRevision and v2 -> Materializes v2 -> RevisionPrepared
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || !res.Requeue {
		t.Fatalf("Rotation 2 step 1 failed to detect drift: %v (requeue: %v)", err, res.Requeue)
	}

	// 2. Step 2: RevisionPrepared -> Provisions Canary & NetworkPolicy -> CanaryProvisioning
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || !res.Requeue {
		t.Fatalf("Rotation 2 step 2 failed: %v (requeue: %v)", err, res.Requeue)
	}

	// 3. Step 3: CanaryProvisioning -> Validating
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || !res.Requeue {
		t.Fatalf("Rotation 2 step 3 failed: %v (requeue: %v)", err, res.Requeue)
	}

	// 4. Step 4: Validating -> Promotes target deployment to v2 -> Promoting completed (Requeue: true)
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || !res.Requeue {
		t.Fatalf("Rotation 2 step 4 failed: %v (requeue: %v)", err, res.Requeue)
	}

	// Verify Rotation 2 completed with a new distinct revision!
	if err := fakeClient.Get(ctx, req.NamespacedName, updatedPolicy); err != nil {
		t.Fatalf("failed to get policy: %v", err)
	}
	secondRevision := updatedPolicy.Status.CurrentRevision
	if secondRevision == "" {
		t.Fatalf("expected non-empty CurrentRevision after rotation 2")
	}
	if secondRevision == firstRevision {
		t.Errorf("CRITICAL BUG: second revision %q should be different from first revision %q", secondRevision, firstRevision)
	}

	// Verify target deployment was patched with secondRevision
	finalDeploy := &appsv1.Deployment{}
	if err := fakeClient.Get(ctx, types.NamespacedName{Name: "user-service", Namespace: "default"}, finalDeploy); err != nil {
		t.Fatalf("failed to get target deployment: %v", err)
	}
	expectedV2Secret := fmt.Sprintf("user-service-db-pass-rev-%s", secondRevision)
	if finalDeploy.Spec.Template.Spec.Containers[0].Env[0].ValueFrom.SecretKeyRef.Name != expectedV2Secret {
		t.Errorf("expected target deployment to reference v2 secret %q, got %q", expectedV2Secret, finalDeploy.Spec.Template.Spec.Containers[0].Env[0].ValueFrom.SecretKeyRef.Name)
	}

	// 5. Subsequent In-Sync Verification Check (Zero Deadlock)
	res, err = reconciler.Reconcile(ctx, req)
	if err != nil || res.Requeue {
		t.Fatalf("expected idle reconcile after rotation 2: %v", err)
	}
}

func TestDynamicSecretPolicyReconciler_GarbageCollectsOldSecretRevisions(t *testing.T) {
	scheme := setupTestScheme(t)
	ctx := context.Background()

	targetDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
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
												Name: "orders-api-db-pass-rev-oldcurrent",
											},
											Key: "db-pass",
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

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-policy",
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
				Name: "orders-api",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			CurrentRevision: "oldcurrent",
			DesiredRevision: "newdesired",
		},
	}

	// Create 3 secrets: oldcurrent (active current), newdesired (active desired), and obsolete1, obsolete2 (orphans)
	secretCurrent := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-db-pass-rev-oldcurrent",
			Namespace: "default",
			Labels: map[string]string{
				LabelRevision:              "oldcurrent",
				canary.LabelTargetWorkload: "orders-api",
				LabelPolicy:                policy.Name,
			},
		},
	}
	secretDesired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-db-pass-rev-newdesired",
			Namespace: "default",
			Labels: map[string]string{
				LabelRevision:              "newdesired",
				canary.LabelTargetWorkload: "orders-api",
				LabelPolicy:                policy.Name,
			},
		},
	}
	secretObsolete1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-db-pass-rev-obsolete1",
			Namespace: "default",
			Labels: map[string]string{
				LabelRevision:              "obsolete1",
				canary.LabelTargetWorkload: "orders-api",
				LabelPolicy:                policy.Name,
			},
		},
	}
	secretObsolete2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-db-pass-rev-obsolete2",
			Namespace: "default",
			Labels: map[string]string{
				LabelRevision:              "obsolete2",
				canary.LabelTargetWorkload: "orders-api",
				LabelPolicy:                policy.Name,
			},
		},
	}
	// Unrelated secret belonging to another workload - must NEVER be deleted
	unrelatedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-workload-rev-obsolete1",
			Namespace: "default",
			Labels: map[string]string{
				LabelRevision:              "obsolete1",
				canary.LabelTargetWorkload: "other-workload",
				LabelPolicy:                "other-policy",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(policy, targetDeployment, secretCurrent, secretDesired, secretObsolete1, secretObsolete2, unrelatedSecret).
		Build()

	r := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	err := r.promoteTargetWorkload(ctx, policy)
	if err != nil {
		t.Fatalf("promoteTargetWorkload failed: %v", err)
	}

	// Verify obsolete secrets are deleted
	secrets := &corev1.SecretList{}
	if err := fakeClient.List(ctx, secrets, client.InNamespace("default")); err != nil {
		t.Fatalf("failed to list secrets: %v", err)
	}

	remainingNames := make(map[string]bool)
	for _, s := range secrets.Items {
		remainingNames[s.Name] = true
	}

	if !remainingNames["orders-api-db-pass-rev-oldcurrent"] {
		t.Errorf("expected current revision secret to be retained")
	}
	if !remainingNames["orders-api-db-pass-rev-newdesired"] {
		t.Errorf("expected desired revision secret to be retained")
	}
	if remainingNames["orders-api-db-pass-rev-obsolete1"] {
		t.Errorf("expected obsolete secret 1 to be garbage-collected")
	}
	if remainingNames["orders-api-db-pass-rev-obsolete2"] {
		t.Errorf("expected obsolete secret 2 to be garbage-collected")
	}
	if !remainingNames["other-workload-rev-obsolete1"] {
		t.Errorf("expected unrelated workload secret to be retained")
	}
}

// TestDynamicSecretPolicyReconciler_GarbageCollectMultiPolicyIsolation verifies that
// when multiple DynamicSecretPolicies target the same Deployment (e.g. Postgres and Redis),
// the GC cycle of Policy A does NOT delete active secrets belonging to Policy B.
func TestDynamicSecretPolicyReconciler_GarbageCollectMultiPolicyIsolation(t *testing.T) {
	scheme := setupTestScheme(t)
	ctx := context.Background()

	targetDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payment-api",
			Namespace: "production",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "api",
							Env: []corev1.EnvVar{
								{
									Name: "DB_PASS",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "payment-api-db-pass-rev-db01",
											},
											Key: "db-password",
										},
									},
								},
								{
									Name: "REDIS_PASS",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "payment-api-redis-pass-rev-redis01",
											},
											Key: "redis-password",
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

	// Policy A: Database Credentials for payment-api
	policyA := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "policy-db",
			Namespace: "production",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://vault.vault.azure.net",
				ObjectName:  "db-pass",
				ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "payment-api",
			},
			TargetRef: &secretv1alpha1.TargetRef{
				ContainerName: "api",
				EnvName:       "DB_PASS",
			},
		},
		Status: secretv1alpha1.DynamicSecretPolicyStatus{
			CurrentRevision: "db-old",
			DesiredRevision: "db-new",
		},
	}

	// Secrets for Policy A (payment-api, policy-db)
	secPolicyACurrent := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payment-api-db-pass-rev-db-old",
			Namespace: "production",
			Labels: map[string]string{
				LabelRevision:              "db-old",
				canary.LabelTargetWorkload: "payment-api",
				LabelPolicy:                "policy-db",
			},
		},
	}
	secPolicyADesired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payment-api-db-pass-rev-db-new",
			Namespace: "production",
			Labels: map[string]string{
				LabelRevision:              "db-new",
				canary.LabelTargetWorkload: "payment-api",
				LabelPolicy:                "policy-db",
			},
		},
	}
	secPolicyAObsolete := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payment-api-db-pass-rev-db-orphan",
			Namespace: "production",
			Labels: map[string]string{
				LabelRevision:              "db-orphan",
				canary.LabelTargetWorkload: "payment-api",
				LabelPolicy:                "policy-db",
			},
		},
	}

	// Secrets for Policy B (same workload "payment-api", but policy-redis)
	// Even though revisions "redis-active" and "redis-desired" differ from Policy A's revisions,
	// Policy A's GC cycle must NEVER reap them!
	secPolicyBActive := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payment-api-redis-pass-rev-redis-active",
			Namespace: "production",
			Labels: map[string]string{
				LabelRevision:              "redis-active",
				canary.LabelTargetWorkload: "payment-api",
				LabelPolicy:                "policy-redis",
			},
		},
	}
	secPolicyBDesired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "payment-api-redis-pass-rev-redis-desired",
			Namespace: "production",
			Labels: map[string]string{
				LabelRevision:              "redis-desired",
				canary.LabelTargetWorkload: "payment-api",
				LabelPolicy:                "policy-redis",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&secretv1alpha1.DynamicSecretPolicy{}).
		WithObjects(
			targetDeployment,
			policyA,
			secPolicyACurrent,
			secPolicyADesired,
			secPolicyAObsolete,
			secPolicyBActive,
			secPolicyBDesired,
		).
		Build()

	r := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Trigger Policy A promotion and GC
	if err := r.promoteTargetWorkload(ctx, policyA); err != nil {
		t.Fatalf("promoteTargetWorkload for Policy A failed: %v", err)
	}

	// Query remaining secrets in namespace
	secretList := &corev1.SecretList{}
	if err := fakeClient.List(ctx, secretList, client.InNamespace("production")); err != nil {
		t.Fatalf("failed to list secrets: %v", err)
	}

	remaining := make(map[string]bool)
	for _, s := range secretList.Items {
		remaining[s.Name] = true
	}

	// Policy A obsolete secret MUST be deleted
	if remaining["payment-api-db-pass-rev-db-orphan"] {
		t.Errorf("expected Policy A obsolete secret to be garbage collected")
	}

	// Policy A active secrets MUST be retained
	if !remaining["payment-api-db-pass-rev-db-old"] {
		t.Errorf("expected Policy A current revision secret to be retained")
	}
	if !remaining["payment-api-db-pass-rev-db-new"] {
		t.Errorf("expected Policy A desired revision secret to be retained")
	}

	// CRITICAL: Policy B secrets targeting the same deployment MUST be completely intact!
	if !remaining["payment-api-redis-pass-rev-redis-active"] {
		t.Errorf("CRITICAL BUG: Policy A GC mistakenly deleted Policy B's active secret!")
	}
	if !remaining["payment-api-redis-pass-rev-redis-desired"] {
		t.Errorf("CRITICAL BUG: Policy A GC mistakenly deleted Policy B's desired secret!")
	}
}

func TestDynamicSecretPolicyReconciler_MaterializeTLSSecretRevision(t *testing.T) {
	scheme := setupTestScheme(t)

	demoCertPEM := "-----BEGIN CERTIFICATE-----\nMIIBkDCB+aADAgECAgEBMA0GCSqGSIb3DQEBCwUAMBMxETAPBgNVBAMTCHRl\nc3RjZXJ0MB4XDTI2MDEwMTAwMDAwMFoXDTM2MDEwMTAwMDAwMFowEzERMA8G\nA1UEAxMIdGVzdGNlcnQwXDANBgkqhkiG9w0BAQEFAANLADBIAkEAv2k/xP4h\n-----END CERTIFICATE-----\n-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg4k8yF6oQ/123\n-----END PRIVATE KEY-----\n"

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tls-policy",
			Namespace: "default",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://vault.vault.azure.net",
				ObjectName:  "ingress-tls",
				ObjectType:  secretv1alpha1.VaultObjectTypeCertificate,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "tls-gateway",
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
		SecretFetcher: &mockSecretFetcher{
			getSecretFunc: func(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
				return &azure.SecretPayload{
					Value:   []byte(demoCertPEM),
					Version: "v1",
				}, nil
			},
		},
	}

	rev, err := r.materializeSecretRevision(context.Background(), policy)
	if err != nil {
		t.Fatalf("failed to materialize TLS secret revision: %v", err)
	}

	secretName := fmt.Sprintf("tls-gateway-ingress-tls-rev-%s", rev)
	secret := &corev1.Secret{}
	if err := fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: secretName}, secret); err != nil {
		t.Fatalf("failed to get materialized TLS secret: %v", err)
	}

	if secret.Type != corev1.SecretTypeTLS {
		t.Errorf("expected SecretTypeTLS (%q), got %q", corev1.SecretTypeTLS, secret.Type)
	}

	if _, ok := secret.Data[corev1.TLSCertKey]; !ok {
		t.Errorf("expected %q key in secret data", corev1.TLSCertKey)
	}
	if _, ok := secret.Data[corev1.TLSPrivateKeyKey]; !ok {
		t.Errorf("expected %q key in secret data", corev1.TLSPrivateKeyKey)
	}
	if secret.Labels[LabelManaged] != ManagedValueTrue {
		t.Errorf("expected %s=%s label on TLS secret, got %q", LabelManaged, ManagedValueTrue, secret.Labels[LabelManaged])
	}
}

// TestDynamicSecretPolicyReconciler_ManagedSecretLabelAndCacheScoping verifies that
// materialized secrets are always stamped with LabelManaged="true", allowing the manager's
// cache to strictly filter operator-managed secrets and isolate unmanaged cluster secrets.
func TestDynamicSecretPolicyReconciler_ManagedSecretLabelAndCacheScoping(t *testing.T) {
	scheme := setupTestScheme(t)
	ctx := context.Background()

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redis-cache-policy",
			Namespace: "dso-examples",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			VaultRef: secretv1alpha1.VaultReference{
				KeyVaultURI: "https://kv-test.vault.azure.net",
				ObjectName:  "redis-password",
				ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
			},
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "redis-client",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(policy).
		Build()

	r := &DynamicSecretPolicyReconciler{
		Client: fakeClient,
		Scheme: scheme,
		SecretFetcher: &mockSecretFetcher{
			getSecretFunc: func(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
				return &azure.SecretPayload{
					Value:   []byte("redis-super-secret-password-123"),
					Version: "rev1",
				}, nil
			},
		},
	}

	rev, err := r.materializeSecretRevision(ctx, policy)
	if err != nil {
		t.Fatalf("materializeSecretRevision failed: %v", err)
	}

	secretName := fmt.Sprintf("redis-client-redis-password-rev-%s", rev)
	secret := &corev1.Secret{}
	if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: "dso-examples", Name: secretName}, secret); err != nil {
		t.Fatalf("failed to retrieve materialized secret %s: %v", secretName, err)
	}

	// Verify all 4 required security and tracking labels are present
	if secret.Labels[LabelManaged] != ManagedValueTrue {
		t.Errorf("expected label %s=%s, got %q", LabelManaged, ManagedValueTrue, secret.Labels[LabelManaged])
	}
	if secret.Labels[LabelPolicy] != "redis-cache-policy" {
		t.Errorf("expected label %s=%s, got %q", LabelPolicy, "redis-cache-policy", secret.Labels[LabelPolicy])
	}
	if secret.Labels[canary.LabelTargetWorkload] != "redis-client" {
		t.Errorf("expected label %s=%s, got %q", canary.LabelTargetWorkload, "redis-client", secret.Labels[canary.LabelTargetWorkload])
	}
	if secret.Labels[LabelRevision] != rev {
		t.Errorf("expected label %s=%s, got %q", LabelRevision, rev, secret.Labels[LabelRevision])
	}
}

