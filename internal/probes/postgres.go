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
	"strings"
	"time"

	_ "github.com/lib/pq"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys/dynamic-secret-operator/internal/azure"
)

// PostgresProbe executes synthetic connectivity and authentication validation against PostgreSQL.
type PostgresProbe struct {
	// DBConnector allows injecting mock SQL drivers during testing.
	DBConnector func(driverName, dataSourceName string) (*sql.DB, error)
}

// Execute parses rotated credentials, opens a connection to PostgreSQL, executes a lightweight "SELECT 1"
// query, wipes in-memory secrets, and strictly sanitizes any returned errors.
func (p *PostgresProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	if config.Endpoint == "" {
		return errors.New("postgresql probe endpoint must not be empty")
	}

	if config.QueryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.QueryTimeout)*time.Second)
		defer cancel()
	}

	// Extract database credentials from secretData
	username := extractSecretValue(secretData, "username", "user", "POSTGRES_USER", "pguser")
	passwordBytes := extractSecretBytes(secretData, "password", "pass", "POSTGRES_PASSWORD", "pgpassword")
	dbname := extractSecretValue(secretData, "dbname", "database", "POSTGRES_DB", "db")
	if dbname == "" {
		dbname = "postgres"
	}
	if username == "" {
		username = "postgres"
	}

	password := string(passwordBytes)
	// Ensure sensitive password memory is wiped after building the DSN
	defer func() {
		azure.ZeroBytes(passwordBytes)
		password = ""
	}()

	host, port, err := splitHostPort(config.Endpoint, "5432")
	if err != nil {
		return fmt.Errorf("invalid postgres endpoint %q: %w", config.Endpoint, err)
	}

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=%d",
		host, port, username, password, dbname, 5)

	connector := p.DBConnector
	if connector == nil {
		connector = sql.Open
	}

	db, err := connector("postgres", dsn)
	if err != nil {
		return SanitizeDBError(err, password, dsn)
	}
	defer func() {
		if db != nil {
			_ = db.Close()
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return SanitizeDBError(err, password, dsn)
	}

	var result int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		return SanitizeDBError(err, password, dsn)
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
		// Also check case-insensitive match
		for dataKey, val := range data {
			if strings.EqualFold(dataKey, k) && len(val) > 0 {
				return string(val)
			}
		}
	}
	return ""
}

func extractSecretBytes(data map[string][]byte, keys ...string) []byte {
	if data == nil {
		return nil
	}
	for _, k := range keys {
		if val, ok := data[k]; ok && len(val) > 0 {
			buf := make([]byte, len(val))
			copy(buf, val)
			return buf
		}
		for dataKey, val := range data {
			if strings.EqualFold(dataKey, k) && len(val) > 0 {
				buf := make([]byte, len(val))
				copy(buf, val)
				return buf
			}
		}
	}
	return nil
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
