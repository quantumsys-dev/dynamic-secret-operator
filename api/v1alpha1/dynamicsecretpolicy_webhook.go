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
	"context"
	"regexp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var dynamicsecretpolicylog = logf.Log.WithName("dynamicsecretpolicy-resource")

// azureKeyVaultURIRegex strictly matches valid Azure Key Vault endpoints with exact domain boundary anchoring.
var azureKeyVaultURIRegex = regexp.MustCompile(`^https://[a-zA-Z0-9-]+(?:\.vault\.azure\.net)(?:/.*)?$`)

// SetupWebhookWithManager registers the webhook with the controller manager.
func (r *DynamicSecretPolicy) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, r).
		WithDefaulter(r).
		WithValidator(r).
		Complete()
}

//+kubebuilder:webhook:path=/mutate-dso-quantumsys-dev-v1alpha1-dynamicsecretpolicy,mutating=true,failurePolicy=fail,sideEffects=None,groups=dso.quantumsys.dev,resources=dynamicsecretpolicies,verbs=create;update,versions=v1alpha1,name=mdynamicsecretpolicy.kb.io,admissionReviewVersions=v1

var _ admission.Defaulter[*DynamicSecretPolicy] = &DynamicSecretPolicy{}

// Default implements webhook defaulting for DynamicSecretPolicy.
func (r *DynamicSecretPolicy) Default(ctx context.Context, obj *DynamicSecretPolicy) error {
	dynamicsecretpolicylog.Info("defaulting DynamicSecretPolicy", "name", obj.Name, "namespace", obj.Namespace)

	// Default RollbackConfig
	if obj.Spec.RollbackConfig == nil {
		obj.Spec.RollbackConfig = &RollbackConfig{
			AutoRollback:            true,
			CircuitBreakerThreshold: 3,
		}
	} else if obj.Spec.RollbackConfig.CircuitBreakerThreshold <= 0 {
		obj.Spec.RollbackConfig.CircuitBreakerThreshold = 3
	}

	// Default CanaryStrategy
	if obj.Spec.CanaryStrategy == nil {
		obj.Spec.CanaryStrategy = &CanaryStrategy{
			TimeoutSeconds: 30,
		}
	} else if obj.Spec.CanaryStrategy.TimeoutSeconds <= 0 {
		obj.Spec.CanaryStrategy.TimeoutSeconds = 30
	}

	return nil
}

//+kubebuilder:webhook:path=/validate-dso-quantumsys-dev-v1alpha1-dynamicsecretpolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=dso.quantumsys.dev,resources=dynamicsecretpolicies,verbs=create;update,versions=v1alpha1,name=vdynamicsecretpolicy.kb.io,admissionReviewVersions=v1

var _ admission.Validator[*DynamicSecretPolicy] = &DynamicSecretPolicy{}

// ValidateCreate implements webhook validation on create.
func (r *DynamicSecretPolicy) ValidateCreate(ctx context.Context, obj *DynamicSecretPolicy) (admission.Warnings, error) {
	dynamicsecretpolicylog.Info("validate create", "name", obj.Name, "namespace", obj.Namespace)
	return nil, obj.validateDynamicSecretPolicy()
}

// ValidateUpdate implements webhook validation on update.
func (r *DynamicSecretPolicy) ValidateUpdate(ctx context.Context, oldObj, newObj *DynamicSecretPolicy) (admission.Warnings, error) {
	dynamicsecretpolicylog.Info("validate update", "name", newObj.Name, "namespace", newObj.Namespace)
	return nil, newObj.validateDynamicSecretPolicy()
}

// ValidateDelete implements webhook validation on delete.
func (r *DynamicSecretPolicy) ValidateDelete(ctx context.Context, obj *DynamicSecretPolicy) (admission.Warnings, error) {
	return nil, nil
}

// validateDynamicSecretPolicy performs structural and business-rule validation.
func (r *DynamicSecretPolicy) validateDynamicSecretPolicy() error {
	var allErrs field.ErrorList

	// 1. VaultReference Validation
	vaultRefPath := field.NewPath("spec", "vaultRef")
	if !azureKeyVaultURIRegex.MatchString(r.Spec.VaultRef.KeyVaultURI) {
		allErrs = append(allErrs, field.Invalid(
			vaultRefPath.Child("keyVaultURI"),
			r.Spec.VaultRef.KeyVaultURI,
			"must be a valid Azure Key Vault URI format (e.g. https://<vault-name>.vault.azure.net)",
		))
	}

	if r.Spec.VaultRef.ObjectName == "" {
		allErrs = append(allErrs, field.Required(
			vaultRefPath.Child("objectName"),
			"objectName must be specified",
		))
	}

	switch r.Spec.VaultRef.ObjectType {
	case VaultObjectTypeSecret, VaultObjectTypeCertificate, VaultObjectTypeKey:
		// Valid
	default:
		allErrs = append(allErrs, field.NotSupported(
			vaultRefPath.Child("objectType"),
			r.Spec.VaultRef.ObjectType,
			[]string{string(VaultObjectTypeSecret), string(VaultObjectTypeCertificate), string(VaultObjectTypeKey)},
		))
	}

	// 2. WorkloadSelector Validation
	workloadPath := field.NewPath("spec", "workloadSelector")
	if r.Spec.WorkloadSelector.Kind == "" {
		allErrs = append(allErrs, field.Required(
			workloadPath.Child("kind"),
			"target workload kind must be specified",
		))
	}
	if r.Spec.WorkloadSelector.Name == "" {
		allErrs = append(allErrs, field.Required(
			workloadPath.Child("name"),
			"target workload name must be specified",
		))
	}

	// 3. CanaryStrategy Validation
	if r.Spec.CanaryStrategy != nil && r.Spec.CanaryStrategy.TimeoutSeconds <= 0 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "canaryStrategy", "timeoutSeconds"),
			r.Spec.CanaryStrategy.TimeoutSeconds,
			"timeoutSeconds must be greater than 0",
		))
	}

	// 4. ValidationProbes Validation
	for idx, probe := range r.Spec.ValidationProbes {
		probePath := field.NewPath("spec", "validationProbes").Index(idx)
		switch probe.Type {
		case ProbeTypeTLS, ProbeTypePostgreSQL, ProbeTypeMySQL, ProbeTypeHTTP:
			// Valid
		default:
			allErrs = append(allErrs, field.NotSupported(
				probePath.Child("type"),
				probe.Type,
				[]string{string(ProbeTypeTLS), string(ProbeTypePostgreSQL), string(ProbeTypeMySQL), string(ProbeTypeHTTP)},
			))
		}
		if probe.QueryTimeout < 0 {
			allErrs = append(allErrs, field.Invalid(
				probePath.Child("queryTimeout"),
				probe.QueryTimeout,
				"queryTimeout cannot be negative",
			))
		}
	}

	// 5. RollbackConfig Validation
	if r.Spec.RollbackConfig != nil && r.Spec.RollbackConfig.CircuitBreakerThreshold < 1 {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "rollbackConfig", "circuitBreakerThreshold"),
			r.Spec.RollbackConfig.CircuitBreakerThreshold,
			"circuitBreakerThreshold must be at least 1",
		))
	}

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: GroupVersion.Group, Kind: "DynamicSecretPolicy"},
		r.Name,
		allErrs,
	)
}
