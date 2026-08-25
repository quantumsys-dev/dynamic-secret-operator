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

var _ WorkloadAdapter = &StatefulSetAdapter{}

// StatefulSetAdapter manages progressive secret rotation for Kubernetes StatefulSets.
// For canary validation, it derives an isolated 1-replica ephemeral Deployment using the
// StatefulSet's PodTemplateSpec to prevent storage allocation/PVC binding conflicts.
type StatefulSetAdapter struct {
	statefulSet *appsv1.StatefulSet
}

// NewStatefulSetAdapter creates a new StatefulSetAdapter instance.
func NewStatefulSetAdapter() *StatefulSetAdapter {
	return &StatefulSetAdapter{
		statefulSet: &appsv1.StatefulSet{},
	}
}

func (a *StatefulSetAdapter) Kind() string {
	return KindStatefulSet
}

func (a *StatefulSetAdapter) TargetObject() client.Object {
	return a.statefulSet
}

func (a *StatefulSetAdapter) Fetch(ctx context.Context, c client.Client, key types.NamespacedName) error {
	a.statefulSet = &appsv1.StatefulSet{}
	if err := c.Get(ctx, key, a.statefulSet); err != nil {
		return fmt.Errorf("failed to fetch target statefulset %q: %w", key.Name, err)
	}
	return nil
}

func (a *StatefulSetAdapter) BuildCanary(policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) *appsv1.Deployment {
	return canary.BuildCanaryFromTemplate(a.statefulSet.Name, &a.statefulSet.Spec.Template, policy, newSecretName)
}

func (a *StatefulSetAdapter) Promote(ctx context.Context, c client.Client, policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) error {
	original := a.statefulSet.DeepCopy()
	MutatePodTemplateSpec(&a.statefulSet.Spec.Template, a.statefulSet.Name, policy, newSecretName)
	if err := c.Patch(ctx, a.statefulSet, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch statefulset %q: %w", a.statefulSet.Name, err)
	}
	return nil
}
