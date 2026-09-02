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

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

const (
	labelRevision = "dso.quantumsys.dev/revision"
)

func TestHappyPath_Promotion(t *testing.T) {
	testNs := "dso-happy-path"
	targetDeployName := "nginx-workload"
	policyName := "nginx-secret-policy"

	feat := features.New("Workload Progressive Promotion").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}
			_ = secretv1alpha1.AddToScheme(r.GetScheme())

			// 1. Create Namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNs}}
			if err := r.Create(ctx, ns); err != nil {
				t.Fatalf("failed creating namespace %s: %v", testNs, err)
			}

			// 2. Create Initial Secret for target workload
			initialSec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "initial-secret",
					Namespace: testNs,
				},
				Data: map[string][]byte{
					"api-key": []byte("initial-key-secret-1234"),
				},
			}
			if err := r.Create(ctx, initialSec); err != nil {
				t.Fatalf("failed creating initial secret: %v", err)
			}

			// 3. Create Target Workload Deployment
			replicas := int32(2)
			targetDeploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      targetDeployName,
					Namespace: testNs,
					Labels: map[string]string{
						"app": targetDeployName,
					},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
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
									Name:  "nginx",
									Image: "nginx:alpine",
									Env: []corev1.EnvVar{
										{
											Name: "API_KEY",
											ValueFrom: &corev1.EnvVarSource{
												SecretKeyRef: &corev1.SecretKeySelector{
													LocalObjectReference: corev1.LocalObjectReference{
														Name: "initial-secret",
													},
													Key: "api-key",
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
			if err := r.Create(ctx, targetDeploy); err != nil {
				t.Fatalf("failed creating target deployment: %v", err)
			}

			// Wait for target deployment to become available
			if err := wait.For(conditions.New(r).DeploymentConditionMatch(targetDeploy, appsv1.DeploymentAvailable, corev1.ConditionTrue), wait.WithTimeout(time.Minute*2)); err != nil {
				t.Logf("warning: deployment availability check timed out: %v", err)
			}

			// 3.1 Create Target Workload Service so in-cluster validation probe resolves
			targetSvc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      targetDeployName,
					Namespace: testNs,
				},
				Spec: corev1.ServiceSpec{
					Selector: map[string]string{"app": targetDeployName},
					Ports: []corev1.ServicePort{
						{
							Port:       80,
							TargetPort: intstr.FromInt(80),
						},
					},
				},
			}
			if err := r.Create(ctx, targetSvc); err != nil {
				t.Fatalf("failed creating target service: %v", err)
			}

			// 4. Apply DynamicSecretPolicy pointing to target workload
			policy := &secretv1alpha1.DynamicSecretPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      policyName,
					Namespace: testNs,
				},
				Spec: secretv1alpha1.DynamicSecretPolicySpec{
					VaultRef: secretv1alpha1.VaultReference{
						KeyVaultURI: "https://synthetic-vault.vault.azure.net",
						ObjectName:  "api-key",
						ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
					},
					WorkloadSelector: secretv1alpha1.WorkloadSelector{
						Kind: "Deployment",
						Name: targetDeployName,
					},
					ValidationProbes: []secretv1alpha1.ValidationProbe{
						{
							Type:         secretv1alpha1.ProbeTypeHTTP,
							Endpoint:     fmt.Sprintf("http://%s.%s.svc.cluster.local:80/", targetDeployName, testNs),
							QueryTimeout: 5,
						},
					},
					RollbackConfig: &secretv1alpha1.RollbackConfig{
						AutoRollback:            true,
						CircuitBreakerThreshold: 3,
					},
				},
			}
			if err := r.Create(ctx, policy); err != nil {
				t.Fatalf("failed creating DynamicSecretPolicy: %v", err)
			}

			return ctx
		}).
		Assess("Verify Secret Materialization, Progressive Promotion and Canary Cleanup", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}
			_ = secretv1alpha1.AddToScheme(r.GetScheme())

			// Assertion 1: Wait for SecretRevision creation with label
			t.Log("Assertion 1: Waiting for immutable SecretRevision materialization...")
			err = wait.For(func(ctx context.Context) (bool, error) {
				secretList := &corev1.SecretList{}
				if err := r.List(ctx, secretList, resources.WithFieldSelector(fmt.Sprintf("metadata.namespace=%s", testNs))); err != nil {
					return false, err
				}
				for _, s := range secretList.Items {
					if rev, ok := s.Labels[labelRevision]; ok && rev != "" {
						t.Logf("Materialized secret %s found with revision %s", s.Name, rev)
						return true, nil
					}
				}
				return false, nil
			}, wait.WithTimeout(time.Second*30), wait.WithInterval(time.Second*1))
			if err != nil {
				t.Fatalf("SecretRevision was not materialized: %v", err)
			}

			// Assertion 2: Wait for Target Deployment PodTemplate revision annotation update
			t.Log("Assertion 2: Waiting for target deployment promotion and rollover...")
			err = wait.For(func(ctx context.Context) (bool, error) {
				deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: targetDeployName, Namespace: testNs}}
				if err := r.Get(ctx, targetDeployName, testNs, deploy); err != nil {
					return false, err
				}
				if deploy.Spec.Template.Annotations != nil && deploy.Spec.Template.Annotations[labelRevision] != "" {
					t.Logf("Target deployment rolled over with revision %s", deploy.Spec.Template.Annotations[labelRevision])
					return true, nil
				}
				return false, nil
			}, wait.WithTimeout(time.Second*45), wait.WithInterval(time.Second*1))
			if err != nil {
				t.Fatalf("Target deployment was not promoted: %v", err)
			}

			// Assertion 3: Verify Canary network policy and deployment are cleaned up
			t.Log("Assertion 3: Verifying canary isolation cleanup...")
			err = wait.For(func(ctx context.Context) (bool, error) {
				netpols := &networkingv1.NetworkPolicyList{}
				if err := r.List(ctx, netpols, resources.WithFieldSelector(fmt.Sprintf("metadata.namespace=%s", testNs))); err != nil {
					return false, err
				}
				for _, np := range netpols.Items {
					if np.Name == fmt.Sprintf("%s-canary-netpol", targetDeployName) {
						return false, nil // Still exists
					}
				}
				return true, nil
			}, wait.WithTimeout(time.Second*30), wait.WithInterval(time.Second*1))
			if err != nil {
				t.Fatalf("Canary NetworkPolicy was not destroyed after promotion: %v", err)
			}

			// Assertion 4: Verify DynamicSecretPolicy Status reflects CurrentRevision
			t.Log("Assertion 4: Verifying policy status reflects completed revision...")
			err = wait.For(func(ctx context.Context) (bool, error) {
				pol := &secretv1alpha1.DynamicSecretPolicy{}
				if err := r.Get(ctx, policyName, testNs, pol); err != nil {
					return false, err
				}
				return pol.Status.CurrentRevision != "", nil
			}, wait.WithTimeout(time.Second*30), wait.WithInterval(time.Second*1))
			if err != nil {
				t.Fatalf("DynamicSecretPolicy status.currentRevision is empty: %v", err)
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err == nil {
				ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNs}}
				_ = r.Delete(ctx, ns)
			}
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)
}

