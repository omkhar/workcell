// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"strings"
	"testing"
)

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
