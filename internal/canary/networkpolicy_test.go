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

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

func TestBuildNetworkPolicy(t *testing.T) {
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

	// 4. Validate Egress Rules (DNS + Probes)
	if len(netpol.Spec.Egress) != 1 {
		t.Fatalf("expected 1 egress rule group, got %d", len(netpol.Spec.Egress))
	}
	ports := netpol.Spec.Egress[0].Ports

	expectedPorts := map[int]corev1.Protocol{
		53:   corev1.ProtocolUDP, // DNS UDP
		5432: corev1.ProtocolTCP, // PostgreSQL
		3306: corev1.ProtocolTCP, // MySQL
		80:   corev1.ProtocolTCP, // HTTP
		443:  corev1.ProtocolTCP, // HTTPS / TLS
	}

	hasTCP53 := false
	for _, p := range ports {
		if p.Port.IntValue() == 53 && *p.Protocol == corev1.ProtocolTCP {
			hasTCP53 = true
		}
		if proto, ok := expectedPorts[p.Port.IntValue()]; ok && *p.Protocol == proto {
			delete(expectedPorts, p.Port.IntValue())
		}
	}

	if !hasTCP53 {
		t.Errorf("expected DNS TCP port 53 in egress rules")
	}

	if len(expectedPorts) > 0 {
		t.Errorf("missing expected egress ports in generated NetworkPolicy: %v", expectedPorts)
	}
}
