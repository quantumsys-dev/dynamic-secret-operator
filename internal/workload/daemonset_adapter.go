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

var _ WorkloadAdapter = &DaemonSetAdapter{}

// DaemonSetAdapter manages progressive secret rotation for Kubernetes DaemonSets.
// For canary validation, it derives an isolated 1-replica ephemeral Deployment using the
// DaemonSet's PodTemplateSpec to safely test secret mounts on a single pod without saturating
// all cluster nodes.
type DaemonSetAdapter struct {
	daemonSet *appsv1.DaemonSet
}

// NewDaemonSetAdapter creates a new DaemonSetAdapter instance.
func NewDaemonSetAdapter() *DaemonSetAdapter {
	return &DaemonSetAdapter{
		daemonSet: &appsv1.DaemonSet{},
	}
}

func (a *DaemonSetAdapter) Kind() string {
	return KindDaemonSet
}

func (a *DaemonSetAdapter) TargetObject() client.Object {
	return a.daemonSet
}

func (a *DaemonSetAdapter) Fetch(ctx context.Context, c client.Client, key types.NamespacedName) error {
	a.daemonSet = &appsv1.DaemonSet{}
	if err := c.Get(ctx, key, a.daemonSet); err != nil {
		return fmt.Errorf("failed to fetch target daemonset %q: %w", key.Name, err)
	}
	return nil
}

func (a *DaemonSetAdapter) BuildCanary(policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) *appsv1.Deployment {
	return canary.BuildCanaryFromTemplate(a.daemonSet.Name, &a.daemonSet.Spec.Template, policy, newSecretName)
}

func (a *DaemonSetAdapter) Promote(ctx context.Context, c client.Client, policy *secretv1alpha1.DynamicSecretPolicy, newSecretName string) error {
	original := a.daemonSet.DeepCopy()
	MutatePodTemplateSpec(&a.daemonSet.Spec.Template, a.daemonSet.Name, policy, newSecretName)
	if err := c.Patch(ctx, a.daemonSet, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to patch daemonset %q: %w", a.daemonSet.Name, err)
	}
	return nil
}
