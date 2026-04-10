// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRejectsUnsupportedAgent(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := run([]string{
		"--policy", "policy.toml",
		"--agent", "codez",
		"--mode", "strict",
		"--output-root", "bundle",
	}, &stderr)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unsupported agent: codez") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunRejectsUnsupportedMode(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := run([]string{
		"--policy", "policy.toml",
		"--agent", "codex",
		"--mode", "strcit",
		"--output-root", "bundle",
	}, &stderr)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unsupported mode: strcit") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
