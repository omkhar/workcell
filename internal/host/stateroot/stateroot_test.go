// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package stateroot

import "testing"

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

	got := LookupRoots()
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

	got := LookupRoots()
	if len(got) != 1 || got[0] != "/tmp/wc" {
		t.Fatalf("LookupRoots() = %v, want [/tmp/wc]", got)
	}
}
