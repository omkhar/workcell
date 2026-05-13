// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/sessions"
)

func TestParseLogsArgsRequiresIDValue(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseLogsArgs([]string{"--id"})
	if err == nil {
		t.Fatal("parseLogsArgs accepted --id without a value")
	}
	if !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("parseLogsArgs error = %v, want non-empty rejection", err)
	}
}

func TestParseLogsArgsRequiresKindValue(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseLogsArgs([]string{"--id", "x", "--kind"})
	if err == nil {
		t.Fatal("parseLogsArgs accepted --kind without a value")
	}
}

func TestParseLogsArgsRejectsEmptyKindValue(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseLogsArgs([]string{"--id", "x", "--kind", ""})
	if err == nil {
		t.Fatal("parseLogsArgs accepted empty --kind value")
	}
}

func TestParseLogsArgsRejectsUnknownFlag(t *testing.T) {
	t.Parallel()

	_, _, _, err := parseLogsArgs([]string{"--bogus"})
	if err == nil {
		t.Fatal("parseLogsArgs accepted unknown flag")
	}
	if !strings.Contains(err.Error(), "Unsupported workcell session logs option") {
		t.Fatalf("parseLogsArgs error = %v, want session-logs-specific message", err)
	}
}

func TestParseLogsArgsAcceptsCanonical(t *testing.T) {
	t.Parallel()

	id, kind, help, err := parseLogsArgs([]string{"--id", "session-1", "--kind", "audit"})
	if err != nil {
		t.Fatalf("parseLogsArgs error = %v", err)
	}
	if help {
		t.Fatal("parseLogsArgs help = true, want false")
	}
	if id != "session-1" {
		t.Fatalf("parseLogsArgs id = %q, want %q", id, "session-1")
	}
	if kind != "audit" {
		t.Fatalf("parseLogsArgs kind = %q, want %q", kind, "audit")
	}
}

func TestParseLogsArgsHandlesHelp(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"-h", "--help"} {
		_, _, help, err := parseLogsArgs([]string{flag})
		if err != nil {
			t.Fatalf("parseLogsArgs(%s) error = %v", flag, err)
		}
		if !help {
			t.Fatalf("parseLogsArgs(%s) help = false, want true", flag)
		}
	}
}

func TestValidateLogsKindNameAcceptsCanonical(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"audit", "debug", "file-trace", "transcript"} {
		if err := validateLogsKindName(kind); err != nil {
			t.Fatalf("validateLogsKindName(%q) error = %v", kind, err)
		}
	}
}

func TestValidateLogsKindNameRejectsUnknown(t *testing.T) {
	t.Parallel()

	err := validateLogsKindName("bogus")
	if err == nil {
		t.Fatal("validateLogsKindName accepted bogus kind")
	}
	if !strings.Contains(err.Error(), "Unsupported log kind") {
		t.Fatalf("validateLogsKindName error = %v, want Unsupported", err)
	}
	if !strings.Contains(err.Error(), "Use --logs audit, --logs debug, --logs file-trace, or --logs transcript.") {
		t.Fatalf("validateLogsKindName error = %v, want secondary hint line", err)
	}
}

func TestConsumeRootArgsStripsLeadingRootFlags(t *testing.T) {
	t.Parallel()

	roots, rest := consumeRootArgs([]string{"--root=/a", "--root=/b", "--id", "x"})
	if len(roots) != 2 || roots[0] != "/a" || roots[1] != "/b" {
		t.Fatalf("consumeRootArgs roots = %v, want [/a /b]", roots)
	}
	if len(rest) != 2 || rest[0] != "--id" || rest[1] != "x" {
		t.Fatalf("consumeRootArgs rest = %v, want [--id x]", rest)
	}
}

func TestConsumeRootArgsDropsEmptyRoots(t *testing.T) {
	t.Parallel()

	roots, rest := consumeRootArgs([]string{"--root=", "--root=/b", "--id", "x"})
	if len(roots) != 1 || roots[0] != "/b" {
		t.Fatalf("consumeRootArgs roots = %v, want [/b]", roots)
	}
	if len(rest) != 2 || rest[0] != "--id" {
		t.Fatalf("consumeRootArgs rest = %v, want [--id x]", rest)
	}
}

func TestConsumeRootArgsLeavesTrailingFlagsAlone(t *testing.T) {
	t.Parallel()

	roots, rest := consumeRootArgs([]string{"--id", "x", "--root=/late"})
	if len(roots) != 0 {
		t.Fatalf("consumeRootArgs roots = %v, want empty (only strips leading --root=)", roots)
	}
	if len(rest) != 3 {
		t.Fatalf("consumeRootArgs rest = %v, want untouched", rest)
	}
}

func TestLogPathForKindMapsAllKnown(t *testing.T) {
	t.Parallel()

	record := sessions.SessionRecord{
		AuditLogPath:      "/a",
		DebugLogPath:      "/d",
		FileTraceLogPath:  "/f",
		TranscriptLogPath: "/t",
	}
	cases := map[string]string{
		"audit":      "/a",
		"debug":      "/d",
		"file-trace": "/f",
		"transcript": "/t",
		"bogus":      "",
	}
	for kind, want := range cases {
		if got := logPathForKind(record, kind); got != want {
			t.Fatalf("logPathForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}
