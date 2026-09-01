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
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/lib/pq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/pkg/telemetry"
)

// PostgresProbe executes synthetic connectivity and authentication validation against PostgreSQL.
type PostgresProbe struct {
	// DBConnector allows injecting mock SQL drivers during testing.
	DBConnector func(driverName, dataSourceName string) (*sql.DB, error)
}

// Execute parses rotated credentials, opens a connection to PostgreSQL, executes a lightweight "SELECT 1"
// query, and strictly sanitizes any returned errors.
func (p *PostgresProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	ctx, span := telemetry.Tracer.Start(ctx, "ExecutePostgresProbe",
		trace.WithAttributes(
			attribute.String("probe.type", string(secretv1alpha1.ProbeTypePostgreSQL)),
		),
	)
	defer span.End()

	if config.Endpoint == "" {
		err := errors.New("postgresql probe endpoint must not be empty")
		span.RecordError(err)
		return err
	}

	if config.QueryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.QueryTimeout)*time.Second)
		defer cancel()
	}

	// Extract database credentials from secretData.
	// When Credentials is explicitly configured in the probe spec, use those key names directly.
	// Otherwise fall back to probing well-known key name conventions.
	var username, password, dbname string

	if config.Credentials != nil {
		// Explicit key mapping — no guessing required
		if config.Credentials.PasswordKey != "" {
			password = string(secretData[config.Credentials.PasswordKey])
		}
		if config.Credentials.UsernameKey != "" {
			username = string(secretData[config.Credentials.UsernameKey])
		}
		if config.Credentials.DatabaseKey != "" {
			dbname = string(secretData[config.Credentials.DatabaseKey])
		}
	}

	// Fall back to well-known name conventions for any fields not resolved above
	if password == "" {
		password = extractSecretValue(secretData, "password", "pass", "POSTGRES_PASSWORD", "pgpassword")
	}
	if password == "" && username == "" && dbname == "" && len(secretData) == 1 {
		// Single-value secret (e.g. Azure Key Vault raw secret) — use the sole value as password
		for _, v := range secretData {
			password = string(v)
		}
	}
	if username == "" {
		username = extractSecretValue(secretData, "username", "user", "POSTGRES_USER", "pguser")
	}
	if dbname == "" {
		dbname = extractSecretValue(secretData, "dbname", "database", "POSTGRES_DB", "db")
	}

	host, port, endpointDB, err := parsePostgresEndpoint(config.Endpoint, "5432")
	if err != nil || host == "" || strings.ContainsAny(host, "?&/\\@#(): \t\r\n") || strings.ContainsAny(port, "?&/\\@#(): \t\r\n") || strings.ContainsAny(endpointDB, "?&\\@# \t\r\n") {
		hostErr := fmt.Errorf("invalid or unsafe postgres endpoint: %q", config.Endpoint)
		span.RecordError(hostErr)
		return hostErr
	}
	if portNum, pErr := strconv.Atoi(port); pErr != nil || portNum <= 0 || portNum > 65535 {
		portErr := fmt.Errorf("invalid or unsafe postgres endpoint: %q", config.Endpoint)
		span.RecordError(portErr)
		return portErr
	}

	if endpointDB != "" && dbname == "" {
		dbname = endpointDB
	}
	if dbname == "" {
		dbname = "appdb"
	}
	if username == "" {
		username = "postgres"
	}

	// Note: database/sql and URL formatting require string DSNs. In Go, string conversions
	// allocate immutable memory on the heap that cannot be reliably zeroed; DSO relies on
	// OS/container-level controls (readOnlyRootFilesystem, runAsNonRoot, dropped capabilities,
	// disabled core dumps) rather than in-process memory zeroing - see docs/security.md.

	escapedUser := url.QueryEscape(username)
	escapedPassword := url.QueryEscape(password)
	escapedDBName := url.QueryEscape(dbname)

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable&connect_timeout=%d",
		escapedUser, escapedPassword, host, port, escapedDBName, 5)

	connector := p.DBConnector
	if connector == nil {
		connector = func(driverName, dataSourceName string) (*sql.DB, error) {
			return otelsql.Open(driverName, dataSourceName, otelsql.WithTracerProvider(otel.GetTracerProvider()))
		}
	}

	db, err := connector("postgres", dsn)
	if err != nil {
		sanitized := SanitizeDBError(err, password, dsn)
		span.RecordError(sanitized)
		return sanitized
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()

	// Execute a synthetic validation ping
	if err := db.PingContext(ctx); err != nil {
		// If custom database doesn't exist, retry ping with default "postgres" database
		if strings.Contains(err.Error(), "does not exist") && dbname != "postgres" {
			fallbackDSN := fmt.Sprintf("postgres://%s:%s@%s:%s/postgres?sslmode=disable&connect_timeout=%d",
				escapedUser, escapedPassword, host, port, 5)
			if fallbackDB, fErr := connector("postgres", fallbackDSN); fErr == nil {
				defer fallbackDB.Close()
				if pErr := fallbackDB.PingContext(ctx); pErr == nil {
					return nil
				}
			}
		}
		sanitized := SanitizeDBError(err, password, dsn)
		span.RecordError(sanitized)
		return sanitized
	}

	var result int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		sanitized := SanitizeDBError(err, password, dsn)
		span.RecordError(sanitized)
		return sanitized
	}

	return nil
}

func extractSecretValue(data map[string][]byte, keys ...string) string {
	if data == nil {
		return ""
	}
	for _, k := range keys {
		if val, ok := data[k]; ok && len(val) > 0 {
			return string(val)
		}
		for dataKey, val := range data {
			if strings.EqualFold(dataKey, k) && len(val) > 0 {
				return string(val)
			}
		}
	}
	return ""
}

func parsePostgresEndpoint(endpoint, defaultPort string) (string, string, string, error) {
	endpoint = strings.TrimPrefix(endpoint, "tcp://")
	endpoint = strings.TrimPrefix(endpoint, "postgres://")
	endpoint = strings.TrimPrefix(endpoint, "postgresql://")

	var dbName string
	if slashIdx := strings.Index(endpoint, "/"); slashIdx != -1 {
		dbName = strings.TrimPrefix(endpoint[slashIdx:], "/")
		endpoint = endpoint[:slashIdx]
	}

	if strings.Contains(endpoint, ":") {
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return "", "", "", err
		}
		return host, port, dbName, nil
	}
	return endpoint, defaultPort, dbName, nil
}

func splitHostPort(endpoint, defaultPort string) (string, string, error) {
	endpoint = strings.TrimPrefix(endpoint, "tcp://")
	if strings.Contains(endpoint, ":") {
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			return "", "", err
		}
		return host, port, nil
	}
	return endpoint, defaultPort, nil
}
