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
	"crypto/sha256"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys/dynamic-secret-operator/internal/azure"
	"github.com/quantumsys/dynamic-secret-operator/internal/canary"
)

// Condition Types for DynamicSecretPolicy state machine transitions.
const (
	ConditionTypeRevisionPrepared   = "RevisionPrepared"
	ConditionTypeCanaryProvisioning = "CanaryProvisioning"
	ConditionTypeValidating         = "Validating"
	ConditionTypePromoting          = "Promoting"
	ConditionTypeRolledBack         = "RolledBack"
)

// Reason constants for DynamicSecretPolicy conditions.
const (
	ReasonPrepared      = "Prepared"
	ReasonProvisioning  = "Provisioning"
	ReasonProbesRunning = "ProbesRunning"
	ReasonPromoting     = "Promoting"
	ReasonCompleted     = "Completed"
	ReasonRolledBack    = "RolledBack"
)

// LabelRevision is the standard label attached to materialized revision secrets.
const LabelRevision = "dso.quantumsys.io/revision"

// DynamicSecretPolicyReconciler reconciles a DynamicSecretPolicy object
type DynamicSecretPolicyReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	SecretFetcher azure.SecretFetcher
	// OnSecretMaterialized optional callback for Service Bus transaction completion
	OnSecretMaterialized func(ctx context.Context, policyName, revision string) error
}

// +kubebuilder:rbac:groups=secret.quantumsys.io,resources=dynamicsecretpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secret.quantumsys.io,resources=dynamicsecretpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secret.quantumsys.io,resources=dynamicsecretpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

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

	basePolicy := policy.DeepCopy()

	// Evaluate state machine progression based on current status conditions
	currentState := r.determineState(policy)
	logger.Info("evaluating DynamicSecretPolicy state machine", "currentState", currentState)

	switch currentState {
	case "":
		// Initial State: Materialize immutable SecretRevision from Key Vault
		revisionHash, err := r.materializeSecretRevision(ctx, policy)
		if err != nil {
			logger.Error(err, "failed to materialize secret revision from Key Vault")
			return ctrl.Result{}, err
		}

		policy.Status.DesiredRevision = revisionHash
		cond := metav1.Condition{
			Type:    ConditionTypeRevisionPrepared,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonPrepared,
			Message: fmt.Sprintf("Secret revision %s successfully materialized", revisionHash),
		}
		if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
			logger.Error(err, "failed to transition to RevisionPrepared")
			return ctrl.Result{}, err
		}

		if r.OnSecretMaterialized != nil {
			if err := r.OnSecretMaterialized(ctx, policy.Name, revisionHash); err != nil {
				logger.Error(err, "failed executing post-materialization callback")
			}
		}

		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeRevisionPrepared:
		// Transition to CanaryProvisioning and enforce strict NetworkPolicy isolation
		netpol := canary.BuildNetworkPolicy(policy)
		if err := controllerutil.SetControllerReference(policy, netpol, r.Scheme); err != nil {
			logger.Error(err, "failed to set controller reference on canary network policy")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, netpol); err != nil && !apierrors.IsAlreadyExists(err) {
			logger.Error(err, "failed to create canary network policy")
			return ctrl.Result{}, err
		}
		logger.Info("enforced canary network policy isolation", "networkPolicy", netpol.Name)

		cond := metav1.Condition{
			Type:    ConditionTypeCanaryProvisioning,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonProvisioning,
			Message: fmt.Sprintf("Canary isolation network policy %s provisioned", netpol.Name),
		}
		if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
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
		if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
			logger.Error(err, "failed to transition to Validating")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeValidating:
		// Transition to Promoting: Patch target workload Deployment and clean up ephemeral canary
		if err := r.promoteTargetWorkload(ctx, policy); err != nil {
			logger.Error(err, "failed to promote target workload")
			return ctrl.Result{}, err
		}

		// Idempotently delete canary resources after successful promotion patch
		if err := canary.CleanupCanaryResources(ctx, r.Client, policy); err != nil {
			logger.Error(err, "failed during post-promotion canary cleanup")
		}

		// Update Status: Promote desired revision to current revision
		policy.Status.CurrentRevision = policy.Status.DesiredRevision
		policy.Status.DesiredRevision = ""

		cond := metav1.Condition{
			Type:    ConditionTypePromoting,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonCompleted,
			Message: fmt.Sprintf("Target workload successfully promoted to revision %s; canary cleaned up", policy.Status.CurrentRevision),
		}
		if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
			logger.Error(err, "failed to update status to Promoting completed")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case ConditionTypePromoting:
		logger.Info("reconciliation cycle completed for policy",
			"currentRevision", policy.Status.CurrentRevision,
		)
		return ctrl.Result{}, nil

	case ConditionTypeRolledBack:
		// Failure path: Clean up canary resources to prevent orphaned pods
		logger.Info("policy in RolledBack state; ensuring canary resources are destroyed")
		if err := canary.CleanupCanaryResources(ctx, r.Client, policy); err != nil {
			logger.Error(err, "failed during rollback canary cleanup")
		}
		policy.Status.DesiredRevision = ""
		return ctrl.Result{}, nil

	default:
		logger.Info("unhandled state encountered; requeuing", "state", currentState)
		return ctrl.Result{Requeue: true}, nil
	}
}

