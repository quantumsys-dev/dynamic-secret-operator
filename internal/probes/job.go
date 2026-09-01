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

package probes

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/pkg/telemetry"
)

const (
	// EnvRevisionSecretName is the standard environment variable injected by the operator
	// into all initContainers and containers of the ephemeral probe Job.
	// Users reference $(DSO_REVISION_SECRET_NAME) in commands, args, or env definitions.
	EnvRevisionSecretName = "DSO_REVISION_SECRET_NAME"

	// jobPollInterval controls how frequently the operator polls for Job completion.
	jobPollInterval = 3 * time.Second

	// maxFailureLogBytes caps the amount of failure log retrieved from failed pods.
	maxFailureLogBytes = 4096
)

// JobProbe implements ProbeExecutor by launching an ephemeral batch/v1.Job in the
// target namespace. It automatically injects the DSO_REVISION_SECRET_NAME environment
// variable into all containers, polls until the Job reaches a terminal state, captures
// failure logs for diagnostics, and guarantees cleanup regardless of outcome.
type JobProbe struct {
	// KubeClient is an optional kubernetes.Interface used to retrieve pod logs.
	// If nil, log retrieval is skipped (only job status is reported).
	KubeClient kubernetes.Interface
}

// Execute creates a probe Job, waits for it to complete or time out, populates
// the returned error with failure logs, and deletes the Job.
//
// The ctx passed in must already carry the policy's desired timeout via
// context.WithTimeout; additionally, if probe.Job.TimeoutSeconds is set, a
// tighter deadline is derived and used instead.
func (p *JobProbe) Execute(
	ctx context.Context,
	k8sClient client.Client,
	policy *secretv1alpha1.DynamicSecretPolicy,
	config secretv1alpha1.ValidationProbe,
	_ map[string][]byte, // secretData is not needed; secret name is injected via DSO_REVISION_SECRET_NAME env var
) error {
	ctx, span := telemetry.Tracer.Start(ctx, "ExecuteJobProbe",
		trace.WithAttributes(
			attribute.String("probe.type", string(secretv1alpha1.ProbeTypeJob)),
			attribute.String("policy.name", policy.Name),
			attribute.String("policy.namespace", policy.Namespace),
		),
	)
	defer span.End()

	if config.Job == nil {
		err := fmt.Errorf("job probe spec is required when probe type is Job")
		span.RecordError(err)
		return err
	}

	// Determine effective timeout: probe-level config wins over context deadline.
	timeout := 60 * time.Second
	if config.Job.TimeoutSeconds != nil && *config.Job.TimeoutSeconds > 0 {
		timeout = time.Duration(*config.Job.TimeoutSeconds) * time.Second
	}
	jobCtx, jobCancel := context.WithTimeout(ctx, timeout)
	defer jobCancel()

	// Derive the materialized revision secret name following the same convention
	// used in runValidationProbes: "<workload>-<objectName>-rev-<desiredRevision>".
	revisionSecretName := fmt.Sprintf("%s-%s-rev-%s",
		policy.Spec.WorkloadSelector.Name,
		policy.Spec.VaultRef.ObjectName,
		policy.Status.DesiredRevision,
	)

	// Build the Job object from the template.
	job := buildProbeJob(policy, config.Job, revisionSecretName)

	// Create the Job; defer cleanup so probe Jobs never litter the cluster.
	if err := k8sClient.Create(jobCtx, job); err != nil {
		createErr := fmt.Errorf("failed to create probe job %q in namespace %q: %w",
			job.Name, job.Namespace, err)
		span.RecordError(createErr)
		return createErr
	}

	defer func() {
		deleteCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		bg := metav1.DeletePropagationBackground
		_ = k8sClient.Delete(deleteCtx, job, &client.DeleteOptions{
			PropagationPolicy: &bg,
		})
	}()

	// Poll until the Job reaches a terminal state.
	var terminalJob *batchv1.Job
	err := wait.PollUntilContextCancel(jobCtx, jobPollInterval, true, func(ctx context.Context) (bool, error) {
		current := &batchv1.Job{}
		if gErr := k8sClient.Get(ctx, types.NamespacedName{
			Name:      job.Name,
			Namespace: job.Namespace,
		}, current); gErr != nil {
			if apierrors.IsNotFound(gErr) {
				return false, nil // transient; keep polling
			}
			return false, gErr
		}
		terminalJob = current

		for _, cond := range current.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})

	if err != nil {
		// Context deadline / timeout exceeded.
		timeoutErr := fmt.Errorf("job probe %q timed out after %s waiting for terminal state: %w",
			job.Name, timeout, err)
		span.RecordError(timeoutErr)
		return timeoutErr
	}

	// Inspect terminal status.
	if terminalJob != nil {
		for _, cond := range terminalJob.Status.Conditions {
			if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
				logs := p.retrieveFailureLogs(ctx, k8sClient, job.Namespace, job.Name)
				failErr := fmt.Errorf("job probe %q failed (reason: %s): %s\n%s",
					job.Name, cond.Reason, cond.Message, logs)
				span.RecordError(failErr)
				return failErr
			}
		}
	}

	return nil
}

