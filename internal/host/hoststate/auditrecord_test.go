// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hoststate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/auditseal"
	"github.com/omkhar/workcell/internal/host/hoststate"
)

func TestAppendAuditRecordWritesAChainThatAuditsealVerifies(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "workcell.audit.log")
	// A legacy unchained prefix stays outside the chain, as in a bash log.
	if err := os.WriteFile(path, []byte("ts=2026-01-01T00:00:00Z legacy=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, record := range [][]string{
		{"session_id=s1", "event=session_started", "workspace=/guest/m1/workspace"},
		{"session_id=other", "event=session_started"},
		{"session_id=s1", "event=session_finished", "exit_status=0"},
	} {
		if err := hoststate.AppendAuditRecord(path, "2026-04-21T00:00:00Z", record); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	first := "timestamp=2026-04-21T00:00:00Z session_id=s1 event=session_started workspace=/guest/m1/workspace record_digest=" +
		hoststate.AuditRecordDigest("", "2026-04-21T00:00:00Z", []string{"session_id=s1", "event=session_started", "workspace=/guest/m1/workspace"}) + " "
	if len(lines) != 4 || lines[1] != first || !strings.Contains(lines[2], " prev_digest=") {
		t.Fatalf("log =\n%s", content)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("the audit-log lock was not released: %v", err)
	}
	signing := filepath.Join(t.TempDir(), "signing")
	seal, err := auditseal.SignSessionHead(signing, path, "gcp-vm", "s1", "2026-04-21T00:16:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auditseal.VerifySessionSeal(signing, path, "gcp-vm", "s1", seal); err != nil {
		t.Fatalf("VerifySessionSeal() = %v", err)
	}
	tampered := strings.Replace(string(content), "exit_status=0", "exit_status=1", 1)
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := auditseal.VerifySessionSeal(signing, path, "gcp-vm", "s1", seal); err == nil {
		t.Fatal("VerifySessionSeal() accepted a tampered record")
	}
}

func TestAppendAuditRecordRefusesATokenThatTheDecoderCannotReturn(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workcell.audit.log")
	for _, args := range [][]string{
		{"bare"},
		{""},
		{"=value"},
		{"workspace=/a\x00b"},
		{"workspace=/a\xffb"},
	} {
		if err := hoststate.AppendAuditRecord(path, "2026-04-21T00:00:00Z", args); err == nil {
			t.Fatalf("AppendAuditRecord(%q) succeeded", args)
		}
	}
	if err := hoststate.AppendAuditRecord(path, "2026\x00", []string{"event=x"}); err == nil {
		t.Fatal("AppendAuditRecord() accepted a timestamp with a NUL byte")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused record created the log: %v", err)
	}
}

func TestAppendAuditRecordQuotesAHostileValueThatAuditsealVerifies(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "workcell.audit.log")
	hostile := "/tmp/a b/$HOME/`id`/it's/x\\y/caf\u00e9/,~#!{}\n\tforged=1 session_id=s2"
	if err := hoststate.AppendAuditRecord(path, "2026-04-21T00:00:00Z", []string{"session_id=s1", "event=session_started", "workspace_origin=" + hostile}); err != nil {
		t.Fatal(err)
	}
	if err := hoststate.AppendAuditRecord(path, "2026-04-21 00:00:01", []string{"session_id=s1", "event=session_finished"}); err != nil {
		t.Fatal(err)
	}
	signing := filepath.Join(t.TempDir(), "signing")
	seal, err := auditseal.SignSessionHead(signing, path, "gcp-vm", "s1", "2026-04-21T00:16:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auditseal.VerifySessionSeal(signing, path, "gcp-vm", "s1", seal); err != nil {
		t.Fatalf("VerifySessionSeal() = %v", err)
	}
	records, err := auditseal.SealedSessionRecords(signing, path, "gcp-vm", "s1", seal)
	if err != nil || len(records) != 2 {
		t.Fatalf("SealedSessionRecords() = %v, %v", records, err)
	}
	for _, field := range records[0] {
		if field.Key == "workspace_origin" && field.Value != hostile {
			t.Fatalf("workspace_origin decoded as %q, want %q", field.Value, hostile)
		}
	}
	if _, err := auditseal.SignSessionHead(signing, path, "gcp-vm", "s2", ""); err == nil {
		t.Fatal("a quoted value forged a record of another session")
	}
}

func TestAppendAuditRecordRefusesAnUnsafeLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.log")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(dir, "shared.log")
	if err := os.WriteFile(shared, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, shared} {
		if err := hoststate.AppendAuditRecord(path, "2026-04-21T00:00:00Z", []string{"event=x"}); err == nil {
			t.Fatalf("AppendAuditRecord(%s) succeeded", path)
		}
	}
}
