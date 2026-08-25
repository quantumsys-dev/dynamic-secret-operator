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

package workload

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/canary"
)

var _ WorkloadAdapter = &DeploymentAdapter{}

// DeploymentAdapter manages progressive secret rotation for Kubernetes Deployments.
type DeploymentAdapter struct {
	deployment *appsv1.Deployment
}

// NewDeploymentAdapter creates a new DeploymentAdapter instance.
func NewDeploymentAdapter() *DeploymentAdapter {
	return &DeploymentAdapter{
		deployment: &appsv1.Deployment{},
	}
}

func (a *DeploymentAdapter) Kind() string {
	return KindDeployment
}

func (a *DeploymentAdapter) TargetObject() client.Object {
	return a.deployment
}

func (a *DeploymentAdapter) Fetch(ctx context.Context, c client.Client, key types.NamespacedName) error {
	a.deployment = &appsv1.Deployment{}
	if err := c.Get(ctx, key, a.deployment); err != nil {
		return fmt.Errorf("failed to fetch target deployment %q: %w", key.Name, err)
	}
	return nil
}

func (a *DeploymentAdapter) BuildCanary(policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) *appsv1.Deployment {
	return canary.BuildCanaryFromTemplate(a.deployment.Name, &a.deployment.Spec.Template, policy, newSecretName)
}

func (a *DeploymentAdapter) Promote(ctx context.Context, c client.Client, policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) error {
	original := a.deployment.DeepCopy()
	MutatePodTemplateSpec(&a.deployment.Spec.Template, a.deployment.Name, policy, newSecretName)
	if err := c.Patch(ctx, a.deployment, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch deployment %q: %w", a.deployment.Name, err)
	}
	return nil
}
