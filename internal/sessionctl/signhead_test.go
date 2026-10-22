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

// TestSignHeadFailsForUnchainedLog proves the signing hook fails for a legacy
// apple-container session (audit records with no record_digest) and writes no
// seal, instead of skipping it as an unsupported provider.
func TestSignHeadFailsForUnchainedLog(t *testing.T) {
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
	}); err == nil || !strings.Contains(err.Error(), "no digest chain") {
		t.Fatalf("sign-head on an unchained log must fail with no digest chain, got %v", err)
	}
	if _, err := os.Stat(auditseal.SealPathForRecord(recordPath)); !os.IsNotExist(err) {
		t.Fatalf("no seal must be written for an unchained log (stat err=%v)", err)
	}
}
