// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestMaterializeWorkspaceRootHandling: symlinked dir root materializes children;
// read-only 0555 dir copies its child and lands 0555; non-dir root is rejected.
func TestMaterializeWorkspaceRootHandling(t *testing.T) {
	t.Parallel()

	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	materialize := func(src string) (MaterializeResult, error) {
		return target.MaterializeWorkspace(context.Background(), MaterializeRequest{StateRoot: t.TempDir(), TargetID: "t", MaterializationID: "m", SourceWorkspace: src})
	}

	src := writeSampleWorkspace(t)
	ro := filepath.Join(src, "ro")
	mustNil(t, os.MkdirAll(ro, 0o755))
	mustNil(t, os.WriteFile(filepath.Join(ro, "f.txt"), []byte("x\n"), 0o644))
	mustNil(t, os.Chmod(ro, 0o555))
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	link := filepath.Join(t.TempDir(), "wl")
	mustNil(t, os.Symlink(src, link))
	res, err := materialize(link) // symlinked dir root
	mustNil(t, err)
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(res.MaterializedWorkspace, "ro"), 0o755) })
	roInfo, err := os.Stat(filepath.Join(res.MaterializedWorkspace, "ro"))
	mustNil(t, err) // child + parent copied through symlinked, read-only dir
	mustNil(t, func() error { _, e := os.Stat(filepath.Join(res.MaterializedWorkspace, "ro", "f.txt")); return e }())
	if roInfo.Mode().Perm() != 0o555 {
		t.Fatalf("materialized read-only dir mode = %o, want 0555", roInfo.Mode().Perm())
	}

	file := filepath.Join(t.TempDir(), "f")
	mustNil(t, os.WriteFile(file, []byte("x"), 0o644))
	fileLink := filepath.Join(t.TempDir(), "fl")
	mustNil(t, os.Symlink(file, fileLink))
	if _, err := materialize(file); err == nil {
		t.Fatalf("regular-file source workspace accepted")
	}
	if _, err := materialize(fileLink); err == nil { // symlink resolves to a non-dir
		t.Fatalf("symlink-to-file source workspace accepted")
	}
}

// TestCopyWorkspaceTreePreservesFileMode: a file whose mode umask 022 would strip
// on create lands with its exact bits on disk and in the manifest entry.
func TestCopyWorkspaceTreePreservesFileMode(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	file := filepath.Join(src, "run.sh")
	mustNil(t, os.WriteFile(file, []byte("#!/bin/sh\n"), 0o600))
	mustNil(t, os.Chmod(file, 0o666))
	dst := filepath.Join(t.TempDir(), "out")
	entries, err := copyWorkspaceTree(src, dst, nil)
	mustNil(t, err)
	info, err := os.Stat(filepath.Join(dst, "run.sh"))
	mustNil(t, err)
	if info.Mode().Perm() != 0o666 {
		t.Fatalf("destination mode = %o, want 0666 (umask must not strip it)", info.Mode().Perm())
	}
	if len(entries) != 1 || entries[0].Mode.Perm() != 0o666 {
		t.Fatalf("manifest entry mode wrong: %+v", entries)
	}
}

// TestCopyWorkspaceTreeRelativeSourceSymlink: a relative source root (".") with an
// internal symlink must materialize (no false-positive escape), while a symlink
// escaping the root is still rejected.
func TestCopyWorkspaceTreeRelativeSourceSymlink(t *testing.T) {
	// Not parallel: uses os.Chdir.
	cwd, err := os.Getwd()
	mustNil(t, err)
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	// (a) relative root with an internal symlink materializes.
	ok := t.TempDir()
	mustNil(t, os.WriteFile(filepath.Join(ok, "real.txt"), []byte("x\n"), 0o644))
	mustNil(t, os.Symlink("real.txt", filepath.Join(ok, "link")))
	mustNil(t, os.Chdir(ok))
	if _, err := copyWorkspaceTree(".", filepath.Join(t.TempDir(), "outa"), nil); err != nil {
		t.Fatalf("relative source with internal symlink rejected: %v", err)
	}

	// (b) a symlink escaping the root is still rejected.
	bad := t.TempDir()
	mustNil(t, os.Symlink("../escape", filepath.Join(bad, "esc")))
	mustNil(t, os.Chdir(bad))
	if _, err := copyWorkspaceTree(".", filepath.Join(t.TempDir(), "outb"), nil); err == nil {
		t.Fatalf("escaping symlink accepted")
	}
}

// TestBootstrapTargetScopesManifestByID: bootstrapping the same target twice with
// different ids writes two distinct persisted manifests, each matching its result.
func TestBootstrapTargetScopesManifestByID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state := t.TempDir()
	b1, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "boot-1", ImageRef: "img:1"})
	mustNil(t, err)
	b2, err := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: state, TargetID: "tid", BootstrapID: "boot-2", ImageRef: "img:2"})
	mustNil(t, err)

	if b1.ManifestPath == b2.ManifestPath {
		t.Fatalf("distinct bootstrap ids share a manifest path %q", b1.ManifestPath)
	}
	for _, b := range []BootstrapResult{b1, b2} {
		data, err := os.ReadFile(b.ManifestPath)
		mustNil(t, err)
		if !strings.Contains(string(data), `"bootstrap_id": "`+b.Manifest.BootstrapID+`"`) {
			t.Fatalf("manifest %q does not match returned bootstrap_id %q:\n%s", b.ManifestPath, b.Manifest.BootstrapID, data)
		}
	}
}

