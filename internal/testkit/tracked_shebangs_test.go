// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

// readTrackedFile returns the contents of one tracked path, judged through the
// descriptor it reads. Opening with O_NOFOLLOW and taking the mode from the
// open file, rather than from an earlier stat of the pathname, means a swap of
// the final component between the check and the read cannot redirect the walk
// to another file. This tree tracks only regular files, so a symlink or a
// directory here is a failure rather than a case to skip.
func readTrackedFile(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("tracked path is %s, not a regular file", info.Mode().Type())
	}
	return io.ReadAll(file)
}

// shebangNeutralizesStartupFiles reports whether a Bash shebang line stops the
// interpreter from reading a caller-controlled startup file. Bash sources
// $BASH_ENV, and $ENV in POSIX mode, before the script's first line, so a
// script whose shebang leaves those names alone runs the caller's code first.
//
// Two spellings close that, and this repository uses both: privileged mode
// (-p), under which Bash ignores both variables outright, and an env(1) prefix
// that clears both names for the interpreter.
//
// Position decides which spelling a token belongs to. env(1) takes its
// assignments before the command name and passes everything after it to that
// command, so "env -S bash BASH_ENV= ENV=" hands Bash two arguments and leaves
// an inherited BASH_ENV in force. Assignments are therefore read only before
// the interpreter, and -p only after it.
func shebangNeutralizesStartupFiles(shebang string) bool {
	fields := strings.Fields(shebang)
	command := -1
	for i, field := range fields {
		if field == "bash" || strings.HasSuffix(field, "/bash") {
			command = i
			break
		}
	}
	if command < 0 {
		return false
	}
	for _, arg := range fields[command+1:] {
		if arg == "--" {
			// Bash stops reading options here; anything after is the operand.
			break
		}
		if arg == "-p" {
			return true
		}
	}
	if command == 0 {
		// The interpreter line names Bash directly and carried no -p, so there
		// is no room before it for an assignment.
		return false
	}
	clearedBashEnv, clearedEnv := false, false
	for _, assignment := range fields[1:command] {
		switch assignment {
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
		content, err := readTrackedFile(filepath.Join(root, rel))
		if err != nil {
			// A candidate this walk could not judge is not a candidate it
			// cleared. Reporting it keeps the walk fail-closed.
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

// TestReadTrackedFileRefusesASymlinkedPath proves the no-follow discipline:
// the walk reads the file the path names, never a target that path was pointed
// at. Without O_NOFOLLOW the symlink below would deliver the decoy body and the
// walk would judge the wrong file.
func TestReadTrackedFileRefusesASymlinkedPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	real := filepath.Join(dir, "real.sh")
	decoy := filepath.Join(dir, "decoy.sh")
	link := filepath.Join(dir, "link.sh")
	if err := os.WriteFile(real, []byte("#!/bin/bash -p\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy, link); err != nil {
		t.Fatal(err)
	}

	if content, err := readTrackedFile(real); err != nil || !strings.HasPrefix(string(content), "#!/bin/bash -p") {
		t.Fatalf("readTrackedFile() on a regular file = %q, %v", content, err)
	}
	if content, err := readTrackedFile(link); err == nil {
		t.Fatalf("readTrackedFile() followed a symlink and returned %q", content)
	}
	if content, err := readTrackedFile(dir); err == nil {
		t.Fatalf("readTrackedFile() accepted a directory and returned %q", content)
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
		// env(1) passes everything after the command name to that command, so
		// these two are Bash arguments and an inherited BASH_ENV still runs.
		{"cleared after the command", "#!/usr/bin/env -S bash BASH_ENV= ENV=", false},
		// -p is a Bash option, so it counts only after the command name, and
		// only before the -- that ends Bash option parsing.
		{"privileged before the command", "#!/usr/bin/env -S -p bash", false},
		{"privileged after the option terminator", "#!/usr/bin/env -S bash -- -p", false},
		{"privileged before the option terminator", "#!/usr/bin/env -S bash -p --", true},
		{"absolute interpreter after assignments", "#!/usr/bin/env -S BASH_ENV= ENV= /bin/bash", true},
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
