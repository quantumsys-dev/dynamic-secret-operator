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
)

var _ WorkloadAdapter = &RolloutAdapter{}

// RolloutAdapter manages progressive secret rotation for Argo Rollout objects (argoproj.io/v1alpha1).
// Unlike the other adapters, it does not provision a DSO-managed synthetic canary: patching
// spec.template already triggers Argo Rollout's own canary/blueGreen progressive delivery and
// AnalysisRuns, so running a second, independent canary mechanism alongside it would fight the
// GitOps controller rather than cooperate with it. DSO instead patches the Rollout directly and
// relies on the reconciler to watch Rollout.Status.Phase for a Healthy result (see
// ConditionTypeRolloutProgressing in the controller) before finalizing the promotion.
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

// BuildCanary always returns nil for Rollout targets: DSO does not provision a synthetic canary
// here (see the RolloutAdapter doc comment for why), so callers must check for a nil result and
// patch the Rollout directly instead of provisioning a canary Deployment.
func (a *RolloutAdapter) BuildCanary(policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) *appsv1.Deployment {
	return nil
}

func (a *RolloutAdapter) Promote(ctx context.Context, c client.Client, policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) error {
	original := a.rollout.DeepCopy()
	MutatePodTemplateSpec(&a.rollout.Spec.Template, a.rollout.Name, policy, newSecretName)
	if err := c.Patch(ctx, a.rollout, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch rollout %q: %w", a.rollout.Name, err)
	}
	return nil
}
