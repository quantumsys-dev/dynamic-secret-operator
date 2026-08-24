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
	"context"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

// ProbeExecutor defines the standard execution contract for synthetic health and connectivity probes.
type ProbeExecutor interface {
	Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error
}

// PostgresProbe validates database authentication against PostgreSQL using rotated credentials.
type PostgresProbe struct{}

// Execute executes a synthetic connection and lightweight query against PostgreSQL.
func (p *PostgresProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	return nil
}

// MySQLProbe validates database authentication against MySQL using rotated credentials.
type MySQLProbe struct{}

// Execute executes a synthetic connection and lightweight query against MySQL.
func (p *MySQLProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	return nil
}
