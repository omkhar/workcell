// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestResolveSessionSubcommandAcceptsCanonical(t *testing.T) {
	t.Parallel()

	cases := []string{
		"start",
		"attach",
		"send",
		"stop",
		"list",
		"show",
		"delete",
		"logs",
		"timeline",
		"diff",
		"export",
		"verify",
		"monitor",
	}
	for _, name := range cases {
		got, err := resolveSessionSubcommand(name)
		if err != nil {
			t.Fatalf("resolveSessionSubcommand(%q) error = %v", name, err)
		}
		if got != name {
			t.Fatalf("resolveSessionSubcommand(%q) = %q, want %q", name, got, name)
		}
	}
}

func TestResolveSessionSubcommandMapsHelp(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "-h", "--help"} {
		got, err := resolveSessionSubcommand(name)
		if err != nil {
			t.Fatalf("resolveSessionSubcommand(%q) error = %v", name, err)
		}
		if got != "usage" {
			t.Fatalf("resolveSessionSubcommand(%q) = %q, want %q", name, got, "usage")
		}
	}
}

func TestResolveSessionSubcommandRejectsUnknown(t *testing.T) {
	t.Parallel()

	got, err := resolveSessionSubcommand("frobnicate")
	if err == nil {
		t.Fatalf("resolveSessionSubcommand accepted unknown subcommand, returned %q", got)
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) {
		t.Fatalf("resolveSessionSubcommand err = %v, want ExitCodeError", err)
	}
	if ec.Code != 2 {
		t.Fatalf("resolveSessionSubcommand ExitCodeError.Code = %d, want 2", ec.Code)
	}
	// Bash diagnostic must stay byte-identical so the user-visible
	// stderr matches the legacy "Unsupported workcell session command: <name>"
	// emitted by session_main.
	want := "Unsupported workcell session command: frobnicate"
	if !strings.Contains(ec.Message, want) {
		t.Fatalf("resolveSessionSubcommand message = %q, want %q", ec.Message, want)
	}
}

func TestDispatchMainEmitsCanonicalRoute(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"start":    "subcommand=start\n",
		"attach":   "subcommand=attach\n",
		"send":     "subcommand=send\n",
		"stop":     "subcommand=stop\n",
		"list":     "subcommand=list\n",
		"show":     "subcommand=show\n",
		"delete":   "subcommand=delete\n",
		"logs":     "subcommand=logs\n",
		"timeline": "subcommand=timeline\n",
		"diff":     "subcommand=diff\n",
		"export":   "subcommand=export\n",
		"verify":   "subcommand=verify\n",
		"monitor":  "subcommand=monitor\n",
	}
	for name, want := range cases {
		var buf bytes.Buffer
		if err := dispatchMain([]string{name}, &buf); err != nil {
			t.Fatalf("dispatchMain(%q) error = %v", name, err)
		}
		if buf.String() != want {
			t.Fatalf("dispatchMain(%q) output = %q, want %q", name, buf.String(), want)
		}
	}
}

func TestDispatchMainEmitsUsageForEmptyAndHelp(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {}, {""}, {"-h"}, {"--help"}} {
		var buf bytes.Buffer
		if err := dispatchMain(args, &buf); err != nil {
			t.Fatalf("dispatchMain(%v) error = %v", args, err)
		}
		if buf.String() != "subcommand=usage\n" {
			t.Fatalf("dispatchMain(%v) output = %q, want %q", args, buf.String(), "subcommand=usage\n")
		}
	}
}

func TestDispatchMainPropagatesExtraArgs(t *testing.T) {
	t.Parallel()

	// Extra args after the subcommand must not affect the routing
	// decision - the bash shim is responsible for passing them through
	// to the matching per-subcommand handler.
	var buf bytes.Buffer
	args := []string{"attach", "--id", "session-1", "--no-stdin"}
	if err := dispatchMain(args, &buf); err != nil {
		t.Fatalf("dispatchMain error = %v", err)
	}
	if buf.String() != "subcommand=attach\n" {
		t.Fatalf("dispatchMain output = %q, want %q", buf.String(), "subcommand=attach\n")
	}
}

func TestDispatchMainRejectsUnknownSubcommand(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := dispatchMain([]string{"frobnicate"}, &buf)
	if err == nil {
		t.Fatal("dispatchMain accepted unknown subcommand")
	}
	var ec *cliexit.ExitCodeError
	if !errors.As(err, &ec) || ec.Code != 2 {
		t.Fatalf("dispatchMain error = %v, want ExitCodeError{Code:2}", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("dispatchMain wrote %q on error, want no output", buf.String())
	}
}
