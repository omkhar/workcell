// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalPolicy is the smallest entrypoint policy that loadPolicyBundle
// accepts: a version stanza plus an empty credentials table.  The
// parser allows only version/includes/documents/ssh/copies/credentials
// at the root, so we keep the fixture intentionally narrow.
const minimalPolicy = `version = 1

[credentials]
`

func writeMinimalPolicy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "injection-policy.toml")
	if err := os.WriteFile(path, []byte(minimalPolicy), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestPolicyMainNoArgsIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runPolicyMain(nil, &stdout, &stderr)
	if !IsPolicyMainUsageError(err) {
		t.Fatalf("err = %v, want usageError", err)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("stderr missing usage text: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout wrote on empty args: %q", stdout.String())
	}
}

func TestPolicyMainHelpSubcommands(t *testing.T) {
	for _, flag := range []string{"help", "-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := runPolicyMain([]string{flag}, &stdout, &stderr); err != nil {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(stdout.String(), "workcell policy show") {
				t.Errorf("stdout missing usage: %q", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr unexpectedly populated: %q", stderr.String())
			}
		})
	}
}

func TestPolicyMainUnknownSubcommandIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runPolicyMain([]string{"bogus"}, &stdout, &stderr)
	if !IsPolicyMainUsageError(err) {
		t.Fatalf("err = %v, want usageError", err)
	}
	if !strings.Contains(stderr.String(), "Unsupported workcell policy command: bogus") {
		t.Errorf("stderr missing rejection: %q", stderr.String())
	}
}

func TestPolicyMainUnknownOptionIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runPolicyMain([]string{"show", "--bogus"}, &stdout, &stderr)
	if !IsPolicyMainUsageError(err) {
		t.Fatalf("err = %v, want usageError", err)
	}
	if !strings.Contains(stderr.String(), "Unsupported workcell policy show option: --bogus") {
		t.Errorf("stderr missing rejection: %q", stderr.String())
	}
}

func TestPolicyMainInjectionPolicyRequiresValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runPolicyMain([]string{"show", "--injection-policy"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("err = nil, want value-required failure")
	}
	if !strings.Contains(err.Error(), "--injection-policy requires a value") {
		t.Fatalf("err = %v, want value-required message", err)
	}
}

func TestPolicyMainInjectionPolicyRejectsEmptyValue(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runPolicyMain([]string{"show", "--injection-policy", ""}, &stdout, &stderr)
	if err == nil {
		t.Fatal("err = nil, want value-required failure")
	}
	if !strings.Contains(err.Error(), "Option --injection-policy requires a value") {
		t.Fatalf("err = %v, want value-required message", err)
	}
}

func TestPolicyMainSubcommandHelpFlagPrintsUsage(t *testing.T) {
	for _, sub := range []string{"show", "validate", "diff"} {
		for _, flag := range []string{"-h", "--help"} {
			t.Run(sub+"/"+flag, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				if err := runPolicyMain([]string{sub, flag}, &stdout, &stderr); err != nil {
					t.Fatalf("err = %v", err)
				}
				if !strings.Contains(stdout.String(), "workcell policy show") {
					t.Errorf("stdout missing usage: %q", stdout.String())
				}
				if stderr.Len() != 0 {
					t.Errorf("stderr unexpectedly populated: %q", stderr.String())
				}
			})
		}
	}
}

func TestPolicyMainShowRendersPolicy(t *testing.T) {
	path := writeMinimalPolicy(t)
	var stdout, stderr bytes.Buffer
	if err := runPolicyMain([]string{"show", "--injection-policy", path}, &stdout, &stderr); err != nil {
		t.Fatalf("err = %v stderr=%q", err, stderr.String())
	}
	// commandShow renders the merged effective policy via
	// renderPolicyTOML; for a minimal policy that's just the version
	// header, which we assert on to confirm we hit the show path
	// rather than failing earlier.
	if !strings.Contains(stdout.String(), "version = 1") {
		t.Errorf("show output missing version header: %q", stdout.String())
	}
}

func TestPolicyMainResolvesRelativePolicyFromBase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policy.toml"), []byte(minimalPolicy), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := runPolicyMain([]string{"--base=" + dir, "show", "--injection-policy", "policy.toml"}, &stdout, &stderr); err != nil {
		t.Fatalf("err = %v stderr=%q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "version = 1") {
		t.Errorf("show output missing version header: %q", stdout.String())
	}
}

