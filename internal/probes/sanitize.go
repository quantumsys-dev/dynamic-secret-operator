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
	"regexp"
	"strings"
)

var (
	dsnPasswordRegex = regexp.MustCompile(`(?i)(password=)[^&;\s]+`)
	uriPasswordRegex = regexp.MustCompile(`(:)[^:@/\s]+(@)`)
)

// SanitizeDBError intercepts database errors and aggressively redacts passwords, tokens,
// and raw connection string parameters before returning to the reconciler or logs.
func SanitizeDBError(err error, sensitiveData ...string) error {
	if err == nil {
		return nil
	}

	msg := err.Error()

	// Redact any explicitly supplied sensitive tokens/passwords
	for _, sensitive := range sensitiveData {
		if trimmed := strings.TrimSpace(sensitive); len(trimmed) > 0 {
			msg = strings.ReplaceAll(msg, trimmed, "[REDACTED]")
		}
	}

	// Redact standard connection string password patterns
	msg = dsnPasswordRegex.ReplaceAllString(msg, "$1[REDACTED]")
	msg = uriPasswordRegex.ReplaceAllString(msg, "$1[REDACTED]$2")

	return fmt.Errorf("database authentication failed: %s", msg)
}
