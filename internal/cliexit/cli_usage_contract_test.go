// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package cliexit

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCLIUsageContract verifies that all cmd/* binaries follow the CLI
// contract: -h and --help exit 0 with usage text, unknown flags exit 2,
// and stray positionals exit 2 with usage text on stderr.
func TestCLIUsageContract(t *testing.T) {
	// Get the repo root from this file's path.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine repo root")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	dirs := mainPackageDirs(t, repoRoot, "./cmd/...")
	if len(dirs) == 0 {
		t.Fatal("no main packages found under cmd/")
	}

	// Build all binaries first
	binaries := make(map[string]string)
	for _, dir := range dirs {
		binName := filepath.Base(dir)
		binPath := filepath.Join(t.TempDir(), binName)
		cmd := exec.Command("go", "build", "-o", binPath, dir)
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("go build %s: %v\noutput: %s", binName, err, string(out))
			continue
		}
		binaries[binName] = binPath
	}

	for binName, binPath := range binaries {
		t.Run(binName, func(t *testing.T) {
			testBinaryCLIContract(t, binName, binPath)
		})
	}
}

// mainPackageDirs lists the directories of every package main matched by
// pattern, so discovery does not depend on which file holds func main.
func mainPackageDirs(t *testing.T, moduleRoot, pattern string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", `{{if eq .Name "main"}}{{.Dir}}{{end}}`, pattern)
	cmd.Dir = moduleRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	// One directory per line: split on newlines only so a path containing
	// spaces stays whole. Non-main packages print an empty line.
	var dirs []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSuffix(line, "\r"); line != "" {
			dirs = append(dirs, line)
		}
	}
	return dirs
}

func TestMainPackageDirsFindsMainOutsideMainGo(t *testing.T) {
	// The space in the module directory name guards against whitespace splitting.
	root := filepath.Join(t.TempDir(), "module with space")
	tool := filepath.Join(root, "cmd", "tool")
	if err := os.MkdirAll(tool, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		filepath.Join(root, "go.mod"): "module example.com/fixture\n\ngo 1.21\n",
		filepath.Join(tool, "cli.go"): "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := mainPackageDirs(t, root, "./cmd/...")
	if len(got) != 1 || filepath.Base(got[0]) != "tool" {
		t.Fatalf("mainPackageDirs = %v, want the cmd/tool package", got)
	}
}

func testBinaryCLIContract(t *testing.T, binName, binPath string) {
	// Binaries that accept positional arguments (subcommands or command args).
	// For these, we skip the "stray positional" test because they may
	// interpret the positional as a subcommand or argument.
	acceptsPositionals := map[string]bool{
		"workcell-apt-broker-client": true, // passes command args
		"workcell-citools":           true, // subcommand-based
		"workcell-hostutil":          true, // subcommand-based
		"workcell-runtimeutil":       true, // subcommand-based
	}

	type contractCase struct {
		name     string
		args     []string
		wantExit int
		// wantUsage requires usage text: on stdout or stderr for help,
		// on stderr for a usage error.
		wantUsage bool
	}
	tests := []contractCase{
		// All binaries must support -h and --help and exit 0
		{name: "help-short", args: []string{"-h"}, wantExit: 0, wantUsage: true},
		{name: "help-long", args: []string{"--help"}, wantExit: 0, wantUsage: true},
		// Unknown flags must exit 2 (usage error)
		{name: "unknown-flag", args: []string{"--this-flag-does-not-exist"}, wantExit: 2},
	}

	// Stray positionals must exit 2 for binaries that don't accept them
	if !acceptsPositionals[binName] {
		tests = append(tests, contractCase{name: "stray-positional", args: []string{"stray-arg"}, wantExit: 2, wantUsage: true})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			cmd := exec.Command(binPath, tt.args...)
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()

			var exitCode int
			if err == nil {
				exitCode = 0
			} else if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				t.Fatalf("unexpected error: %v", err)
			}

			if exitCode != tt.wantExit {
				t.Errorf("%s %v: exit code %d, want %d\nstdout:\n%s\nstderr:\n%s",
					binName, tt.args, exitCode, tt.wantExit, stdout.String(), stderr.String())
			}
			if !tt.wantUsage {
				return
			}
			usageText := stderr.String()
			if tt.wantExit == 0 {
				usageText = stdout.String() + usageText
			}
			// Go's flag package prints "Usage of", hand-written usage prints
			// "usage:" or "Usage:"; all contain "usage" case-insensitively.
			if !strings.Contains(strings.ToLower(usageText), "usage") {
				t.Errorf("%s %v: no usage text\nstdout:\n%s\nstderr:\n%s",
					binName, tt.args, stdout.String(), stderr.String())
			}
		})
	}
}
