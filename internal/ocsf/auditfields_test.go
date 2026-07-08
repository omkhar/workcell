// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import "testing"

// TestKnownAuditFieldsCoversWriterKeys pins the allowlist against the audit-field
// names the writers actually emit. The OCSF mapping turns ONLY these into typed
// audit.<key> properties; any other key is bucketed under a single redacted
// property, so a tampered audit line cannot make a secret-shaped key into a JSON
// property name. If a writer gains a new field, add it here (a missing entry
// degrades safely to the redacted bucket rather than leaking).
func TestKnownAuditFieldsCoversWriterKeys(t *testing.T) {
	// A representative subset the launcher + apple-container writers emit on
	// every record; the mapping depends on these being recognized.
	expected := []string{
		"event", "session_id", "timestamp", "ts",
		"target_kind", "target_provider", "target_id",
		"exit_status", "workspace", "workspace_origin",
		"record_digest", "prev_digest",
	}
	for _, k := range expected {
		if _, ok := knownAuditFields[k]; !ok {
			t.Errorf("knownAuditFields is missing writer-emitted key %q", k)
		}
	}
	// A tampered/secret-shaped key must NOT be in the allowlist (so it routes to
	// the redacted bucket instead of becoming a typed property name).
	for _, k := range []string{"ghp_ExampleToken", "/Users/op/.ssh/id_rsa", ""} {
		if _, ok := knownAuditFields[k]; ok {
			t.Errorf("knownAuditFields unexpectedly allowlists untrusted key %q", k)
		}
	}
}
