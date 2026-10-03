// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

// jq exit 3 is a compile error and 2 is a usage error. Any other status, a
// runtime error on the empty fixture included, means the program compiled.
func jqCompiles(t *testing.T, program metadatautil.WorkflowJQProgram) error {
	t.Helper()
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Fatalf("jq is required to execute workflow jq programs: %v", err)
	}
	args := append(append([]string{}, program.Flags...), program.Program)
	command := exec.Command(jq, args...)
	command.Stdin = strings.NewReader("[]")
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() != 2 && exitError.ExitCode() != 3 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %s", err, output)
	}
	return nil
}

func TestWorkflowInlineJQProgramsCompile(t *testing.T) {
	programs, err := metadatautil.WorkflowInlineJQPrograms("../..")
	if err != nil {
		t.Fatal(err)
	}
	// The repository has dozens of inline programs. A parser regression that
	// finds none must fail here rather than pass an empty loop.
	if len(programs) < 20 {
		t.Fatalf("found %d inline jq programs, want at least 20", len(programs))
	}
	for _, program := range programs {
		t.Run(program.Where, func(t *testing.T) {
			if err := jqCompiles(t, program); err != nil {
				t.Fatalf("jq program does not compile: %v\n%s", err, program.Program)
			}
		})
	}
}

func TestWorkflowInlineJQNegativeControl(t *testing.T) {
	bad := metadatautil.WorkflowJQProgram{Program: ".a | | .b"}
	if jqCompiles(t, bad) == nil {
		t.Fatal("a program with a syntax error must fail to compile")
	}
	missingArg := metadatautil.WorkflowJQProgram{Program: ".a == $undeclared"}
	if jqCompiles(t, missingArg) == nil {
		t.Fatal("a program that uses an undeclared variable must fail to compile")
	}
	runtimeOnly := metadatautil.WorkflowJQProgram{Program: ".changed_files[]"}
	if err := jqCompiles(t, runtimeOnly); err != nil {
		t.Fatalf("a runtime error on the empty fixture must not fail: %v", err)
	}
}
