// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareBundleNoPolicyClearsState(t *testing.T) {
	realHome := t.TempDir()
	result, err := PrepareBundle(PrepareBundleOptions{
		Agent:                "codex",
		Mode:                 "strict",
		RealHome:             realHome,
		BundleParentOverride: filepath.Join(realHome, "host-inputs"),
		ProcessPID:           os.Getpid(),
	})
	if err != nil {
		t.Fatalf("PrepareBundle returned error: %v", err)
	}
	if result.InjectionBundleRoot != "" {
		t.Fatalf("expected empty bundle root, got %q", result.InjectionBundleRoot)
	}
	if result.InjectionSSHEnabled != "0" {
		t.Fatalf("expected SSH disabled marker, got %q", result.InjectionSSHEnabled)
	}
	if result.InjectionSSHConfigAssurance != "off" {
		t.Fatalf("expected SSH assurance 'off', got %q", result.InjectionSSHConfigAssurance)
	}
}

func TestPrepareBundleRejectsMissingPolicy(t *testing.T) {
	realHome := t.TempDir()
	_, err := PrepareBundle(PrepareBundleOptions{
		Agent:                "codex",
		Mode:                 "strict",
		PolicyPath:           filepath.Join(realHome, "missing-policy.toml"),
		RealHome:             realHome,
		BundleParentOverride: filepath.Join(realHome, "host-inputs"),
		ProcessPID:           os.Getpid(),
	})
	if err == nil || !strings.Contains(err.Error(), "Injection policy file does not exist") {
		t.Fatalf("expected policy-missing error, got %v", err)
	}
}

func TestPrepareBundleRejectsCredentialSourceUnderWorkspace(t *testing.T) {
	realHome := t.TempDir()
	workspace := filepath.Join(realHome, "workspace")
	policyPath := filepath.Join(realHome, "policy.toml")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(workspace, "copilot-token.txt")
	if err := os.WriteFile(credentialPath, []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("[credentials.copilot_github_token]\nsource = \"workspace/copilot-token.txt\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := PrepareBundle(PrepareBundleOptions{
		Agent:                "copilot",
		Mode:                 "strict",
		WorkspacePath:        workspace,
		PolicyPath:           policyPath,
		RealHome:             realHome,
		BundleParentOverride: filepath.Join(realHome, "host-inputs"),
		ProcessPID:           os.Getpid(),
	})
	if err == nil || !strings.Contains(err.Error(), "credentials.copilot_github_token source must be outside the mounted workspace") {
		t.Fatalf("expected workspace credential rejection, got %v", err)
	}
}

func TestPrepareBundleRejectsCredentialSourceUnderTildeWorkspace(t *testing.T) {
	realHome := t.TempDir()
	t.Setenv("HOME", realHome)
	workspace := filepath.Join(realHome, "workspace")
	policyPath := filepath.Join(realHome, "policy.toml")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(workspace, "copilot-token.txt")
	if err := os.WriteFile(credentialPath, []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("[credentials.copilot_github_token]\nsource = \"workspace/copilot-token.txt\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := PrepareBundle(PrepareBundleOptions{
		Agent:                "copilot",
		Mode:                 "strict",
		WorkspacePath:        "~/workspace",
		PolicyPath:           policyPath,
		RealHome:             realHome,
		BundleParentOverride: filepath.Join(realHome, "host-inputs"),
		ProcessPID:           os.Getpid(),
	})
	if err == nil || !strings.Contains(err.Error(), "credentials.copilot_github_token source must be outside the mounted workspace") {
		t.Fatalf("expected workspace credential rejection, got %v", err)
	}
}

func TestPrepareBundleRejectsMissingLaunchWorkspaceForCredentialValidation(t *testing.T) {
	realHome := t.TempDir()
	policyPath := filepath.Join(realHome, "policy.toml")
	credentialPath := filepath.Join(realHome, "copilot-token.txt")
	if err := os.WriteFile(credentialPath, []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("[credentials.copilot_github_token]\nsource = \"copilot-token.txt\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := PrepareBundle(PrepareBundleOptions{
		Agent:                "copilot",
		Mode:                 "strict",
		WorkspacePath:        filepath.Join(realHome, "missing-workspace"),
		PolicyPath:           policyPath,
		RealHome:             realHome,
		BundleParentOverride: filepath.Join(realHome, "host-inputs"),
		ProcessPID:           os.Getpid(),
	})
	if err == nil || !strings.Contains(err.Error(), "stat workspace for credential-source validation") {
		t.Fatalf("expected missing workspace rejection, got %v", err)
	}
}

