// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"golang.org/x/sys/unix"
)

// TestAuditPathValueEncoding proves the encoder renders any injection payload
// safe: the encoded value has no whitespace/control character and decodes back to
// the exact input, for spaces, newlines, tabs, control chars, and percents.
func TestAuditPathValueEncoding(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"/Users/me/My Project",
		"line\nts=2026 session_id=victim event=forged",
		"tab\tsep",
		"cr\rret",
		"100%25 literal",
		"nul\x00byte",
		"plain/no/special",
	} {
		enc := encodeAuditPathValue(in)
		for _, r := range enc {
			if unicode.IsSpace(r) || unicode.IsControl(r) {
				t.Fatalf("encoded %q still contains whitespace/control: %q", in, enc)
			}
		}
		if got := decodeAuditPathValue(enc); got != in {
			t.Fatalf("round-trip: encode(%q)=%q decode=%q", in, enc, got)
		}
	}
}

// TestAuditPathValueEncodingPreservesBytes proves the byte-wise encoder round-
// trips ANY byte string, including invalid UTF-8, so a rune-based encoder that
// collapses bad bytes to U+FFFD cannot silently corrupt or collide distinct
// paths.
func TestAuditPathValueEncodingPreservesBytes(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		string([]byte{0x2f, 0x66, 0xff, 0x6f}), // /f<0xff>o — invalid UTF-8
		string([]byte{0x80, 0x81, 0xfe}),       // all high/continuation bytes
		"space and\nnewline",
		string([]byte{0x7f}), // DEL
	} {
		enc := encodeAuditPathValue(in)
		if strings.ContainsAny(enc, " \t\r\n") {
			t.Fatalf("encoded %x still contains whitespace: %q", in, enc)
		}
		if got := decodeAuditPathValue(enc); got != in {
			t.Fatalf("round-trip: encode(%x)=%q decode=%x", in, enc, got)
		}
	}

	// Distinct non-UTF-8 names must not collapse to the same encoding.
	a := encodeAuditPathValue(string([]byte{0xff, 0x01}))
	b := encodeAuditPathValue(string([]byte{0xfe, 0x02}))
	if a == b {
		t.Fatalf("distinct non-UTF-8 paths collapsed to the same encoding: %q", a)
	}
}

// TestAuditHasEventsExactMatch: only a genuine event= field counts; a substring
// of another field's value does not.
func TestAuditHasEventsExactMatch(t *testing.T) {
	t.Parallel()

	real := []string{
		"ts=1 session_id=s event=workspace_materialized target_id=t",
		"ts=1 session_id=s event=bootstrap_ready target_id=t",
		"ts=1 session_id=s event=session_started target_id=t",
	}
	if !auditHasEvents(real, "workspace_materialized", "bootstrap_ready", "session_started") {
		t.Fatalf("genuine start events not recognized")
	}
	forged := []string{
		"ts=1 session_id=s event=workspace_materialized target_id=t",
		"ts=1 session_id=s event=bootstrap_ready target_id=t",
		"ts=1 session_id=s event=noop decoy=x/event=session_started",
	}
	if auditHasEvents(forged, "workspace_materialized", "bootstrap_ready", "session_started") {
		t.Fatalf("forged session_started substring wrongly recognized")
	}
}

// TestAppendAuditLineRejectsSymlink: appending to a symlinked audit log is
// refused so evidence cannot be redirected to an attacker-chosen file.
func TestAppendAuditLineRejectsSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	evil := filepath.Join(dir, "evil.log")
	mustNil(t, os.WriteFile(evil, nil, 0o600))
	logPath := filepath.Join(dir, "workcell.audit.log")
	mustNil(t, os.Symlink(evil, logPath))
	if err := appendAuditLine(dir, logPath, "ts=1 event=x"); err == nil {
		t.Fatalf("appendAuditLine wrote through a symlinked log")
	}
	if data, _ := os.ReadFile(evil); len(data) != 0 {
		t.Fatalf("evidence leaked into symlink target: %q", data)
	}
	if err := appendAuditLine(dir, filepath.Join(dir, "real.log"), "ts=1 event=x"); err != nil {
		t.Fatalf("appendAuditLine on a regular file failed: %v", err)
	}
}

