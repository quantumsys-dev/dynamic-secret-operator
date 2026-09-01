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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

// CleanupCanaryResources deletes ephemeral canary resources (Deployment, NetworkPolicy,
// CiliumNetworkPolicy, and probe Jobs) idempotently, ignoring NotFound errors.
func CleanupCanaryResources(ctx context.Context, c client.Client, policy *secretv1alpha1.DynamicSecretPolicy) error {
	logger := log.FromContext(ctx).WithValues(
		"policy", policy.Name,
		"namespace", policy.Namespace,
	)
	targetName := policy.Spec.WorkloadSelector.Name

	// 1. Delete Canary Deployment
	canaryDeployName := fmt.Sprintf("%s-canary", targetName)
	canaryDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      canaryDeployName,
			Namespace: policy.Namespace,
		},
	}
	if err := c.Delete(ctx, canaryDeploy); client.IgnoreNotFound(err) != nil {
		logger.Error(err, "failed to delete canary deployment", "canaryDeployment", canaryDeployName)
		return fmt.Errorf("failed to delete canary deployment %q: %w", canaryDeployName, err)
	}
	logger.Info("cleaned up canary deployment", "canaryDeployment", canaryDeployName)

	// 2. Delete Canary NetworkPolicy
	canaryNetpolName := fmt.Sprintf("%s-canary-netpol", targetName)
	canaryNetpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      canaryNetpolName,
			Namespace: policy.Namespace,
		},
	}
	if err := c.Delete(ctx, canaryNetpol); client.IgnoreNotFound(err) != nil {
		logger.Error(err, "failed to delete canary network policy", "networkPolicy", canaryNetpolName)
		return fmt.Errorf("failed to delete canary network policy %q: %w", canaryNetpolName, err)
	}
	logger.Info("cleaned up canary network policy", "networkPolicy", canaryNetpolName)

	// 3. Delete optional Canary CiliumNetworkPolicy
	ciliumNetpolName := fmt.Sprintf("%s-canary-cilium-netpol", targetName)
	ciliumObj := &unstructured.Unstructured{}
	ciliumObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2",
		Kind:    "CiliumNetworkPolicy",
	})
	ciliumObj.SetName(ciliumNetpolName)
	ciliumObj.SetNamespace(policy.Namespace)
	if err := c.Delete(ctx, ciliumObj); client.IgnoreNotFound(err) != nil {
		// Cilium CRD may not exist in cluster, ignore errors
		logger.V(1).Info("cilium network policy cleanup skipped or not found", "error", err)
	}

	// 4. Delete lingering probe Jobs owned by or labeled for this policy
	jobList := &batchv1.JobList{}
	if err := c.List(ctx, jobList, client.InNamespace(policy.Namespace), client.MatchingLabels{
		"dso.quantumsys.dev/policy": policy.Name,
	}); err == nil {
		bg := metav1.DeletePropagationBackground
		for i := range jobList.Items {
			job := &jobList.Items[i]
			if delErr := c.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &bg}); client.IgnoreNotFound(delErr) != nil {
				logger.Error(delErr, "failed to delete probe job", "job", job.Name)
			} else {
				logger.Info("cleaned up probe job", "job", job.Name)
			}
		}
	}

	return nil
}
