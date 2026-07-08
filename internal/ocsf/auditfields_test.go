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
	// A representative subset the launcher + apple-container writers emit; the
	// mapping depends on these being recognized. Covers all five emit paths:
	// the bash function bodies (launch/exit/assurance_change/session_control),
	// the framing wrapper, the append_session_control_audit_record CALL-SITE
	// inline keys, the apple-container Go writers, and the schema sentinel.
	expected := []string{
		// bash bodies + framing wrapper (append_audit_record_to_path).
		"event", "session_id", "timestamp",
		"exit_status", "workspace", "record_digest", "prev_digest",
		// assurance_change is the only body that emits reason=package-mutation.
		"reason",
		// append_session_control_audit_record CALL-SITE inline keys: these are
		// audit-line keys even though they live at the call site, not the body.
		"source", "command", "argv", "force", "transport_status",
		"final_assurance", "stdin_mode", "container_name",
		// apple-container Go writers (recovery.go / target_session.go).
		"ts", "target_kind", "target_provider", "target_id",
		"workspace_origin", "materialization_id", "access_model",
		"bootstrap_id", "image_ref", "runtime_api", "status",
		"workspace_transport", "workspace_control_plane",
		// workspace_repo_mcp: launch/exit bodies record the repo-defined MCP
		// deny/acknowledge decision (A2).
		"workspace_repo_mcp",
		// v is the apple-container schema-version sentinel (" v=1"), on every
		// apple-container audit line — must be typed, not redacted-bucketed.
		"v",
	}
	for _, k := range expected {
		if _, ok := knownAuditFields[k]; !ok {
			t.Errorf("knownAuditFields is missing writer-emitted key %q", k)
		}
	}
	// A tampered/secret-shaped key must NOT be in the allowlist (so it routes to
	// the redacted bucket instead of becoming a typed property name). The git_*
	// names are SessionRecord fields / safe-git alias guards, NOT audit-line keys,
	// so allowlisting them would let a tampered `git_dir=secret` audit line become
	// a typed (unredacted) property — they must stay OUT.
	//
	// The remaining names are SessionRecord-ONLY fields: they are written solely
	// by write_session_record (go_hostutil session-record-write), never reach the
	// audit log, and so must NOT be typed audit properties. audit_log_path/
	// debug_log_path/file_trace_log_path/transcript_log_path/session_audit_dir/
	// target_assurance_class/workspace_root/worktree_path/monitor_pid are record
	// path/metadata fields; current_assurance/initial_assurance/live_status/
	// observed_at/started_at/finished_at are record status/timestamp fields
	// (started_at/finished_at are the ts= VALUE alias on apple-container lines,
	// not a key). prepare is only echoed to stderr, never an audit record.
	for _, k := range []string{
		"ghp_ExampleToken", "/Users/op/.ssh/id_rsa", "",
		"git_dir", "git_work_tree", "git_common_dir", "git_exec_path",
		"git_branch", "git_head", "git_base",
		"audit_log_path", "debug_log_path", "file_trace_log_path",
		"transcript_log_path", "session_audit_dir", "target_assurance_class",
		"workspace_root", "worktree_path", "monitor_pid",
		"current_assurance", "initial_assurance", "live_status",
		"observed_at", "started_at", "finished_at", "prepare",
	} {
		if _, ok := knownAuditFields[k]; ok {
			t.Errorf("knownAuditFields unexpectedly allowlists untrusted/non-audit key %q", k)
		}
	}
}
