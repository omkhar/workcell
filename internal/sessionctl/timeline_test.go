// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestParseTimelineArgsRequiresIDValue(t *testing.T) {
	t.Parallel()

	_, _, err := parseTimelineArgs([]string{"--id"})
	if err == nil {
		t.Fatal("parseTimelineArgs accepted --id without a value")
	}
	if !strings.Contains(err.Error(), "requires a value") {
		t.Fatalf("parseTimelineArgs error = %v, want requires-a-value rejection", err)
	}
	ec, ok := cliexit.IsExitCodeError(err)
	if !ok || ec.Code != 2 {
		t.Fatalf("parseTimelineArgs error = %v, want *cliexit.ExitCodeError{Code:2}", err)
	}
}

func TestParseTimelineArgsRejectsEmptyIDValue(t *testing.T) {
	t.Parallel()

	_, _, err := parseTimelineArgs([]string{"--id", ""})
	if err == nil {
		t.Fatal("parseTimelineArgs accepted empty --id value")
	}
}

func TestParseTimelineArgsRejectsFlagLikeIDValue(t *testing.T) {
	t.Parallel()

	_, _, err := parseTimelineArgs([]string{"--id", "--help"})
	if err == nil {
		t.Fatal("parseTimelineArgs accepted flag-like --id value")
	}
	if !strings.Contains(err.Error(), "Option --id requires a value") {
		t.Fatalf("parseTimelineArgs error = %v, want missing-value rejection", err)
	}
}

func TestParseTimelineArgsRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	_, _, err := parseTimelineArgs([]string{"--bogus"})
	if err == nil {
		t.Fatal("parseTimelineArgs accepted unknown flag")
	}
	if !strings.Contains(err.Error(), "Unsupported") {
		t.Fatalf("parseTimelineArgs error = %v, want Unsupported", err)
	}
}

func TestParseTimelineArgsAcceptsCanonical(t *testing.T) {
	t.Parallel()

	id, help, err := parseTimelineArgs([]string{"--id", "session-1"})
	if err != nil {
		t.Fatalf("parseTimelineArgs error = %v", err)
	}
	if help {
		t.Fatal("parseTimelineArgs help = true, want false")
	}
	if id != "session-1" {
		t.Fatalf("parseTimelineArgs id = %q, want %q", id, "session-1")
	}
}

func TestParseTimelineArgsHandlesHelp(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		_, help, err := parseTimelineArgs([]string{flag})
		if err != nil {
			t.Fatalf("parseTimelineArgs(%s) error = %v", flag, err)
		}
		if !help {
			t.Fatalf("parseTimelineArgs(%s) help = false, want true", flag)
		}
	}
}

// LookupRoots tests live under internal/host/stateroot now.
