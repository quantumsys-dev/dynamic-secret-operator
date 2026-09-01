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

package canary

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

func TestBuildCiliumNetworkPolicy(t *testing.T) {
	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-dsp",
			Namespace: "production",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "orders-api",
			},
			ValidationProbes: []secretv1alpha1.ValidationProbe{
				{
					Type:     secretv1alpha1.ProbeTypePostgreSQL,
					Endpoint: "10.0.1.5:5432",
				},
				{
					Type:     secretv1alpha1.ProbeTypeHTTP,
					Endpoint: "https://10.0.2.10:8443/healthz",
				},
			},
		},
	}

	cnp := BuildCiliumNetworkPolicy(context.Background(), policy)

	if cnp.GetAPIVersion() != "cilium.io/v2" {
		t.Errorf("expected apiVersion cilium.io/v2, got %s", cnp.GetAPIVersion())
	}
	if cnp.GetKind() != "CiliumNetworkPolicy" {
		t.Errorf("expected kind CiliumNetworkPolicy, got %s", cnp.GetKind())
	}
	if cnp.GetName() != "orders-api-canary-cilium-netpol" {
		t.Errorf("expected name orders-api-canary-cilium-netpol, got %s", cnp.GetName())
	}
	if cnp.GetNamespace() != "production" {
		t.Errorf("expected namespace production, got %s", cnp.GetNamespace())
	}

	spec, ok := cnp.Object["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected spec map in unstructured CiliumNetworkPolicy")
	}

	egress, ok := spec["egress"].([]interface{})
	if !ok || len(egress) != 3 {
		t.Fatalf("expected 3 egress rules (DNS + 2 probes), got %d", len(egress))
	}

	dnsRule, ok := egress[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected DNS egress rule to be a map")
	}
	toEndpoints, ok := dnsRule["toEndpoints"].([]interface{})
	if !ok || len(toEndpoints) != 1 {
		t.Fatalf("expected 1 toEndpoints selector on DNS rule, got %d", len(toEndpoints))
	}
	endpointSelector, ok := toEndpoints[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected toEndpoints[0] to be a map")
	}
	if _, hasNamespaceMatch := endpointSelector["matchLabels"]; hasNamespaceMatch {
		t.Errorf("expected no namespace-scoped matchLabels on DNS rule, since the DNS namespace name varies by distribution")
	}
	matchExpressions, ok := endpointSelector["matchExpressions"].([]interface{})
	if !ok || len(matchExpressions) != 1 {
		t.Fatalf("expected 1 matchExpressions entry on DNS rule, got %d", len(matchExpressions))
	}
	expr, ok := matchExpressions[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected matchExpressions[0] to be a map")
	}
	if expr["key"] != "k8s:k8s-app" {
		t.Errorf("expected matchExpressions key 'k8s:k8s-app', got %v", expr["key"])
	}
	if expr["operator"] != "In" {
		t.Errorf("expected matchExpressions operator 'In', got %v", expr["operator"])
	}
	values, ok := expr["values"].([]interface{})
	if !ok || len(values) != 2 || values[0] != "kube-dns" || values[1] != "coredns" {
		t.Errorf("expected matchExpressions values [kube-dns, coredns], got %v", expr["values"])
	}
}

func TestBuildCiliumNetworkPolicy_FailsClosedOnUnresolvableEndpoint(t *testing.T) {
	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unresolvable-dsp",
			Namespace: "production",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			WorkloadSelector: secretv1alpha1.WorkloadSelector{
				Kind: "Deployment",
				Name: "unresolvable-api",
			},
			ValidationProbes: []secretv1alpha1.ValidationProbe{
				{
					Type: secretv1alpha1.ProbeTypePostgreSQL,
					// RFC 2606 reserves the .invalid TLD to always fail to resolve.
					Endpoint: "this-host-does-not-exist.invalid:5432",
				},
			},
		},
	}

	cnp := BuildCiliumNetworkPolicy(context.Background(), policy)
	spec, ok := cnp.Object["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected spec map in unstructured CiliumNetworkPolicy")
	}
	egress, ok := spec["egress"].([]interface{})
	if !ok || len(egress) != 1 {
		t.Fatalf("expected only the DNS egress rule (unresolvable probe endpoint skipped), got %d rules: %+v", len(egress), egress)
	}
}