func TestPolicyMainValidateReportsPolicyValid(t *testing.T) {
	path := writeMinimalPolicy(t)
	var stdout, stderr bytes.Buffer
	if err := runPolicyMain([]string{"validate", "--injection-policy", path}, &stdout, &stderr); err != nil {
		t.Fatalf("err = %v stderr=%q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "policy_valid=1") {
		t.Errorf("validate output missing policy_valid=1: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "resolver_readiness=not-applicable") {
		t.Errorf("validate output missing resolver_readiness: %q", stdout.String())
	}
}

func TestPolicyMainValidateRejectsUnsupportedDocumentKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "injection-policy.toml")
	if err := os.WriteFile(path, []byte(`version = 1

[documents]
copilot = "copilot.md"
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := runPolicyMain([]string{"validate", "--injection-policy", path}, &stdout, &stderr)
	if err == nil {
		t.Fatal("validate unexpectedly accepted documents.copilot")
	}
	if !strings.Contains(err.Error(), "documents contains unsupported keys: copilot") {
		t.Fatalf("err = %v, want unsupported documents.copilot failure", err)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty output on validation failure", stdout.String())
	}
}

func TestPolicyMainShowRendersSupportedDocumentKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "injection-policy.toml")
	if err := os.WriteFile(path, []byte(`version = 1

[documents]
common = "common.md"
codex = "AGENTS.md"
claude = "CLAUDE.md"
gemini = "GEMINI.md"
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := runPolicyMain([]string{"show", "--injection-policy", path}, &stdout, &stderr); err != nil {
		t.Fatalf("err = %v stderr=%q", err, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		`claude = "CLAUDE.md"`,
		`codex = "AGENTS.md"`,
		`common = "common.md"`,
		`gemini = "GEMINI.md"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("show output missing %s: %q", want, output)
		}
	}
	if strings.Contains(output, "copilot") {
		t.Fatalf("show output unexpectedly rendered unsupported copilot document key: %q", output)
	}
}

func TestPolicyMainDiffReportsClean(t *testing.T) {
	path := writeMinimalPolicy(t)
	var stdout, stderr bytes.Buffer
	if err := runPolicyMain([]string{"diff", "--injection-policy", path}, &stdout, &stderr); err != nil {
		t.Fatalf("err = %v stderr=%q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "diff_status=") {
		t.Errorf("diff output missing diff_status: %q", stdout.String())
	}
}

func TestPolicyMainShowMissingPolicyReturnsError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.toml")
	var stdout, stderr bytes.Buffer
	err := runPolicyMain([]string{"show", "--injection-policy", missing}, &stdout, &stderr)
	if err == nil {
		t.Fatal("err = nil, want load failure for missing policy")
	}
	if IsPolicyMainUsageError(err) {
		t.Fatalf("err = %v, want runtime (not usage) error", err)
	}
}

func TestParsePolicyMainArgsDefaultsToInjectionPolicyPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	var stdout, stderr bytes.Buffer
	got, helpRequested, err := parsePolicyMainArgs("show", nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if helpRequested {
		t.Fatal("helpRequested = true, want false")
	}
	want := filepath.Join(tmp, ".config", "workcell", "injection-policy.toml")
	// CanonicalizePath/RealHome resolves the HOME dir; compare by
	// suffix so symlinked tmp dirs (e.g. macOS /private/var) still pass.
	if !strings.HasSuffix(got, filepath.Join(".config", "workcell", "injection-policy.toml")) {
		t.Fatalf("policyPath = %q, want suffix .config/workcell/injection-policy.toml (sample want=%q)", got, want)
	}
}

func TestParsePolicyMainArgsAcceptsExplicitPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got, helpRequested, err := parsePolicyMainArgs("validate", []string{"--injection-policy", "/etc/policy.toml"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if helpRequested {
		t.Fatal("helpRequested = true, want false")
	}
	if got != "/etc/policy.toml" {
		t.Fatalf("policyPath = %q, want /etc/policy.toml", got)
	}
}

func TestIsPolicyMainUsageError(t *testing.T) {
	t.Parallel()
	if !IsPolicyMainUsageError(usageError{}) {
		t.Error("IsPolicyMainUsageError(usageError{}) = false, want true")
	}
	if IsPolicyMainUsageError(nil) {
		t.Error("IsPolicyMainUsageError(nil) = true, want false")
	}
	if IsPolicyMainUsageError(os.ErrNotExist) {
		t.Error("IsPolicyMainUsageError(ErrNotExist) = true, want false")
	}
}
