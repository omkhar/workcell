// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package colimautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRuntimeMountsAllowsWorkspaceAndReadOnlyCache(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace := filepath.Join(tmp, "workspace")
	hostInputs := filepath.Join(home, "Library", "Caches", "colima", "workcell-host-inputs")
	shadow := filepath.Join(home, "Library", "Caches", "colima", "workcell-shadow")
	tokenHandoff := filepath.Join(home, "Library", "Caches", "colima", "workcell-token-handoff")
	for _, path := range []string{home, workspace, hostInputs, shadow, tokenHandoff} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)

	configPath := filepath.Join(tmp, "lima.yaml")
	if err := os.WriteFile(configPath, []byte("mounts:\n"+
		"  - location: "+workspace+"\n"+
		"    mountPoint: "+workspace+"\n"+
		"    writable: true\n"+
		"  - location: "+tokenHandoff+"\n"+
		"    mountPoint: "+tokenHandoff+"\n"+
		"    writable: true\n"+
		"  - location: "+hostInputs+"\n"+
		"    mountPoint: "+hostInputs+"\n"+
		"    writable: false\n"+
		"  - location: "+shadow+"\n"+
		"    mountPoint: "+shadow+"\n"+
		"    writable: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture"); err != nil {
		t.Fatalf("ValidateRuntimeMounts() error = %v", err)
	}
}

func TestValidateRuntimeMountsRequiresWorkcellCacheMounts(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace := filepath.Join(tmp, "workspace")
	tokenHandoff := filepath.Join(home, "Library", "Caches", "colima", "workcell-token-handoff")
	for _, path := range []string{home, workspace, tokenHandoff} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)

	configPath := filepath.Join(tmp, "lima.yaml")
	if err := os.WriteFile(configPath, []byte("mounts:\n"+
		"  - location: "+workspace+"\n"+
		"    writable: true\n"+
		"  - location: "+tokenHandoff+"\n"+
		"    writable: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture")
	if err == nil || !strings.Contains(err.Error(), "missing read-only Workcell cache mount") {
		t.Fatalf("ValidateRuntimeMounts() error = %v, want missing cache mount failure", err)
	}
}

func TestValidateRuntimeMountsRejectsUnexpectedWritableMount(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace := filepath.Join(tmp, "workspace")
	other := filepath.Join(tmp, "other")
	tokenHandoff := filepath.Join(home, "Library", "Caches", "colima", "workcell-token-handoff")
	for _, path := range []string{home, workspace, other, tokenHandoff} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)

	configPath := filepath.Join(tmp, "lima.yaml")
	if err := os.WriteFile(configPath, []byte("mounts:\n"+
		"  - location: "+workspace+"\n"+
		"    writable: true\n"+
		"  - location: "+tokenHandoff+"\n"+
		"    writable: true\n"+
		"  - location: "+other+"\n"+
		"    writable: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture")
	if err == nil || !strings.Contains(err.Error(), "unexpected writable host mount") {
		t.Fatalf("ValidateRuntimeMounts() error = %v, want writable mount failure", err)
	}
}

func TestValidateProfileConfigAcceptsManagedProfile(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace := filepath.Join(tmp, "workspace")
	hostInputs := filepath.Join(home, "Library", "Caches", "colima", "workcell-host-inputs")
	shadow := filepath.Join(home, "Library", "Caches", "colima", "workcell-shadow")
	tokenHandoff := filepath.Join(home, "Library", "Caches", "colima", "workcell-token-handoff")
	for _, path := range []string{home, workspace, hostInputs, shadow, tokenHandoff} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)

	configPath := filepath.Join(tmp, "colima.yaml")
	if err := os.WriteFile(configPath, []byte("vmType: vz\n"+
		"mountType: virtiofs\n"+
		"runtime: docker\n"+
		"cpu: 4\n"+
		"memory: 8\n"+
		"disk: 100\n"+
		"mounts:\n"+
		"  - location: "+workspace+"\n"+
		"    writable: true\n"+
		"  - location: "+tokenHandoff+"\n"+
		"    writable: true\n"+
		"  - location: "+hostInputs+"\n"+
		"    writable: false\n"+
		"  - location: "+shadow+"\n"+
		"    writable: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateProfileConfig(configPath, workspace, "4", "8", "100"); err != nil {
		t.Fatalf("ValidateProfileConfig() error = %v", err)
	}
}

func TestValidateProfileConfigRequiresWorkcellCacheMounts(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace := filepath.Join(tmp, "workspace")
	tokenHandoff := filepath.Join(home, "Library", "Caches", "colima", "workcell-token-handoff")
	for _, path := range []string{home, workspace, tokenHandoff} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)

	configPath := filepath.Join(tmp, "colima.yaml")
	if err := os.WriteFile(configPath, []byte("vmType: vz\n"+
		"mountType: virtiofs\n"+
		"runtime: docker\n"+
		"cpu: 4\n"+
		"memory: 8\n"+
		"disk: 100\n"+
		"mounts:\n"+
		"  - location: "+workspace+"\n"+
		"    writable: true\n"+
		"  - location: "+tokenHandoff+"\n"+
		"    writable: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateProfileConfig(configPath, workspace, "4", "8", "100")
	if err == nil || !strings.Contains(err.Error(), "missing read-only Workcell cache mount") {
		t.Fatalf("ValidateProfileConfig() error = %v, want missing cache mount failure", err)
	}
}

func TestValidateProfileConfigRejectsForwardAgent(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace := filepath.Join(tmp, "workspace")
	hostInputs := filepath.Join(home, "Library", "Caches", "colima", "workcell-host-inputs")
	shadow := filepath.Join(home, "Library", "Caches", "colima", "workcell-shadow")
	tokenHandoff := filepath.Join(home, "Library", "Caches", "colima", "workcell-token-handoff")
	for _, path := range []string{home, workspace, hostInputs, shadow, tokenHandoff} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)

	configPath := filepath.Join(tmp, "colima.yaml")
	if err := os.WriteFile(configPath, []byte("vmType: vz\n"+
		"mountType: virtiofs\n"+
		"runtime: docker\n"+
		"cpu: 4\n"+
		"memory: 8\n"+
		"disk: 100\n"+
		"forwardAgent: true\n"+
		"mounts:\n"+
		"  - location: "+workspace+"\n"+
		"    writable: true\n"+
		"  - location: "+tokenHandoff+"\n"+
		"    writable: true\n"+
		"  - location: "+hostInputs+"\n"+
		"    writable: false\n"+
		"  - location: "+shadow+"\n"+
		"    writable: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateProfileConfig(configPath, workspace, "4", "8", "100")
	if err == nil || !strings.Contains(err.Error(), "must not forward the SSH agent") {
		t.Fatalf("ValidateProfileConfig() error = %v, want forwardAgent failure", err)
	}
}
