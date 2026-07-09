// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/auditseal"
)

// TestSignHeadSkipsNoChainProvider proves the signing hook leaves an
// apple-container-style session (audit records with no record_digest) unsigned
// rather than erroring or writing a seal that could never verify.
func TestSignHeadSkipsNoChainProvider(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "workcell.audit.log")
	if err := os.WriteFile(logPath, []byte(strings.Join([]string{
		"timestamp=2026-07-08T00:00:00Z session_id=session-ac event=session_started v=1",
		"timestamp=2026-07-08T00:00:01Z session_id=session-ac event=session_finished v=1",
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	recordPath := filepath.Join(tmp, "session-ac.json")

	if err := SignHeadMain([]string{
		"--signing-dir=" + filepath.Join(tmp, "signing"),
		"--audit-log=" + logPath,
		"--session-id=session-ac",
		"--record-path=" + recordPath,
		"--provider=apple-container",
	}); err != nil {
		t.Fatalf("sign-head on a no-chain provider must be a clean no-op, got %v", err)
	}
	if _, err := os.Stat(auditseal.SealPathForRecord(recordPath)); !os.IsNotExist(err) {
		t.Fatalf("no seal must be written for a no-chain provider (stat err=%v)", err)
	}
}
