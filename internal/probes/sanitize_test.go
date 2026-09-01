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
	"errors"
	"strings"
	"testing"
)

func FuzzSanitizeDBError(f *testing.F) {
	// Seed corpus with realistic connection strings and error messages
	f.Add("pq: password authentication failed for user 'app_user'", "s3cr3tP@ss!", "postgres://app_user:s3cr3tP@ss!@db.internal:5432/orders")
	f.Add("driver: bad connection to host 10.0.0.1", "SuperSecret123", "app_user:SuperSecret123@tcp(10.0.0.1:3306)/dbname")
	f.Add("failed to connect: password=MySecretToken123 host=localhost", "MySecretToken123", "user=admin password=MySecretToken123 host=localhost")
	f.Add("Access denied for user 'root'@'localhost' (using password: YES)", "adminPass#99", "")

	f.Fuzz(func(t *testing.T, rawErrMsg string, secretToken string, dsn string) {
		if rawErrMsg == "" {
			return
		}

		rawErr := errors.New(rawErrMsg)
		var sensitive []string
		if secretToken != "" {
			sensitive = append(sensitive, secretToken)
		}
		if dsn != "" {
			sensitive = append(sensitive, dsn)
		}

		sanitizedErr := SanitizeDBError(rawErr, sensitive...)
		if sanitizedErr == nil {
			t.Fatalf("sanitized error should not be nil when input error is non-nil")
		}

		sanitizedMsg := sanitizedErr.Error()

		// Invariant: The explicit secret token (if >= 4 chars to avoid single-char false positives) must NEVER appear in the sanitized message
		if len(strings.TrimSpace(secretToken)) >= 4 {
			if strings.Contains(sanitizedMsg, strings.TrimSpace(secretToken)) {
				t.Fatalf("security violation: sensitive token %q leaked in sanitized error message %q", secretToken, sanitizedMsg)
			}
		}
	})
}
