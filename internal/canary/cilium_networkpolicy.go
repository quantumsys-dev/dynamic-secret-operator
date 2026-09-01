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
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

// BuildCiliumNetworkPolicy constructs an eBPF-powered CiliumNetworkPolicy (cilium.io/v2)
// to provide rich L3/L4/L7 egress visibility and packet telemetry via Hubble.
// It enforces default-deny ingress and restricts egress to in-cluster CoreDNS and probe endpoints.
func BuildCiliumNetworkPolicy(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) *unstructured.Unstructured {
	targetName := policy.Spec.WorkloadSelector.Name
	netpolName := fmt.Sprintf("%s-canary-cilium-netpol", targetName)

	egressRules := []interface{}{
		// 1. CoreDNS in-cluster egress with L7 DNS visibility. Matched by the standard k8s-app
		// label rather than a namespace (which varies across distributions: kube-system,
		// openshift-dns, etc), across all namespaces.
		map[string]interface{}{
			"toEndpoints": []interface{}{
				map[string]interface{}{
					"matchExpressions": []interface{}{
						map[string]interface{}{
							"key":      "k8s:k8s-app",
							"operator": "In",
							"values":   []interface{}{"kube-dns", "coredns"},
						},
					},
				},
			},
			"toPorts": []interface{}{
				map[string]interface{}{
					"ports": []interface{}{
						map[string]interface{}{"port": "53", "protocol": "UDP"},
						map[string]interface{}{"port": "53", "protocol": "TCP"},
					},
					"rules": map[string]interface{}{
						"dns": []interface{}{
							map[string]interface{}{"matchPattern": "*"},
						},
					},
				},
			},
		},
	}

	// 2. Probe endpoint egress
	for _, probe := range policy.Spec.ValidationProbes {
		if probe.Type == secretv1alpha1.ProbeTypeJob {
			continue
		}
		cidrs, ports := extractTargetCIDRAndPort(ctx, probe.Endpoint, probe.Type)
		if len(cidrs) == 0 {
			// Neither a literal IP/CIDR nor a resolvable DNS name: fail closed by skipping this
			// rule entirely, rather than omitting toCIDRSet, which Cilium would otherwise treat
			// as unrestricted egress on these ports.
			continue
		}

		var ciliumPorts []interface{}
		for _, p := range ports {
			ciliumPorts = append(ciliumPorts, map[string]interface{}{
				"port":     strconv.Itoa(p),
				"protocol": "TCP",
			})
		}

		var cidrSet []interface{}
		for _, cidr := range cidrs {
			cidrSet = append(cidrSet, map[string]interface{}{"cidr": cidr})
		}

		egressRules = append(egressRules, map[string]interface{}{
			"toPorts": []interface{}{
				map[string]interface{}{
					"ports": ciliumPorts,
				},
			},
			"toCIDRSet": cidrSet,
		})
	}

	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2",
			"kind":       "CiliumNetworkPolicy",
			"metadata": map[string]interface{}{
				"name":      netpolName,
				"namespace": policy.Namespace,
				"labels": map[string]interface{}{
					"app.kubernetes.io/managed-by": "dynamic-secret-operator",
					LabelCanary:                    "true",
					LabelTargetWorkload:            targetName,
				},
			},
			"spec": map[string]interface{}{
				"endpointSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						LabelCanary:         "true",
						LabelTargetWorkload: targetName,
					},
				},
				"ingress": []interface{}{},
				"egress":  egressRules,
			},
		},
	}

	ownerRef := metav1.OwnerReference{
		APIVersion: secretv1alpha1.GroupVersion.String(),
		Kind:       "DynamicSecretPolicy",
		Name:       policy.Name,
		UID:        policy.UID,
	}
	u.SetOwnerReferences([]metav1.OwnerReference{ownerRef})

	return u
}
