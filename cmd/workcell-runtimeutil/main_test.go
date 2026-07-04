// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestUsageReturnsCode2 pins the D8 exit-code contract: a missing or unknown
// top-level command is a usage error and returns 2, matching the other
// workcell Go CLIs.
func TestUsageReturnsCode2(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"definitely-not-a-subcommand"}} {
		var stderr bytes.Buffer
		if code := run(args, io.Discard, &stderr); code != 2 {
			t.Fatalf("run(%v) = %d, want 2; stderr=%q", args, code, stderr.String())
		}
		if s := stderr.String(); !strings.Contains(s, "usage:") && !strings.Contains(s, "unknown command") {
			t.Fatalf("run(%v) stderr = %q, want usage or unknown-command text", args, s)
		}
	}
}

// These tests cover the render-injection-bundle subcommand absorbed
// from the former workcell-render-injection-bundle binary.  They
// guard the bash exit-code contract: agent/mode validation failures
// must return code 2 with a descriptive stderr message.
func TestRenderInjectionBundleRejectsUnsupportedAgent(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := runRenderInjectionBundle([]string{
		"--policy", "policy.toml",
		"--agent", "codez",
		"--mode", "strict",
		"--output-root", "bundle",
	}, &stderr)
	if code != 2 {
		t.Fatalf("runRenderInjectionBundle() = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unsupported agent: codez") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRenderInjectionBundleRejectsUnsupportedMode(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	code := runRenderInjectionBundle([]string{
		"--policy", "policy.toml",
		"--agent", "codex",
		"--mode", "strcit",
		"--output-root", "bundle",
	}, &stderr)
	if code != 2 {
		t.Fatalf("runRenderInjectionBundle() = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unsupported mode: strcit") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