type ProbeJobState string

const (
	// ProbeJobStateRunning indicates the probe Job is actively executing in the cluster.
	ProbeJobStateRunning ProbeJobState = "Running"
	// ProbeJobStateSucceeded indicates the probe Job completed with exit code 0.
	ProbeJobStateSucceeded ProbeJobState = "Succeeded"
	// ProbeJobStateFailed indicates the probe Job encountered an error or failed container.
	ProbeJobStateFailed ProbeJobState = "Failed"
	// ProbeJobStateTimedOut indicates the probe Job exceeded its configured timeout.
	ProbeJobStateTimedOut ProbeJobState = "TimedOut"
)

// DeriveProbeJobName calculates the deterministic name for an ephemeral probe Job.
func DeriveProbeJobName(policyName, revisionSecretName string) string {
	jobName := fmt.Sprintf("dso-probe-%s-%s", policyName, sanitizeName(revisionSecretName))
	if len(jobName) > 63 {
		jobName = jobName[:63]
	}
	return jobName
}

// BuildProbeJob constructs a batchv1.Job from the user-supplied JobTemplateSpec,
// automatically injects the DSO_REVISION_SECRET_NAME environment variable into all containers,
// and attaches an OwnerReference to the policy so garbage collection is policy-scoped.
func BuildProbeJob(
	policy *secretv1alpha1.DynamicSecretPolicy,
	spec *secretv1alpha1.JobProbeSpec,
	revisionSecretName string,
) *batchv1.Job {
	// Copy the template to avoid mutating the spec.
	tmpl := spec.JobTemplate.DeepCopy()

	// Automatically inject the DSO_REVISION_SECRET_NAME environment variable into all containers.
	injectRevisionSecretEnv(&tmpl.Spec.Template.Spec, revisionSecretName)

	// Generate a deterministic but unique Job name scoped to the policy + revision.
	jobName := DeriveProbeJobName(policy.Name, revisionSecretName)

	// Ensure backoffLimit is 0 — probe Jobs must fail fast; no retries.
	zero := int32(0)
	if tmpl.Spec.BackoffLimit == nil {
		tmpl.Spec.BackoffLimit = &zero
	}

	ownerRef := metav1.OwnerReference{
		APIVersion: secretv1alpha1.GroupVersion.String(),
		Kind:       "DynamicSecretPolicy",
		Name:       policy.Name,
		UID:        policy.UID,
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: policy.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "dynamic-secret-operator",
				"dso.quantumsys.dev/probe":     "job",
				"dso.quantumsys.dev/policy":    policy.Name,
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: tmpl.Spec,
	}
}

// EvaluateJobStatus inspects the status conditions and timeout deadline of an active
// probe Job without blocking the reconciler thread.
func EvaluateJobStatus(
	ctx context.Context,
	k8sClient client.Client,
	kubeClient kubernetes.Interface,
	job *batchv1.Job,
	timeoutSeconds int32,
) (ProbeJobState, error) {
	if job == nil {
		return ProbeJobStateFailed, fmt.Errorf("probe job is nil")
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return ProbeJobStateSucceeded, nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			logs := RetrieveFailureLogs(ctx, k8sClient, kubeClient, job.Namespace, job.Name)
			return ProbeJobStateFailed, fmt.Errorf("job probe %q failed (reason: %s): %s\n%s",
				job.Name, cond.Reason, cond.Message, logs)
		}
	}

	if timeoutSeconds > 0 && !job.CreationTimestamp.IsZero() {
		deadline := job.CreationTimestamp.Add(time.Duration(timeoutSeconds) * time.Second)
		if time.Now().After(deadline) {
			logs := RetrieveFailureLogs(ctx, k8sClient, kubeClient, job.Namespace, job.Name)
			return ProbeJobStateTimedOut, fmt.Errorf("job probe %q timed out after %ds waiting for completion\n%s",
				job.Name, timeoutSeconds, logs)
		}
	}

	return ProbeJobStateRunning, nil
}

