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

package probes

import (
	"fmt"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

// NewProbeExecutor routes to the corresponding ProbeExecutor implementation based on probe type.
func NewProbeExecutor(probeType string) (ProbeExecutor, error) {
	switch secretv1alpha1.ProbeType(probeType) {
	case secretv1alpha1.ProbeTypeHTTP:
		return &HTTPProbe{}, nil
	case secretv1alpha1.ProbeTypeTLS:
		return &TLSProbe{}, nil
	case secretv1alpha1.ProbeTypePostgreSQL:
		return &PostgresProbe{}, nil
	case secretv1alpha1.ProbeTypeMySQL:
		return &MySQLProbe{}, nil
	default:
		return nil, fmt.Errorf("unsupported probe type: %s", probeType)
	}
}
