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

	argorolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/canary"
)

var _ WorkloadAdapter = &RolloutAdapter{}

// RolloutAdapter manages progressive secret rotation for Argo Rollout objects (argoproj.io/v1alpha1).
// For canary validation, it derives an isolated 1-replica ephemeral Deployment using the
// Rollout's PodTemplateSpec to safely validate the new secret revision without interfering
// with active Argo Rollout canary/blue-green steps or analysis templates.
type RolloutAdapter struct {
	rollout *argorolloutsv1alpha1.Rollout
}

// NewRolloutAdapter creates a new RolloutAdapter instance.
func NewRolloutAdapter() *RolloutAdapter {
	return &RolloutAdapter{
		rollout: &argorolloutsv1alpha1.Rollout{},
	}
}

func (a *RolloutAdapter) Kind() string {
	return KindRollout
}

func (a *RolloutAdapter) TargetObject() client.Object {
	return a.rollout
}

func (a *RolloutAdapter) Fetch(ctx context.Context, c client.Client, key types.NamespacedName) error {
	a.rollout = &argorolloutsv1alpha1.Rollout{}
	if err := c.Get(ctx, key, a.rollout); err != nil {
		return fmt.Errorf("failed to fetch target rollout %q: %w", key.Name, err)
	}
	return nil
}

func (a *RolloutAdapter) BuildCanary(policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) *appsv1.Deployment {
	return canary.BuildCanaryFromTemplate(a.rollout.Name, &a.rollout.Spec.Template, policy, newSecretName)
}

func (a *RolloutAdapter) Promote(ctx context.Context, c client.Client, policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) error {
	original := a.rollout.DeepCopy()
	MutatePodTemplateSpec(&a.rollout.Spec.Template, a.rollout.Name, policy, newSecretName)
	if err := c.Patch(ctx, a.rollout, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch rollout %q: %w", a.rollout.Name, err)
	}
	return nil
}