// buildProbeJob is retained as an internal alias for BuildProbeJob.
func buildProbeJob(
	policy *secretv1alpha1.DynamicSecretPolicy,
	spec *secretv1alpha1.JobProbeSpec,
	revisionSecretName string,
) *batchv1.Job {
	return BuildProbeJob(policy, spec, revisionSecretName)
}

// injectRevisionSecretEnv injects or updates the DSO_REVISION_SECRET_NAME environment
// variable in all initContainers and containers of the pod specification.
func injectRevisionSecretEnv(spec *corev1.PodSpec, revisionSecretName string) {
	if spec == nil {
		return
	}
	spec.InitContainers = setOrAppendEnv(spec.InitContainers, EnvRevisionSecretName, revisionSecretName)
	spec.Containers = setOrAppendEnv(spec.Containers, EnvRevisionSecretName, revisionSecretName)
}

func setOrAppendEnv(containers []corev1.Container, key, value string) []corev1.Container {
	for i := range containers {
		found := false
		for j := range containers[i].Env {
			if containers[i].Env[j].Name == key {
				containers[i].Env[j].Value = value
				containers[i].Env[j].ValueFrom = nil
				found = true
				break
			}
		}
		if !found {
			containers[i].Env = append(containers[i].Env, corev1.EnvVar{
				Name:  key,
				Value: value,
			})
		}
	}
	return containers
}

// RetrieveFailureLogs attempts to fetch the tail of logs from the first failed
// pod created by the Job. Returns an empty string on any error so failure
// diagnostics are always best-effort and never block the probe result.
func RetrieveFailureLogs(ctx context.Context, k8sClient client.Client, kubeClient kubernetes.Interface, namespace, jobName string) string {
	if kubeClient == nil {
		return "(log retrieval unavailable: kubernetes client not configured)"
	}

	podList := &corev1.PodList{}
	if err := k8sClient.List(ctx, podList,
		client.InNamespace(namespace),
		client.MatchingLabels{"batch.kubernetes.io/job-name": jobName},
	); err != nil || len(podList.Items) == 0 {
		return "(no pods found for failed job)"
	}

	// Find the first failed pod.
	var targetPod *corev1.Pod
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodFailed {
			targetPod = pod
			break
		}
	}
	if targetPod == nil {
		targetPod = &podList.Items[0]
	}

	// Use the first container's logs.
	containerName := ""
	if len(targetPod.Spec.Containers) > 0 {
		containerName = targetPod.Spec.Containers[0].Name
	}

	limit := int64(maxFailureLogBytes)
	req := kubeClient.CoreV1().Pods(namespace).GetLogs(targetPod.Name, &corev1.PodLogOptions{
		Container:  containerName,
		LimitBytes: &limit,
		Previous:   false,
		Timestamps: false,
	})

	logCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rc, err := req.Stream(logCtx)
	if err != nil {
		return fmt.Sprintf("(failed to stream pod logs: %v)", err)
	}
	defer rc.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(rc, maxFailureLogBytes)); err != nil {
		return fmt.Sprintf("(error reading pod logs: %v)", err)
	}
	return buf.String()
}

func (p *JobProbe) retrieveFailureLogs(ctx context.Context, k8sClient client.Client, namespace, jobName string) string {
	return RetrieveFailureLogs(ctx, k8sClient, p.KubeClient, namespace, jobName)
}

// sanitizeName lowercases and truncates a string so it is safe to embed in
// a Kubernetes resource name. Non-alphanumeric characters are replaced with "-".
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := b.String()
	// Trim leading/trailing dashes.
	result = strings.Trim(result, "-")
	if len(result) > 32 {
		result = result[:32]
	}
	return result
}
