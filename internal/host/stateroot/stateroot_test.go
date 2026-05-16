// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package stateroot

import (
	"strconv"
	"testing"
)

func TestConsumeRootArgsStripsLeadingRootFlags(t *testing.T) {
	t.Parallel()

	roots, rest := ConsumeRootArgs([]string{"--root=/a", "--root=/b", "--id", "x"})
	if len(roots) != 2 || roots[0] != "/a" || roots[1] != "/b" {
		t.Fatalf("ConsumeRootArgs roots = %v, want [/a /b]", roots)
	}
	if len(rest) != 2 || rest[0] != "--id" || rest[1] != "x" {
		t.Fatalf("ConsumeRootArgs rest = %v, want [--id x]", rest)
	}
}

func TestConsumeRootArgsDropsEmptyRoots(t *testing.T) {
	t.Parallel()

	roots, rest := ConsumeRootArgs([]string{"--root=", "--root=/b", "--id", "x"})
	if len(roots) != 1 || roots[0] != "/b" {
		t.Fatalf("ConsumeRootArgs roots = %v, want [/b]", roots)
	}
	if len(rest) != 2 || rest[0] != "--id" {
		t.Fatalf("ConsumeRootArgs rest = %v, want [--id x]", rest)
	}
}

func TestConsumeRootArgsLeavesTrailingFlagsAlone(t *testing.T) {
	t.Parallel()

	roots, rest := ConsumeRootArgs([]string{"--id", "x", "--root=/late"})
	if len(roots) != 0 {
		t.Fatalf("ConsumeRootArgs roots = %v, want empty (only strips leading --root=)", roots)
	}
	if len(rest) != 3 {
		t.Fatalf("ConsumeRootArgs rest = %v, want untouched", rest)
	}
}

func TestLookupRootsReadsEnv(t *testing.T) {
	t.Setenv("WORKCELL_STATE_ROOT", "/tmp/wc")
	t.Setenv("COLIMA_STATE_ROOT", "/tmp/colima")

	got, err := LookupRoots()
	if err != nil {
		t.Fatalf("LookupRoots err = %v, want nil", err)
	}
	want := []string{"/tmp/wc", "/tmp/colima"}
	if len(got) != len(want) {
		t.Fatalf("LookupRoots() = %v, want %v", got, want)
	}
	for i, root := range got {
		if root != want[i] {
			t.Fatalf("LookupRoots()[%d] = %q, want %q", i, root, want[i])
		}
	}
}

func TestLookupRootsSkipsEmpty(t *testing.T) {
	t.Setenv("WORKCELL_STATE_ROOT", "/tmp/wc")
	t.Setenv("COLIMA_STATE_ROOT", "")

	got, err := LookupRoots()
	if err != nil {
		t.Fatalf("LookupRoots err = %v, want nil", err)
	}
	if len(got) != 1 || got[0] != "/tmp/wc" {
		t.Fatalf("LookupRoots() = %v, want [/tmp/wc]", got)
	}
}

func TestLookupRootsRejectsControlChars(t *testing.T) {
	// NUL bytes are rejected by os.Setenv itself, so this test only
	// covers \n and \r — the bytes that survive process boundaries
	// and still break the bash `while read` loop. FormatRootArgs's
	// argv path covers NUL via TestFormatRootArgsRejectsControlChars.
	for _, root := range []string{"\n", "\r", "/tmp/wc\nsmuggled"} {
		t.Run("workcell="+strconv.Quote(root), func(t *testing.T) {
			t.Setenv("WORKCELL_STATE_ROOT", root)
			t.Setenv("COLIMA_STATE_ROOT", "")
			if _, err := LookupRoots(); err == nil {
				t.Errorf("LookupRoots accepted control char in WORKCELL_STATE_ROOT")
			}
		})
		t.Run("colima="+strconv.Quote(root), func(t *testing.T) {
			t.Setenv("WORKCELL_STATE_ROOT", "")
			t.Setenv("COLIMA_STATE_ROOT", root)
			if _, err := LookupRoots(); err == nil {
				t.Errorf("LookupRoots accepted control char in COLIMA_STATE_ROOT")
			}
		})
	}
}

func TestFormatRootArgsEmitsBothInWorkcellThenColimaOrder(t *testing.T) {
	t.Parallel()

	got, err := FormatRootArgs("/tmp/wc", "/tmp/colima")
	if err != nil {
		t.Fatalf("FormatRootArgs err = %v, want nil", err)
	}
	want := []string{"--root=/tmp/wc", "--root=/tmp/colima"}
	if len(got) != len(want) {
		t.Fatalf("FormatRootArgs() = %v, want %v", got, want)
	}
	for i, line := range got {
		if line != want[i] {
			t.Fatalf("FormatRootArgs()[%d] = %q, want %q", i, line, want[i])
		}
	}
}

func TestFormatRootArgsSkipsEmpty(t *testing.T) {
	t.Parallel()

	if got, err := FormatRootArgs("", "/tmp/colima"); err != nil || len(got) != 1 || got[0] != "--root=/tmp/colima" {
		t.Fatalf("FormatRootArgs(empty,/tmp/colima) = %v, %v, want [--root=/tmp/colima], nil", got, err)
	}
	if got, err := FormatRootArgs("/tmp/wc", ""); err != nil || len(got) != 1 || got[0] != "--root=/tmp/wc" {
		t.Fatalf("FormatRootArgs(/tmp/wc,empty) = %v, %v, want [--root=/tmp/wc], nil", got, err)
	}
	if got, err := FormatRootArgs("", ""); err != nil || len(got) != 0 {
		t.Fatalf("FormatRootArgs(empty,empty) = %v, %v, want [], nil", got, err)
	}
}

// TestFormatRootArgsRejectsControlChars guards against an
// attacker-controlled env var injecting forged --root= lines into the
// bash consumer's `while read` loop after passing through `env -i`.
func TestFormatRootArgsRejectsControlChars(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"\n", "\r", "\x00", "/tmp/wc\nsmuggled", "/tmp/wc\rsmuggled"} {
		if _, err := FormatRootArgs(root, "/tmp/ok"); err == nil {
			t.Errorf("FormatRootArgs(%q, /tmp/ok) accepted control char", root)
		}
		if _, err := FormatRootArgs("/tmp/ok", root); err == nil {
			t.Errorf("FormatRootArgs(/tmp/ok, %q) accepted control char", root)
		}
	}
}
