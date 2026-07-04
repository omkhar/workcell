// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWrongArityExitsWithUsageCode(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestCitoolsHelperProcess", "--", "tree-compare", "only-one-root")
	cmd.Env = append(os.Environ(), "WORKCELL_CITOOLS_HELPER_PROCESS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("workcell-citools tree-compare with one arg exited 0")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("cmd.Run() error = %T %v, want ExitError", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", exitErr.ExitCode(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr = %q, want usage text", stderr.String())
	}
}

func TestNoArgsExitsWithUsageCode(t *testing.T) {
	assertCitoolsUsageExit(t)
}

func TestUnknownCommandExitsWithUsageCode(t *testing.T) {
	assertCitoolsUsageExit(t, "definitely-not-a-subcommand")
}

// assertCitoolsUsageExit runs the binary (via the helper-process trick) with
// the given argv tail and asserts a usage exit (code 2 + "usage:" on stderr).
func assertCitoolsUsageExit(t *testing.T, argv ...string) {
	t.Helper()
	runArgs := append([]string{"-test.run=TestCitoolsHelperProcess", "--"}, argv...)
	cmd := exec.Command(os.Args[0], runArgs...)
	cmd.Env = append(os.Environ(), "WORKCELL_CITOOLS_HELPER_PROCESS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("workcell-citools %v exited 0, want usage error", argv)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("cmd.Run() error = %T %v, want ExitError", err, err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2; stderr=%q", exitErr.ExitCode(), stderr.String())
	}
	if s := stderr.String(); !strings.Contains(s, "usage:") && !strings.Contains(s, "unknown command") {
		t.Fatalf("stderr = %q, want usage or unknown-command text", s)
	}
}

func TestCitoolsHelperProcess(t *testing.T) {
	if os.Getenv("WORKCELL_CITOOLS_HELPER_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"workcell-citools"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}
