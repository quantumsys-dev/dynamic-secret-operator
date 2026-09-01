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
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

// LabelCanary is the label key identifying canary workloads.
const LabelCanary = "dso.quantumsys.dev/canary"

// LabelTargetWorkload identifies the primary workload being rotated.
const LabelTargetWorkload = "dso.quantumsys.dev/target"

// BuildNetworkPolicy constructs an isolating Kubernetes NetworkPolicy around the canary workload.
// It enforces strict zero-trust default-deny on all Ingress, and restricts Egress strictly to DNS
// and the explicit target endpoint IP/CIDR and network ports required by configured validation probes.
func BuildNetworkPolicy(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) *networkingv1.NetworkPolicy {
	targetName := policy.Spec.WorkloadSelector.Name
	netpolName := fmt.Sprintf("%s-canary-netpol", targetName)

	udpProtocol := corev1.ProtocolUDP
	tcpProtocol := corev1.ProtocolTCP
	dnsPort := intstr.FromInt(53)

	// 1. Mandatory Core DNS Egress Rules locked down to in-cluster DNS pods by the standard
	// k8s-app label, matched across all namespaces since the DNS namespace name varies by
	// distribution (kube-system, openshift-dns, etc). This still prevents DNS tunneling
	// exfiltration to arbitrary external nameservers.
	egressRules := []networkingv1.NetworkPolicyEgressRule{
		{
			To: []networkingv1.NetworkPolicyPeer{
				{
					NamespaceSelector: &metav1.LabelSelector{},
					PodSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "k8s-app",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"kube-dns", "coredns"},
							},
						},
					},
				},
			},
			Ports: []networkingv1.NetworkPolicyPort{
				{
					Protocol: &udpProtocol,
					Port:     &dnsPort,
				},
				{
					Protocol: &tcpProtocol,
					Port:     &dnsPort,
				},
			},
		},
	}

	// 2. Dynamic Probe Egress Rules with Target IP/CIDR restrictions
	for _, probe := range policy.Spec.ValidationProbes {
		if probe.Type == secretv1alpha1.ProbeTypeJob {
			continue
		}
		cidrs, ports := extractTargetCIDRAndPort(ctx, probe.Endpoint, probe.Type)
		if len(cidrs) == 0 {
			// Neither a literal IP/CIDR nor a resolvable DNS name: fail closed by skipping this
			// rule entirely rather than omitting the "To" restriction, which Kubernetes would
			// otherwise interpret as unrestricted egress (0.0.0.0/0) on these ports.
			continue
		}

		var npPorts []networkingv1.NetworkPolicyPort
		for _, portNum := range ports {
			p := intstr.FromInt(portNum)
			npPorts = append(npPorts, networkingv1.NetworkPolicyPort{
				Protocol: &tcpProtocol,
				Port:     &p,
			})
		}

		var peers []networkingv1.NetworkPolicyPeer
		for _, cidr := range cidrs {
			peers = append(peers, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{
					CIDR: cidr,
				},
			})
		}

		egressRules = append(egressRules, networkingv1.NetworkPolicyEgressRule{
			Ports: npPorts,
			To:    peers,
		})
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
			// Strict Least-Privilege Egress (DNS + Active Probes restricted by IP/CIDR & Port)
			Egress: egressRules,
		},
	}
}

// extractTargetCIDRAndPort parses an endpoint string and resolves target IP/CIDR(s) and port.
// If the host portion is neither a literal IP nor a CIDR, it is resolved as a DNS name (e.g. an
// in-cluster Service hostname) so egress can still be scoped to concrete addresses. If that
// resolution also fails, it returns no CIDRs (fail closed) rather than letting the caller treat
// an empty restriction as unrestricted egress.
func extractTargetCIDRAndPort(ctx context.Context, endpoint string, probeType secretv1alpha1.ProbeType) ([]string, []int) {
	defaultPorts := map[secretv1alpha1.ProbeType][]int{
		secretv1alpha1.ProbeTypePostgreSQL: {5432},
		secretv1alpha1.ProbeTypeMySQL:      {3306},
		secretv1alpha1.ProbeTypeHTTP:       {80, 443},
		secretv1alpha1.ProbeTypeTLS:        {443},
	}

	if endpoint == "" {
		return nil, defaultPorts[probeType]
	}

	raw := endpoint
	// Strip URL scheme if present
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			raw = u.Host
			if u.Scheme == "https" && u.Port() == "" {
				defaultPorts[probeType] = []int{443}
			} else if u.Scheme == "http" && u.Port() == "" {
				defaultPorts[probeType] = []int{80}
			}
		}
	}

	var ports []int
	var host string

	// Handle case: "10.240.1.0/24:3306" (CIDR with explicit port)
	if lastColon := strings.LastIndex(raw, ":"); lastColon != -1 && strings.Contains(raw, "/") && lastColon > strings.Index(raw, "/") {
		candidatePort := raw[lastColon+1:]
		if portNum, err := strconv.Atoi(candidatePort); err == nil && portNum > 0 {
			ports = []int{portNum}
			host = raw[:lastColon]
		}
	}

	if host == "" {
		// Handle database path suffix: "10.240.0.5:5432/mydb"
		clean := raw
		if idx := strings.Index(clean, "/"); idx != -1 {
			// Check if clean is a standalone CIDR like 10.0.0.0/16
			if _, _, err := net.ParseCIDR(clean); err != nil {
				clean = clean[:idx]
			}
		}

		if h, p, err := net.SplitHostPort(clean); err == nil {
			host = h
			if portNum, err := strconv.Atoi(p); err == nil && portNum > 0 {
				ports = []int{portNum}
			}
		} else {
			host = clean
		}
	}

	if len(ports) == 0 {
		ports = defaultPorts[probeType]
	}

	// Determine if host is a valid CIDR or IP
	if _, ipNet, err := net.ParseCIDR(host); err == nil {
		return []string{ipNet.String()}, ports
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return []string{fmt.Sprintf("%s/32", ip.String())}, ports
		}
		return []string{fmt.Sprintf("%s/128", ip.String())}, ports
	}

	// Not a literal IP/CIDR: resolve as a DNS name. A bounded timeout keeps a slow or broken
	// resolver from stalling reconciliation; failure to resolve returns no CIDRs (fail closed).
	resolveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(resolveCtx, host)
	if err != nil || len(addrs) == 0 {
		return nil, ports
	}

	cidrs := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if ip4 := addr.IP.To4(); ip4 != nil {
			cidrs = append(cidrs, fmt.Sprintf("%s/32", ip4.String()))
		} else {
			cidrs = append(cidrs, fmt.Sprintf("%s/128", addr.IP.String()))
		}
	}
	return cidrs, ports
}

