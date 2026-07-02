// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"strings"
	"testing"
)

func TestAuthUsageTextNonEmptyAndTrailingNewline(t *testing.T) {
	t.Parallel()
	got := AuthUsageText()
	if got == "" {
		t.Fatal("AuthUsageText() returned empty string")
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("AuthUsageText() does not end with newline")
	}
}

func TestAuthUsageTextListsAllSubcommands(t *testing.T) {
	t.Parallel()
	got := AuthUsageText()
	for _, want := range []string{
		"Usage:",
		"workcell auth init",
		"workcell auth set",
		"workcell auth unset",
		"workcell auth status",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AuthUsageText() missing %q", want)
		}
	}
}

func TestAuthUsageTextDocumentsRequiredFlags(t *testing.T) {
	t.Parallel()
	got := AuthUsageText()
	for _, want := range []string{
		"--injection-policy PATH",
		"--managed-root PATH",
		"--agent codex|claude|gemini",
		"--credential KEY",
		"--source PATH",
		"--resolver NAME",
		"--ack-host-resolver",
		"--mode strict|development|build|breakglass",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AuthUsageText() missing %q", want)
		}
	}
}

func TestPolicyUsageTextNonEmptyAndTrailingNewline(t *testing.T) {
	t.Parallel()
	got := PolicyUsageText()
	if got == "" {
		t.Fatal("PolicyUsageText() returned empty string")
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("PolicyUsageText() does not end with newline")
	}
}

func TestPolicyUsageTextListsAllSubcommands(t *testing.T) {
	t.Parallel()
	got := PolicyUsageText()
	for _, want := range []string{
		"Usage:",
		"workcell policy show",
		"workcell policy validate",
		"workcell policy diff",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PolicyUsageText() missing %q", want)
		}
	}
}

func TestPolicyUsageTextDocumentsRequiredFlags(t *testing.T) {
	t.Parallel()
	got := PolicyUsageText()
	for _, want := range []string{
		"--injection-policy PATH",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PolicyUsageText() missing %q", want)
		}
	}
}
