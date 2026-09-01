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

package integration

import (
	"context"
	"os"
	"regexp"
	"testing"

	argov1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestJQPathExpressionRevisionVolumes_MatchesOnlyDSOManagedSecretNames verifies that the
// embedded regex correctly identifies DSO-materialized revision secret names (built as
// <target>-<objectName>-rev-<12-hex-hash> by materializeSecretRevision) and rejects unrelated
// volume secret names, so the Argo CD ignoreDifferences override stays scoped to just DSO's own
// volumes rather than reintroducing the original whole-array blind spot.
func TestJQPathExpressionRevisionVolumes_MatchesOnlyDSOManagedSecretNames(t *testing.T) {
	patternMatch := regexp.MustCompile(`test\("([^"]+)"\)`).FindStringSubmatch(JQPathExpressionRevisionVolumes)
	if len(patternMatch) != 2 {
		t.Fatalf("could not extract regex pattern from JQPathExpressionRevisionVolumes: %s", JQPathExpressionRevisionVolumes)
	}
	pattern := regexp.MustCompile(patternMatch[1])

	dsoManagedSecretName := "order-service-db-pass-rev-a1b2c3d4e5f6"
	if !pattern.MatchString(dsoManagedSecretName) {
		t.Errorf("expected DSO-managed secret name %q to match, but it didn't", dsoManagedSecretName)
	}

	unrelatedNames := []string{
		"tls-certificate-secret",
		"my-app-config",
		"order-service-rev-old", // legacy/manual name, not a real 12-hex revision hash
		"",
	}
	for _, name := range unrelatedNames {
		if pattern.MatchString(name) {
			t.Errorf("expected unrelated secret name %q NOT to match, but it did", name)
		}
	}
}

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = appsv1.AddToScheme(s)
	_ = argov1alpha1.AddToScheme(s)
	return s
}

func TestIsAutoPatchEnabled(t *testing.T) {
	_ = os.Unsetenv(EnvArgoCDAutoPatchEnabled)
	if IsAutoPatchEnabled() {
		t.Errorf("expected false when unset")
	}

	_ = os.Setenv(EnvArgoCDAutoPatchEnabled, "false")
	if IsAutoPatchEnabled() {
		t.Errorf("expected false when set to false")
	}

	_ = os.Setenv(EnvArgoCDAutoPatchEnabled, "true")
	if !IsAutoPatchEnabled() {
		t.Errorf("expected true when set to true")
	}

	_ = os.Setenv(EnvArgoCDAutoPatchEnabled, "TRUE")
	if !IsAutoPatchEnabled() {
		t.Errorf("expected true when set to TRUE")
	}
}

func TestReconcileArgoCDIgnoreDifferences_Disabled(t *testing.T) {
	_ = os.Setenv(EnvArgoCDAutoPatchEnabled, "false")
	ctx := context.Background()
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "prod",
			Labels: map[string]string{
				LabelArgoCDInstance: "my-app",
			},
		},
	}

	if err := ReconcileArgoCDIgnoreDifferences(ctx, client, deploy, "Deployment"); err != nil {
		t.Errorf("expected nil error when disabled, got %v", err)
	}
}

func TestReconcileArgoCDIgnoreDifferences_NoTracking(t *testing.T) {
	_ = os.Setenv(EnvArgoCDAutoPatchEnabled, "true")
	ctx := context.Background()
	scheme := newTestScheme()
	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "prod",
		},
	}

	if err := ReconcileArgoCDIgnoreDifferences(ctx, client, deploy, "Deployment"); err != nil {
		t.Errorf("expected nil error when no tracking labels, got %v", err)
	}
}

func TestReconcileArgoCDIgnoreDifferences_PatchesApp(t *testing.T) {
	_ = os.Setenv(EnvArgoCDAutoPatchEnabled, "true")
	ctx := context.Background()
	scheme := newTestScheme()

	app := &argov1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "argocd",
		},
		Spec: argov1alpha1.ApplicationSpec{
			IgnoreDifferences: []argov1alpha1.ResourceIgnoreDifferences{},
		},
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "prod",
			Labels: map[string]string{
				LabelArgoCDInstance: "my-app",
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, deploy).Build()

	if err := ReconcileArgoCDIgnoreDifferences(ctx, client, deploy, "Deployment"); err != nil {
		t.Fatalf("expected successful reconcile, got %v", err)
	}

	updatedApp := &argov1alpha1.Application{}
	if err := client.Get(ctx, types.NamespacedName{Name: "my-app", Namespace: "argocd"}, updatedApp); err != nil {
		t.Fatalf("failed to fetch updated application: %v", err)
	}

	if len(updatedApp.Spec.IgnoreDifferences) != 1 {
		t.Fatalf("expected 1 ignoreDifferences entry, got %d", len(updatedApp.Spec.IgnoreDifferences))
	}

	entry := updatedApp.Spec.IgnoreDifferences[0]
	if entry.Group != "apps" || entry.Kind != "Deployment" {
		t.Errorf("expected entry for apps/Deployment, got %s/%s", entry.Group, entry.Kind)
	}

	if len(entry.JSONPointers) != 1 {
		t.Errorf("expected 1 JSON pointer, got %d", len(entry.JSONPointers))
	}
	if len(entry.JQPathExpressions) != 1 || entry.JQPathExpressions[0] != JQPathExpressionRevisionVolumes {
		t.Errorf("expected 1 JQ path expression for revision volumes, got %v", entry.JQPathExpressions)
	}
}

