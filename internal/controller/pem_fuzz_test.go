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

package controller

import (
	"encoding/pem"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func FuzzExtractPEMCertAndKey(f *testing.F) {
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"))
	f.Add([]byte("-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----"))
	f.Add([]byte("Random non-PEM data"))
	f.Add([]byte(""))
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Execution must never panic under any arbitrary input
		cert, key := extractPEMCertAndKey(data)

		// Assertions on valid extraction:
		// When standard pem.Decode discovers valid PEM blocks, verify extractPEMCertAndKey
		// successfully partitions them into cert and key byte slices.
		rest := data
		for {
			var block *pem.Block
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			if strings.Contains(block.Type, "CERTIFICATE") {
				assert.NotEmpty(t, cert, "Valid CERTIFICATE block was not extracted")
			}
			if strings.Contains(block.Type, "PRIVATE KEY") || strings.Contains(block.Type, "KEY") {
				assert.NotEmpty(t, key, "Valid PRIVATE KEY block was not extracted")
			}
		}
	})
}
