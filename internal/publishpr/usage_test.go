// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package publishpr

import (
	"strings"
	"testing"
)

func TestUsageTextNonEmpty(t *testing.T) {
	t.Parallel()
	if got := UsageText(); got == "" {
		t.Fatalf("UsageText() returned empty string")
	}
}

func TestUsageTextEndsWithNewline(t *testing.T) {
	t.Parallel()
	got := UsageText()
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("UsageText() does not end with newline")
	}
}

func TestUsageTextDocumentsRequiredFlags(t *testing.T) {
	t.Parallel()
	got := UsageText()
	for _, want := range []string{
		"Usage: workcell publish-pr [options]",
		"--workspace PATH",
		"--branch NAME",
		"--base NAME",
		"--allow-non-main-base",
		"--gh-bin PATH",
		"--snapshot index|worktree",
		"--title TEXT",
		"--title-file PATH",
		"--body TEXT",
		"--body-file PATH",
		"--commit-message TEXT",
		"--commit-message-file PATH",
		"--ready",
		"--dry-run",
		"-h, --help",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("UsageText() missing %q", want)
		}
	}
}

func TestUsageTextDocumentsNotes(t *testing.T) {
	t.Parallel()
	got := UsageText()
	for _, want := range []string{
		"publish-pr runs on the host, not inside the Workcell container.",
		"--allow-non-main-base is an explicit lower-assurance escape hatch",
		"Host-side git commands explicitly bypass repo hooks during publication",
		"The default snapshot is worktree",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("UsageText() missing note %q", want)
		}
	}
}
