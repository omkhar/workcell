// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// shellSafePath matches a candidate base that is safe to splice as literal
// text into generated shell script source (a double-quoted assignment, a
// redirect target) as well as safe for a tool's own naive space-splitting.
// This is an allowlist rather than a denylist of "$ and backtick": a
// candidate can come from an ambient environment variable (XDG_RUNTIME_DIR)
// that this package does not control, and a denylist only covers the
// metacharacters someone remembered to name.
var shellSafePath = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

// repoRoot returns the checkout root, derived from this file's own path at
// compile time: a plain location that a hostile TMPDIR never touches.
func repoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("unable to determine repo root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// ExecFixtureDir returns a directory for an executable test fixture (a
// hook, filter, or remote helper a test writes and then has git, cmd/go, or
// the shell actually invoke). Such a fixture needs a path that is all of:
// writable by an arbitrary or unmapped uid, exec-capable, and shell-safe (a
// shell's own re-interpolation, a tool's naive argument splitting, or a
// caller that splices the returned path into generated shell script text can
// all misread whitespace or a metacharacter such as $ or a backtick). No
// single hardcoded location satisfies every supported environment: the repo
// checkout can be unwritable under a remapped uid or carry whitespace on some
// dev machines, a hardcoded /tmp is mounted noexec inside the workcell
// container, and TMPDIR itself is deliberately hostile under the CI lane
// that exercises that axis. So this probes candidates at runtime and uses
// the first one that can actually execute a script.
//
// The returned path always matches shellSafePath, so a caller may still
// choose to quote it (ShellQuote) as defense in depth, but does not have to
// treat it as hostile.
//
// The repo checkout root is tried last, after /dev/shm and TMPDIR: under
// the container's uidmap hostile axis the checkout is writable by every
// uid (ownership fix #717) and always exec-capable, so it is a real
// fallback rather than a redundant one.
func ExecFixtureDir(tb testing.TB) string {
	tb.Helper()

	type candidate struct {
		name string
		path string
	}
	var candidates []candidate
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		candidates = append(candidates, candidate{"$XDG_RUNTIME_DIR", runtimeDir})
	} else {
		candidates = append(candidates, candidate{"$XDG_RUNTIME_DIR", ""})
	}
	candidates = append(candidates,
		// /dev/shm: a fixed, world-writable, exec-capable tmpfs on Linux
		// (including the workcell container's exec-capable roots); absent on
		// darwin, where the probe below simply skips it.
		candidate{"/dev/shm", "/dev/shm"},
		candidate{"os.TempDir()", os.TempDir()},
		candidate{"repo checkout root", repoRoot(tb)},
	)

	var tried []string
	for _, c := range candidates {
		if c.path == "" {
			tried = append(tried, c.name+": unset")
			continue
		}
		if !shellSafePath.MatchString(c.path) {
			tried = append(tried, c.name+" ("+c.path+"): shell-unsafe path")
			continue
		}
		if info, err := os.Stat(c.path); err != nil || !info.IsDir() {
			tried = append(tried, c.name+" ("+c.path+"): absent")
			continue
		}
		dir, err := os.MkdirTemp(c.path, "workcell-exec-fixture-")
		if err != nil {
			tried = append(tried, c.name+" ("+c.path+"): not writable: "+err.Error())
			continue
		}
		probe := filepath.Join(dir, "probe.sh")
		if err := os.WriteFile(probe, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			_ = os.RemoveAll(dir)
			tried = append(tried, c.name+" ("+c.path+"): not writable: "+err.Error())
			continue
		}
		if err := exec.Command(probe).Run(); err != nil {
			_ = os.RemoveAll(dir)
			tried = append(tried, c.name+" ("+c.path+"): noexec: "+err.Error())
			continue
		}
		_ = os.Remove(probe)
		tb.Cleanup(func() { _ = os.RemoveAll(dir) })
		return dir
	}

	// Every candidate failed: this is a real environment defect, not a
	// reason to quietly drop coverage of whatever security control the
	// fixture backs. Fail loudly rather than skip.
	tb.Fatalf("no writable, exec-capable, shell-safe directory found for executable test fixtures; tried:\n  %s", strings.Join(tried, "\n  "))
	return ""
}
