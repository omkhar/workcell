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