// TestMaterializeRejectsStateRootOverlappingSource: a state root equal to, inside,
// or a parent of the source workspace is degenerate and must be rejected before
// any state is created; a separate state root works.
func TestMaterializeRejectsStateRootOverlappingSource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	src := writeSampleWorkspace(t)

	for name, state := range map[string]string{
		"equal":  src,
		"inside": filepath.Join(src, ".workcell-state"),
		"parent": filepath.Dir(src),
	} {
		if _, e := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: src}); e == nil {
			t.Fatalf("%s: state root %q overlapping source %q accepted", name, state, src)
		}
	}

	res, err := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: t.TempDir(), TargetID: "tid", MaterializationID: "mid", SourceWorkspace: src})
	mustNil(t, err)
	if _, err := os.Stat(filepath.Join(res.MaterializedWorkspace, "src", "main.go")); err != nil {
		t.Fatalf("source content missing: %v", err)
	}
}

// TestCoreAuditTokenRejection: the id/ref tokens validated by materialization and
// bootstrap reject whitespace/control (audit-line injection prevention).
func TestCoreAuditTokenRejection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	source := writeSampleWorkspace(t)
	for _, v := range []string{"a\nb", "a b", "a\tb", "a\rb"} {
		if _, e := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: t.TempDir(), TargetID: "tid", MaterializationID: v, SourceWorkspace: source}); e == nil {
			t.Fatalf("materialization_id %q accepted", v)
		}
		if _, e := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: t.TempDir(), TargetID: v, MaterializationID: "mid", SourceWorkspace: source}); e == nil {
			t.Fatalf("target_id %q accepted", v)
		}
		if _, e := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: t.TempDir(), TargetID: "tid", BootstrapID: v, ImageRef: "img:1"}); e == nil {
			t.Fatalf("bootstrap_id %q accepted", v)
		}
		if _, e := target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: t.TempDir(), TargetID: "tid", BootstrapID: "bid", ImageRef: v}); e == nil {
			t.Fatalf("image_ref %q accepted", v)
		}
	}
}

// TestCopyWorkspaceTreeExcludesGitCaseInsensitive: a `.GIT` directory is excluded
// like `.git` (case-insensitive), so git control state cannot leak into the VM
// workspace on a case-insensitive volume (APFS default).
func TestCopyWorkspaceTreeExcludesGitCaseInsensitive(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	mustNil(t, os.MkdirAll(filepath.Join(src, ".GIT"), 0o755))
	mustNil(t, os.WriteFile(filepath.Join(src, ".GIT", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	mustNil(t, os.WriteFile(filepath.Join(src, "keep.txt"), []byte("x\n"), 0o644))
	dst := filepath.Join(t.TempDir(), "out")
	entries, err := copyWorkspaceTree(src, dst, []string{".git"})
	mustNil(t, err)
	if _, err := os.Stat(filepath.Join(dst, ".GIT")); !os.IsNotExist(err) {
		t.Fatalf(".GIT leaked into the materialized workspace")
	}
	for _, e := range entries {
		if strings.EqualFold(e.Path, ".GIT") || strings.HasPrefix(strings.ToLower(e.Path), ".git/") {
			t.Fatalf("manifest records excluded git entry %q", e.Path)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "keep.txt")); err != nil {
		t.Fatalf("kept file missing: %v", err)
	}
}

// TestPathAndExclusionCaseInsensitivity unit-tests the case-insensitive,
// component-aware containment and exclusion comparisons.
func TestPathAndExclusionCaseInsensitivity(t *testing.T) {
	t.Parallel()

	if !pathWithin("/foo/bar", "/FOO/BAR/baz") {
		t.Fatalf("case-insensitive containment not detected")
	}
	if pathWithin("/foo", "/foobar") {
		t.Fatalf("/foobar wrongly treated as inside /foo (component boundary)")
	}
	if pathWithin("/foo", "/foo") {
		t.Fatalf("equal path wrongly treated as strictly within")
	}
	for _, name := range []string{".GIT", ".Git", ".git/config", ".GIT/hooks/pre-commit"} {
		if !isExcludedPath(name, []string{".git"}) {
			t.Fatalf("%q not excluded case-insensitively", name)
		}
	}
	if isExcludedPath(".gitignore", []string{".git"}) {
		t.Fatalf(".gitignore wrongly excluded (component boundary)")
	}
}

// TestMaterializeRejectsCaseInsensitiveOverlap: a state root spelled with a
// different case than a source component still overlaps the source and is
// rejected.
func TestMaterializeRejectsCaseInsensitiveOverlap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	base := t.TempDir()
	src := filepath.Join(base, "work")
	mustNil(t, os.MkdirAll(filepath.Join(src, "src"), 0o755))
	mustNil(t, os.WriteFile(filepath.Join(src, "src", "main.go"), []byte("package main\n"), 0o644))

	// base/WORK is the same dir as base/work (case-insensitive): inside → overlap,
	// and equal → overlap.
	for _, state := range []string{filepath.Join(base, "WORK", "state"), filepath.Join(base, "WORK")} {
		if _, e := target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: src}); e == nil {
			t.Fatalf("case-variant overlapping state root %q accepted", state)
		}
	}
}

func writeSampleWorkspace(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	mustNil(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	mustNil(t, os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	mustNil(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	mustNil(t, os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644))
	mustNil(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("# sample\n"), 0o644))
	// A 0666 file whose bits umask 022 would strip on create; the copy must chmod.
	run := filepath.Join(root, "run.sh")
	mustNil(t, os.WriteFile(run, []byte("#!/bin/sh\n"), 0o600))
	mustNil(t, os.Chmod(run, 0o666))
	return root
}
