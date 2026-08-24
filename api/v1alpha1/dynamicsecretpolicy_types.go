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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VaultObjectType defines the supported object types in external vaults.
// +kubebuilder:validation:Enum=Secret;Certificate;Key
type VaultObjectType string

const (
	VaultObjectTypeSecret      VaultObjectType = "Secret"
	VaultObjectTypeCertificate VaultObjectType = "Certificate"
	VaultObjectTypeKey         VaultObjectType = "Key"
)

// ProbeType defines the type of health/validation probe to execute against the workload.
// +kubebuilder:validation:Enum=TLS;PostgreSQL;MySQL;HTTP
type ProbeType string

const (
	ProbeTypeTLS        ProbeType = "TLS"
	ProbeTypePostgreSQL ProbeType = "PostgreSQL"
	ProbeTypeMySQL      ProbeType = "MySQL"
	ProbeTypeHTTP       ProbeType = "HTTP"
)

// VaultReference specifies the location and identity of the secret in Azure Key Vault / external vault.
type VaultReference struct {
	// KeyVaultURI is the URI of the Azure Key Vault or secret backend.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	KeyVaultURI string `json:"keyVaultURI"`

	// ObjectName is the name of the secret, certificate, or key within the vault.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ObjectName string `json:"objectName"`

	// ObjectType specifies whether the secret is a Secret, Certificate, or Key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Secret;Certificate;Key
	ObjectType VaultObjectType `json:"objectType"`
}

// WorkloadSelector defines the target deployment/workload to receive progressive rollouts.
type WorkloadSelector struct {
	// Kind is the type of Kubernetes resource to target (e.g. Deployment, StatefulSet).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// Name is the name of the target workload resource in the same namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// CanaryStrategy configures the progressive canary delivery timing.
type CanaryStrategy struct {
	// TimeoutSeconds defines the maximum duration in seconds to wait for canary health validation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	TimeoutSeconds int32 `json:"timeoutSeconds"`
}

// ValidationProbe configures synthetic probes executed during rollout.
type ValidationProbe struct {
	// Type of probe to execute (TLS, PostgreSQL, MySQL, HTTP).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=TLS;PostgreSQL;MySQL;HTTP
	Type ProbeType `json:"type"`

	// Endpoint is the network address or URL tested by the probe.
	// +kubebuilder:validation:Optional
	Endpoint string `json:"endpoint,omitempty"`

	// QueryTimeout specifies probe timeout in seconds.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	QueryTimeout int32 `json:"queryTimeout,omitempty"`
}

// RollbackConfig defines automated rollback rules and safety thresholds.
type RollbackConfig struct {
	// AutoRollback determines if the operator automatically reverts to the last known good revision on failure.
	// +kubebuilder:default=true
	AutoRollback bool `json:"autoRollback,omitempty"`

	// CircuitBreakerThreshold is the maximum number of consecutive failures permitted before halting reconciliations.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	CircuitBreakerThreshold int32 `json:"circuitBreakerThreshold,omitempty"`
}

// DynamicSecretPolicySpec defines the desired state of DynamicSecretPolicy.
type DynamicSecretPolicySpec struct {
	// VaultRef references the external secret store and secret identity.
	// +kubebuilder:validation:Required
	VaultRef VaultReference `json:"vaultRef"`

	// WorkloadSelector identifies the target workload for progressive rotation.
	// +kubebuilder:validation:Required
	WorkloadSelector WorkloadSelector `json:"workloadSelector"`

	// CanaryStrategy configures progressive canary timing and timeouts.
	// +kubebuilder:validation:Optional
	CanaryStrategy *CanaryStrategy `json:"canaryStrategy,omitempty"`

	// ValidationProbes is a list of synthetic probes to run against canary pods.
	// +kubebuilder:validation:Optional
	ValidationProbes []ValidationProbe `json:"validationProbes,omitempty"`

	// RollbackConfig sets failure thresholds and rollback automation behavior.
	// +kubebuilder:validation:Optional
	RollbackConfig *RollbackConfig `json:"rollbackConfig,omitempty"`
}

// DynamicSecretPolicyStatus defines the observed state of DynamicSecretPolicy.
type DynamicSecretPolicyStatus struct {
	// CurrentRevision is the active, validated secret version currently applied.
	// +optional
	CurrentRevision string `json:"currentRevision,omitempty"`

	// DesiredRevision is the target secret version being rotated/canaried.
	// +optional
	DesiredRevision string `json:"desiredRevision,omitempty"`

	// ConsecutiveFailures tracks the number of consecutive rollout/probe failures.
	// +optional
	ConsecutiveFailures int32 `json:"consecutiveFailures,omitempty"`

	// Conditions represent the latest observations of an object's state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Current Revision",type="string",JSONPath=".status.currentRevision"
// +kubebuilder:printcolumn:name="Desired Revision",type="string",JSONPath=".status.desiredRevision"
// +kubebuilder:printcolumn:name="Failures",type="integer",JSONPath=".status.consecutiveFailures"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// DynamicSecretPolicy is the Schema for the dynamicsecretpolicies API.
type DynamicSecretPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DynamicSecretPolicySpec   `json:"spec,omitempty"`
	Status DynamicSecretPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DynamicSecretPolicyList contains a list of DynamicSecretPolicy.
type DynamicSecretPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DynamicSecretPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DynamicSecretPolicy{}, &DynamicSecretPolicyList{})
}