func TestPrepareBundleAuthStatusAllowsMissingWorkspace(t *testing.T) {
	realHome := t.TempDir()
	policyPath := filepath.Join(realHome, "policy.toml")
	credentialPath := filepath.Join(realHome, "copilot-token.txt")
	if err := os.WriteFile(credentialPath, []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte("[credentials.copilot_github_token]\nsource = \"copilot-token.txt\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := PrepareBundle(PrepareBundleOptions{
		Agent:                "copilot",
		Mode:                 "strict",
		WorkspacePath:        filepath.Join(realHome, "missing-workspace"),
		PolicyPath:           policyPath,
		AuthStatus:           true,
		RealHome:             realHome,
		BundleParentOverride: filepath.Join(realHome, "host-inputs"),
		ProcessPID:           os.Getpid(),
	})
	if err != nil {
		t.Fatalf("expected auth-status to allow missing workspace, got %v", err)
	}
}

func TestPrepareBundleRequiresAgentAndMode(t *testing.T) {
	if _, err := PrepareBundle(PrepareBundleOptions{Agent: "", Mode: "strict", RealHome: "/tmp"}); err == nil {
		t.Fatalf("expected error for empty agent")
	}
	if _, err := PrepareBundle(PrepareBundleOptions{Agent: "codex", Mode: "", RealHome: "/tmp"}); err == nil {
		t.Fatalf("expected error for empty mode")
	}
}

func TestPrepareBundleRequiresRealHome(t *testing.T) {
	if _, err := PrepareBundle(PrepareBundleOptions{Agent: "codex", Mode: "strict"}); err == nil {
		t.Fatalf("expected error for empty real home")
	}
}

func TestDefaultInjectionPolicyPath(t *testing.T) {
	got := DefaultInjectionPolicyPath("/home/u")
	want := "/home/u/.config/workcell/injection-policy.toml"
	if got != want {
		t.Fatalf("DefaultInjectionPolicyPath = %q, want %q", got, want)
	}
}

func TestDefaultInjectionBundleParent(t *testing.T) {
	got := DefaultInjectionBundleParent("/home/u")
	want := "/home/u/Library/Caches/colima/workcell-host-inputs"
	if got != want {
		t.Fatalf("DefaultInjectionBundleParent = %q, want %q", got, want)
	}
}

func TestFormatBundleResultForShellEmitsExpectedKeys(t *testing.T) {
	result := &PrepareBundleResult{
		InjectionBundleRoot:                 "/tmp/bundle",
		DirectMountSpecPath:                 "/tmp/bundle.mounts.json",
		DirectSourceMounts:                  []string{"-v", "/a:/b:ro", "-v", "/c:/d:ro"},
		InjectionPolicySHA256:               "abc123",
		InjectionCredentialKeys:             "claude_oauth,codex_auth",
		InjectionCredentialInputKinds:       "claude_oauth:keychain",
		InjectionCredentialResolvers:        "claude_oauth:keychain_export",
		InjectionCredentialMaterialization:  "claude_oauth:direct_mount",
		InjectionCredentialResolutionStates: "claude_oauth:ready",
		InjectionProviderAuthReadyStates:    "anthropic:ready",
		InjectionSharedAuthReadyStates:      "session-jwt:ready",
		InjectionExtraEndpoints:             "api.example.com:443",
		InjectionSSHEnabled:                 "1",
		InjectionSSHConfigAssurance:         "on",
		InjectionSecretCopyTargets:          "claude_oauth",
	}
	output, err := FormatBundleResultForShell(result)
	if err != nil {
		t.Fatalf("FormatBundleResultForShell err = %v, want nil", err)
	}
	for _, expected := range []string{
		"INJECTION_BUNDLE_ROOT=/tmp/bundle",
		"DIRECT_MOUNT_SPEC_PATH=/tmp/bundle.mounts.json",
		"DIRECT_SOURCE_MOUNTS_COUNT=4",
		"DIRECT_SOURCE_MOUNTS_0=-v",
		"DIRECT_SOURCE_MOUNTS_1=/a:/b:ro",
		"DIRECT_SOURCE_MOUNTS_2=-v",
		"DIRECT_SOURCE_MOUNTS_3=/c:/d:ro",
		"INJECTION_POLICY_SHA256=abc123",
		"INJECTION_CREDENTIAL_KEYS=claude_oauth,codex_auth",
		"INJECTION_SSH_ENABLED=1",
		"INJECTION_SSH_CONFIG_ASSURANCE=on",
		"INJECTION_SECRET_COPY_TARGETS=claude_oauth",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q; output:\n%s", expected, output)
		}
	}
}

func TestFormatBundleResultForShellNilSafe(t *testing.T) {
	got, err := FormatBundleResultForShell(nil)
	if err != nil {
		t.Fatalf("FormatBundleResultForShell(nil) err = %v, want nil", err)
	}
	if got != "" {
		t.Fatalf("expected empty string for nil result, got %q", got)
	}
}

