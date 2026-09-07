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

// minTrackedBashScripts is the inventory floor for the shebang walk. A
// tracked-file walk that returns nothing, or that stops early, otherwise
// passes vacuously. The count on this tree is well above this floor; raise
// the floor only with the tree.
const minTrackedBashScripts = 100

// trackedFiles returns every tracked path in the repository, relative to root.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var paths []string
	for _, path := range strings.Split(string(out), "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		t.Fatal("git ls-files reported no tracked files")
	}
	return paths
}

// shebangNeutralizesStartupFiles reports whether a Bash shebang line stops the
// interpreter from reading a caller-controlled startup file. Bash sources
// $BASH_ENV, and $ENV in POSIX mode, before the script's first line, so a
// script whose shebang leaves those names alone runs the caller's code first.
//
// Two spellings close that, and this repository uses both: privileged mode
// (-p), under which Bash ignores both variables outright, and an env(1) prefix
// that clears both names for the interpreter.
func shebangNeutralizesStartupFiles(shebang string) bool {
	clearedBashEnv, clearedEnv := false, false
	for _, field := range strings.Fields(shebang) {
		switch field {
		case "-p":
			return true
		case "BASH_ENV=":
			clearedBashEnv = true
		case "ENV=":
			clearedEnv = true
		}
	}
	return clearedBashEnv && clearedEnv
}

// TestTrackedBashScriptsNeutralizeStartupFiles extends the single-script
// shebang assertion in internal/metadatautil to every tracked Bash script. The
// per-script check pins one reviewed file; the startup-file property is
// required of all of them.
func TestTrackedBashScriptsNeutralizeStartupFiles(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	checked := 0
	for _, rel := range trackedFiles(t, root) {
		path := filepath.Join(root, rel)
		info, err := os.Lstat(path)
		if err != nil {
			t.Errorf("stat %s: %v", rel, err)
			continue
		}
		if !info.Mode().IsRegular() {
			// A tracked symlink or submodule gitlink holds no script body.
			continue
		}
		// A regular tracked file that cannot be read is a candidate this walk
		// failed to judge, not one it cleared.
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		shebang, _, _ := strings.Cut(string(content), "\n")
		if !strings.HasPrefix(shebang, "#!") || !strings.Contains(shebang, "bash") {
			continue
		}
		checked++
		if !shebangNeutralizesStartupFiles(shebang) {
			t.Errorf("%s starts with %s; every tracked Bash script must clear BASH_ENV and ENV, either with the env(1) prefix form or with `#!/bin/bash -p`", rel, shebang)
		}
	}
	if checked < minTrackedBashScripts {
		t.Fatalf("scanned %d Bash scripts, want at least %d; the tracked-file walk did not complete", checked, minTrackedBashScripts)
	}
}

func TestShebangNeutralizesStartupFilesRejectsUnhardenedForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		shebang string
		want    bool
	}{
		{"cleared env prefix", "#!/usr/bin/env -S BASH_ENV= ENV= bash", true},
		{"privileged", "#!/bin/bash -p", true},
		{"cleared prefix and privileged", "#!/usr/bin/env -S BASH_ENV= ENV= LC_ALL=C bash -p", true},
		{"bare", "#!/bin/bash", false},
		{"bare env", "#!/usr/bin/env bash", false},
		{"BASH_ENV cleared only", "#!/usr/bin/env -S BASH_ENV= bash", false},
		{"ENV cleared only", "#!/usr/bin/env -S ENV= bash", false},
		{"assigned not cleared", "#!/usr/bin/env -S BASH_ENV=/tmp/rc ENV=/tmp/rc bash", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := shebangNeutralizesStartupFiles(test.shebang); got != test.want {
				t.Fatalf("shebangNeutralizesStartupFiles(%q) = %t, want %t", test.shebang, got, test.want)
			}
		})
	}
}
