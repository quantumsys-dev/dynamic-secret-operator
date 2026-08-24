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
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys/dynamic-secret-operator/internal/azure"
	"github.com/quantumsys/dynamic-secret-operator/internal/canary"
	"github.com/quantumsys/dynamic-secret-operator/internal/probes"
	"github.com/quantumsys/dynamic-secret-operator/pkg/telemetry"
)

// Condition Types for DynamicSecretPolicy state machine transitions.
const (
	ConditionTypeRevisionPrepared      = "RevisionPrepared"
	ConditionTypeCanaryProvisioning    = "CanaryProvisioning"
	ConditionTypeValidating            = "Validating"
	ConditionTypePromoting             = "Promoting"
	ConditionTypeRolledBack            = "RolledBack"
	ConditionTypeCircuitBreakerTripped = "CircuitBreakerTripped"
)

// Reason constants for DynamicSecretPolicy conditions.
const (
	ReasonPrepared                    = "Prepared"
	ReasonProvisioning                = "Provisioning"
	ReasonProbesRunning               = "ProbesRunning"
	ReasonPromoting                   = "Promoting"
	ReasonCompleted                   = "Completed"
	ReasonRolledBack                  = "RolledBack"
	ReasonValidationThresholdExceeded = "ValidationThresholdExceeded"
	ReasonCircuitBreakerReset         = "CircuitBreakerReset"
)

// LabelRevision is the standard label attached to materialized revision secrets.
const LabelRevision = "dso.quantumsys.io/revision"

