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
	"encoding/pem"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	argorolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/azure"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/canary"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/integration"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/probes"
	sourceProvider "github.com/quantumsys-dev/dynamic-secret-operator/internal/source"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/workload"
	"github.com/quantumsys-dev/dynamic-secret-operator/pkg/telemetry"
)

// Condition Types for DynamicSecretPolicy state machine transitions.
const (
	ConditionTypeRevisionPrepared   = "RevisionPrepared"
	ConditionTypeCanaryProvisioning = "CanaryProvisioning"
	ConditionTypeValidating         = "Validating"
	// ConditionTypeRolloutProgressing indicates an Argo Rollout target has been patched with the
	// new secret revision and DSO is awaiting Rollout.Status.Phase to report Healthy before
	// finalizing the promotion. Only reachable for Rollout targets; see RolloutAdapter.BuildCanary.
	ConditionTypeRolloutProgressing    = "RolloutProgressing"
	ConditionTypePromoting             = "Promoting"
	ConditionTypeRolledBack            = "RolledBack"
	ConditionTypeCircuitBreakerTripped = "CircuitBreakerTripped"
)

// Reason constants for DynamicSecretPolicy conditions.
const (
	ReasonPrepared                    = "Prepared"
	ReasonProvisioning                = "Provisioning"
	ReasonProbesRunning               = "ProbesRunning"
	ReasonRolloutProgressing          = "RolloutProgressing"
	ReasonRolloutDegraded             = "RolloutDegraded"
	ReasonPromoting                   = "Promoting"
	ReasonCompleted                   = "Completed"
	ReasonRolledBack                  = "RolledBack"
	ReasonValidationThresholdExceeded = "ValidationThresholdExceeded"
	ReasonCircuitBreakerReset         = "CircuitBreakerReset"
)

// LabelRevision is the standard label attached to materialized revision secrets.
const LabelRevision = "dso.quantumsys.dev/revision"

// LabelPolicy identifies the DynamicSecretPolicy owning the materialized revision secret.
const LabelPolicy = "dso.quantumsys.dev/policy"

// LabelManaged identifies secrets the Dynamic Secret Operator cares about, so the shared
// manager cache (see cmd/main.go) can stay scoped to just these secrets instead of every
// Secret in the cluster. It carries one of two values: ManagedValueTrue for secrets DSO
// itself creates and owns (materialized revisions), or ManagedValueWatch for externally
// owned source secrets DSO only observes (e.g. an ESO-synced intermediate secret referenced
// by a K8sSecret source). Users must apply ManagedValueWatch to their source secret (for
// example via ExternalSecret's target.template.metadata.labels) for DSO to detect its changes.
const LabelManaged = "dso.quantumsys.dev/managed"

// ManagedValueTrue represents a secret created and owned by DSO.
const ManagedValueTrue = "true"

// ManagedValueWatch represents an externally owned source secret DSO watches but does not own.
const ManagedValueWatch = "watch"

// DynamicSecretPolicyReconciler reconciles a DynamicSecretPolicy object
type DynamicSecretPolicyReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	SecretFetcher azure.SecretFetcher
	// ProviderRegistry manages pluggable secret ingestion backends (Azure, ESO K8sSecret, AWS, GCP, Vault)
	ProviderRegistry *sourceProvider.Registry
	// KubeClient allows retrieving pod failure logs from Job probes
	KubeClient kubernetes.Interface
	// MaxConcurrentReconciles controls worker parallelism in controller-runtime
	MaxConcurrentReconciles int
	// EventsChannel allows external event streams (like Azure Service Bus) to trigger reconciliations
	EventsChannel <-chan event.GenericEvent
	// ProbeRunner allows executing or mocking validation probes
	ProbeRunner func(ctx context.Context, probe secretv1alpha1.ValidationProbe, secretData map[string][]byte) error
	// OnSecretMaterialized optional callback for Service Bus transaction completion
	OnSecretMaterialized func(ctx context.Context, policyName, revision string) error
}

