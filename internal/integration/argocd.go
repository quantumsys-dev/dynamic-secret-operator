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
	"fmt"
	"os"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// EnvArgoCDAutoPatchEnabled controls whether DSO automatically discovers and patches
// parent Argo CD Application ignoreDifferences to prevent Self-Heal drift loops.
const EnvArgoCDAutoPatchEnabled = "ARGOCD_AUTOPATCH_ENABLED"

// Standard Argo CD tracking labels and annotations.
const (
	LabelArgoCDInstance         = "app.kubernetes.io/instance"
	LabelArgoCDTrackingInstance = "argocd.argoproj.io/instance"
	AnnotationArgoCDTrackingID  = "argocd.argoproj.io/tracking-id"
)

// Required DSO JSON pointers for Argo CD ignoreDifferences.
const (
	JSONPointerRevisionAnnotation = "/spec/template/metadata/annotations/dso.quantumsys.dev~1revision"
	JSONPointerVolumes            = "/spec/template/spec/volumes"
)

// IsAutoPatchEnabled returns true if the operator is configured to automatically
// patch Argo CD Application ignoreDifferences.
func IsAutoPatchEnabled() bool {
	return strings.ToLower(os.Getenv(EnvArgoCDAutoPatchEnabled)) == "true"
}

// ReconcileArgoCDIgnoreDifferences discovers the parent Argo CD Application for the given
// workload and ensures that DSO mutations (revision annotation and volume secret changes)
// are listed in spec.ignoreDifferences, retrying with exponential backoff on 409 conflict storms.
func ReconcileArgoCDIgnoreDifferences(
	ctx context.Context,
	c client.Client,
	targetObj client.Object,
	kind string,
) error {
	if !IsAutoPatchEnabled() || targetObj == nil {
		return nil
	}

	logger := log.FromContext(ctx).WithValues(
		"integration", "argocd",
		"workloadName", targetObj.GetName(),
		"workloadNamespace", targetObj.GetNamespace(),
		"workloadKind", kind,
	)

	appName := discoverArgoCDAppName(targetObj)
	if appName == "" {
		logger.V(1).Info("workload has no Argo CD tracking label/annotation; skipping auto-patch")
		return nil
	}

	targetGroup := "apps"
	if kind == "Rollout" {
		targetGroup = "argoproj.io"
	}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		latestApp, fetchErr := fetchArgoCDApplication(ctx, c, appName, targetObj.GetNamespace())
		if fetchErr != nil {
			return fetchErr
		}
		if !ensureIgnoreDifferences(latestApp, targetGroup, kind) {
			return nil // already has required ignoreDifferences
		}
		return c.Update(ctx, latestApp)
	})

	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.V(1).Info("parent Argo CD Application not found in cluster; skipping auto-patch", "application", appName)
			return nil
		}
		logger.Error(err, "failed to update Argo CD Application ignoreDifferences", "application", appName)
		return fmt.Errorf("failed to update Argo CD Application %q: %w", appName, err)
	}

	logger.Info("successfully updated Argo CD Application ignoreDifferences for DSO",
		"application", appName,
		"group", targetGroup,
		"kind", kind,
	)

	return nil
}

// discoverArgoCDAppName extracts the Argo CD Application name from workload metadata.
func discoverArgoCDAppName(obj client.Object) string {
	annotations := obj.GetAnnotations()
	if annotations != nil {
		if trackingID := annotations[AnnotationArgoCDTrackingID]; trackingID != "" {
			parts := strings.Split(trackingID, ":")
			if len(parts) > 0 && parts[0] != "" {
				return parts[0]
			}
		}
	}

	labels := obj.GetLabels()
	if labels != nil {
		if instance := labels[LabelArgoCDTrackingInstance]; instance != "" {
			return instance
		}
		if instance := labels[LabelArgoCDInstance]; instance != "" {
			return instance
		}
	}

	return ""
}

// fetchArgoCDApplication retrieves the Application from the workload namespace or the default "argocd" namespace.
func fetchArgoCDApplication(ctx context.Context, c client.Client, appName, workloadNamespace string) (*Application, error) {
	app := &Application{}

	// 1. Try workload namespace
	if err := c.Get(ctx, types.NamespacedName{Name: appName, Namespace: workloadNamespace}, app); err == nil {
		return app, nil
	}

	// 2. Try default "argocd" namespace
	if err := c.Get(ctx, types.NamespacedName{Name: appName, Namespace: "argocd"}, app); err == nil {
		return app, nil
	}

	// 3. Fallback: Search across all namespaces
	appList := &ApplicationList{}
	if err := c.List(ctx, appList); err == nil {
		for i := range appList.Items {
			if appList.Items[i].Name == appName {
				return &appList.Items[i], nil
			}
		}
	}

	return nil, apierrors.NewNotFound(SchemeGroupVersion.WithResource("applications").GroupResource(), appName)
}

// ensureIgnoreDifferences verifies and appends required JSON pointers to the Application spec.
// Returns true if modifications were made.
func ensureIgnoreDifferences(app *Application, group, kind string) bool {
	requiredPointers := []string{
		JSONPointerRevisionAnnotation,
		JSONPointerVolumes,
	}

	for i := range app.Spec.IgnoreDifferences {
		item := &app.Spec.IgnoreDifferences[i]
		if (item.Group == group || (item.Group == "" && group == "apps")) && item.Kind == kind {
			existingPointers := make(map[string]bool)
			for _, p := range item.JSONPointers {
				existingPointers[p] = true
			}

			modified := false
			for _, req := range requiredPointers {
				if !existingPointers[req] {
					item.JSONPointers = append(item.JSONPointers, req)
					modified = true
				}
			}
			return modified
		}
	}

	// No matching group/kind entry found; create a new one
	app.Spec.IgnoreDifferences = append(app.Spec.IgnoreDifferences, ResourceIgnoreDifferences{
		Group:        group,
		Kind:         kind,
		JSONPointers: requiredPointers,
	})
	return true
}
