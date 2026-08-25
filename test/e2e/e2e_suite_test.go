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
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/e2e-framework/support/kind"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

var (
	testenv         env.Environment
	clusterName     = "dso-e2e-cluster"
	operatorImage   = "quantumsys-dev/dso:e2e"
	systemNamespace = "dso-system"
	crdBasePath     = filepath.Join("..", "..", "config", "crd", "bases")
)

func TestMain(m *testing.M) {
	cfg, err := envconf.NewFromFlags()
	if err != nil {
		fmt.Printf("failed to initialize e2e config: %v\n", err)
		os.Exit(1)
	}

	testenv = env.NewWithConfig(cfg)

	// Setup cluster lifecycle hooks
	testenv.Setup(
		envfuncs.CreateCluster(kind.NewProvider(), clusterName),
		envfuncs.LoadDockerImageToCluster(clusterName, operatorImage),
		envfuncs.CreateNamespace(systemNamespace),
		installCRDs(crdBasePath),
		deployOperator(systemNamespace, operatorImage),
	)

	// Dump debug logs if any feature fails
	testenv.AfterEachFeature(dumpOperatorLogsOnFailure)

	// Teardown cluster after suite
	testenv.Finish(
		envfuncs.DestroyCluster(clusterName),
	)

	os.Exit(testenv.Run(m))
}

// installCRDs applies all CRD manifests from config/crd/bases
func installCRDs(crdDir string) env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		r, err := resources.New(cfg.Client().RESTConfig())
		if err != nil {
			return ctx, fmt.Errorf("failed to create client: %w", err)
		}

		_ = secretv1alpha1.AddToScheme(r.GetScheme())

		crdFiles, err := filepath.Glob(filepath.Join(crdDir, "*.yaml"))
		if err != nil {
			return ctx, fmt.Errorf("failed to glob CRD files: %w", err)
		}

		for _, crdFile := range crdFiles {
			if err := decoder.ApplyWithManifestDir(ctx, r, crdDir, filepath.Base(crdFile), []resources.CreateOption{}); err != nil {
				return ctx, fmt.Errorf("failed applying CRD %s: %w", crdFile, err)
			}
		}
		return ctx, nil
	}
}

// deployOperator creates RBAC and Deployment for dynamic-secret-operator in dso-system
func deployOperator(namespace, image string) env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		r, err := resources.New(cfg.Client().RESTConfig())
		if err != nil {
			return ctx, fmt.Errorf("failed to create client: %w", err)
		}

		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dso-controller-manager",
				Namespace: namespace,
			},
		}

		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "dso-manager-rolebinding",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      sa.Name,
					Namespace: namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "cluster-admin", // Integration E2E runner scope
			},
		}

		replicas := int32(1)
		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "dso-controller-manager",
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/name": "dynamic-secret-operator",
				},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app.kubernetes.io/name": "dynamic-secret-operator",
					},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							"app.kubernetes.io/name": "dynamic-secret-operator",
						},
					},
					Spec: corev1.PodSpec{
						ServiceAccountName: sa.Name,
						Containers: []corev1.Container{
							{
								Name:            "manager",
								Image:           image,
								ImagePullPolicy: corev1.PullNever,
								Command:         []string{"/manager"},
								Args: []string{
									"--leader-elect=false",
								},
								Env: []corev1.EnvVar{
									{
										Name:  "E2E_SYNTHETIC_MODE",
										Value: "true",
									},
								},
							},
						},
					},
				},
			},
		}

		if err := r.Create(ctx, sa); err != nil {
			return ctx, err
		}
		if err := r.Create(ctx, crb); err != nil {
			return ctx, err
		}
		if err := r.Create(ctx, deploy); err != nil {
			return ctx, err
		}

		return ctx, nil
	}
}

// dumpOperatorLogsOnFailure captures pod logs from dso-system if a test step failed
func dumpOperatorLogsOnFailure(ctx context.Context, cfg *envconf.Config, t *testing.T, feat features.Feature) (context.Context, error) {
	if !t.Failed() {
		return ctx, nil
	}

	t.Logf("=== TEST FAILED: Dumping DSO Operator Logs from namespace %s ===", systemNamespace)

	clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
	if err != nil {
		t.Logf("failed to build clientset for log dump: %v", err)
		return ctx, nil
	}

	podList, err := clientset.CoreV1().Pods(systemNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=dynamic-secret-operator",
	})
	if err != nil {
		t.Logf("failed listing operator pods: %v", err)
		return ctx, nil
	}

	for _, pod := range podList.Items {
		t.Logf("--- Pod: %s (Phase: %s) ---", pod.Name, pod.Status.Phase)
		req := clientset.CoreV1().Pods(systemNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
		podLogs, err := req.Stream(ctx)
		if err != nil {
			t.Logf("error opening log stream for %s: %v", pod.Name, err)
			continue
		}
		buf := new(bytes.Buffer)
		_, _ = io.Copy(buf, podLogs)
		_ = podLogs.Close()
		t.Logf("Logs for %s:\n%s\n", pod.Name, buf.String())
	}

	return ctx, nil
}
