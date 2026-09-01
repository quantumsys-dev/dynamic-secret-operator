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
	batchv1 "k8s.io/api/batch/v1"
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
// +kubebuilder:validation:Enum=TLS;PostgreSQL;MySQL;HTTP;Job
type ProbeType string

const (
	ProbeTypeTLS        ProbeType = "TLS"
	ProbeTypePostgreSQL ProbeType = "PostgreSQL"
	ProbeTypeMySQL      ProbeType = "MySQL"
	ProbeTypeHTTP       ProbeType = "HTTP"
	// ProbeTypeJob launches an ephemeral batch/v1.Job using a user-supplied container image.
	// This avoids embedding third-party drivers into the operator binary and enables
	// arbitrarily complex validation logic (e.g., Redis PING, Kafka produce/consume, custom scripts).
	ProbeTypeJob ProbeType = "Job"
)

// SourceType defines the supported secret source provider backends.
// +kubebuilder:validation:Enum=AzureKeyVault;K8sSecret;AWSSecretsManager;GCPSecretManager;Vault
type SourceType string

const (
	// SourceTypeAzureKeyVault configures real-time event-driven secret ingestion from Azure Key Vault.
	SourceTypeAzureKeyVault SourceType = "AzureKeyVault"
	// SourceTypeK8sSecret configures universal multi-cloud ingestion via intermediate Kubernetes secrets (e.g., ESO synergy).
	SourceTypeK8sSecret SourceType = "K8sSecret"
	// SourceTypeAWSSecretsManager configures ingestion from AWS Secrets Manager via EventBridge/SQS.
	SourceTypeAWSSecretsManager SourceType = "AWSSecretsManager"
	// SourceTypeGCPSecretManager configures ingestion from Google Cloud Secret Manager via Pub/Sub.
	SourceTypeGCPSecretManager SourceType = "GCPSecretManager"
	// SourceTypeVault configures ingestion from HashiCorp Vault.
	SourceTypeVault SourceType = "Vault"
)

// AzureKeyVaultSource configures real-time event-driven secret ingestion from Azure Key Vault.
type AzureKeyVaultSource struct {
	// KeyVaultURI is the URI of the Azure Key Vault.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.matches('^https://[a-zA-Z0-9-]+\\\\.(?:vault\\\\.azure\\\\.net|vault\\\\.azure\\\\.cn|vault\\\\.usgovcloudapi\\\\.net)(?:/|$)?')",message="Invalid Azure Key Vault URI"
	KeyVaultURI string `json:"keyVaultURI"`

	// ObjectName is the name of the secret, certificate, or key within Key Vault.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.size() > 0",message="objectName must not be empty"
	ObjectName string `json:"objectName"`

	// ObjectType specifies whether the secret is a Secret, Certificate, or Key.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Secret;Certificate;Key
	// +kubebuilder:default=Secret
	ObjectType VaultObjectType `json:"objectType,omitempty"`
}

// K8sSecretSource configures universal multi-cloud ingestion via intermediate Kubernetes secrets (e.g. ESO).
type K8sSecretSource struct {
	// Name of the intermediate source secret synchronized by ESO or external tools in the same namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.size() > 0",message="name must not be empty"
	Name string `json:"name"`
}

// AWSSecretsManagerSource reserved stub for native AWS Secrets Manager + EventBridge/SQS provider.
type AWSSecretsManagerSource struct {
	// SecretID is the ARN or friendly name of the AWS secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SecretID string `json:"secretID"`

	// Region is the AWS region (e.g. us-east-1).
	// +kubebuilder:validation:Optional
	Region string `json:"region,omitempty"`
}

// GCPSecretManagerSource reserved stub for native GCP Secret Manager + Pub/Sub provider.
type GCPSecretManagerSource struct {
	// SecretID is the resource path (projects/*/secrets/*) of the GCP secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SecretID string `json:"secretID"`
}

// VaultSource reserved stub for native HashiCorp Vault webhook/engine provider.
type VaultSource struct {
	// Path is the Vault secret path.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
}

