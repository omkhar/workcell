// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(--help): %v", err)
	}
	if !strings.Contains(stdout.String(), "workcell support-bundle") {
		t.Fatalf("help missing canonical syntax:\n%s", stdout.String())
	}
}

func TestRunStdoutEmitsValidJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"--host-os", "darwin", "--host-arch", "arm64"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var b Bundle
	if err := json.Unmarshal(stdout.Bytes(), &b); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if b.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %q", b.SchemaVersion)
	}
	if b.Install.HostOS != "darwin" {
		t.Fatalf("host os flag not applied: %q", b.Install.HostOS)
	}
}

func TestRunOutputFile(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "bundle.json")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--output", out}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(--output): %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if !strings.Contains(stdout.String(), out) {
		t.Fatalf("expected confirmation on stdout, got %q", stdout.String())
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("bundle file perms = %v, want 0600", info.Mode().Perm())
	}
}

func TestRunOutputEqualsForm(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "b.json")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--output=" + out}, &stdout, &stderr); err != nil {
		t.Fatalf("Run(--output=): %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("output not written: %v", err)
	}
}

func TestRunUnknownFlagIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"--bogus", "x"}, &stdout, &stderr)
	assertUsageError(t, err)
}

func TestRunMissingValueIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"--output"}, &stdout, &stderr)
	assertUsageError(t, err)
}

func assertUsageError(t *testing.T, err error) {
	t.Helper()
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("expected ExitCodeError, got %v", err)
	}
	if ec.Code != 2 {
		t.Fatalf("exit code = %d, want 2", ec.Code)
	}
}
