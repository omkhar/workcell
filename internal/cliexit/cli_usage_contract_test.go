// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package cliexit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCLIUsageContract verifies that all cmd/* binaries follow the CLI
// contract: -h exits 0, unknown flags exit 2, stray positionals exit 2.
func TestCLIUsageContract(t *testing.T) {
	// Get the repo root from this file's path.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine repo root")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	cmdDir := filepath.Join(repoRoot, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", cmdDir, err)
	}

	// Build all binaries first
	binaries := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		binName := entry.Name()
		mainFile := filepath.Join(cmdDir, binName, "main.go")
		if _, err := os.Stat(mainFile); err != nil {
			t.Logf("skip %s: no main.go", binName)
			continue
		}

		binPath := filepath.Join(t.TempDir(), binName)
		cmd := exec.Command("go", "build", "-o", binPath, filepath.Join(cmdDir, binName))
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

func testBinaryCLIContract(t *testing.T, binName, binPath string) {
	// Binaries that accept positional arguments (subcommands or command args).
	// For these, we skip the "stray positional" test because they may
	// interpret the positional as a subcommand or argument.
	acceptsPositionals := map[string]bool{
		"workcell-apt-broker-client": true, // passes command args
		"workcell-apt-broker-server": true, // server startup args
		"workcell-citools":           true, // subcommand-based
		"workcell-hostutil":          true, // subcommand-based
		"workcell-runtimeutil":       true, // subcommand-based
	}

	tests := []struct {
		name     string
		args     []string
		wantExit int
	}{
		// All binaries must support -h and exit 0
		{
			name:     "help-short",
			args:     []string{"-h"},
			wantExit: 0,
		},
		// Unknown flags must exit 2 (usage error)
		{
			name:     "unknown-flag",
			args:     []string{"--this-flag-does-not-exist"},
			wantExit: 2,
		},
	}

	// Stray positionals must exit 2 for binaries that don't accept them
	if !acceptsPositionals[binName] {
		tests = append(tests, struct {
			name     string
			args     []string
			wantExit int
		}{
			name:     "stray-positional",
			args:     []string{"stray-arg"},
			wantExit: 2,
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(binPath, tt.args...)
			output, err := cmd.CombinedOutput()

			var exitCode int
			if err == nil {
				exitCode = 0
			} else if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				t.Fatalf("unexpected error: %v", err)
			}

			if exitCode != tt.wantExit {
				t.Errorf("%s %v: exit code %d, want %d\noutput:\n%s",
					binName, tt.args, exitCode, tt.wantExit, string(output))
			}
		})
	}
}
