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
	if err := os.WriteFile(configPath, []byte("mountType: virtiofs\n"+
		"mounts:\n"+
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

	if err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", "virtiofs"); err != nil {
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
	if err := os.WriteFile(configPath, []byte("mountType: virtiofs\n"+
		"mounts:\n"+
		"  - location: "+workspace+"\n"+
		"    writable: true\n"+
		"  - location: "+tokenHandoff+"\n"+
		"    writable: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", "virtiofs")
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
	if err := os.WriteFile(configPath, []byte("mountType: virtiofs\n"+
		"mounts:\n"+
		"  - location: "+workspace+"\n"+
		"    writable: true\n"+
		"  - location: "+tokenHandoff+"\n"+
		"    writable: true\n"+
		"  - location: "+other+"\n"+
		"    writable: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", "virtiofs")
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

	if err := ValidateProfileConfig(configPath, workspace, "4", "8", "100", "vz", "virtiofs"); err != nil {
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

	err := ValidateProfileConfig(configPath, workspace, "4", "8", "100", "vz", "virtiofs")
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

	err := ValidateProfileConfig(configPath, workspace, "4", "8", "100", "vz", "virtiofs")
	if err == nil || !strings.Contains(err.Error(), "must not forward the SSH agent") {
		t.Fatalf("ValidateProfileConfig() error = %v, want forwardAgent failure", err)
	}
}

// writeManagedConfig writes a managed colima.yaml or lima.yaml fixture with all
// Workcell mounts and the given VM and mount types.
func writeManagedConfig(t *testing.T, vmType, mountType string) (configPath, workspace string) {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	workspace = filepath.Join(tmp, "workspace")
	cache := filepath.Join(home, "Library", "Caches", "colima")
	hostInputs := filepath.Join(cache, "workcell-host-inputs")
	shadow := filepath.Join(cache, "workcell-shadow")
	tokenHandoff := filepath.Join(cache, "workcell-token-handoff")
	for _, path := range []string{home, workspace, hostInputs, shadow, tokenHandoff} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	configPath = filepath.Join(tmp, "config.yaml")
	if err := os.WriteFile(configPath, []byte("vmType: "+vmType+"\n"+
		"mountType: "+mountType+"\n"+
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
	return configPath, workspace
}

func TestValidateRuntimeMountsAcceptsOnlySelectedMountType(t *testing.T) {
	for _, tc := range []struct {
		configured, selected string
		wantErr              bool
	}{
		{"virtiofs", "virtiofs", false},
		{"9p", "9p", false},
		{"virtiofs", "9p", true},
		{"9p", "virtiofs", true},
		{"reverse-sshfs", "9p", true},
	} {
		configPath, workspace := writeManagedConfig(t, "qemu", tc.configured)
		err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", tc.selected)
		if tc.wantErr != (err != nil) {
			t.Fatalf("configured %s selected %s: ValidateRuntimeMounts() error = %v, wantErr %v", tc.configured, tc.selected, err, tc.wantErr)
		}
		if tc.wantErr && !strings.Contains(err.Error(), "unexpected Lima mountType") {
			t.Fatalf("configured %s selected %s: error = %v, want mountType failure", tc.configured, tc.selected, err)
		}
	}
}

func TestValidateProfileConfigAcceptsOnlySelectedVMAndMountType(t *testing.T) {
	for _, tc := range []struct {
		vmType, mountType, wantVM, wantMount, wantErr string
	}{
		{"qemu", "9p", "qemu", "9p", ""},
		{"vz", "virtiofs", "vz", "virtiofs", ""},
		{"vz", "virtiofs", "qemu", "9p", "unexpected Colima vmType"},
		{"qemu", "virtiofs", "qemu", "9p", "unexpected Colima mountType"},
		{"qemu", "9p", "vz", "virtiofs", "unexpected Colima vmType"},
	} {
		configPath, workspace := writeManagedConfig(t, tc.vmType, tc.mountType)
		err := ValidateProfileConfig(configPath, workspace, "4", "8", "100", tc.wantVM, tc.wantMount)
		if tc.wantErr == "" && err != nil {
			t.Fatalf("%s/%s: ValidateProfileConfig() error = %v", tc.vmType, tc.mountType, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Fatalf("%s/%s want %s/%s: error = %v, want %q", tc.vmType, tc.mountType, tc.wantVM, tc.wantMount, err, tc.wantErr)
		}
	}
}
