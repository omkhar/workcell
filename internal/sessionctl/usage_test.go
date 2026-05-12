// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"strings"
	"testing"
)

func TestUsageTextListsAllSubcommands(t *testing.T) {
	t.Parallel()
	got := UsageText()
	for _, want := range []string{
		"workcell session start",
		"workcell session attach",
		"workcell session send",
		"workcell session stop",
		"workcell session list",
		"workcell session show",
		"workcell session delete",
		"workcell session logs",
		"workcell session timeline",
		"workcell session diff",
		"workcell session export",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("UsageText() missing %q", want)
		}
	}
}

func TestUsageTextDocumentsRequiredFlags(t *testing.T) {
	t.Parallel()
	got := UsageText()
	for _, want := range []string{
		"--id SESSION_ID",
		"--message TEXT",
		"--kind KIND",
		"--colima-profile NAME",
		"--session-workspace direct|isolated",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("UsageText() missing %q", want)
		}
	}
}
