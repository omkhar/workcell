// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestRunRejectsMissingSubcommand(t *testing.T) {
	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("run(nil) = %v, want usage error", err)
	}
	if ec, ok := cliexit.IsExitCodeError(err); !ok || ec.Code != 2 {
		t.Fatalf("run(nil) exit code = %v (ok=%v), want ExitCodeError{Code:2}", err, ok)
	}
}

// TestUsageExitsWithCode2 pins the D8 exit-code contract at the process level:
// a usage error exits 2, matching the other workcell Go CLIs.
func TestUsageExitsWithCode2(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestColimautilHelperProcess", "--")
	cmd.Env = append(os.Environ(), "WORKCELL_COLIMAUTIL_HELPER_PROCESS=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
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

func TestColimautilHelperProcess(t *testing.T) {
	if os.Getenv("WORKCELL_COLIMAUTIL_HELPER_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"workcell-colimautil"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestRunRejectsUnknownSubcommand(t *testing.T) {
	err := run([]string{"unknown-subcommand"})
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("run(unknown) = %v, want usage error", err)
	}
}

func TestRunRejectsWrongArity(t *testing.T) {
	for _, args := range [][]string{
		{"validate-runtime-mounts", "only-one"},
		{"validate-profile-config", "a", "b"},
	} {
		err := run(args)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("run(%v) = %v, want usage error", args, err)
		}
	}
}