// DynamicSecretPolicyReconciler reconciles a DynamicSecretPolicy object
type DynamicSecretPolicyReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	SecretFetcher azure.SecretFetcher
	// EventsChannel allows external event streams (like Azure Service Bus) to trigger reconciliations
	EventsChannel <-chan event.GenericEvent
	// ProbeRunner allows executing or mocking validation probes
	ProbeRunner func(ctx context.Context, probe secretv1alpha1.ValidationProbe, secretData map[string][]byte) error
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

	ctx, span := telemetry.Tracer.Start(ctx, "ReconcileDynamicSecretPolicy",
		trace.WithAttributes(
			attribute.String("policy.name", req.Name),
			attribute.String("policy.namespace", req.Namespace),
		),
	)
	defer span.End()

	logger := log.FromContext(ctx).WithValues(
		"dynamicSecretPolicy", req.NamespacedName,
	)

	// Fetch the DynamicSecretPolicy instance
	policy := &secretv1alpha1.DynamicSecretPolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	basePolicy := policy.DeepCopy()

	// Evaluate Circuit Breaker threshold
	var threshold int32 = 3
	if policy.Spec.RollbackConfig != nil && policy.Spec.RollbackConfig.CircuitBreakerThreshold > 0 {
		threshold = policy.Spec.RollbackConfig.CircuitBreakerThreshold
	}

	// Evaluate state machine progression based on version drift and active rollout phase
	currentState := r.determineState(policy)
	logger.Info("evaluating DynamicSecretPolicy state machine",
		"currentState", currentState,
		"currentRevision", policy.Status.CurrentRevision,
		"desiredRevision", policy.Status.DesiredRevision,
	)
	span.SetAttributes(attribute.String("policy.state", currentState))

	// If Circuit Breaker is tripped during an active rollout, halt reconciliation
	if currentState != "" && policy.Status.ConsecutiveFailures >= threshold {
		logger.Error(nil, "circuit breaker tripped; halting reconciliation loop",
			"consecutiveFailures", policy.Status.ConsecutiveFailures,
			"threshold", threshold,
		)
		telemetry.CircuitBreakersTripped.WithLabelValues(policy.Name, policy.Namespace).Inc()
		span.SetStatus(codes.Error, "Circuit breaker tripped")

		if !meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypeCircuitBreakerTripped) {
			cond := metav1.Condition{
				Type:    ConditionTypeCircuitBreakerTripped,
				Status:  metav1.ConditionTrue,
				Reason:  ReasonValidationThresholdExceeded,
				Message: fmt.Sprintf("Circuit breaker tripped after %d consecutive failures (threshold: %d)", policy.Status.ConsecutiveFailures, threshold),
			}
			_ = r.updateStatus(ctx, policy, basePolicy, cond)
			_ = canary.CleanupCanaryResources(ctx, r.Client, policy)
		}
		// Returning no error and no requeue stops the controller loop until manual intervention or a new event arrives
		return ctrl.Result{}, nil
	}

	switch currentState {
	case "":
		// Check for Version Drift: Materialize upstream secret revision from Key Vault
		revisionHash, err := r.materializeSecretRevision(ctx, policy)
		if err != nil {
			logger.Error(err, "failed to materialize secret revision from Key Vault")
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		// In-Sync Check: If the upstream secret hash equals active CurrentRevision, workload is already up-to-date
		if policy.Status.CurrentRevision != "" && revisionHash == policy.Status.CurrentRevision {
			logger.Info("workload in sync with current secret revision",
				"currentRevision", policy.Status.CurrentRevision,
			)
			return ctrl.Result{}, nil
		}

		// Drift Detected: Initiate new progressive rollout cycle
		telemetry.RotationsTotal.WithLabelValues(policy.Name, policy.Namespace).Inc()

		// Reset failure counters and clear prior rollout / breaker conditions
		policy.Status.ConsecutiveFailures = 0
		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeCircuitBreakerTripped)
		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeRolledBack)
		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypePromoting)
		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeValidating)
		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeCanaryProvisioning)

		policy.Status.DesiredRevision = revisionHash
		cond := metav1.Condition{
			Type:    ConditionTypeRevisionPrepared,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonPrepared,
			Message: fmt.Sprintf("Secret revision %s successfully materialized; initiating rollout", revisionHash),
		}
		if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
			logger.Error(err, "failed to transition to RevisionPrepared")
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		if r.OnSecretMaterialized != nil {
			if err := r.OnSecretMaterialized(ctx, policy.Name, revisionHash); err != nil {
				logger.Error(err, "failed executing post-materialization callback")
			}
		}

		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeRevisionPrepared:
		targetName := policy.Spec.WorkloadSelector.Name
		targetDeploy := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{Name: targetName, Namespace: policy.Namespace}, targetDeploy); err != nil {
			logger.Error(err, "failed to fetch target deployment for canary provisioning", "targetDeployment", targetName)
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		secretName := fmt.Sprintf("%s-rev-%s", targetName, policy.Status.DesiredRevision)

		// 1. Provision Canary Deployment
		canaryDeploy := canary.BuildCanaryDeployment(targetDeploy, policy, secretName)
		if err := controllerutil.SetControllerReference(policy, canaryDeploy, r.Scheme); err != nil {
			logger.Error(err, "failed to set controller reference on canary deployment")
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, canaryDeploy); err != nil && !apierrors.IsAlreadyExists(err) {
			logger.Error(err, "failed to create canary deployment")
			span.RecordError(err)
			return ctrl.Result{}, err
		}
		logger.Info("provisioned canary deployment", "canaryDeployment", canaryDeploy.Name)

		// 2. Provision Canary NetworkPolicy
		netpol := canary.BuildNetworkPolicy(policy)
		if err := controllerutil.SetControllerReference(policy, netpol, r.Scheme); err != nil {
			logger.Error(err, "failed to set controller reference on canary network policy")
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, netpol); err != nil && !apierrors.IsAlreadyExists(err) {
			logger.Error(err, "failed to create canary network policy")
			span.RecordError(err)
			return ctrl.Result{}, err
		}
		logger.Info("enforced canary network policy isolation", "networkPolicy", netpol.Name)

		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeRevisionPrepared)
		cond := metav1.Condition{
			Type:    ConditionTypeCanaryProvisioning,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonProvisioning,
			Message: fmt.Sprintf("Canary deployment %s and isolation policy %s provisioned", canaryDeploy.Name, netpol.Name),
		}
		if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
			logger.Error(err, "failed to transition to CanaryProvisioning")
			span.RecordError(err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeCanaryProvisioning:
		// Transition to Validating
		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeCanaryProvisioning)
		cond := metav1.Condition{
			Type:    ConditionTypeValidating,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonProbesRunning,
			Message: "Synthetic probes and health validation in progress",
		}
		if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
			logger.Error(err, "failed to transition to Validating")
			span.RecordError(err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeValidating:
		// Execute validation probes against the canary workload
		if err := r.runValidationProbes(ctx, policy); err != nil {
			policy.Status.ConsecutiveFailures++
			telemetry.RotationsFailed.WithLabelValues(policy.Name, policy.Namespace).Inc()
			span.RecordError(err)
			logger.Error(err, "validation probe failed",
				"consecutiveFailures", policy.Status.ConsecutiveFailures,
				"threshold", threshold,
			)

			if policy.Status.ConsecutiveFailures >= threshold {
				telemetry.CircuitBreakersTripped.WithLabelValues(policy.Name, policy.Namespace).Inc()
				logger.Error(err, "consecutive failure threshold reached; tripping circuit breaker",
					"failures", policy.Status.ConsecutiveFailures,
					"threshold", threshold,
				)
				cond := metav1.Condition{
					Type:    ConditionTypeCircuitBreakerTripped,
					Status:  metav1.ConditionTrue,
					Reason:  ReasonValidationThresholdExceeded,
					Message: fmt.Sprintf("Circuit breaker tripped after %d consecutive failures (threshold: %d): %v", policy.Status.ConsecutiveFailures, threshold, err),
				}
				_ = r.updateStatus(ctx, policy, basePolicy, cond)
				_ = canary.CleanupCanaryResources(ctx, r.Client, policy)
				return ctrl.Result{}, nil
			}

			// Under threshold: update status and return error to trigger exponential backoff
			cond := metav1.Condition{
				Type:    ConditionTypeValidating,
				Status:  metav1.ConditionFalse,
				Reason:  "ProbeFailed",
				Message: fmt.Sprintf("Validation probe failed: %v", err),
			}
			_ = r.updateStatus(ctx, policy, basePolicy, cond)
			return ctrl.Result{}, err
		}

		// Validation succeeded: reset failure counter and promote
		policy.Status.ConsecutiveFailures = 0

		if err := r.promoteTargetWorkload(ctx, policy); err != nil {
			logger.Error(err, "failed to promote target workload")
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		// Idempotently delete canary resources after successful promotion patch
		if err := canary.CleanupCanaryResources(ctx, r.Client, policy); err != nil {
			logger.Error(err, "failed during post-promotion canary cleanup")
		}

		// Update Status: Promote desired revision to current revision and clear desired revision
		policy.Status.CurrentRevision = policy.Status.DesiredRevision
		policy.Status.DesiredRevision = ""

		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeValidating)
		cond := metav1.Condition{
			Type:    ConditionTypePromoting,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonCompleted,
			Message: fmt.Sprintf("Target workload successfully promoted to revision %s; canary cleaned up", policy.Status.CurrentRevision),
		}
		if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
			logger.Error(err, "failed to update status to Promoting completed")
			span.RecordError(err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil

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

// runValidationProbes iterates over configured probes and executes them sequentially against the canary workload.
func (r *DynamicSecretPolicyReconciler) runValidationProbes(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) error {
	if len(policy.Spec.ValidationProbes) == 0 {
		return nil
	}

	targetName := policy.Spec.WorkloadSelector.Name
	secretName := fmt.Sprintf("%s-rev-%s", targetName, policy.Status.DesiredRevision)

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: policy.Namespace}, secret); err != nil {
		return fmt.Errorf("failed to fetch secret revision %q for probe validation: %w", secretName, err)
	}

	runner := r.ProbeRunner
	if runner == nil {
		runner = func(pCtx context.Context, probe secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
			executor, err := probes.NewProbeExecutor(string(probe.Type))
			if err != nil {
				return err
			}
			return executor.Execute(pCtx, probe, secretData)
		}
	}

	for _, probe := range policy.Spec.ValidationProbes {
		start := time.Now()
		err := runner(ctx, probe, secret.Data)
		telemetry.ProbeDurationSeconds.WithLabelValues(policy.Name, policy.Namespace, string(probe.Type)).Observe(time.Since(start).Seconds())
		if err != nil {
			return err
		}
	}
	return nil
}