// SecretSource defines the pluggable provider backend configuration.
// +kubebuilder:validation:XValidation:rule="self.type != 'AzureKeyVault' || has(self.azureKeyVault)",message="azureKeyVault configuration is required when source type is AzureKeyVault"
// +kubebuilder:validation:XValidation:rule="self.type != 'K8sSecret' || has(self.k8sSecret)",message="k8sSecret configuration is required when source type is K8sSecret"
// +kubebuilder:validation:XValidation:rule="self.type != 'AWSSecretsManager' || has(self.awsSecretsManager)",message="awsSecretsManager configuration is required when source type is AWSSecretsManager"
// +kubebuilder:validation:XValidation:rule="self.type != 'GCPSecretManager' || has(self.gcpSecretManager)",message="gcpSecretManager configuration is required when source type is GCPSecretManager"
// +kubebuilder:validation:XValidation:rule="self.type != 'Vault' || has(self.vault)",message="vault configuration is required when source type is Vault"
type SecretSource struct {
	// Type specifies the source provider backend.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=AzureKeyVault;K8sSecret;AWSSecretsManager;GCPSecretManager;Vault
	Type SourceType `json:"type"`

	// AzureKeyVault configures real-time event-driven ingestion from Azure Key Vault.
	// +kubebuilder:validation:Optional
	AzureKeyVault *AzureKeyVaultSource `json:"azureKeyVault,omitempty"`

	// K8sSecret configures universal multi-cloud ingestion via intermediate Kubernetes secrets (e.g., ESO synergy).
	// +kubebuilder:validation:Optional
	K8sSecret *K8sSecretSource `json:"k8sSecret,omitempty"`

	// AWSSecretsManager configures ingestion from AWS Secrets Manager.
	// +kubebuilder:validation:Optional
	AWSSecretsManager *AWSSecretsManagerSource `json:"awsSecretsManager,omitempty"`

	// GCPSecretManager configures ingestion from Google Cloud Secret Manager.
	// +kubebuilder:validation:Optional
	GCPSecretManager *GCPSecretManagerSource `json:"gcpSecretManager,omitempty"`

	// Vault configures ingestion from HashiCorp Vault.
	// +kubebuilder:validation:Optional
	Vault *VaultSource `json:"vault,omitempty"`
}

// NetworkPolicyProvider defines the network policy engine used for canary sandboxing.
// +kubebuilder:validation:Enum=Standard;Cilium
type NetworkPolicyProvider string

const (
	// NetworkPolicyProviderStandard generates networking.k8s.io/v1.NetworkPolicy.
	NetworkPolicyProviderStandard NetworkPolicyProvider = "Standard"
	// NetworkPolicyProviderCilium generates cilium.io/v2.CiliumNetworkPolicy for eBPF/Hubble visibility.
	NetworkPolicyProviderCilium NetworkPolicyProvider = "Cilium"
)

// NetworkPolicySpec configures the network isolation engine for canary workloads.
type NetworkPolicySpec struct {
	// Provider specifies the network policy engine (Standard for networking.k8s.io/v1, Cilium for cilium.io/v2 CiliumNetworkPolicy).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Standard;Cilium
	// +kubebuilder:default=Standard
	Provider NetworkPolicyProvider `json:"provider,omitempty"`
}

// VaultReference specifies the location and identity of the secret in Azure Key Vault / external vault.
// Deprecated: Use SecretSource (spec.source) instead.
type VaultReference struct {
	// KeyVaultURI is the URI of the Azure Key Vault or secret backend.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.matches('^https://[a-zA-Z0-9-]+\\\\.(?:vault\\\\.azure\\\\.net|vault\\\\.azure\\\\.cn|vault\\\\.usgovcloudapi\\\\.net)(?:/|$)?')",message="Invalid Azure Key Vault URI"
	KeyVaultURI string `json:"keyVaultURI"`

	// ObjectName is the name of the secret, certificate, or key within the vault.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.size() > 0",message="objectName must not be empty"
	ObjectName string `json:"objectName"`

	// ObjectType specifies whether the secret is a Secret, Certificate, or Key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Secret;Certificate;Key
	ObjectType VaultObjectType `json:"objectType"`
}