func TestReconcileArgoCDIgnoreDifferences_RolloutSupport(t *testing.T) {
	_ = os.Setenv(EnvArgoCDAutoPatchEnabled, "true")
	ctx := context.Background()
	scheme := newTestScheme()

	app := &argov1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollout-app",
			Namespace: "argocd",
		},
		Spec: argov1alpha1.ApplicationSpec{
			IgnoreDifferences: []argov1alpha1.ResourceIgnoreDifferences{
				{
					Group: "argoproj.io",
					Kind:  "Rollout",
					JSONPointers: []string{
						JSONPointerRevisionAnnotation,
					},
				},
			},
		},
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollout-service",
			Namespace: "prod",
			Annotations: map[string]string{
				AnnotationArgoCDTrackingID: "rollout-app:argoproj.io/Rollout:prod/rollout-service",
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, deploy).Build()

	if err := ReconcileArgoCDIgnoreDifferences(ctx, client, deploy, "Rollout"); err != nil {
		t.Fatalf("expected successful reconcile, got %v", err)
	}

	updatedApp := &argov1alpha1.Application{}
	if err := client.Get(ctx, types.NamespacedName{Name: "rollout-app", Namespace: "argocd"}, updatedApp); err != nil {
		t.Fatalf("failed to fetch updated application: %v", err)
	}

	if len(updatedApp.Spec.IgnoreDifferences) != 1 {
		t.Fatalf("expected 1 ignoreDifferences entry, got %d", len(updatedApp.Spec.IgnoreDifferences))
	}

	entry := updatedApp.Spec.IgnoreDifferences[0]
	if entry.Group != "argoproj.io" || entry.Kind != "Rollout" {
		t.Errorf("expected entry for argoproj.io/Rollout, got %s/%s", entry.Group, entry.Kind)
	}

	// Should have merged the missing JQ path expression for revision volumes, while keeping the
	// pre-existing revision annotation pointer intact (not duplicated).
	if len(entry.JSONPointers) != 1 {
		t.Errorf("expected 1 JSON pointer after merge, got %d", len(entry.JSONPointers))
	}
	if len(entry.JQPathExpressions) != 1 || entry.JQPathExpressions[0] != JQPathExpressionRevisionVolumes {
		t.Errorf("expected 1 JQ path expression for revision volumes after merge, got %v", entry.JQPathExpressions)
	}
}

func TestReconcileArgoCDIgnoreDifferences_PreservesCustomUserEntries(t *testing.T) {
	_ = os.Setenv(EnvArgoCDAutoPatchEnabled, "true")
	ctx := context.Background()
	scheme := newTestScheme()

	app := &argov1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-app",
			Namespace: "argocd",
		},
		Spec: argov1alpha1.ApplicationSpec{
			IgnoreDifferences: []argov1alpha1.ResourceIgnoreDifferences{
				{
					Group: "apps",
					Kind:  "StatefulSet",
					JSONPointers: []string{
						"/spec/replicas",
					},
				},
				{
					Group: "custom.io",
					Kind:  "CustomResource",
					JSONPointers: []string{
						"/spec/customField",
					},
				},
			},
		},
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "order-service",
			Namespace: "prod",
			Labels: map[string]string{
				LabelArgoCDInstance: "custom-app",
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, deploy).Build()

	if err := ReconcileArgoCDIgnoreDifferences(ctx, client, deploy, "Deployment"); err != nil {
		t.Fatalf("expected successful reconcile, got %v", err)
	}

	updatedApp := &argov1alpha1.Application{}
	if err := client.Get(ctx, types.NamespacedName{Name: "custom-app", Namespace: "argocd"}, updatedApp); err != nil {
		t.Fatalf("failed to fetch updated application: %v", err)
	}

	// Must now have 3 entries (2 pre-existing custom ones + 1 DSO deployment entry)
	if len(updatedApp.Spec.IgnoreDifferences) != 3 {
		t.Fatalf("CRITICAL BUG: user-defined ignoreDifferences were wiped! Expected 3 entries, got %d", len(updatedApp.Spec.IgnoreDifferences))
	}

	// Verify custom entries were preserved
	if updatedApp.Spec.IgnoreDifferences[0].Kind != "StatefulSet" || updatedApp.Spec.IgnoreDifferences[0].JSONPointers[0] != "/spec/replicas" {
		t.Errorf("expected StatefulSet entry preserved untouched")
	}
	if updatedApp.Spec.IgnoreDifferences[1].Kind != "CustomResource" || updatedApp.Spec.IgnoreDifferences[1].JSONPointers[0] != "/spec/customField" {
		t.Errorf("expected CustomResource entry preserved untouched")
	}
	// Verify DSO entry was appended
	dsoEntry := updatedApp.Spec.IgnoreDifferences[2]
	if dsoEntry.Kind != "Deployment" || len(dsoEntry.JSONPointers) != 1 || len(dsoEntry.JQPathExpressions) != 1 {
		t.Errorf("expected Deployment entry correctly added with 1 JSON pointer and 1 JQ path expression, got %+v", dsoEntry)
	}
}

