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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/workload"
)

const (
	// EnvRevisionSecretName is the standard environment variable injected by the operator
	// into all initContainers and containers of the ephemeral probe Job.
	// Users reference $(DSO_REVISION_SECRET_NAME) in commands, args, or env definitions.
	EnvRevisionSecretName = "DSO_REVISION_SECRET_NAME"

	// maxFailureLogBytes caps the amount of failure log retrieved from failed pods.
	maxFailureLogBytes = 4096
)

// ProbeJobState represents the lifecycle status of an ephemeral validation probe Job.
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
// automatically mutates volumes and env vars to point to the newly materialized secret,
// injects the DSO_REVISION_SECRET_NAME environment variable into all containers,
// and attaches an OwnerReference to the policy so garbage collection is policy-scoped.
func BuildProbeJob(
	policy *secretv1alpha1.DynamicSecretPolicy,
	spec *secretv1alpha1.JobProbeSpec,
	revisionSecretName string,
) *batchv1.Job {
	// Copy the template to avoid mutating the spec.
	tmpl := spec.JobTemplate.DeepCopy()

	// Mutate volumes, envs, and volumeMounts in the Job pod template to point to the new secret revision
	workload.MutatePodTemplateSpec(&tmpl.Spec.Template, policy.Spec.WorkloadSelector.Name, policy, revisionSecretName)

	// Automatically inject the DSO_REVISION_SECRET_NAME environment variable into all containers for scripting convenience.
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
	defer func() {
		_ = rc.Close()
	}()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(rc, maxFailureLogBytes)); err != nil {
		return fmt.Sprintf("(error reading pod logs: %v)", err)
	}
	return buf.String()
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
