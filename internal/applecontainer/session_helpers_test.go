// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import "testing"

func TestLastFieldKey(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"ts=1 session_id=s event=x k=v":             "k",
		"ts=1 event=session_finished exit_status=0": "exit_status",
		"":             "",
		"noequals":     "noequals",
		"a=1 trailing": "trailing",
	}
	for in, want := range cases {
		if got := lastFieldKey(in); got != want {
			t.Fatalf("lastFieldKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConflictingCompleteLine(t *testing.T) {
	t.Parallel()
	// want ends in the fixed sentinel (as the real render does), so image_ref is not last.
	want := "ts=1 session_id=s event=bootstrap_ready bootstrap_id=b image_ref=img:2" + auditLineSentinel
	lines := func(extra string) []string { return []string{want, extra} }

	// Exact line present alongside want → not a conflict.
	if conflictingCompleteLine([]string{want}, "bootstrap_ready", want) {
		t.Fatal("exact line flagged as conflict")
	}
	// A strict PREFIX (torn fragment cut mid-value, no sentinel) → ignored.
	if conflictingCompleteLine(lines("ts=1 session_id=s event=bootstrap_ready bootstrap_id=b image_ref=img:"), "bootstrap_ready", want) {
		t.Fatal("torn prefix fragment flagged as conflict")
	}
	// A COMPLETE line whose VALUE is a genuine prefix of want's (img vs img:2) — with the
	// sentinel it diverges before the end, so it is NOT a prefix of want → CONFLICT. This is
	// the case the sentinel fixes; without it, this complete line was wrongly ignored.
	if !conflictingCompleteLine(lines("ts=1 session_id=s event=bootstrap_ready bootstrap_id=b image_ref=img"+auditLineSentinel), "bootstrap_ready", want) {
		t.Fatal("complete prefix-value line not flagged (sentinel not disambiguating)")
	}
	// A COMPLETE line with a DIVERGENT value → conflict.
	if !conflictingCompleteLine(lines("ts=1 session_id=s event=bootstrap_ready bootstrap_id=b image_ref=img:9"+auditLineSentinel), "bootstrap_ready", want) {
		t.Fatal("divergent complete line not flagged")
	}
	// A different event entirely → not a conflict for this event.
	if conflictingCompleteLine(lines("ts=1 session_id=s event=session_started foo=bar"+auditLineSentinel), "bootstrap_ready", want) {
		t.Fatal("unrelated event flagged as conflict")
	}
}
