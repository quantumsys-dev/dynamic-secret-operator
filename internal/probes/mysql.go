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
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/pkg/telemetry"
)

// MySQLProbe executes synthetic connectivity and authentication validation against MySQL.
type MySQLProbe struct {
	// DBConnector allows injecting mock SQL drivers during testing.
	DBConnector func(driverName, dataSourceName string) (*sql.DB, error)
}

// Execute parses rotated credentials, opens a connection to MySQL, executes a lightweight "SELECT 1"
// query, and strictly sanitizes any returned errors.
func (p *MySQLProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	ctx, span := telemetry.Tracer.Start(ctx, "ExecuteMySQLProbe",
		trace.WithAttributes(
			attribute.String("probe.type", string(secretv1alpha1.ProbeTypeMySQL)),
		),
	)
	defer span.End()

	if config.Endpoint == "" {
		err := errors.New("mysql probe endpoint must not be empty")
		span.RecordError(err)
		return err
	}

	if config.QueryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.QueryTimeout)*time.Second)
		defer cancel()
	}

	// Extract database credentials from secretData
	username := extractSecretValue(secretData, "username", "user", "MYSQL_USER", "mysql_user")
	password := extractSecretValue(secretData, "password", "pass", "MYSQL_PASSWORD", "mysql_password")
	dbname := extractSecretValue(secretData, "dbname", "database", "MYSQL_DATABASE", "db")
	if username == "" {
		username = "root"
	}

	// Note: database/sql and DSN formatting require string parameters. In Go, string conversions
	// allocate immutable memory on the heap that cannot be zeroed via byte-wiping.
	// Raw byte zeroing is maintained in materializeSecretRevision for secret payloads.

	host, port, err := splitHostPort(config.Endpoint, "3306")
	if err != nil || host == "" || strings.ContainsAny(host, "?&/\\@#(): \t\r\n") || strings.ContainsAny(port, "?&/\\@#(): \t\r\n") {
		hostErr := fmt.Errorf("invalid or unsafe mysql endpoint: %q", config.Endpoint)
		span.RecordError(hostErr)
		return hostErr
	}
	if portNum, pErr := strconv.Atoi(port); pErr != nil || portNum <= 0 || portNum > 65535 {
		portErr := fmt.Errorf("invalid or unsafe mysql endpoint: %q", config.Endpoint)
		span.RecordError(portErr)
		return portErr
	}

	escapedUser := url.QueryEscape(username)
	escapedPassword := url.QueryEscape(password)
	escapedDBName := url.QueryEscape(dbname)

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?timeout=5s",
		escapedUser, escapedPassword, host, port, escapedDBName)

	connector := p.DBConnector
	if connector == nil {
		connector = sql.Open
	}

	db, err := connector("mysql", dsn)
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

	if err := db.PingContext(ctx); err != nil {
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
