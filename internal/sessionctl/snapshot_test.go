// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

const snapshotFixtureHead = "0123456789abcdef0123456789abcdef01234567"

func snapshotFixtureFields(overrides map[string]string) map[string]string {
	fields := map[string]string{
		"profile":           "wcl-detached-fixture",
		"container_name":    "workcell-session-fixture",
		"monitor_pid":       "4242",
		"session_audit_dir": "/tmp/audit-fixture",
		"workspace_origin":  "/tmp/origin-repo",
		"git_head":          snapshotFixtureHead,
		"status":            "running",
		"live_status":       "running",
	}
	for key, value := range overrides {
		if value == "" {
			delete(fields, key)
			continue
		}
		fields[key] = value
	}
	return fields
}

func runSnapshotFixture(t *testing.T, overrides map[string]string) (string, error) {
	t.Helper()
	root := t.TempDir()
	writeStopFixtureRecord(t, root, "wcl-detached-fixture", "fixture-1", snapshotFixtureFields(overrides))
	var buf bytes.Buffer
	err := snapshotMain([]string{"--root=" + root, "--id", "fixture-1"}, &buf, io.Discard)
	return buf.String(), err
}

func TestSnapshotMainEmitsPlanForRunningSession(t *testing.T) {
	out, err := runSnapshotFixture(t, nil)
	if err != nil {
		t.Fatalf("snapshotMain error = %v", err)
	}
	origin := sha256.Sum256([]byte("/tmp/origin-repo"))
	want := strings.Join([]string{
		"session_id=fixture-1",
		"profile=wcl-detached-fixture",
		"container_name=workcell-session-fixture",
		"workspace=/tmp/fixture-workspace",
		"git_head=" + snapshotFixtureHead,
		"origin_hash=" + hex.EncodeToString(origin[:]),
		"pause=1",
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("snapshotMain output =\n%s\nwant\n%s", out, want)
	}
}

func TestSnapshotMainDoesNotPauseTerminalSession(t *testing.T) {
	for _, status := range []string{"stopped", "exited", "failed", "aborted"} {
		out, err := runSnapshotFixture(t, map[string]string{"live_status": status})
		if err != nil {
			t.Fatalf("snapshotMain(%s) error = %v", status, err)
		}
		if !strings.Contains(out, "pause=0\n") {
			t.Fatalf("snapshotMain(%s) output = %q, want pause=0", status, out)
		}
	}
}

func TestSnapshotMainHashesWorkspaceWhenOriginIsMissing(t *testing.T) {
	out, err := runSnapshotFixture(t, map[string]string{"workspace_origin": ""})
	if err != nil {
		t.Fatalf("snapshotMain error = %v", err)
	}
	origin := sha256.Sum256([]byte("/tmp/fixture-workspace"))
	if !strings.Contains(out, "origin_hash="+hex.EncodeToString(origin[:])+"\n") {
		t.Fatalf("snapshotMain output = %q, want the workspace as the origin key", out)
	}
}

func TestSnapshotMainRejectsInvalidRecords(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]string
		want      string
	}{
		{"attached", map[string]string{"monitor_pid": ""}, "only works for detached sessions"},
		{"no container", map[string]string{"container_name": ""}, "missing a container name"},
		{"no git head", map[string]string{"git_head": ""}, "requires a recorded git head commit"},
		{"option git head", map[string]string{"git_head": "--output=/tmp/x"}, "requires a recorded git head commit"},
		{"short git head", map[string]string{"git_head": "0123456"}, "requires a recorded git head commit"},
		{"stopping", map[string]string{"live_status": "stopping"}, "requires a running or terminal detached session"},
	}
	for _, tc := range cases {
		out, err := runSnapshotFixture(t, tc.overrides)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: snapshotMain error = %v, want %q", tc.name, err, tc.want)
		}
		if out != "" {
			t.Fatalf("%s: snapshotMain wrote %q on rejection", tc.name, out)
		}
	}
}

func TestSnapshotMainUsageErrors(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{},
		{"--id"},
		{"--id", "--help"},
		{"--bogus"},
		{"--id", "session-1\nsession_id=other"},
	} {
		var buf bytes.Buffer
		err := snapshotMain(args, &buf, io.Discard)
		var ec *cliexit.ExitCodeError
		if !errors.As(err, &ec) || ec.Code != 2 {
			t.Fatalf("snapshotMain(%q) error = %v, want ExitCodeError{Code:2}", args, err)
		}
		if buf.Len() != 0 {
			t.Fatalf("snapshotMain(%q) wrote %q on rejection", args, buf.String())
		}
	}
}

func TestSnapshotMainHelpPrintsUsage(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if err := snapshotMain([]string{"--help"}, io.Discard, &stderr); err != nil {
		t.Fatalf("snapshotMain(--help) error = %v", err)
	}
	if !strings.Contains(stderr.String(), "workcell session snapshot --id SESSION_ID") {
		t.Fatalf("snapshotMain(--help) stderr = %q, want usage banner", stderr.String())
	}
}