// WorkloadSelector defines the target deployment/workload to receive progressive rollouts.
type WorkloadSelector struct {
	// Kind is the type of Kubernetes resource to target (Deployment, StatefulSet, DaemonSet, Rollout).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Deployment;StatefulSet;DaemonSet;Rollout
	// +kubebuilder:default=Deployment
	Kind string `json:"kind"`

	// Name is the name of the target workload resource in the same namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.size() > 0",message="target workload name must not be empty"
	Name string `json:"name"`
}

// CanaryStrategy configures the progressive canary delivery timing.
type CanaryStrategy struct {
	// TimeoutSeconds defines the maximum duration in seconds to wait for canary health validation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=30
	// +kubebuilder:validation:XValidation:rule="self > 0",message="timeoutSeconds must be greater than 0"
	TimeoutSeconds int32 `json:"timeoutSeconds"`
}

// ProbeCredentials maps specific keys within the materialized secret to database credentials.
// Use this to explicitly declare which key holds the password, username, or database name,
// instead of relying on well-known name guessing.
type ProbeCredentials struct {
	// PasswordKey is the key in the materialized Kubernetes secret that holds the password.
	// Defaults to common names (password, pass, POSTGRES_PASSWORD) if not set.
	// For single-value secrets from Azure Key Vault, use the objectName value (e.g. "db-password").
	// +kubebuilder:validation:Optional
	PasswordKey string `json:"passwordKey,omitempty"`

	// UsernameKey is the key in the secret that holds the username.
	// +kubebuilder:validation:Optional
	UsernameKey string `json:"usernameKey,omitempty"`

	// DatabaseKey is the key in the secret that holds the database name.
	// +kubebuilder:validation:Optional
	DatabaseKey string `json:"databaseKey,omitempty"`
}

// JobProbeSpec defines the configuration for a Job-based validation probe.
// The operator will create an ephemeral batch/v1.Job in the target namespace,
// monitor it to completion, capture any failure logs, and clean it up automatically.
type JobProbeSpec struct {
	// TimeoutSeconds is the maximum number of seconds to wait for the Job to complete.
	// If the Job does not reach a terminal state within this time, it is deleted and
	// the probe is marked as failed.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=60
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`

	// JobTemplate is the batch/v1 job template spec to instantiate.
	// The operator will set an OwnerReference on the created Job.
	// The operator automatically injects the environment variable DSO_REVISION_SECRET_NAME
	// containing the name of the materialized Kubernetes Secret holding the new credentials
	// into all initContainers and containers in the Job template. Reference $(DSO_REVISION_SECRET_NAME)
	// in command, args, or env definitions.
	// +kubebuilder:validation:Required
	JobTemplate batchv1.JobTemplateSpec `json:"jobTemplate"`
}

// ValidationProbe configures synthetic probes executed during rollout.
// +kubebuilder:validation:XValidation:rule="self.type != 'Job' || has(self.job)",message="job specification is required when probe type is Job"
type ValidationProbe struct {
	// Type of probe to execute (TLS, PostgreSQL, MySQL, HTTP, Job).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=TLS;PostgreSQL;MySQL;HTTP;Job
	Type ProbeType `json:"type"`

	// Endpoint is the network address or URL tested by the probe.
	// Format: "host:port" or "host:port/dbname" for database probes.
	// Not used for Job probes.
	// +kubebuilder:validation:Optional
	Endpoint string `json:"endpoint,omitempty"`

	// QueryTimeout specifies probe timeout in seconds.
	// Not used for Job probes (use spec.job.timeoutSeconds instead).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:XValidation:rule="self >= 1",message="queryTimeout must be at least 1 second"
	QueryTimeout int32 `json:"queryTimeout,omitempty"`

	// Credentials explicitly maps secret keys to database credential fields.
	// If not set, the probe falls back to well-known key name conventions.
	// Not used for Job probes.
	// +kubebuilder:validation:Optional
	Credentials *ProbeCredentials `json:"credentials,omitempty"`

	// Job configures the ephemeral batch/v1.Job to run as the validation probe.
	// Required when Type is "Job"; ignored for all other probe types.
	// +kubebuilder:validation:Optional
	Job *JobProbeSpec `json:"job,omitempty"`
}

// RollbackConfig defines automated rollback rules and safety thresholds.
type RollbackConfig struct {
	// AutoRollback determines if the operator automatically reverts to the last known good revision on failure.
	// +kubebuilder:default=true
	AutoRollback bool `json:"autoRollback,omitempty"`

	// CircuitBreakerThreshold is the maximum number of consecutive failures permitted before halting reconciliations.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	// +kubebuilder:validation:XValidation:rule="self >= 1",message="circuitBreakerThreshold must be at least 1"
	CircuitBreakerThreshold int32 `json:"circuitBreakerThreshold,omitempty"`
}

// TargetRef specifies explicit binding targets inside the workload pod template to avoid ambiguous mutations.
type TargetRef struct {
	// VolumeName explicitly matches the Pod volume name (spec.template.spec.volumes[].name)
	// to replace its secretName with the materialized revision secret.
	// +kubebuilder:validation:Optional
	VolumeName string `json:"volumeName,omitempty"`

	// EnvName explicitly matches the container environment variable name (spec.template.spec.containers[].env[].name)
	// to bind valueFrom.secretKeyRef.name to the materialized revision secret.
	// +kubebuilder:validation:Optional
	EnvName string `json:"envName,omitempty"`

	// ContainerName specifies the target container name when envName is used.
	// If empty, matches across all containers and initContainers.
	// +kubebuilder:validation:Optional
	ContainerName string `json:"containerName,omitempty"`
}

// DynamicSecretPolicySpec defines the desired state of DynamicSecretPolicy.
// +kubebuilder:validation:XValidation:rule="has(self.source) || has(self.vaultRef)",message="either spec.source or spec.vaultRef must be specified"
type DynamicSecretPolicySpec struct {
	// Source configures the pluggable secret provider backend (AzureKeyVault, K8sSecret / ESO, AWS, GCP, Vault).
	// +kubebuilder:validation:Optional
	Source *SecretSource `json:"source,omitempty"`

	// VaultRef is the legacy Azure Key Vault reference. Deprecated: use spec.source instead.
	// +kubebuilder:validation:Optional
	VaultRef VaultReference `json:"vaultRef,omitempty"`

	// NetworkPolicy configures network isolation (standard NetworkPolicy or eBPF CiliumNetworkPolicy).
	// +kubebuilder:validation:Optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`

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

	// TargetRef explicitly binds the secret to a specific volume name or environment variable.
	// +kubebuilder:validation:Optional
	TargetRef *TargetRef `json:"targetRef,omitempty"`
}

