// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	session := []string{"--mode=strict", "--agent-ui=cli", "--wrapper-context=1"}
	breakglass := []string{"--mode=breakglass", "--agent-ui=cli", "--wrapper-context=0"}
	join := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	tests := []struct {
		name       string
		args       []string
		pid1       bool
		wantCode   int
		wantStderr string
	}{
		{name: "help", args: []string{"--help"}, wantCode: 0},
		{name: "no-args", args: nil, wantCode: 2, wantStderr: usageMessage + "\n"},
		{name: "bad-action", args: join([]string{"run"}, session, []string{"codex"}), wantCode: 2, wantStderr: usageMessage + "\n"},
		{name: "options-out-of-order", args: []string{"check", "--agent-ui=cli", "--mode=strict", "--wrapper-context=1", "codex"}, wantCode: 2, wantStderr: usageMessage + "\n"},
		{name: "check-allows", args: join([]string{"check"}, session, []string{"codex", "exec", "hi"}), wantCode: 0},
		{name: "check-refuses", args: join([]string{"check"}, session, []string{"codex", "--yolo"}), wantCode: 2, wantStderr: "Workcell blocked unsafe Codex override: --yolo\n"},
		{name: "validate-wrong-command", args: join([]string{"validate-command"}, session, []string{"codex", "bash"}), wantCode: 2, wantStderr: "Workcell blocked non-provider command: bash (use the host launcher --allow-arbitrary-command path only for lower-assurance boundary debugging).\n"},
		{name: "validate-empty", args: join([]string{"validate-command"}, session, []string{"codex"}), wantCode: 0},
		{name: "breakglass-not-pid1", args: join([]string{"check"}, breakglass, []string{"codex", "--yolo"}), wantCode: 2, wantStderr: "Workcell blocked unsafe Codex override: --yolo\n"},
		{name: "breakglass-pid1", args: join([]string{"check"}, breakglass, []string{"codex", "--yolo"}), pid1: true, wantCode: 0},
		// Each option sets its own field: the wrapper context turns
		// breakglass off, and only AGENT_UI=gui allows codex app-server.
		{name: "breakglass-pid1-wrapper", args: []string{"check", "--mode=breakglass", "--agent-ui=cli", "--wrapper-context=1", "codex", "--yolo"}, pid1: true, wantCode: 2, wantStderr: "Workcell blocked unsafe Codex override: --yolo\n"},
		{name: "breakglass-pid1-no-wrapper", args: []string{"check", "--mode=breakglass", "--agent-ui=cli", "--wrapper-context=0", "codex", "--yolo"}, pid1: true, wantCode: 0},
		{name: "gui-app-server", args: []string{"check", "--mode=strict", "--agent-ui=gui", "--wrapper-context=0", "codex", "app-server"}, wantCode: 0},
		{name: "cli-app-server", args: []string{"check", "--mode=strict", "--agent-ui=cli", "--wrapper-context=0", "codex", "app-server"}, wantCode: 2, wantStderr: "Workcell blocked unsupported Codex CLI subcommand outside the managed GUI path: app-server\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, tt.pid1, &stdout, &stderr)
			if code != tt.wantCode || stderr.String() != tt.wantStderr {
				t.Fatalf("run(%q) = %d, stderr %q; want %d, %q", tt.args, code, stderr.String(), tt.wantCode, tt.wantStderr)
			}
		})
	}
}

// mainArgsEnv carries the newline-separated arguments of main when the test binary runs as the
// policy command.
const mainArgsEnv = "WORKCELL_PROVIDER_POLICY_TEST_MAIN_ARGS"

func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv(mainArgsEnv); ok {
		os.Args = append([]string{"workcell-provider-policy"}, strings.Split(args, "\n")...)
		main()
	}
	os.Exit(m.Run())
}

// TestMainBreakglassNeedsPID1Parent runs main as a child of this test, whose
// PID is not 1, so breakglass must not stand the policy down. Run under
// unshare, the same command is a child of PID 1 and breakglass applies.
func TestMainBreakglassNeedsPID1Parent(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := "check\n--mode=breakglass\n--agent-ui=cli\n--wrapper-context=0\ncodex\n--yolo"
	run := func(argv ...string) (int, string) {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = append(os.Environ(), mainArgsEnv+"="+args)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), stderr.String()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0, stderr.String()
	}
	if os.Getppid() == 1 {
		t.Skip("the test itself runs as a child of PID 1")
	}
	if code, stderr := run(self); code != 2 || stderr != "Workcell blocked unsafe Codex override: --yolo\n" {
		t.Fatalf("breakglass outside PID 1 = %d, %q; want 2 and the --yolo refusal", code, stderr)
	}
	if err := exec.Command("unshare", "--user", "--map-root-user", "--pid", "--fork", "true").Run(); err != nil {
		t.Skipf("no unprivileged PID namespace here: %v", err)
	}
	// bash is PID 1 of the new namespace. The trailing exit keeps bash from
	// replacing itself with the command, so the command is its child.
	if code, stderr := run("unshare", "--user", "--map-root-user", "--pid", "--fork", "bash", "-c", `"$0"; exit $?`, self); code != 0 || stderr != "" {
		t.Fatalf("breakglass under PID 1 = %d, %q; want 0", code, stderr)
	}
}
