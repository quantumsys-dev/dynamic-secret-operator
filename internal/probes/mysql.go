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
	"time"

	_ "github.com/go-sql-driver/mysql"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys/dynamic-secret-operator/internal/azure"
)

// MySQLProbe executes synthetic connectivity and authentication validation against MySQL.
type MySQLProbe struct {
	// DBConnector allows injecting mock SQL drivers during testing.
	DBConnector func(driverName, dataSourceName string) (*sql.DB, error)
}

// Execute parses rotated credentials, opens a connection to MySQL, executes a lightweight "SELECT 1"
// query, wipes in-memory secrets, and strictly sanitizes any returned errors.
func (p *MySQLProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	if config.Endpoint == "" {
		return errors.New("mysql probe endpoint must not be empty")
	}

	if config.QueryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.QueryTimeout)*time.Second)
		defer cancel()
	}

	// Extract database credentials from secretData
	username := extractSecretValue(secretData, "username", "user", "MYSQL_USER", "mysql_user")
	passwordBytes := extractSecretBytes(secretData, "password", "pass", "MYSQL_PASSWORD", "mysql_password")
	dbname := extractSecretValue(secretData, "dbname", "database", "MYSQL_DATABASE", "db")
	if username == "" {
		username = "root"
	}

	password := string(passwordBytes)
	// Ensure sensitive password memory is wiped after building the DSN
	defer func() {
		azure.ZeroBytes(passwordBytes)
		password = ""
	}()

	host, port, err := splitHostPort(config.Endpoint, "3306")
	if err != nil {
		return fmt.Errorf("invalid mysql endpoint %q: %w", config.Endpoint, err)
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?timeout=5s",
		username, password, host, port, dbname)

	connector := p.DBConnector
	if connector == nil {
		connector = sql.Open
	}

	db, err := connector("mysql", dsn)
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