// +kubebuilder:rbac:groups=dso.quantumsys.dev,resources=dynamicsecretpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dso.quantumsys.dev,resources=dynamicsecretpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dso.quantumsys.dev,resources=dynamicsecretpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get;list
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=argoproj.io,resources=rollouts;applications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cilium.io,resources=ciliumnetworkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DynamicSecretPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, span := telemetry.Tracer.Start(ctx, "ReconcileDynamicSecretPolicy",
		trace.WithAttributes(
			attribute.String("policy.name", req.Name),
			attribute.String("policy.namespace", req.Namespace),
		),
	)
	defer span.End()

	logger := log.FromContext(ctx).WithValues(
		"policy_name", req.Name,
		"namespace", req.Namespace,
		"dynamicSecretPolicy", req.NamespacedName,
	)
	ctx = log.IntoContext(ctx, logger)

	// Fetch the DynamicSecretPolicy instance
	policy := &secretv1alpha1.DynamicSecretPolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	basePolicy := policy.DeepCopy()

	// Enrich logger context with current or desired revision hash
	activeRevision := policy.Status.DesiredRevision
	if activeRevision == "" {
		activeRevision = policy.Status.CurrentRevision
	}
	if activeRevision != "" {
		logger = logger.WithValues("revision_hash", activeRevision)
		ctx = log.IntoContext(ctx, logger)
	}

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

	var revisionHash string
	if currentState == "" || policy.Status.ConsecutiveFailures >= threshold || meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypeCircuitBreakerTripped) {
		var err error
		revisionHash, err = r.materializeSecretRevision(ctx, policy)
		if err != nil {
			logger.Error(err, "failed to materialize secret revision from Key Vault")
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		if revisionHash != "" && revisionHash != activeRevision {
			logger = logger.WithValues("revision_hash", revisionHash)
			ctx = log.IntoContext(ctx, logger)
		}

		// If the upstream secret has changed to a NEW revision, reset the circuit breaker and state
		if revisionHash != "" && policy.Status.CurrentRevision != revisionHash && policy.Status.DesiredRevision != revisionHash {
			logger.Info("upstream secret drift detected; resetting circuit breaker",
				"newRevision", revisionHash,
				"currentRevision", policy.Status.CurrentRevision,
				"desiredRevision", policy.Status.DesiredRevision,
			)
			policy.Status.ConsecutiveFailures = 0
			policy.Status.DesiredRevision = revisionHash
			meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeCircuitBreakerTripped)
			currentState = "" // Force restart of the state machine
		}
	}

	// If Circuit Breaker is tripped, halt reconciliation
	if policy.Status.ConsecutiveFailures >= threshold || meta.IsStatusConditionTrue(policy.Status.Conditions, ConditionTypeCircuitBreakerTripped) {
		logger.Error(nil, "circuit breaker tripped; halting reconciliation loop",
			"consecutiveFailures", policy.Status.ConsecutiveFailures,
			"threshold", threshold,
		)
		telemetry.CircuitBreakersTripped.WithLabelValues(policy.Namespace).Inc()
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
		// In-Sync Check: If the upstream secret hash equals active CurrentRevision, workload is already up-to-date
		if policy.Status.CurrentRevision != "" && revisionHash == policy.Status.CurrentRevision {
			logger.Info("workload in sync with current secret revision",
				"currentRevision", policy.Status.CurrentRevision,
			)
			return ctrl.Result{}, nil
		}

		// Drift Detected: Initiate new progressive rollout cycle
		telemetry.RotationsTotal.WithLabelValues(policy.Namespace).Inc()

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
			cbCtx, cbCancel := context.WithTimeout(ctx, 10*time.Second)
			defer cbCancel()
			if err := r.OnSecretMaterialized(cbCtx, policy.Name, revisionHash); err != nil {
				logger.Error(err, "failed executing post-materialization callback")
			}
		}

		return ctrl.Result{Requeue: true}, nil

	case ConditionTypeRevisionPrepared:
		targetKind := policy.Spec.WorkloadSelector.Kind
		if targetKind == "" {
			targetKind = workload.KindDeployment
		}
		targetName := policy.Spec.WorkloadSelector.Name

		adapter, err := workload.NewAdapter(targetKind)
		if err != nil {
			logger.Error(err, "unsupported workload kind specified in policy", "kind", targetKind)
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		if err := adapter.Fetch(ctx, r.Client, types.NamespacedName{Name: targetName, Namespace: policy.Namespace}); err != nil {
			logger.Error(err, "failed to fetch target workload for canary provisioning", "kind", targetKind, "targetWorkload", targetName)
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		secretName := fmt.Sprintf("%s-%s-rev-%s", targetName, policy.Spec.GetVaultObjectName(), policy.Status.DesiredRevision)

		// 1. Provision Canary Deployment derived polymorphically from target workload
		canaryDeploy := adapter.BuildCanary(policy, secretName)
		if canaryDeploy == nil {
			// Argo Rollout targets: no DSO-managed canary is provisioned (see RolloutAdapter's
			// doc comment). Patch the Rollout directly and let Argo's own progressive delivery
			// (canary/blueGreen steps, AnalysisRuns) validate the change; the reconciler then
			// waits for Rollout.Status.Phase to report Healthy before finalizing the promotion.
			if err := integration.ReconcileArgoCDIgnoreDifferences(ctx, r.Client, adapter.TargetObject(), targetKind); err != nil {
				logger.Error(err, "failed to reconcile Argo CD ignoreDifferences; continuing with rollout promotion", "targetWorkload", targetName)
			}
			if err := adapter.Promote(ctx, r.Client, policy, secretName); err != nil {
				logger.Error(err, "failed to patch target rollout", "targetWorkload", targetName)
				span.RecordError(err)
				return ctrl.Result{}, err
			}

			meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeRevisionPrepared)
			cond := metav1.Condition{
				Type:    ConditionTypeRolloutProgressing,
				Status:  metav1.ConditionTrue,
				Reason:  ReasonRolloutProgressing,
				Message: fmt.Sprintf("Rollout %q patched with secret revision %s; awaiting Argo's native progressive delivery to report Healthy", targetName, policy.Status.DesiredRevision),
			}
			if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
				logger.Error(err, "failed to transition to RolloutProgressing")
				span.RecordError(err)
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

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
		logger.Info("provisioned canary deployment", "canaryDeployment", canaryDeploy.Name, "targetKind", targetKind)

		// 2. Provision Canary NetworkPolicy (Standard or eBPF CiliumNetworkPolicy)
		netpolName := ""
		if policy.Spec.NetworkPolicy != nil && policy.Spec.NetworkPolicy.Provider == secretv1alpha1.NetworkPolicyProviderCilium {
			ciliumNetpol := canary.BuildCiliumNetworkPolicy(ctx, policy)
			if err := r.Create(ctx, ciliumNetpol); err != nil && !apierrors.IsAlreadyExists(err) {
				logger.Error(err, "failed to create canary cilium network policy")
				span.RecordError(err)
				return ctrl.Result{}, err
			}
			netpolName = ciliumNetpol.GetName()
			logger.Info("enforced eBPF canary CiliumNetworkPolicy isolation", "ciliumNetworkPolicy", netpolName)
		} else {
			netpol := canary.BuildNetworkPolicy(ctx, policy)
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
			netpolName = netpol.Name
			logger.Info("enforced canary network policy isolation", "networkPolicy", netpolName)
		}

		meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeRevisionPrepared)
		cond := metav1.Condition{
			Type:    ConditionTypeCanaryProvisioning,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonProvisioning,
			Message: fmt.Sprintf("Canary deployment %s and isolation policy %s provisioned", canaryDeploy.Name, netpolName),
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

	case ConditionTypeRolloutProgressing:
		// Only reachable for Argo Rollout targets (see RolloutAdapter.BuildCanary). Poll the
		// Rollout's own status instead of running synthetic probes, since Argo's progressive
		// delivery (canary/blueGreen steps, AnalysisRuns) is the source of truth for this change.
		targetName := policy.Spec.WorkloadSelector.Name
		rollout := &argorolloutsv1alpha1.Rollout{}
		if err := r.Get(ctx, types.NamespacedName{Name: targetName, Namespace: policy.Namespace}, rollout); err != nil {
			logger.Error(err, "failed to fetch target Rollout while awaiting progressive delivery", "targetWorkload", targetName)
			span.RecordError(err)
			return ctrl.Result{}, err
		}

		// Argo Rollouts stamps status.observedGeneration with the decimal string of
		// metadata.generation once it has processed the latest spec change; status.phase is
		// stale until these match.
		if rollout.Status.ObservedGeneration != strconv.FormatInt(rollout.Generation, 10) {
			return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
		}

		switch rollout.Status.Phase {
		case argorolloutsv1alpha1.RolloutPhaseHealthy:
			policy.Status.ConsecutiveFailures = 0
			r.gcOldSecretRevisions(ctx, policy, targetName)

			policy.Status.CurrentRevision = policy.Status.DesiredRevision
			policy.Status.DesiredRevision = ""

			meta.RemoveStatusCondition(&policy.Status.Conditions, ConditionTypeRolloutProgressing)
			cond := metav1.Condition{
				Type:    ConditionTypePromoting,
				Status:  metav1.ConditionTrue,
				Reason:  ReasonCompleted,
				Message: fmt.Sprintf("Argo Rollout %q reached Healthy phase; revision %s promoted and old secret revisions cleaned up", targetName, policy.Status.CurrentRevision),
			}
			if err := r.updateStatus(ctx, policy, basePolicy, cond); err != nil {
				logger.Error(err, "failed to update status to Promoting completed")
				span.RecordError(err)
				return ctrl.Result{}, err
			}
			logger.Info("Argo Rollout reached Healthy phase; secret revision promotion finalized",
				"targetWorkload", targetName, "secretRevision", policy.Status.CurrentRevision)
			return ctrl.Result{Requeue: true}, nil

		case argorolloutsv1alpha1.RolloutPhaseDegraded:
			policy.Status.ConsecutiveFailures++
			telemetry.RotationsFailed.WithLabelValues(policy.Namespace).Inc()
			logger.Error(nil, "Argo Rollout reported Degraded phase after secret revision patch",
				"targetWorkload", targetName,
				"consecutiveFailures", policy.Status.ConsecutiveFailures,
				"threshold", threshold,
			)

			if policy.Status.ConsecutiveFailures >= threshold {
				telemetry.CircuitBreakersTripped.WithLabelValues(policy.Namespace).Inc()
				cond := metav1.Condition{
					Type:    ConditionTypeCircuitBreakerTripped,
					Status:  metav1.ConditionTrue,
					Reason:  ReasonValidationThresholdExceeded,
					Message: fmt.Sprintf("Circuit breaker tripped after %d consecutive Argo Rollout degradations (threshold: %d)", policy.Status.ConsecutiveFailures, threshold),
				}
				_ = r.updateStatus(ctx, policy, basePolicy, cond)
				return ctrl.Result{}, nil
			}

			cond := metav1.Condition{
				Type:    ConditionTypeRolloutProgressing,
				Status:  metav1.ConditionFalse,
				Reason:  ReasonRolloutDegraded,
				Message: fmt.Sprintf("Argo Rollout %q is Degraded after the secret revision patch", targetName),
			}
			_ = r.updateStatus(ctx, policy, basePolicy, cond)
			return ctrl.Result{}, fmt.Errorf("argo rollout %q degraded after secret revision patch", targetName)

		default:
			// Progressing or Paused: Argo's own canary/blueGreen steps or AnalysisRuns are still
			// running (or awaiting manual/automatic promotion) - keep polling without treating
			// this as a validation failure.
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

	case ConditionTypeValidating:
		// Execute validation probes against the canary workload in a non-blocking manner
		result, err := r.reconcileValidationProbes(ctx, policy)
		if err != nil {
			policy.Status.ConsecutiveFailures++
			telemetry.RotationsFailed.WithLabelValues(policy.Namespace).Inc()
			span.RecordError(err)
			logger.Error(err, "validation probe failed",
				"consecutiveFailures", policy.Status.ConsecutiveFailures,
				"threshold", threshold,
			)

			if policy.Status.ConsecutiveFailures >= threshold {
				telemetry.CircuitBreakersTripped.WithLabelValues(policy.Namespace).Inc()
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

		if result.Requeue || result.RequeueAfter > 0 {
			// Async Job probe is currently executing in cluster; release worker goroutine
			return result, nil
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

// reconcileValidationProbes executes configured validation probes against the canary workload in a non-blocking manner.
// For Job probes, it creates or inspects ephemeral Jobs asynchronously, returning ctrl.Result{RequeueAfter: ...}
// without holding the reconciler worker goroutine.
func (r *DynamicSecretPolicyReconciler) reconcileValidationProbes(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) (ctrl.Result, error) {
	if len(policy.Spec.ValidationProbes) == 0 {
		return ctrl.Result{}, nil
	}

	targetName := policy.Spec.WorkloadSelector.Name
	secretName := fmt.Sprintf("%s-%s-rev-%s", targetName, policy.Spec.GetVaultObjectName(), policy.Status.DesiredRevision)

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: policy.Namespace}, secret); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to fetch secret revision %q for probe validation: %w", secretName, err)
	}

	runner := r.ProbeRunner
	if runner == nil {
		runner = func(pCtx context.Context, probe secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
			executor, err := probes.NewProbeExecutor(string(probe.Type))
			if err != nil {
				return err
			}
			if executor == nil {
				return fmt.Errorf("executor is nil for probe type %s", probe.Type)
			}
			return executor.Execute(pCtx, probe, secretData)
		}
	}

	for _, probe := range policy.Spec.ValidationProbes {
		if probe.Type == secretv1alpha1.ProbeTypeJob {
			if probe.Job == nil {
				return ctrl.Result{}, fmt.Errorf("job probe spec is required when probe type is Job")
			}

			jobName := probes.DeriveProbeJobName(policy.Name, secretName)
			currentJob := &batchv1.Job{}
			err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: policy.Namespace}, currentJob)
			if err != nil {
				if apierrors.IsNotFound(err) {
					// 1. Create the probe Job asynchronously
					newJob := probes.BuildProbeJob(policy, probe.Job, secretName)
					if refErr := controllerutil.SetControllerReference(policy, newJob, r.Scheme); refErr != nil {
						return ctrl.Result{}, fmt.Errorf("failed to set controller reference on probe job: %w", refErr)
					}
					if createErr := r.Create(ctx, newJob); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
						return ctrl.Result{}, fmt.Errorf("failed to create probe job %q: %w", jobName, createErr)
					}
					// Return non-blocking requeue (also watched via Owns(Job))
					return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
				}
				return ctrl.Result{}, fmt.Errorf("failed to inspect probe job %q: %w", jobName, err)
			}

			// 2. Evaluate active Job state without blocking
			timeoutSecs := int32(60)
			if probe.Job.TimeoutSeconds != nil && *probe.Job.TimeoutSeconds > 0 {
				timeoutSecs = *probe.Job.TimeoutSeconds
			}

			state, evalErr := probes.EvaluateJobStatus(ctx, r.Client, r.KubeClient, currentJob, timeoutSecs)
			switch state {
			case probes.ProbeJobStateRunning:
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			case probes.ProbeJobStateSucceeded:
				bg := metav1.DeletePropagationBackground
				_ = r.Delete(ctx, currentJob, &client.DeleteOptions{PropagationPolicy: &bg})
				telemetry.ProbeDurationSeconds.WithLabelValues(policy.Namespace, string(probe.Type)).Observe(time.Since(currentJob.CreationTimestamp.Time).Seconds())
				// Continue to next probe
			case probes.ProbeJobStateFailed, probes.ProbeJobStateTimedOut:
				// Delete the failed job so the next backoff attempt creates a fresh one
				bg := metav1.DeletePropagationBackground
				_ = r.Delete(ctx, currentJob, &client.DeleteOptions{PropagationPolicy: &bg})
				return ctrl.Result{}, evalErr
			default:
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			}
		}

		// Synchronous network probes (HTTP, TLS, MySQL, PostgreSQL)
		timeout := 15 * time.Second
		if probe.QueryTimeout > 0 {
			timeout = time.Duration(probe.QueryTimeout) * time.Second
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, timeout)
		start := time.Now()
		err := runner(probeCtx, probe, secret.Data)
		probeCancel()
		telemetry.ProbeDurationSeconds.WithLabelValues(policy.Namespace, string(probe.Type)).Observe(time.Since(start).Seconds())
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// promoteTargetWorkload patches the production target workload (Deployment, StatefulSet, DaemonSet)
// with the new Secret revision via its resolved polymorphic WorkloadAdapter.
// It strictly replaces only operator-managed secret references (<targetWorkload.Name>-rev-<hash>
// or matching the Key Vault object key), preserving all third-party secrets, TLS certificates,
// and unmanaged environment configurations.
func (r *DynamicSecretPolicyReconciler) promoteTargetWorkload(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) error {
	logger := log.FromContext(ctx)
	targetKind := policy.Spec.WorkloadSelector.Kind
	if targetKind == "" {
		targetKind = workload.KindDeployment
	}
	targetName := policy.Spec.WorkloadSelector.Name

	adapter, err := workload.NewAdapter(targetKind)
	if err != nil {
		return fmt.Errorf("unsupported workload kind %q: %w", targetKind, err)
	}

	targetKey := types.NamespacedName{
		Name:      targetName,
		Namespace: policy.Namespace,
	}

	if err := adapter.Fetch(ctx, r.Client, targetKey); err != nil {
		return fmt.Errorf("failed to fetch target %s %q: %w", targetKind, targetName, err)
	}

	// Reconcile Argo CD ignoreDifferences if auto-patching is enabled
	if err := integration.ReconcileArgoCDIgnoreDifferences(ctx, r.Client, adapter.TargetObject(), targetKind); err != nil {
		logger.Error(err, "failed to reconcile Argo CD ignoreDifferences; continuing with workload promotion", "targetWorkload", targetName)
	}

	newSecretName := fmt.Sprintf("%s-%s-rev-%s", targetName, policy.Spec.GetVaultObjectName(), policy.Status.DesiredRevision)
	if err := adapter.Promote(ctx, r.Client, policy, newSecretName); err != nil {
		return fmt.Errorf("failed to promote target %s %q: %w", targetKind, targetName, err)
	}

	r.gcOldSecretRevisions(ctx, policy, targetName)

	logger.Info("successfully patched target workload with new secret revision",
		"targetKind", targetKind,
		"targetWorkload", targetName,
		"secretRevision", policy.Status.DesiredRevision,
	)
	return nil
}

// gcOldSecretRevisions deletes materialized revision secrets for targetName that are neither the
// current nor the desired revision, keeping at most those two around at any time.
func (r *DynamicSecretPolicyReconciler) gcOldSecretRevisions(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy, targetName string) {
	secrets := &corev1.SecretList{}
	labelSelector := client.MatchingLabels{
		canary.LabelTargetWorkload: targetName,
		LabelPolicy:                policy.Name,
	}
	if err := r.List(ctx, secrets, client.InNamespace(policy.Namespace), labelSelector); err == nil {
		for _, s := range secrets.Items {
			rev := s.Labels[LabelRevision]
			if rev != policy.Status.DesiredRevision && rev != policy.Status.CurrentRevision {
				_ = r.Delete(ctx, &s)
			}
		}
	}
}

// materializeSecretRevision pulls the secret payload from the registered provider backend (Azure, ESO, AWS, GCP, Vault),
// calculates a deterministic hash, and materializes the immutable Secret in the cluster. It does not zero in-memory
// byte buffers: Go's garbage-collected runtime cannot reliably guarantee that, so DSO relies on OS/container-level
// controls instead (readOnlyRootFilesystem, runAsNonRoot, dropped capabilities, disabled core dumps) - see docs/security.md.
func (r *DynamicSecretPolicyReconciler) materializeSecretRevision(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) (string, error) {
	logger := log.FromContext(ctx)

	reg := r.ProviderRegistry
	if reg == nil {
		reg = sourceProvider.SetupDefaultRegistry(r.Client, r.SecretFetcher)
	}

	src := policy.Spec.GetResolvedSource()
	provider, err := reg.Get(src.Type)
	if err != nil {
		return "", fmt.Errorf("failed to resolve source provider for policy %q: %w", policy.Name, err)
	}

	kvCtx, kvCancel := context.WithTimeout(ctx, 15*time.Second)
	defer kvCancel()

	payload, err := provider.FetchSecret(kvCtx, policy)
	if err != nil {
		return "", fmt.Errorf("failed to fetch secret from source provider %q: %w", src.Type, err)
	}

	// Compute deterministic short hash (first 12 characters of SHA-256)
	hasher := sha256.New()
	if len(payload.Data) == 1 {
		for _, v := range payload.Data {
			hasher.Write(v)
		}
	} else {
		keys := make([]string, 0, len(payload.Data))
		for k := range payload.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			hasher.Write([]byte(k))
			hasher.Write(payload.Data[k])
		}
	}
	if payload.Version != "" {
		hasher.Write([]byte(payload.Version))
	}
	revisionHash := fmt.Sprintf("%x", hasher.Sum(nil))[:12]
	secretName := fmt.Sprintf("%s-%s-rev-%s", policy.Spec.WorkloadSelector.Name, policy.Spec.GetVaultObjectName(), revisionHash)

	secretType := corev1.SecretTypeOpaque
	secretData := make(map[string][]byte, len(payload.Data))
	for k, v := range payload.Data {
		secretData[k] = v
	}

	// For TLS certificates, split and map PEM blocks into standard kubernetes.io/tls keys
	if src.Type == secretv1alpha1.SourceTypeAzureKeyVault && src.AzureKeyVault != nil && src.AzureKeyVault.ObjectType == secretv1alpha1.VaultObjectTypeCertificate {
		for _, v := range payload.Data {
			if strings.Contains(string(v), "-----BEGIN ") {
				certData, keyData := extractPEMCertAndKey(v)
				if len(certData) > 0 {
					secretData[corev1.TLSCertKey] = certData
					if len(keyData) > 0 {
						secretData[corev1.TLSPrivateKeyKey] = keyData
					}
					secretType = corev1.SecretTypeTLS
				}
				break
			}
		}
	} else {
		// General check: if tls.crt and tls.key exist in secretData, set type to TLS
		if _, hasCert := secretData[corev1.TLSCertKey]; hasCert {
			if _, hasKey := secretData[corev1.TLSPrivateKeyKey]; hasKey {
				secretType = corev1.SecretTypeTLS
			}
		}
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: policy.Namespace,
			Labels: map[string]string{
				LabelRevision:              revisionHash,
				canary.LabelTargetWorkload: policy.Spec.WorkloadSelector.Name,
				LabelPolicy:                policy.Name,
				LabelManaged:               ManagedValueTrue,
			},
		},
		Type: secretType,
		Data: secretData,
	}

	// Set ControllerReference for automatic garbage collection when policy is deleted
	if err := controllerutil.SetControllerReference(policy, secret, r.Scheme); err != nil {
		return "", fmt.Errorf("failed to set controller reference on secret: %w", err)
	}

	// Execute Kubernetes API call
	createErr := r.Create(ctx, secret)

	// Idempotency: If secret already exists, treat materialization as successful
	if createErr != nil {
		if apierrors.IsAlreadyExists(createErr) {
			logger.V(1).Info("secret revision already exists; continuing", "secret", secretName)
			return revisionHash, nil
		}
		return "", fmt.Errorf("failed to create secret revision %q: %w", secretName, createErr)
	}

	logger.Info("materialized immutable secret revision",
		"secret", secretName,
		"revision", revisionHash,
		"workload", policy.Spec.WorkloadSelector.Name,
		"sourceType", src.Type,
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
	if meta.FindStatusCondition(policy.Status.Conditions, ConditionTypeRolloutProgressing) != nil {
		return ConditionTypeRolloutProgressing
	}
	if meta.FindStatusCondition(policy.Status.Conditions, ConditionTypeCanaryProvisioning) != nil {
		return ConditionTypeCanaryProvisioning
	}
	if meta.FindStatusCondition(policy.Status.Conditions, ConditionTypeRevisionPrepared) != nil {
		return ConditionTypeRevisionPrepared
	}

	for _, c := range policy.Status.Conditions {
		if c.Status == metav1.ConditionTrue {
			return c.Type
		}
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
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&batchv1.Job{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findPoliciesForSourceSecret),
		)

	if r.MaxConcurrentReconciles > 0 {
		builder = builder.WithOptions(controller.Options{
			MaxConcurrentReconciles: r.MaxConcurrentReconciles,
		})
	}

	// Check if optional Argo Rollouts CRD is actually installed in the cluster via API discovery
	if discoveryClient, err := discovery.NewDiscoveryClientForConfig(mgr.GetConfig()); err == nil {
		if resources, err := discoveryClient.ServerResourcesForGroupVersion("argoproj.io/v1alpha1"); err == nil && resources != nil {
			for _, res := range resources.APIResources {
				if res.Kind == "Rollout" {
					builder = builder.Owns(&argorolloutsv1alpha1.Rollout{})
					ctrl.Log.Info("Argo Rollouts CRD detected; enabling Rollout watch")
					break
				}
			}
		} else {
			ctrl.Log.Info("Argo Rollouts CRD not detected in cluster; skipping Rollout watch")
		}
	} else {
		ctrl.Log.Info("Could not create discovery client; skipping Rollout watch")
	}

	if r.EventsChannel != nil {
		builder = builder.WatchesRawSource(source.Channel(r.EventsChannel, &handler.EnqueueRequestForObject{}))
	}

	return builder.Complete(r)
}

// findPoliciesForSourceSecret maps unmanaged source secrets (e.g. ESO synchronized secrets)
// to the DynamicSecretPolicy resources that ingest them.
func (r *DynamicSecretPolicyReconciler) findPoliciesForSourceSecret(ctx context.Context, obj client.Object) []ctrl.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok || secret.Labels[LabelManaged] == ManagedValueTrue {
		return nil // Ignore our own materialized secrets (handled via Owns(&corev1.Secret{}))
	}

	var policies secretv1alpha1.DynamicSecretPolicyList
	if err := r.List(ctx, &policies, client.InNamespace(secret.Namespace)); err != nil {
		return nil
	}

	var reqs []ctrl.Request
	for _, p := range policies.Items {
		src := p.Spec.GetResolvedSource()
		if src.Type == secretv1alpha1.SourceTypeK8sSecret && src.K8sSecret != nil && src.K8sSecret.Name == secret.Name {
			reqs = append(reqs, ctrl.Request{NamespacedName: types.NamespacedName{Name: p.Name, Namespace: p.Namespace}})
		}
	}
	return reqs
}

// extractPEMCertAndKey decodes raw PEM-encoded payload bytes and partitions them into
// certificate blocks (tls.crt) and private key blocks (tls.key).
func extractPEMCertAndKey(data []byte) ([]byte, []byte) {
	var certBlocks []byte
	var keyBlocks []byte

	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		encoded := pem.EncodeToMemory(block)
		if strings.Contains(block.Type, "CERTIFICATE") {
			certBlocks = append(certBlocks, encoded...)
		} else if strings.Contains(block.Type, "PRIVATE KEY") || strings.Contains(block.Type, "KEY") {
			keyBlocks = append(keyBlocks, encoded...)
		}
	}
	return certBlocks, keyBlocks
}
