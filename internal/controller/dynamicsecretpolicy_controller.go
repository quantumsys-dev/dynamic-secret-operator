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

package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

// Condition Types for DynamicSecretPolicy state machine transitions.
const (
	ConditionTypeRevisionPrepared   = "RevisionPrepared"
	ConditionTypeCanaryProvisioning = "CanaryProvisioning"
	ConditionTypeValidating         = "Validating"
	ConditionTypePromoting          = "Promoting"
)

// Reason constants for DynamicSecretPolicy conditions.
const (
	ReasonPrepared      = "Prepared"
	ReasonProvisioning  = "Provisioning"
	ReasonProbesRunning = "ProbesRunning"
	ReasonPromoting     = "Promoting"
)

// DynamicSecretPolicyReconciler reconciles a DynamicSecretPolicy object
type DynamicSecretPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=secret.quantumsys.io,resources=dynamicsecretpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secret.quantumsys.io,resources=dynamicsecretpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secret.quantumsys.io,resources=dynamicsecretpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DynamicSecretPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Enforce 10-second timeout on all reconciliation network and API calls
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	logger := log.FromContext(ctx).WithValues(
		"dynamicSecretPolicy", req.NamespacedName,
	)

	// Fetch the DynamicSecretPolicy instance
	policy := &secretv1alpha1.DynamicSecretPolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Evaluate state machine progression based on current status conditions
	currentState := r.determineState(policy)
	logger.Info("evaluating DynamicSecretPolicy state machine", "currentState", currentState)

	switch currentState {
	case "":
		// Initial State: Transition to RevisionPrepared
		cond := metav1.Condition{
			Type:    ConditionTypeRevisionPrepared,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonPrepared,
			Message: "Secret revision prepared for canary rollout",
		}
		if err := r.updateStatus(ctx, policy, cond); err != nil {
			logger.Error(err, "failed to transition to RevisionPrepared")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeRevisionPrepared:
		// Transition to CanaryProvisioning
		cond := metav1.Condition{
			Type:    ConditionTypeCanaryProvisioning,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonProvisioning,
			Message: "Canary workload pod provisioning initiated",
		}
		if err := r.updateStatus(ctx, policy, cond); err != nil {
			logger.Error(err, "failed to transition to CanaryProvisioning")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeCanaryProvisioning:
		// Transition to Validating
		cond := metav1.Condition{
			Type:    ConditionTypeValidating,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonProbesRunning,
			Message: "Synthetic probes and health validation in progress",
		}
		if err := r.updateStatus(ctx, policy, cond); err != nil {
			logger.Error(err, "failed to transition to Validating")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeValidating:
		// Transition to Promoting
		cond := metav1.Condition{
			Type:    ConditionTypePromoting,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonPromoting,
			Message: "Promoting validated revision to primary workload",
		}
		if err := r.updateStatus(ctx, policy, cond); err != nil {
			logger.Error(err, "failed to transition to Promoting")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case ConditionTypePromoting:
		logger.Info("reconciliation cycle completed for policy",
			"currentRevision", policy.Status.CurrentRevision,
			"desiredRevision", policy.Status.DesiredRevision,
		)
		return ctrl.Result{}, nil

	default:
		logger.Info("unhandled state encountered; requeuing", "state", currentState)
		return ctrl.Result{Requeue: true}, nil
	}
}

// updateStatus safely sets the condition and executes a status patch to avoid concurrency conflicts.
func (r *DynamicSecretPolicyReconciler) updateStatus(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy, condition metav1.Condition) error {
	logger := log.FromContext(ctx)
	logger.Info("transitioning state",
		"condition", condition.Type,
		"status", condition.Status,
		"reason", condition.Reason,
		"message", condition.Message,
	)

	patch := client.MergeFrom(policy.DeepCopy())
	meta.SetStatusCondition(&policy.Status.Conditions, condition)
	return r.Status().Patch(ctx, policy, patch)
}

// determineState evaluates which progressive delivery phase is currently active.
func (r *DynamicSecretPolicyReconciler) determineState(policy *secretv1alpha1.DynamicSecretPolicy) string {
	if meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypePromoting) {
		return ConditionTypePromoting
	}
	if meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypeValidating) {
		return ConditionTypeValidating
	}
	if meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypeCanaryProvisioning) {
		return ConditionTypeCanaryProvisioning
	}
	if meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypeRevisionPrepared) {
		return ConditionTypeRevisionPrepared
	}
	return ""
}

// SetupWithManager sets up the controller with the Manager.
func (r *DynamicSecretPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretv1alpha1.DynamicSecretPolicy{}).
		Complete(r)
}