// TestFormatBundleResultForShellRejectsControlChars pins the
// fail-closed contract: when an upstream extractor smuggles a newline
// into a field value, FormatBundleResultForShell must return an error
// and emit NOTHING.  A partial KEY=VALUE plan would let the bash shim
// re-import a forged second record (e.g. `INJECTION_SSH_ENABLED=1`)
// and confuse the launcher; the shellproto.WriteFields contract
// already validates each value at the emit boundary, so this test
// only needs to assert that we surface that error to the caller
// instead of dropping it on the floor.
func TestFormatBundleResultForShellRejectsControlChars(t *testing.T) {
	t.Parallel()

	result := &PrepareBundleResult{
		InjectionBundleRoot: "/tmp/bundle\nINJECTION_SSH_ENABLED=1",
		DirectMountSpecPath: "/tmp/bundle.mounts.json",
	}
	got, err := FormatBundleResultForShell(result)
	if err == nil {
		t.Fatalf("FormatBundleResultForShell err = nil, want error for newline-containing field; got output %q", got)
	}
	if got != "" {
		t.Fatalf("FormatBundleResultForShell output = %q, want empty on error", got)
	}
}

// TestInstallSyntheticProbeEnvRestoresHomeOnPartialFailure pins the
// HOME-leak fix.  When the synthetic Claude branch fails after the
// synthetic Codex branch has already pointed HOME at the bundle's
// codex-home, the returned cleanup must restore the caller's HOME.
// The previous code returned a no-op shadow on the error path and
// the calling Go process inherited the test-only HOME.
func TestInstallSyntheticProbeEnvRestoresHomeOnPartialFailure(t *testing.T) {
	// t.Setenv saves and restores HOME at the testing harness level so a
	// regression in this test cannot leak HOME beyond the test boundary.
	originalHome := "/tmp/installSyntheticProbeEnv-original"
	t.Setenv("HOME", originalHome)

	bundleRoot := t.TempDir()
	// Make the synthetic Claude export path collide with an existing
	// directory at the same name so writeFile0600's os.OpenFile call
	// fails: writeFile0600 first MkdirAll's the parent (the bundle root,
	// already a directory), then OpenFile the leaf, which here is a
	// pre-created directory so OpenFile returns EISDIR.
	syntheticClaudePath := filepath.Join(bundleRoot, "self-staging-probe-claude-export.json")
	if err := os.MkdirAll(syntheticClaudePath, 0o700); err != nil {
		t.Fatalf("seed collision directory: %v", err)
	}

	cleanup, err := installSyntheticProbeEnv(bundleRoot, true, true)
	if cleanup == nil {
		t.Fatal("installSyntheticProbeEnv returned nil cleanup; callers cannot defer it")
	}
	defer cleanup()
	if err == nil {
		t.Fatal("installSyntheticProbeEnv did not surface the synthetic-claude write failure")
	}
	// Before defer fires, HOME is still pointing at the synthetic codex
	// home — confirm so we can be sure the post-cleanup assertion is
	// meaningful.
	if got := os.Getenv("HOME"); got == originalHome {
		t.Fatalf("HOME was unexpectedly already restored before cleanup ran: got %q", got)
	}
	// Now run cleanup explicitly so we can assert restoration; defer
	// still runs after the test but is a safety net.
	cleanup()
	if got := os.Getenv("HOME"); got != originalHome {
		t.Fatalf("installSyntheticProbeEnv leaked HOME on partial failure: got %q, want %q", got, originalHome)
	}
}

// TestInstallSyntheticProbeEnvUnsetsHomeWhenOriginallyUnset pins the
// sibling case to TestInstallSyntheticProbeEnvRestoresHomeOnPartialFailure:
// when HOME is unset at entry, the synthetic-codex branch sets HOME, and
// cleanup MUST unset it again so the calling process does not inherit a
// HOME that points at a path the bundle parent removal will then delete.
func TestInstallSyntheticProbeEnvUnsetsHomeWhenOriginallyUnset(t *testing.T) {
	// Capture and restore HOME at the test boundary so we can clear it
	// without leaking the unset state to sibling tests.
	originalHome, hadOriginalHome := os.LookupEnv("HOME")
	t.Cleanup(func() {
		if hadOriginalHome {
			_ = os.Setenv("HOME", originalHome)
		} else {
			_ = os.Unsetenv("HOME")
		}
	})
	if err := os.Unsetenv("HOME"); err != nil {
		t.Fatalf("unset HOME: %v", err)
	}

	bundleRoot := t.TempDir()
	cleanup, err := installSyntheticProbeEnv(bundleRoot, true, false)
	if cleanup == nil {
		t.Fatal("installSyntheticProbeEnv returned nil cleanup")
	}
	if err != nil {
		t.Fatalf("installSyntheticProbeEnv unexpected error: %v", err)
	}
	if _, ok := os.LookupEnv("HOME"); !ok {
		t.Fatal("HOME was not set by the synthetic-codex branch before cleanup ran; the test cannot detect a leak")
	}
	cleanup()
	if value, ok := os.LookupEnv("HOME"); ok {
		t.Fatalf("installSyntheticProbeEnv leaked HOME after cleanup when originally unset: got %q, want unset", value)
	}
}
