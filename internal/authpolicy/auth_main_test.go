// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// runAuthMain wraps authMain to capture stdout/stderr separately for
// assertion. It does NOT use t.Parallel-incompatible globals.
func runAuthMain(args []string) (stdout string, stderr string, code int, errStr string) {
	var out, errBuf bytes.Buffer
	err := authMain(args, &out, &errBuf)
	code = 0
	if err != nil {
		var ec *ExitCodeError
		if errors.As(err, &ec) {
			code = ec.Code
		} else {
			code = 1
		}
		errStr = err.Error()
	}
	return out.String(), errBuf.String(), code, errStr
}

func TestAuthMainHelpFlagPrintsUsage(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"--help", "-h", "help"} {
		stdout, _, code, _ := runAuthMain([]string{arg})
		if code != 0 {
			t.Fatalf("authMain(%q) code = %d, want 0", arg, code)
		}
		if !strings.HasPrefix(stdout, "Usage: workcell auth init") {
			t.Fatalf("authMain(%q) stdout = %q", arg, stdout)
		}
	}
}

func TestAuthMainNoSubcommandFailsExitTwo(t *testing.T) {
	t.Parallel()
	stdout, stderr, code, _ := runAuthMain(nil)
	if code != 2 {
		t.Fatalf("authMain([]) code = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("authMain([]) stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage: workcell auth init") {
		t.Fatalf("authMain([]) stderr = %q", stderr)
	}
}

func TestAuthMainUnknownSubcommandFailsExitTwo(t *testing.T) {
	t.Parallel()
	_, stderr, code, _ := runAuthMain([]string{"bogus"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "Unsupported workcell auth command: bogus") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestAuthMainInitUnknownOption(t *testing.T) {
	t.Parallel()
	_, stderr, code, _ := runAuthMain([]string{"init", "--bogus", "value"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "Unsupported workcell auth init option: --bogus") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestAuthMainSetMissingAgent(t *testing.T) {
	t.Parallel()
	_, _, code, err := runAuthMain([]string{"set", "--credential", "codex_auth"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(err, "requires --agent") {
		t.Fatalf("err = %q", err)
	}
}

func TestAuthMainSetMissingCredential(t *testing.T) {
	t.Parallel()
	_, _, code, err := runAuthMain([]string{"set", "--agent", "codex"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(err, "requires --credential") {
		t.Fatalf("err = %q", err)
	}
}

func TestAuthMainSetResolverRequiresAck(t *testing.T) {
	t.Parallel()
	_, _, code, err := runAuthMain([]string{
		"set",
		"--agent", "claude",
		"--credential", "claude_auth",
		"--resolver", "claude-macos-keychain",
	})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(err, "requires --ack-host-resolver with --resolver") {
		t.Fatalf("err = %q", err)
	}
}

func TestAuthMainUnsetMissingCredential(t *testing.T) {
	t.Parallel()
	_, _, code, err := runAuthMain([]string{"unset"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(err, "requires --credential") {
		t.Fatalf("err = %q", err)
	}
}

func TestAuthMainStatusMissingPolicyFileExitTwo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.toml")
	_, _, code, errStr := runAuthMain([]string{
		"--base=" + dir,
		"status",
		"--injection-policy", missing,
		"--agent", "codex",
	})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(errStr, "Injection policy file does not exist:") {
		t.Fatalf("errStr = %q", errStr)
	}
}

func TestAuthMainStatusUnknownOption(t *testing.T) {
	t.Parallel()
	_, stderr, code, _ := runAuthMain([]string{"status", "--bogus", "x"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "Unsupported workcell auth status option: --bogus") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestAuthMainStatusWorkspaceIgnored(t *testing.T) {
	t.Parallel()
	// Without --injection-policy this falls back to the user default,
	// which the in-process Run will treat as missing. We just want to
	// observe that --workspace did not provoke an unknown-option error.
	_, stderr, code, _ := runAuthMain([]string{
		"status",
		"--workspace", "/tmp/anywhere",
	})
	// The status command exits non-zero because the default policy
	// file does not exist on a sandboxed test machine, but the failure
	// must not mention --workspace as an unknown option.
	if code == 0 {
		// In case the default policy does exist on a dev box, that is
		// still acceptable: --workspace was accepted.
		return
	}
	if strings.Contains(stderr, "Unsupported workcell auth status option: --workspace") {
		t.Fatalf("--workspace must be accepted; stderr = %q", stderr)
	}
}

func TestAuthMainConsumeBaseArg(t *testing.T) {
	t.Parallel()
	base, rest := consumeBaseArg([]string{"--base=/x", "init"})
	if base != "/x" {
		t.Fatalf("base = %q, want /x", base)
	}
	if len(rest) != 1 || rest[0] != "init" {
		t.Fatalf("rest = %v, want [init]", rest)
	}

	base, rest = consumeBaseArg([]string{"init"})
	if base != "" {
		t.Fatalf("base = %q, want empty", base)
	}
	if len(rest) != 1 || rest[0] != "init" {
		t.Fatalf("rest = %v, want [init]", rest)
	}
}

func TestAuthMainOptionValueOrDieRejectsDoubleDash(t *testing.T) {
	t.Parallel()
	if _, err := optionValueOrDie("--agent", "--credential"); err == nil {
		t.Fatal("optionValueOrDie should reject value starting with --")
	}
	if _, err := optionValueOrDie("--agent", "codex"); err != nil {
		t.Fatalf("optionValueOrDie rejected valid value: %v", err)
	}
}

func TestAuthMainRawOptionValueOrDieAllowsDoubleDash(t *testing.T) {
	t.Parallel()
	// raw accepts any non-empty value (paths starting with -- are odd
	// but historically allowed by the bash raw_option_value_or_die).
	if _, err := rawOptionValueOrDie("--policy", "./x"); err != nil {
		t.Fatalf("rawOptionValueOrDie rejected valid value: %v", err)
	}
	if _, err := rawOptionValueOrDie("--policy", ""); err == nil {
		t.Fatal("rawOptionValueOrDie should reject empty value")
	}
}

func TestAuthMainInitMissingValue(t *testing.T) {
	t.Parallel()
	_, _, code, err := runAuthMain([]string{"init", "--injection-policy"})
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(err, "Option --injection-policy requires a value.") {
		t.Fatalf("err = %q", err)
	}
}

func TestAuthMainInitEndToEnd(t *testing.T) {
	// Not parallel: relies on stable cwd-relative resolution.
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.toml")
	managed := filepath.Join(dir, "managed")
	stdout, _, code, errStr := runAuthMain([]string{
		"--base=" + dir,
		"init",
		"--injection-policy", policy,
		"--managed-root", managed,
	})
	if code != 0 {
		t.Fatalf("code = %d errStr = %q", code, errStr)
	}
	if !strings.Contains(stdout, "policy_path=") {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "managed_root=") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestAuthMainExitCodeErrorImplementsError(t *testing.T) {
	t.Parallel()
	err := &ExitCodeError{Code: 2, Message: "hello"}
	if err.Error() != "hello" {
		t.Fatalf("Error() = %q", err.Error())
	}
	var asErr *ExitCodeError
	if !errors.As(error(err), &asErr) {
		t.Fatal("errors.As failed to unwrap ExitCodeError")
	}
}

func TestAuthMainBufferHelper(t *testing.T) {
	t.Parallel()
	stdout, stderr, err := authMainBuffer([]string{"--help"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	if !strings.HasPrefix(stdout, "Usage:") {
		t.Fatalf("stdout = %q", stdout)
	}
}
