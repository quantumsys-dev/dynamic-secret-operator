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
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

func TestMySQLProbe_Execute(t *testing.T) {
	t.Run("succeeds on successful connection and SELECT 1 query", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		mock.ExpectPing()
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

		probe := &MySQLProbe{
			DBConnector: func(driverName, dataSourceName string) (*sql.DB, error) {
				return db, nil
			},
		}

		cfg := secretv1alpha1.ValidationProbe{
			Type:         secretv1alpha1.ProbeTypeMySQL,
			Endpoint:     "mysql-db.default.svc:3306",
			QueryTimeout: 5,
		}

		secretData := map[string][]byte{
			"username": []byte("appuser"),
			"password": []byte("mysqlpass123"),
			"dbname":   []byte("appdb"),
		}

		if err := probe.Execute(context.Background(), cfg, secretData); err != nil {
			t.Fatalf("expected successful probe execution, got: %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unfulfilled mock expectations: %v", err)
		}
	})

	t.Run("anti-leakage: redacts sensitive password from error output", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		sensitivePass := "ultraSecretMysqlToken777!"
		mock.ExpectPing().WillReturnError(errors.New("Error 1045 (28000): Access denied for user 'appuser'@'localhost' (using password: " + sensitivePass + ")"))

		probe := &MySQLProbe{
			DBConnector: func(driverName, dataSourceName string) (*sql.DB, error) {
				return db, nil
			},
		}

		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeMySQL,
			Endpoint: "localhost:3306",
		}

		secretData := map[string][]byte{
			"username": []byte("appuser"),
			"password": []byte(sensitivePass),
		}

		err = probe.Execute(context.Background(), cfg, secretData)
		if err == nil {
			t.Fatalf("expected error from failed ping, got nil")
		}

		errMsg := err.Error()

		// Anti-leakage assertions: password MUST be completely absent
		if strings.Contains(errMsg, sensitivePass) {
			t.Fatalf("CRITICAL SECURITY LEAK: sensitive password found in error message: %s", errMsg)
		}

		if !strings.Contains(errMsg, "[REDACTED]") {
			t.Errorf("expected '[REDACTED]' in sanitized error message, got: %s", errMsg)
		}

		if !strings.Contains(errMsg, "database authentication failed:") {
			t.Errorf("expected 'database authentication failed:' prefix, got: %s", errMsg)
		}
	})

	t.Run("fails on empty endpoint", func(t *testing.T) {
		probe := &MySQLProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeMySQL,
			Endpoint: "",
		}

		if err := probe.Execute(context.Background(), cfg, nil); err == nil {
			t.Fatalf("expected error for empty endpoint, got nil")
		}
	})

	t.Run("correctly URL-encodes special characters in credentials", func(t *testing.T) {
		var capturedDSN string
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		if err != nil {
			t.Fatalf("failed to create sqlmock: %v", err)
		}
		defer db.Close()

		mock.ExpectPing()
		mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

		probe := &MySQLProbe{
			DBConnector: func(driverName, dataSourceName string) (*sql.DB, error) {
				capturedDSN = dataSourceName
				return db, nil
			},
		}

		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeMySQL,
			Endpoint: "mysql-db:3306",
		}

		secretData := map[string][]byte{
			"username": []byte("admin@tenant:1"),
			"password": []byte("p@ss:w/ord?#123"),
			"dbname":   []byte("app/db"),
		}

		if err := probe.Execute(context.Background(), cfg, secretData); err != nil {
			t.Fatalf("expected successful probe execution with special characters, got: %v", err)
		}

		expectedUser := "admin%40tenant%3A1"
		expectedPass := "p%40ss%3Aw%2Ford%3F%23123"
		expectedDB := "app%2Fdb"
		expectedPrefix := expectedUser + ":" + expectedPass + "@tcp(mysql-db:3306)/" + expectedDB

		if !strings.HasPrefix(capturedDSN, expectedPrefix) {
			t.Errorf("expected DSN to contain encoded credentials prefix %q, got %q", expectedPrefix, capturedDSN)
		}
	})
}
