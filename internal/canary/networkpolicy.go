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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

// LabelCanary is the label key identifying canary workloads.
const LabelCanary = "dso.quantumsys.io/canary"

// LabelTargetWorkload identifies the primary workload being rotated.
const LabelTargetWorkload = "dso.quantumsys.io/target"

// BuildNetworkPolicy constructs an isolating Kubernetes NetworkPolicy around the canary workload.
// It enforces strict zero-trust default-deny on all Ingress, and restricts Egress strictly to DNS
// and the explicit network ports required by configured validation probes.
func BuildNetworkPolicy(policy *secretv1alpha1.DynamicSecretPolicy) *networkingv1.NetworkPolicy {
	targetName := policy.Spec.WorkloadSelector.Name
	netpolName := fmt.Sprintf("%s-canary-netpol", targetName)

	udpProtocol := corev1.ProtocolUDP
	tcpProtocol := corev1.ProtocolTCP
	dnsPort := intstr.FromInt(53)

	// 1. Mandatory Core DNS Egress Rules (UDP and TCP on port 53)
	egressPorts := []networkingv1.NetworkPolicyPort{
		{
			Protocol: &udpProtocol,
			Port:     &dnsPort,
		},
		{
			Protocol: &tcpProtocol,
			Port:     &dnsPort,
		},
	}

	// 2. Dynamic Probe Egress Ports (PostgreSQL: 5432, MySQL: 3306, HTTP: 80/443, TLS: 443)
	addedPorts := make(map[int]bool)
	addTCPPort := func(portNum int) {
		if !addedPorts[portNum] {
			p := intstr.FromInt(portNum)
			egressPorts = append(egressPorts, networkingv1.NetworkPolicyPort{
				Protocol: &tcpProtocol,
				Port:     &p,
			})
			addedPorts[portNum] = true
		}
	}

	for _, probe := range policy.Spec.ValidationProbes {
		switch probe.Type {
		case secretv1alpha1.ProbeTypePostgreSQL:
			addTCPPort(5432)
		case secretv1alpha1.ProbeTypeMySQL:
			addTCPPort(3306)
		case secretv1alpha1.ProbeTypeHTTP:
			addTCPPort(80)
			addTCPPort(443)
		case secretv1alpha1.ProbeTypeTLS:
			addTCPPort(443)
		}
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      netpolName,
			Namespace: policy.Namespace,
			Labels: map[string]string{
				LabelCanary:         "true",
				LabelTargetWorkload: targetName,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelCanary:         "true",
					LabelTargetWorkload: targetName,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			// Strict Default Deny on Ingress (empty slice)
			Ingress: []networkingv1.NetworkPolicyIngressRule{},
			// Strict Least-Privilege Egress (DNS + Active Probes)
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					Ports: egressPorts,
				},
			},
		},
	}
}
