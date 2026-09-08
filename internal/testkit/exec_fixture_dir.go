// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ExecFixtureDir returns a directory for an executable test fixture (a
// hook, filter, or remote helper a test writes and then has git, cmd/go, or
// the shell actually invoke). Such a fixture needs a path that is all of:
// writable by an arbitrary or unmapped uid, exec-capable, and free of
// whitespace (a shell or a tool's own naive argument splitting can misread a
// space). No single hardcoded location satisfies every supported
// environment: the repo checkout can be unwritable under a remapped uid or
// carry whitespace on some dev machines, a hardcoded /tmp is mounted noexec
// inside the workcell container, and TMPDIR itself is deliberately hostile
// under the CI lane that exercises that axis. So this probes candidates at
// runtime and uses the first one that can actually execute a script.
func ExecFixtureDir(tb testing.TB) string {
	tb.Helper()

	var candidates []string
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		candidates = append(candidates, runtimeDir)
	}
	// /dev/shm: a fixed, world-writable, exec-capable tmpfs on Linux
	// (including the workcell container's exec-capable roots); absent on
	// darwin, where the probe below simply skips it.
	candidates = append(candidates, "/dev/shm", os.TempDir())

	for _, base := range candidates {
		if base == "" || strings.ContainsAny(base, " \t\n") {
			continue
		}
		if info, err := os.Stat(base); err != nil || !info.IsDir() {
			continue
		}
		dir, err := os.MkdirTemp(base, "workcell-exec-fixture-")
		if err != nil {
			continue
		}
		probe := filepath.Join(dir, "probe.sh")
		if err := os.WriteFile(probe, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			_ = os.RemoveAll(dir)
			continue
		}
		if err := exec.Command(probe).Run(); err != nil {
			_ = os.RemoveAll(dir)
			continue
		}
		_ = os.Remove(probe)
		tb.Cleanup(func() { _ = os.RemoveAll(dir) })
		return dir
	}

	tb.Skip("no writable, exec-capable, whitespace-free directory found for executable test fixtures")
	return ""
}