// promoteTargetWorkload patches the production target Deployment with the new Secret revision.
// It strictly replaces only operator-managed secret references (<targetWorkload.Name>-rev-<hash>
// or matching the Key Vault object key), preserving all third-party secrets, TLS certificates,
// and unmanaged environment configurations.
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
	newSecretName := fmt.Sprintf("%s-rev-%s", targetName, policy.Status.DesiredRevision)
	managedPrefix := fmt.Sprintf("%s-rev-", targetName)

	if targetDeploy.Spec.Template.Annotations == nil {
		targetDeploy.Spec.Template.Annotations = make(map[string]string)
	}
	targetDeploy.Spec.Template.Annotations[LabelRevision] = policy.Status.DesiredRevision

	// Update only volume mounts referencing operator-managed secrets
	for i := range targetDeploy.Spec.Template.Spec.Volumes {
		vol := &targetDeploy.Spec.Template.Spec.Volumes[i]
		if vol.Secret != nil {
			if strings.HasPrefix(vol.Secret.SecretName, managedPrefix) ||
				(policy.Status.CurrentRevision == "" && vol.Name == policy.Spec.VaultRef.ObjectName) {
				vol.Secret.SecretName = newSecretName
			}
		}
	}

	// Update container environment secret references (targeted replacement)
	for cIdx := range targetDeploy.Spec.Template.Spec.Containers {
		container := &targetDeploy.Spec.Template.Spec.Containers[cIdx]

		for eIdx := range container.Env {
			envVar := &container.Env[eIdx]
			if envVar.ValueFrom != nil && envVar.ValueFrom.SecretKeyRef != nil {
				ref := envVar.ValueFrom.SecretKeyRef
				// Only update if it's already an operator-managed revision or matches the Key Vault secret key
				if strings.HasPrefix(ref.Name, managedPrefix) ||
					ref.Key == policy.Spec.VaultRef.ObjectName ||
					(policy.Status.CurrentRevision != "" && ref.Name == fmt.Sprintf("%s-rev-%s", targetName, policy.Status.CurrentRevision)) {
					ref.Name = newSecretName
				}
			}
		}

		for efIdx := range container.EnvFrom {
			envFrom := &container.EnvFrom[efIdx]
			if envFrom.SecretRef != nil {
				if strings.HasPrefix(envFrom.SecretRef.Name, managedPrefix) ||
					(policy.Status.CurrentRevision != "" && envFrom.SecretRef.Name == fmt.Sprintf("%s-rev-%s", targetName, policy.Status.CurrentRevision)) {
					envFrom.SecretRef.Name = newSecretName
				}
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

// determineState evaluates which progressive delivery phase is currently active based on version drift and rollout state.
func (r *DynamicSecretPolicyReconciler) determineState(policy *secretv1alpha1.DynamicSecretPolicy) string {
	// If there is no active desired revision, we are in-sync or checking drift against upstream
	if policy.Status.DesiredRevision == "" {
		return ""
	}

	// An active rollout is in progress for policy.Status.DesiredRevision
	if meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypeRolledBack) {
		return ConditionTypeRolledBack
	}
	if meta.FindStatusCondition(policy.Status.Conditions, ConditionTypeValidating) != nil {
		return ConditionTypeValidating
	}
	if meta.FindStatusCondition(policy.Status.Conditions, ConditionTypeCanaryProvisioning) != nil {
		return ConditionTypeCanaryProvisioning
	}
	if meta.FindStatusCondition(policy.Status.Conditions, ConditionTypeRevisionPrepared) != nil {
		return ConditionTypeRevisionPrepared
	}

	// If DesiredRevision is set but condition is pending, start rollout at RevisionPrepared
	return ConditionTypeRevisionPrepared
}

// SetupWithManager sets up the controller with the Manager.
func (r *DynamicSecretPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&secretv1alpha1.DynamicSecretPolicy{}).
		Owns(&corev1.Secret{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&appsv1.Deployment{})

	if r.EventsChannel != nil {
		builder = builder.WatchesRawSource(source.Channel(r.EventsChannel, &handler.EnqueueRequestForObject{}))
	}

	return builder.Complete(r)
}
