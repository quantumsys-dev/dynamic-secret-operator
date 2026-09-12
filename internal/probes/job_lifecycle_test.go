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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestEvaluateJobStatus(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	k8sClient := ctrlclient.NewClientBuilder().WithScheme(scheme).Build()
	kubeClient := fake.NewSimpleClientset() // Fake client-go for log stream simulation

	tests := []struct {
		name           string
		job            *batchv1.Job
		timeoutSecs    int32
		expectedState  ProbeJobState
		expectedErrMsg string
	}{
		{
			name: "Job Successfully Completed",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "probe-success", Namespace: "default"},
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
					},
				},
			},
			timeoutSecs:   60,
			expectedState: ProbeJobStateSucceeded,
		},
		{
			name: "Job Failed",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: "probe-failed", Namespace: "default"},
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded", Message: "Job has reached the specified backoff limit"},
					},
				},
			},
			timeoutSecs:    60,
			expectedState:  ProbeJobStateFailed,
			expectedErrMsg: "BackoffLimitExceeded",
		},
		{
			name: "Job Timed Out",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "probe-timeout",
					Namespace:         "default",
					CreationTimestamp: metav1.NewTime(time.Now().Add(-65 * time.Second)), // Created 65s ago
				},
				Status: batchv1.JobStatus{},
			},
			timeoutSecs:    60, // Timeout is 60s
			expectedState:  ProbeJobStateTimedOut,
			expectedErrMsg: "timed out after 60s",
		},
		{
			name: "Job Still Running (Under Timeout)",
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "probe-running",
					Namespace:         "default",
					CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Second)),
				},
			},
			timeoutSecs:   60,
			expectedState: ProbeJobStateRunning,
		},
		{
			name:           "Nil Job Pointer Defense",
			job:            nil,
			timeoutSecs:    60,
			expectedState:  ProbeJobStateFailed,
			expectedErrMsg: "probe job is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := EvaluateJobStatus(ctx, k8sClient, kubeClient, tt.job, tt.timeoutSecs)
			assert.Equal(t, tt.expectedState, state)

			if tt.expectedErrMsg != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDeriveProbeJobName(t *testing.T) {
	tests := []struct {
		name       string
		policy     string
		secretName string
	}{
		{"Standard Lengths", "redis-policy", "target-secret-rev-a1b2c3d4e5f6"},
		{"Excessively Long Names", "this-is-a-very-long-policy-name-that-exceeds-standard-limits", "super-long-target-deployment-name-rev-1234567890abcdef12345678"},
		{"Special Characters", "My_Policy.Name!", "Secret@Rev#123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobName := DeriveProbeJobName(tt.policy, tt.secretName)

			// Assert DNS-1123 length compliance
			assert.LessOrEqual(t, len(jobName), 63, "Job name exceeds 63 characters")

			// Assert no trailing hyphens
			assert.NotEqual(t, byte('-'), jobName[len(jobName)-1], "Job name must not end with a hyphen")

			// Assert prefix presence
			assert.Contains(t, jobName, "dso-probe-")
		})
	}
}

func TestRetrieveFailureLogs(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	t.Run("nil kubeClient returns unavailable message", func(t *testing.T) {
		k8sClient := ctrlclient.NewClientBuilder().WithScheme(scheme).Build()
		logs := RetrieveFailureLogs(ctx, k8sClient, nil, "default", "probe-job")
		assert.Contains(t, logs, "kubernetes client not configured")
	})

	t.Run("no pods found returns safe message", func(t *testing.T) {
		k8sClient := ctrlclient.NewClientBuilder().WithScheme(scheme).Build()
		kubeClient := fake.NewSimpleClientset()
		logs := RetrieveFailureLogs(ctx, k8sClient, kubeClient, "default", "non-existent-job")
		assert.Contains(t, logs, "no pods found for failed job")
	})

	t.Run("pod found streams log successfully via client-go fake", func(t *testing.T) {
		failedPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "probe-job-pod-1",
				Namespace: "default",
				Labels: map[string]string{
					"batch.kubernetes.io/job-name": "probe-job",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "validator", Image: "redis:alpine"},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodFailed,
			},
		}

		k8sClient := ctrlclient.NewClientBuilder().WithScheme(scheme).WithObjects(failedPod).Build()
		kubeClient := fake.NewSimpleClientset(failedPod)

		logs := RetrieveFailureLogs(ctx, k8sClient, kubeClient, "default", "probe-job")
		assert.Equal(t, "fake logs", logs)
	})
}

func FuzzDeriveProbeJobName(f *testing.F) {
	f.Add("redis-policy", "target-secret-rev-a1b2c3d4e5f6")
	f.Add("this-is-a-very-long-policy-name-that-exceeds-standard-limits", "super-long-target-deployment-name-rev-1234567890abcdef12345678")
	f.Add("My_Policy.Name!", "Secret@Rev#123")
	f.Add("", "")
	f.Add("---", "---")
	f.Add("63-character-boundary-test-policy-name-exactly-at-the-edge-padding", "revision-hash-1234567890abcdef")

	f.Fuzz(func(t *testing.T, policy, rev string) {
		name := DeriveProbeJobName(policy, rev)
		assert.LessOrEqual(t, len(name), 63, "Job name exceeds 63 characters")
		assert.True(t, len(name) >= len("dso-probe"), "Job name must be at least dso-probe")
		if len(name) > 0 {
			assert.NotEqual(t, byte('-'), name[len(name)-1], "Job name must not end with a hyphen")
		}
	})
}
