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
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

// BuildCiliumNetworkPolicy constructs an eBPF-powered CiliumNetworkPolicy (cilium.io/v2)
// to provide rich L3/L4/L7 egress visibility and packet telemetry via Hubble.
// It enforces default-deny ingress and restricts egress to in-cluster CoreDNS and probe endpoints.
func BuildCiliumNetworkPolicy(policy *secretv1alpha1.DynamicSecretPolicy) *unstructured.Unstructured {
	targetName := policy.Spec.WorkloadSelector.Name
	netpolName := fmt.Sprintf("%s-canary-cilium-netpol", targetName)

	egressRules := []interface{}{
		// 1. CoreDNS in-cluster egress with L7 DNS visibility
		map[string]interface{}{
			"toEndpoints": []interface{}{
				map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"k8s:io.kubernetes.pod.namespace": "kube-system",
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
		cidr, ports := extractTargetCIDRAndPort(probe.Endpoint, probe.Type)
		var ciliumPorts []interface{}
		for _, p := range ports {
			ciliumPorts = append(ciliumPorts, map[string]interface{}{
				"port":     strconv.Itoa(p),
				"protocol": "TCP",
			})
		}

		rule := map[string]interface{}{
			"toPorts": []interface{}{
				map[string]interface{}{
					"ports": ciliumPorts,
				},
			},
		}
		if cidr != "" {
			rule["toCIDRSet"] = []interface{}{
				map[string]interface{}{"cidr": cidr},
			}
		}
		egressRules = append(egressRules, rule)
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