func TestFailurePath_RollbackAndCircuitBreaker(t *testing.T) {
	testNs := "dso-failure-path"
	targetDeployName := "payment-workload"
	policyName := "payment-secret-policy"

	feat := features.New("Workload Rollback and Circuit Breaker").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}
			_ = secretv1alpha1.AddToScheme(r.GetScheme())

			// 1. Create Namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNs}}
			if err := r.Create(ctx, ns); err != nil {
				t.Fatalf("failed creating namespace %s: %v", testNs, err)
			}

			// 2. Create Target Workload Deployment
			replicas := int32(1)
			targetDeploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      targetDeployName,
					Namespace: testNs,
					Labels: map[string]string{
						"app": targetDeployName,
					},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": targetDeployName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": targetDeployName},
							Annotations: map[string]string{
								"initial-version": "baseline-untouched",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "app",
									Image: "nginx:alpine",
								},
							},
						},
					},
				},
			}
			if err := r.Create(ctx, targetDeploy); err != nil {
				t.Fatalf("failed creating target deployment: %v", err)
			}

			// 3. Create DynamicSecretPolicy with unreachable probe endpoint to guarantee probe failure
			policy := &secretv1alpha1.DynamicSecretPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      policyName,
					Namespace: testNs,
				},
				Spec: secretv1alpha1.DynamicSecretPolicySpec{
					VaultRef: secretv1alpha1.VaultReference{
						KeyVaultURI: "https://synthetic-vault.vault.azure.net",
						ObjectName:  "db-credentials",
						ObjectType:  secretv1alpha1.VaultObjectTypeSecret,
					},
					WorkloadSelector: secretv1alpha1.WorkloadSelector{
						Kind: "Deployment",
						Name: targetDeployName,
					},
					ValidationProbes: []secretv1alpha1.ValidationProbe{
						{
							Type:         secretv1alpha1.ProbeTypeHTTP,
							Endpoint:     "http://127.0.0.1:9999/unreachable-endpoint-fail",
							QueryTimeout: 2,
						},
					},
					RollbackConfig: &secretv1alpha1.RollbackConfig{
						AutoRollback:            true,
						CircuitBreakerThreshold: 3,
					},
				},
			}
			if err := r.Create(ctx, policy); err != nil {
				t.Fatalf("failed creating DynamicSecretPolicy: %v", err)
			}

			return ctx
		}).
		Assess("Verify Circuit Breaker Trips and Production Deployment Remains Untouched", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}
			_ = secretv1alpha1.AddToScheme(r.GetScheme())

			// Assertion 1: Wait for consecutive failures to increment & Circuit Breaker to trip
			t.Log("Assertion 1: Waiting for circuit breaker condition to trip...")
			err = wait.For(func(ctx context.Context) (bool, error) {
				pol := &secretv1alpha1.DynamicSecretPolicy{}
				if err := r.Get(ctx, policyName, testNs, pol); err != nil {
					return false, err
				}
				if pol.Status.ConsecutiveFailures >= 3 && meta.IsStatusConditionTrue(pol.Status.Conditions, "CircuitBreakerTripped") {
					t.Logf("Circuit breaker tripped: ConsecutiveFailures=%d, Condition=%+v", pol.Status.ConsecutiveFailures, pol.Status.Conditions)
					return true, nil
				}
				return false, nil
			}, wait.WithTimeout(time.Second*60), wait.WithInterval(time.Second*2))
			if err != nil {
				t.Fatalf("Circuit breaker did not trip as expected: %v", err)
			}

			// Assertion 2: Verify target production deployment was NOT updated / untouched
			t.Log("Assertion 2: Verifying production deployment is UNTOUCHED...")
			deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: targetDeployName, Namespace: testNs}}
			if err := r.Get(ctx, targetDeployName, testNs, deploy); err != nil {
				t.Fatalf("failed to get production deployment: %v", err)
			}
			if deploy.Spec.Template.Annotations[labelRevision] != "" {
				t.Fatalf("CRITICAL: Production deployment was incorrectly modified with revision: %s", deploy.Spec.Template.Annotations[labelRevision])
			}
			if deploy.Spec.Template.Annotations["initial-version"] != "baseline-untouched" {
				t.Fatalf("CRITICAL: Initial deployment annotation was modified: %+v", deploy.Spec.Template.Annotations)
			}

			// Assertion 3: Verify all canary resources are cleaned up / destroyed
			t.Log("Assertion 3: Verifying no lingering canary resources exist...")
			netpols := &networkingv1.NetworkPolicyList{}
			if err := r.List(ctx, netpols, resources.WithFieldSelector(fmt.Sprintf("metadata.namespace=%s", testNs))); err == nil {
				for _, np := range netpols.Items {
					if np.Name == fmt.Sprintf("%s-canary-netpol", targetDeployName) {
						t.Fatalf("Lingering canary network policy found: %s", np.Name)
					}
				}
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			r, err := resources.New(cfg.Client().RESTConfig())
			if err == nil {
				ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNs}}
				_ = r.Delete(ctx, ns)
			}
			return ctx
		}).
		Feature()

	testenv.Test(t, feat)
}
