// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package colimautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// limaConfigWithExtraMount writes the lima.yaml that Colima generates for a
// managed profile: the four Workcell mounts, then one extra mount at
// location with the given writable flag. It returns the config path, the
// workspace and the home that HOME names. Each location is single-quoted, as
// a YAML encoder writes a path with these characters.
func limaConfigWithExtraMount(t *testing.T, location func(home string) string, writable bool) (string, string, string) {
	t.Helper()
	tmp := t.TempDir()
	// A hostile home name: a space, a dollar sign and backticks.
	home := filepath.Join(tmp, "home dir $x `y`")
	workspace := filepath.Join(tmp, "workspace")
	cache := CacheRoot(home)
	hostInputs := filepath.Join(cache, "workcell-host-inputs")
	shadow := filepath.Join(cache, "workcell-shadow")
	tokenHandoff := filepath.Join(cache, "workcell-token-handoff")
	for _, path := range []string{home, workspace, hostInputs, shadow, tokenHandoff, ChildColimaCacheRoot(home)} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	extra := location(home)
	flag := "false"
	if writable {
		flag = "true"
	}
	// Colima puts its own cache root first, with no mountPoint.
	content := "mountType: virtiofs\n" +
		"mounts:\n" +
		"  - location: " + q(extra) + "\n" +
		"    writable: " + flag + "\n" +
		"  - location: " + q(workspace) + "\n" +
		"    mountPoint: " + q(workspace) + "\n" +
		"    writable: true\n" +
		"  - location: " + q(tokenHandoff) + "\n" +
		"    mountPoint: " + q(tokenHandoff) + "\n" +
		"    writable: true\n" +
		"  - location: " + q(hostInputs) + "\n" +
		"    mountPoint: " + q(hostInputs) + "\n" +
		"    writable: false\n" +
		"  - location: " + q(shadow) + "\n" +
		"    mountPoint: " + q(shadow) + "\n" +
		"    writable: false\n"
	configPath := filepath.Join(tmp, "lima.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return configPath, workspace, home
}

func TestValidateRuntimeMountsAcceptsReadOnlyColimaCacheRoot(t *testing.T) {
	configPath, workspace, _ := limaConfigWithExtraMount(t, ChildColimaCacheRoot, false)
	if err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", "virtiofs"); err != nil {
		t.Fatalf("ValidateRuntimeMounts() error = %v, want the read-only Colima cache root accepted", err)
	}
}

func TestValidateRuntimeMountsRejectsWritableColimaCacheRoot(t *testing.T) {
	configPath, workspace, _ := limaConfigWithExtraMount(t, ChildColimaCacheRoot, true)
	err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", "virtiofs")
	if err == nil || !strings.Contains(err.Error(), "unexpected writable host mount") {
		t.Fatalf("ValidateRuntimeMounts() error = %v, want writable mount failure", err)
	}
}

// TestValidateRuntimeMountsRejectsColimaCacheRootNeighbours checks that only
// the exact Colima cache root passes. The staging root is the mount that the
// report shows: it holds the snapshot store and every staging root, so a
// read-only mount of it must still fail.
func TestValidateRuntimeMountsRejectsColimaCacheRootNeighbours(t *testing.T) {
	for name, location := range map[string]func(string) string{
		"staging root":   CacheRoot,
		"staging parent": func(home string) string { return filepath.Dir(CacheRoot(home)) },
		"snapshot store": func(home string) string { return filepath.Join(CacheRoot(home), "workcell-snapshots") },
		"xdg home":       ChildCacheHome,
		"workcell cache": func(home string) string { return filepath.Dir(ChildCacheHome(home)) },
		"sibling":        func(home string) string { return filepath.Join(ChildCacheHome(home), "lima") },
		"prefix sibling": func(home string) string { return ChildColimaCacheRoot(home) + "-evil" },
		"child":          func(home string) string { return filepath.Join(ChildColimaCacheRoot(home), "caches") },
		"dot-dot spelled": func(home string) string {
			return filepath.Join(ChildColimaCacheRoot(home), "caches") + "/../../colima/../../.."
		},
		"home":          func(home string) string { return home },
		"tilde staging": func(string) string { return "~/" + mustRel(CacheRoot("/h"), "/h") },
	} {
		t.Run(name, func(t *testing.T) {
			configPath, workspace, home := limaConfigWithExtraMount(t, location, false)
			if name == "child" {
				if err := os.MkdirAll(location(home), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", "virtiofs")
			if err == nil || !strings.Contains(err.Error(), "unexpected host mount") {
				t.Fatalf("ValidateRuntimeMounts() error = %v, want unexpected host mount failure", err)
			}
		})
	}
}

// TestValidateRuntimeMountsRejectsSymlinkedColimaCacheRoot checks that the
// Colima cache root cannot point the read-only mount at other host data.
func TestValidateRuntimeMountsRejectsSymlinkedColimaCacheRoot(t *testing.T) {
	for name, link := range map[string]func(string) string{
		"root":   ChildColimaCacheRoot,
		"parent": ChildCacheHome,
	} {
		t.Run(name, func(t *testing.T) {
			configPath, workspace, home := limaConfigWithExtraMount(t, ChildColimaCacheRoot, false)
			target := filepath.Join(filepath.Dir(home), "secret")
			if err := os.MkdirAll(filepath.Join(target, "colima"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(link(home)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link(home)); err != nil {
				t.Fatal(err)
			}
			err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", "virtiofs")
			if err == nil || !strings.Contains(err.Error(), "unexpected host mount") {
				t.Fatalf("ValidateRuntimeMounts() error = %v, want unexpected host mount failure", err)
			}
		})
	}
}

// TestValidateRuntimeMountsAllowsMissingColimaCacheRoot keeps the Colima
// mount optional: Colima drops it when a user mount covers it.
func TestValidateRuntimeMountsAllowsMissingColimaCacheRoot(t *testing.T) {
	configPath, workspace := writeManagedConfig(t, "vz", "virtiofs")
	if err := ValidateRuntimeMounts(configPath, workspace, "wcl-fixture", "virtiofs"); err != nil {
		t.Fatalf("ValidateRuntimeMounts() error = %v", err)
	}
}

func TestChildCacheHomeIsOutsideStagingRoot(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		staging := cacheRoot(goos, "/h")
		own := filepath.Join(childCacheHome(goos, "/h"), "colima")
		for _, pair := range [][2]string{{staging, own}, {own, staging}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err != nil {
				t.Fatal(err)
			}
			if rel == "." || !strings.HasPrefix(rel, "..") {
				t.Fatalf("%s: %s is inside %s", goos, pair[1], pair[0])
			}
		}
		// Colima drops its cache mount when a user mount is a string prefix of
		// it. No staging root may be one.
		for _, sub := range []string{"workcell-host-inputs", "workcell-shadow", "workcell-token-handoff"} {
			if strings.HasPrefix(own, filepath.Join(staging, sub)) {
				t.Fatalf("%s: staging root %s is a prefix of %s", goos, sub, own)
			}
		}
	}
}

func q(path string) string { return "'" + path + "'" }

func mustRel(path, base string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		panic(err)
	}
	return rel
}