// GetResolvedSource returns the active SecretSource, synthesizing one from legacy VaultRef if needed.
func (s *DynamicSecretPolicySpec) GetResolvedSource() SecretSource {
	if s.Source != nil {
		return *s.Source
	}
	if s.VaultRef.KeyVaultURI != "" || s.VaultRef.ObjectName != "" {
		objType := s.VaultRef.ObjectType
		if objType == "" {
			objType = VaultObjectTypeSecret
		}
		return SecretSource{
			Type: SourceTypeAzureKeyVault,
			AzureKeyVault: &AzureKeyVaultSource{
				KeyVaultURI: s.VaultRef.KeyVaultURI,
				ObjectName:  s.VaultRef.ObjectName,
				ObjectType:  objType,
			},
		}
	}
	return SecretSource{}
}

// GetVaultObjectName returns the primary name/identifier of the secret being managed.
func (s *DynamicSecretPolicySpec) GetVaultObjectName() string {
	src := s.GetResolvedSource()
	switch src.Type {
	case SourceTypeAzureKeyVault:
		if src.AzureKeyVault != nil {
			return src.AzureKeyVault.ObjectName
		}
	case SourceTypeK8sSecret:
		if src.K8sSecret != nil {
			return src.K8sSecret.Name
		}
	case SourceTypeAWSSecretsManager:
		if src.AWSSecretsManager != nil {
			return src.AWSSecretsManager.SecretID
		}
	case SourceTypeGCPSecretManager:
		if src.GCPSecretManager != nil {
			return src.GCPSecretManager.SecretID
		}
	case SourceTypeVault:
		if src.Vault != nil {
			return src.Vault.Path
		}
	}
	if s.VaultRef.ObjectName != "" {
		return s.VaultRef.ObjectName
	}
	return "secret"
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
