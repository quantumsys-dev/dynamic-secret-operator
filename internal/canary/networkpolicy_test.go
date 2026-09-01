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
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

func TestBuildNetworkPolicy(t *testing.T) {
	t.Run("builds default DNS and probe egress rules", func(t *testing.T) {
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
					{Type: secretv1alpha1.ProbeTypePostgreSQL},
					{Type: secretv1alpha1.ProbeTypeMySQL},
					{Type: secretv1alpha1.ProbeTypeHTTP},
					{Type: secretv1alpha1.ProbeTypeTLS},
				},
			},
		}

		netpol := BuildNetworkPolicy(policy)

		// 1. Validate Metadata and Selectors
		expectedName := "orders-api-canary-netpol"
		if netpol.Name != expectedName {
			t.Errorf("expected name %q, got %q", expectedName, netpol.Name)
		}
		if netpol.Namespace != "production" {
			t.Errorf("expected namespace %q, got %q", "production", netpol.Namespace)
		}
		if netpol.Spec.PodSelector.MatchLabels[LabelCanary] != "true" {
			t.Errorf("expected PodSelector match label %s=true", LabelCanary)
		}
		if netpol.Spec.PodSelector.MatchLabels[LabelTargetWorkload] != "orders-api" {
			t.Errorf("expected PodSelector match label %s=orders-api", LabelTargetWorkload)
		}

		// 2. Validate Policy Types
		if len(netpol.Spec.PolicyTypes) != 2 {
			t.Fatalf("expected 2 PolicyTypes (Ingress, Egress), got %d", len(netpol.Spec.PolicyTypes))
		}
		if netpol.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress || netpol.Spec.PolicyTypes[1] != networkingv1.PolicyTypeEgress {
			t.Errorf("expected PolicyTypes [Ingress, Egress], got %v", netpol.Spec.PolicyTypes)
		}

		// 3. Validate Strict Ingress Default-Deny
		if netpol.Spec.Ingress == nil || len(netpol.Spec.Ingress) != 0 {
			t.Errorf("expected Ingress slice to be empty [] for default-deny, got: %v", netpol.Spec.Ingress)
		}

		// 4. Validate DNS rule presence and strict in-cluster CoreDNS restriction
		if len(netpol.Spec.Egress) < 1 {
			t.Fatalf("expected egress rules, got %d", len(netpol.Spec.Egress))
		}
		dnsRule := netpol.Spec.Egress[0]
		if len(dnsRule.Ports) != 2 {
			t.Errorf("expected 2 DNS ports (UDP/TCP 53), got %d", len(dnsRule.Ports))
		}
		if len(dnsRule.To) != 1 {
			t.Fatalf("expected DNS egress rule to have 1 To peer (kube-system CoreDNS), got %d", len(dnsRule.To))
		}
		if dnsRule.To[0].NamespaceSelector == nil || dnsRule.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" {
			t.Errorf("expected DNS egress to restrict namespaceSelector to kube-system")
		}
		if dnsRule.To[0].PodSelector == nil || len(dnsRule.To[0].PodSelector.MatchExpressions) == 0 {
			t.Errorf("expected DNS egress to restrict podSelector to CoreDNS/kube-dns pods")
		}
	})

	t.Run("restricts egress to explicit target endpoint IP and CIDR blocks", func(t *testing.T) {
		policy := &secretv1alpha1.DynamicSecretPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secure-dsp",
				Namespace: "default",
			},
			Spec: secretv1alpha1.DynamicSecretPolicySpec{
				WorkloadSelector: secretv1alpha1.WorkloadSelector{
					Kind: "Deployment",
					Name: "secure-api",
				},
				ValidationProbes: []secretv1alpha1.ValidationProbe{
					{
						Type:     secretv1alpha1.ProbeTypePostgreSQL,
						Endpoint: "10.240.0.5:5432/mydb",
					},
					{
						Type:     secretv1alpha1.ProbeTypeMySQL,
						Endpoint: "10.240.1.0/24:3306",
					},
					{
						Type:     secretv1alpha1.ProbeTypeHTTP,
						Endpoint: "https://10.240.2.15:8443/healthz",
					},
				},
			},
		}

		netpol := BuildNetworkPolicy(policy)

		// Rule 0 is DNS
		// Rule 1 is PostgreSQL with IPBlock 10.240.0.5/32 and port 5432
		if len(netpol.Spec.Egress) != 4 {
			t.Fatalf("expected 4 egress rules (1 DNS + 3 probes), got %d", len(netpol.Spec.Egress))
		}

		pgRule := netpol.Spec.Egress[1]
		if len(pgRule.To) != 1 || pgRule.To[0].IPBlock == nil || pgRule.To[0].IPBlock.CIDR != "10.240.0.5/32" {
			t.Errorf("expected PostgreSQL egress restricted to 10.240.0.5/32, got: %v", pgRule.To)
		}
		if len(pgRule.Ports) != 1 || pgRule.Ports[0].Port.IntValue() != 5432 {
			t.Errorf("expected PostgreSQL port 5432, got: %v", pgRule.Ports)
		}

		mysqlRule := netpol.Spec.Egress[2]
		if len(mysqlRule.To) != 1 || mysqlRule.To[0].IPBlock == nil || mysqlRule.To[0].IPBlock.CIDR != "10.240.1.0/24" {
			t.Errorf("expected MySQL egress restricted to 10.240.1.0/24, got: %v", mysqlRule.To)
		}
		if len(mysqlRule.Ports) != 1 || mysqlRule.Ports[0].Port.IntValue() != 3306 {
			t.Errorf("expected MySQL port 3306, got: %v", mysqlRule.Ports)
		}

		httpRule := netpol.Spec.Egress[3]
		if len(httpRule.To) != 1 || httpRule.To[0].IPBlock == nil || httpRule.To[0].IPBlock.CIDR != "10.240.2.15/32" {
			t.Errorf("expected HTTP egress restricted to 10.240.2.15/32, got: %v", httpRule.To)
		}
		if len(httpRule.Ports) != 1 || httpRule.Ports[0].Port.IntValue() != 8443 {
			t.Errorf("expected HTTP port 8443, got: %v", httpRule.Ports)
		}
	})
}

