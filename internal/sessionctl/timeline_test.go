// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"strings"
	"testing"
)

func TestParseTimelineArgsRequiresIDValue(t *testing.T) {
	t.Parallel()

	_, _, err := parseTimelineArgs([]string{"--id"})
	if err == nil {
		t.Fatal("parseTimelineArgs accepted --id without a value")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("parseTimelineArgs error = %v, want non-empty rejection", err)
	}
}

func TestParseTimelineArgsRejectsEmptyIDValue(t *testing.T) {
	t.Parallel()

	_, _, err := parseTimelineArgs([]string{"--id", ""})
	if err == nil {
		t.Fatal("parseTimelineArgs accepted empty --id value")
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

func TestLookupRootsReadsEnv(t *testing.T) {
	t.Setenv("WORKCELL_STATE_ROOT", "/tmp/wc")
	t.Setenv("COLIMA_STATE_ROOT", "/tmp/colima")

	got := lookupRoots()
	want := []string{"/tmp/wc", "/tmp/colima"}
	if len(got) != len(want) {
		t.Fatalf("lookupRoots() = %v, want %v", got, want)
	}
	for i, root := range got {
		if root != want[i] {
			t.Fatalf("lookupRoots()[%d] = %q, want %q", i, root, want[i])
		}
	}
}

func TestLookupRootsSkipsEmpty(t *testing.T) {
	t.Setenv("WORKCELL_STATE_ROOT", "/tmp/wc")
	t.Setenv("COLIMA_STATE_ROOT", "")

	got := lookupRoots()
	if len(got) != 1 || got[0] != "/tmp/wc" {
		t.Fatalf("lookupRoots() = %v, want [/tmp/wc]", got)
	}
}