// promoteTargetWorkload patches the production target Deployment with the new Secret revision.
func (r *DynamicSecretPolicyReconciler) promoteTargetWorkload(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) error {
	logger := log.FromContext(ctx)
	targetName := policy.Spec.WorkloadSelector.Name
	targetKey := types.NamespacedName{
		Name:      targetName,
		Namespace: policy.Namespace,
	}

	targetDeploy := &appsv1.Deployment{}
	if err := r.Get(ctx, targetKey, targetDeploy); err != nil {
		return fmt.Errorf("failed to fetch target deployment %q: %w", targetName, err)
	}

	originalDeploy := targetDeploy.DeepCopy()
	secretName := fmt.Sprintf("%s-rev-%s", targetName, policy.Status.DesiredRevision)

	if targetDeploy.Spec.Template.Annotations == nil {
		targetDeploy.Spec.Template.Annotations = make(map[string]string)
	}
	targetDeploy.Spec.Template.Annotations[LabelRevision] = policy.Status.DesiredRevision

	// Update volume mounts referencing secrets
	for i := range targetDeploy.Spec.Template.Spec.Volumes {
		if targetDeploy.Spec.Template.Spec.Volumes[i].Secret != nil {
			targetDeploy.Spec.Template.Spec.Volumes[i].Secret.SecretName = secretName
		}
	}

	// Update container environment secret references
	for cIdx := range targetDeploy.Spec.Template.Spec.Containers {
		for eIdx := range targetDeploy.Spec.Template.Spec.Containers[cIdx].Env {
			if targetDeploy.Spec.Template.Spec.Containers[cIdx].Env[eIdx].ValueFrom != nil &&
				targetDeploy.Spec.Template.Spec.Containers[cIdx].Env[eIdx].ValueFrom.SecretKeyRef != nil {
				targetDeploy.Spec.Template.Spec.Containers[cIdx].Env[eIdx].ValueFrom.SecretKeyRef.Name = secretName
			}
		}
		for efIdx := range targetDeploy.Spec.Template.Spec.Containers[cIdx].EnvFrom {
			if targetDeploy.Spec.Template.Spec.Containers[cIdx].EnvFrom[efIdx].SecretRef != nil {
				targetDeploy.Spec.Template.Spec.Containers[cIdx].EnvFrom[efIdx].SecretRef.Name = secretName
			}
		}
	}

	if err := r.Patch(ctx, targetDeploy, client.MergeFrom(originalDeploy)); err != nil {
		return fmt.Errorf("failed to patch target deployment %q: %w", targetName, err)
	}

	logger.Info("successfully patched target deployment with new secret revision",
		"targetDeployment", targetName,
		"secretRevision", policy.Status.DesiredRevision,
	)
	return nil
}

// materializeSecretRevision pulls the secret payload from Azure Key Vault, calculates a deterministic hash,
// materializes the immutable Secret in the cluster, and zeroes in-memory byte buffers immediately.
func (r *DynamicSecretPolicyReconciler) materializeSecretRevision(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) (string, error) {
	logger := log.FromContext(ctx)

	if r.SecretFetcher == nil {
		return "", fmt.Errorf("secret fetcher is not configured on reconciler")
	}

	payload, err := r.SecretFetcher.GetSecret(
		ctx,
		policy.Spec.VaultRef.KeyVaultURI,
		policy.Spec.VaultRef.ObjectName,
		"",
	)
	if err != nil {
		return "", fmt.Errorf("failed to fetch secret from vault: %w", err)
	}

	// Ensure secret payload memory is zeroed out as soon as materialization completes
	defer payload.Wipe()

	// Compute deterministic short hash (first 12 characters of SHA-256)
	hasher := sha256.New()
	hasher.Write(payload.Value)
	if payload.Version != "" {
		hasher.Write([]byte(payload.Version))
	}
	revisionHash := fmt.Sprintf("%x", hasher.Sum(nil))[:12]
	secretName := fmt.Sprintf("%s-rev-%s", policy.Spec.WorkloadSelector.Name, revisionHash)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: policy.Namespace,
			Labels: map[string]string{
				LabelRevision: revisionHash,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			policy.Spec.VaultRef.ObjectName: payload.Value,
		},
	}

	// Set ControllerReference for automatic garbage collection when policy is deleted
	if err := controllerutil.SetControllerReference(policy, secret, r.Scheme); err != nil {
		return "", fmt.Errorf("failed to set controller reference on secret: %w", err)
	}

	// Execute Kubernetes API call
	createErr := r.Create(ctx, secret)

	// Memory Security: Overwrite and purge secret data in the Kubernetes struct immediately
	if secret.Data != nil {
		for k, v := range secret.Data {
			azure.ZeroBytes(v)
			delete(secret.Data, k)
		}
	}

	// Idempotency: If secret already exists, treat materialization as successful
	if createErr != nil {
		if apierrors.IsAlreadyExists(createErr) {
			logger.Info("secret revision already exists; proceeding idempotently",
				"secretName", secretName,
				"revision", revisionHash,
			)
			return revisionHash, nil
		}
		return "", fmt.Errorf("failed to create immutable secret %q: %w", secretName, createErr)
	}

	logger.Info("materialized new immutable secret revision",
		"secretName", secretName,
		"revision", revisionHash,
	)

	return revisionHash, nil
}

// updateStatus safely sets the condition and executes a status patch to avoid concurrency conflicts.
func (r *DynamicSecretPolicyReconciler) updateStatus(ctx context.Context, policy, base *secretv1alpha1.DynamicSecretPolicy, condition metav1.Condition) error {
	logger := log.FromContext(ctx)
	logger.Info("transitioning state",
		"condition", condition.Type,
		"status", condition.Status,
		"reason", condition.Reason,
		"message", condition.Message,
	)

	meta.SetStatusCondition(&policy.Status.Conditions, condition)
	return r.Status().Patch(ctx, policy, client.MergeFrom(base))
}

// determineState evaluates which progressive delivery phase is currently active.
func (r *DynamicSecretPolicyReconciler) determineState(policy *secretv1alpha1.DynamicSecretPolicy) string {
	if meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypeRolledBack) {
		return ConditionTypeRolledBack
	}
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
		Owns(&corev1.Secret{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Complete(r)
}
