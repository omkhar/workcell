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
	output := FormatBundleResultForShell(result)
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
	if got := FormatBundleResultForShell(nil); got != "" {
		t.Fatalf("expected empty string for nil result, got %q", got)
	}
}