// TestAppendAuditLineRejectsFIFO: a pre-created FIFO at the log path is refused
// without blocking, so a local process cannot stall the lifecycle or divert
// evidence into a pipe. The O_NONBLOCK open + Fstat regular-file check must return
// promptly; a hang here would fail the -race deadline rather than pass.
func TestAppendAuditLineRejectsFIFO(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "workcell.audit.log")
	if err := unix.Mkfifo(logPath, 0o600); err != nil {
		t.Skipf("Mkfifo unavailable on this runner: %v", err)
	}
	// Hold the read end open (non-blocking) so the O_WRONLY open in appendAuditLine
	// succeeds and the Fstat regular-file guard — not merely an ENXIO on a
	// reader-less FIFO — is what rejects the write.
	rfd, err := unix.Open(logPath, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("cannot open FIFO read end on this runner: %v", err)
	}
	defer unix.Close(rfd)

	done := make(chan error, 1)
	go func() { done <- appendAuditLine(dir, logPath, "ts=1 event=x") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("appendAuditLine accepted a FIFO log")
		}
		if !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("expected not-a-regular-file error, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("appendAuditLine blocked on a FIFO log (O_NONBLOCK/Fstat did not reject)")
	}
	// Nothing must have been written into the pipe.
	var buf [16]byte
	if n, _ := unix.Read(rfd, buf[:]); n > 0 {
		t.Fatalf("evidence written into the FIFO: %q", buf[:n])
	}

	// Happy path: a regular log still appends.
	if err := appendAuditLine(dir, filepath.Join(dir, "real.log"), "ts=1 event=x"); err != nil {
		t.Fatalf("appendAuditLine on a regular file failed: %v", err)
	}
}

func TestAppendAuditLineRejectsHardLink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "workcell.audit.log")
	victim := filepath.Join(dir, "victim")
	mustNil(t, os.WriteFile(victim, []byte("original\n"), 0o600))
	if err := os.Link(victim, logPath); err != nil {
		t.Skipf("hard links unavailable on this runner: %v", err)
	}
	// The log is a regular file but multiply linked, so writing would land in the
	// victim inode; appendAuditLine must reject it and leave the victim untouched.
	if err := appendAuditLine(dir, logPath, "ts=1 event=x"); err == nil {
		t.Fatalf("appendAuditLine accepted a hard-linked log")
	} else if !strings.Contains(err.Error(), "multiply linked") {
		t.Fatalf("expected multiply-linked error, got: %v", err)
	}
	if data, _ := os.ReadFile(victim); strings.Contains(string(data), "event=x") {
		t.Fatalf("evidence leaked into hard-link target: %q", data)
	}
}

// TestAppendAuditLineRejectsSymlinkedParent: a symlink on a target-managed PARENT
// directory (below the state root) is rejected, while a legit system symlink
// ABOVE the trusted state root (e.g. macOS /var, an ancestor of t.TempDir) does
// not false-positive.
func TestAppendAuditLineRejectsSymlinkedParent(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir() // on macOS this is under /var/folders/... (the /var symlink is above it)
	targetRoot := filepath.Join(stateRoot, "targets", "local_vm", "apple-container", "tid")
	mustNil(t, os.MkdirAll(targetRoot, 0o755))
	auditLog := filepath.Join(targetRoot, "workcell.audit.log")

	// Happy path: no target-managed symlink; the /var ancestor above the trusted
	// root must NOT be flagged.
	if err := appendAuditLine(stateRoot, auditLog, "ts=1 event=x"); err != nil {
		t.Fatalf("append under a real state root (with /var ancestor) failed: %v", err)
	}

	// Swap a target-managed parent dir (the provider dir) for a symlink → reject.
	providerDir := filepath.Dir(targetRoot) // <stateRoot>/targets/local_vm/apple-container
	aside := providerDir + ".real"
	mustNil(t, os.Rename(providerDir, aside))
	mustNil(t, os.Symlink(aside, providerDir))
	if err := appendAuditLine(stateRoot, auditLog, "ts=2 event=forged"); err == nil {
		t.Fatalf("append accepted a symlinked target-managed parent directory")
	}
	data, _ := os.ReadFile(filepath.Join(aside, "tid", "workcell.audit.log"))
	if strings.Contains(string(data), "event=forged") {
		t.Fatalf("evidence written through a symlinked parent: %q", data)
	}
}
