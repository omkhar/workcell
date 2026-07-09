// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/sessions"
)

// verifyFixture lays a genuine, signed session on disk and returns the state
// root, signing dir, session id, and the durable audit-log path so a test can
// tamper it. The record uses the legacy `<root>/<profile>/sessions` layout that
// sessions discovery accepts.
func verifyFixture(t *testing.T) (root, signingDir, sessionID, logPath string) {
	t.Helper()
	root = t.TempDir()
	signingDir = filepath.Join(t.TempDir(), "signing")
	sessionID = "session-1"

	// Canonical layout: the audit log is a SIBLING of the sessions dir under the
	// profile state dir (<root>/<profile>/workcell.audit.log), exactly where
	// profile_audit_log_path/the signer place it and where verify recomputes it.
	logPath = filepath.Join(root, "wcl-fixture", "workcell.audit.log")
	if err := os.MkdirAll(filepath.Join(root, "wcl-fixture", "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Two-record genuine chain for the session.
	lines := []string{}
	prev := ""
	for i, args := range [][]string{
		{"session_id=" + sessionID, "event=launch"},
		{"session_id=" + sessionID, "event=exit", "exit_status=0"},
	} {
		ts := "2026-07-08T00:00:0" + string(rune('0'+i)) + "Z"
		digest := hoststate.AuditRecordDigest(prev, ts, args)
		line := "timestamp=" + ts + " " + strings.Join(args, " ")
		if prev != "" {
			line += " prev_digest=" + prev
		}
		line += " record_digest=" + digest
		lines = append(lines, line)
		prev = digest
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}

	recordPath := filepath.Join(root, "wcl-fixture", "sessions", sessionID+".json")
	if err := sessions.WriteSessionRecord(recordPath, map[string]string{
		"session_id":      sessionID,
		"profile":         "wcl-fixture",
		"target_provider": "colima",
		"agent":           "codex",
		"mode":            "strict",
		"status":          "exited",
		"workspace":       "/tmp/workspace",
		"started_at":      "2026-07-08T00:00:00Z",
		"finished_at":     "2026-07-08T00:00:09Z",
		"exit_status":     "0",
		"final_assurance": "managed-mutable",
		"audit_log_path":  logPath,
	}); err != nil {
		t.Fatalf("write record: %v", err)
	}

	if err := SignHeadMain([]string{
		"--signing-dir=" + signingDir,
		"--audit-log=" + logPath,
		"--session-id=" + sessionID,
		"--record-path=" + recordPath,
		"--provider=colima",
		"--signed-at=2026-07-08T00:00:09Z",
	}); err != nil {
		t.Fatalf("SignHeadMain: %v", err)
	}
	return root, signingDir, sessionID, logPath
}

func runVerify(t *testing.T, root, signingDir, sessionID string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := verifyMain([]string{
		"--root=" + root,
		"--id", sessionID,
		"--signing-dir=" + signingDir,
		"--real-home=/nonexistent",
	}, &buf)
	return buf.String(), err
}

func TestVerifyCLIGenuineSessionPasses(t *testing.T) {
	root, signingDir, sessionID, _ := verifyFixture(t)
	out, err := runVerify(t, root, signingDir, sessionID)
	if err != nil {
		t.Fatalf("genuine verify failed: %v", err)
	}
	if !strings.Contains(out, "session_verify=verified") {
		t.Fatalf("expected verified output, got %q", out)
	}
}

func TestVerifyCLIFailsClosedOnTamper(t *testing.T) {
	root, signingDir, sessionID, logPath := verifyFixture(t)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	tampered := strings.Replace(string(data), "event=launch", "event=xaunch", 1)
	if err := os.WriteFile(logPath, []byte(tampered), 0o600); err != nil {
		t.Fatalf("write tampered: %v", err)
	}
	_, err = runVerify(t, root, signingDir, sessionID)
	assertExitCode(t, err, 1)
}

// rewriteRecordAuditLogPath rewrites the on-disk record's audit_log_path to
// point elsewhere, preserving all other genuine fields, so a test can simulate
// the offline redirected-path attack.
func rewriteRecordAuditLogPath(t *testing.T, root, sessionID, newLogPath string) {
	t.Helper()
	recordPath := filepath.Join(root, "wcl-fixture", "sessions", sessionID+".json")
	if err := sessions.WriteSessionRecord(recordPath, map[string]string{
		"session_id":      sessionID,
		"profile":         "wcl-fixture",
		"target_provider": "colima",
		"agent":           "codex",
		"mode":            "strict",
		"status":          "exited",
		"workspace":       "/tmp/workspace",
		"started_at":      "2026-07-08T00:00:00Z",
		"finished_at":     "2026-07-08T00:00:09Z",
		"exit_status":     "0",
		"final_assurance": "managed-mutable",
		"audit_log_path":  newLogPath,
	}); err != nil {
		t.Fatalf("rewrite record: %v", err)
	}
}

// TestVerifyCLIFailsClosedOnRedirectedAuditLogPath is the P1 verification-bypass
// repro: an attacker keeps a pristine COPY of the old signed log, repoints the
// (attacker-writable) record's audit_log_path at the copy, and modifies the
// profile's REAL canonical log. Verify must NOT trust the recorded path — it
// recomputes over the canonical location and rejects the redirected record fail
// closed, never reporting "verified" against the pristine copy.
func TestVerifyCLIFailsClosedOnRedirectedAuditLogPath(t *testing.T) {
	root, signingDir, sessionID, logPath := verifyFixture(t)

	// Pristine copy of the genuine, signed log elsewhere.
	pristine, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	pristineCopy := filepath.Join(t.TempDir(), "pristine.audit.log")
	if err := os.WriteFile(pristineCopy, pristine, 0o600); err != nil {
		t.Fatalf("write pristine copy: %v", err)
	}
	// Tamper the REAL canonical log.
	tampered := strings.Replace(string(pristine), "event=launch", "event=xaunch", 1)
	if err := os.WriteFile(logPath, []byte(tampered), 0o600); err != nil {
		t.Fatalf("write tampered canonical: %v", err)
	}
	// Redirect the record at the pristine copy.
	rewriteRecordAuditLogPath(t, root, sessionID, pristineCopy)

	out, err := runVerify(t, root, signingDir, sessionID)
	if strings.Contains(out, "session_verify=verified") {
		t.Fatalf("redirected audit log path was accepted as verified: %q", out)
	}
	assertExitCode(t, err, 1)
	var ec *cliexit.ExitCodeError
	errors.As(err, &ec)
	if !strings.Contains(ec.Message, "does not match the profile's canonical location") {
		t.Fatalf("expected canonical-location mismatch failure, got %q", ec.Message)
	}
}

// TestVerifyCLIRecomputesOverCanonicalLog proves verify recomputes the head over
// the canonical profile log (not the recorded path) even when the recorded path
// still equals canonical: tampering the canonical log fails the chain closed.
// (Companion to the redirect repro: this covers the "verify against canonical"
// half without triggering the path-mismatch guard.)
func TestVerifyCLIRecomputesOverCanonicalLog(t *testing.T) {
	root, signingDir, sessionID, logPath := verifyFixture(t)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	tampered := strings.Replace(string(data), "exit_status=0", "exit_status=1", 1)
	if err := os.WriteFile(logPath, []byte(tampered), 0o600); err != nil {
		t.Fatalf("write tampered: %v", err)
	}
	out, err := runVerify(t, root, signingDir, sessionID)
	if strings.Contains(out, "session_verify=verified") {
		t.Fatalf("tampered canonical log was accepted as verified: %q", out)
	}
	assertExitCode(t, err, 1)
}

func TestVerifyCLIFailsClosedWhenUnsigned(t *testing.T) {
	root, signingDir, sessionID, _ := verifyFixture(t)
	// Remove the seal sidecar: an unsigned session must fail closed.
	recordPath := filepath.Join(root, "wcl-fixture", "sessions", sessionID+".json")
	sealPath := strings.TrimSuffix(recordPath, ".json") + ".audit-sig"
	if err := os.Remove(sealPath); err != nil {
		t.Fatalf("remove seal: %v", err)
	}
	_, err := runVerify(t, root, signingDir, sessionID)
	assertExitCode(t, err, 1)
}

func TestVerifyCLIFailsClosedForNoChainProvider(t *testing.T) {
	root := t.TempDir()
	signingDir := filepath.Join(t.TempDir(), "signing")
	sessionID := "session-ac"

	logPath := filepath.Join(root, "wcl-fixture", "workcell.audit.log")
	if err := os.MkdirAll(filepath.Join(root, "wcl-fixture", "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// apple-container-style lifecycle lines: no record_digest, no chain.
	if err := os.WriteFile(logPath, []byte(strings.Join([]string{
		"timestamp=2026-07-08T00:00:00Z session_id=session-ac event=session_started v=1",
		"timestamp=2026-07-08T00:00:01Z session_id=session-ac event=session_finished v=1",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	recordPath := filepath.Join(root, "wcl-fixture", "sessions", sessionID+".json")
	if err := sessions.WriteSessionRecord(recordPath, map[string]string{
		"session_id":      sessionID,
		"profile":         "wcl-fixture",
		"target_provider": "apple-container",
		"agent":           "codex",
		"mode":            "strict",
		"status":          "exited",
		"workspace":       "/tmp/workspace",
		"started_at":      "2026-07-08T00:00:00Z",
		"finished_at":     "2026-07-08T00:00:09Z",
		"exit_status":     "0",
		"final_assurance": "managed-mutable",
		"audit_log_path":  logPath,
	}); err != nil {
		t.Fatalf("write record: %v", err)
	}

	var buf bytes.Buffer
	err := verifyMain([]string{
		"--root=" + root,
		"--id", sessionID,
		"--signing-dir=" + signingDir,
		"--real-home=/nonexistent",
	}, &buf)
	assertExitCode(t, err, 1)
	var ec *cliexit.ExitCodeError
	errors.As(err, &ec)
	if !strings.Contains(ec.Message, "no signable digest chain") {
		t.Fatalf("expected the no-signable-chain reason, got %q", ec.Message)
	}
}

func TestVerifyCLIMissingIDIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	err := verifyMain([]string{"--signing-dir=/tmp/x"}, &buf)
	assertExitCode(t, err, 2)
}

func TestVerifyCLIUnknownFlagIsUsageError(t *testing.T) {
	var buf bytes.Buffer
	err := verifyMain([]string{"--id", "x", "--signing-dir=/tmp/x", "--bogus"}, &buf)
	assertExitCode(t, err, 2)
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected exit code %d, got nil error", want)
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected ExitCodeError, got %v", err)
	}
	if ec.Code != want {
		t.Fatalf("exit code = %d, want %d", ec.Code, want)
	}
}
