// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRequireDirectMountRemovesSourceAndReturnsEntry(t *testing.T) {
	entry := map[string]any{
		"source":     "/host/auth.json",
		"mount_path": "/opt/workcell/host-inputs/credentials/codex-auth.json",
	}

	directMount, err := RequireDirectMount(entry, "credentials.codex_auth")
	if err != nil {
		t.Fatalf("RequireDirectMount returned error: %v", err)
	}
	if _, ok := entry["source"]; ok {
		t.Fatalf("RequireDirectMount did not remove source from entry")
	}
	if directMount.Source != "/host/auth.json" {
		t.Fatalf("unexpected source: %q", directMount.Source)
	}
	if directMount.MountPath != "/opt/workcell/host-inputs/credentials/codex-auth.json" {
		t.Fatalf("unexpected mount path: %q", directMount.MountPath)
	}
}

func TestRequireDirectMountRejectsMissingMountPath(t *testing.T) {
	_, err := RequireDirectMount(map[string]any{"source": "/host/auth.json"}, "credentials.codex_auth")
	if err == nil {
		t.Fatalf("RequireDirectMount should have failed")
	}
	if got := err.Error(); got != "credentials.codex_auth is missing its direct mount path" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestRequireDirectMountRejectsMissingSource(t *testing.T) {
	_, err := RequireDirectMount(
		map[string]any{
			"mount_path": "/opt/workcell/host-inputs/credentials/codex-auth.json",
		},
		"credentials.codex_auth",
	)
	if err == nil {
		t.Fatalf("RequireDirectMount should have failed")
	}
	if got := err.Error(); got != "credentials.codex_auth is missing its host source path" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestRunExtractDirectMountsMutatesManifestAndWritesSortedMounts(t *testing.T) {
	manifestFixture := map[string]any{
		"credentials": map[string]any{
			"codex_auth": map[string]any{
				"source":     "/host/auth.json",
				"mount_path": "/opt/workcell/host-inputs/credentials/codex-auth.json",
			},
		},
		"copies": []any{
			map[string]any{
				"source": map[string]any{
					"source":     "/host/secret.txt",
					"mount_path": "/opt/workcell/host-inputs/copies/0",
				},
				"target": "/state/agent-home/.config/workcell/token.txt",
			},
		},
		"ssh": map[string]any{
			"config": map[string]any{
				"source":     "/host/ssh-config",
				"mount_path": "/opt/workcell/host-inputs/ssh/config",
			},
			"identities": []any{
				map[string]any{
					"source":      "/host/id_a",
					"mount_path":  "/opt/workcell/host-inputs/ssh/identity-0",
					"target_name": "id_a",
				},
				map[string]any{
					"source":      "/host/id_b",
					"target_name": "id_b",
					"comment":     "ignored because no mount_path",
				},
			},
		},
	}

	gotManifest, gotMounts := runGoExtractDirectMounts(t, manifestFixture)

	var manifest map[string]any
	if err := json.Unmarshal(gotManifest, &manifest); err != nil {
		t.Fatalf("json.Unmarshal manifest: %v", err)
	}
	credentials, ok := manifest["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected credentials shape: %#v", manifest["credentials"])
	}
	codexAuth, ok := credentials["codex_auth"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected codex_auth shape: %#v", credentials["codex_auth"])
	}
	if _, ok := codexAuth["source"]; ok {
		t.Fatalf("credentials.codex_auth still contains source: %#v", codexAuth)
	}
	if got := codexAuth["mount_path"]; got != "/opt/workcell/host-inputs/credentials/codex-auth.json" {
		t.Fatalf("credentials.codex_auth.mount_path = %v", got)
	}

	copies, ok := manifest["copies"].([]any)
	if !ok || len(copies) != 1 {
		t.Fatalf("unexpected copies shape: %#v", manifest["copies"])
	}
	copyEntry, ok := copies[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected copy entry: %#v", copies[0])
	}
	copySource, ok := copyEntry["source"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected copy source shape: %#v", copyEntry["source"])
	}
	if _, ok := copySource["source"]; ok {
		t.Fatalf("copies[0].source still contains source: %#v", copySource)
	}
	if got := copySource["mount_path"]; got != "/opt/workcell/host-inputs/copies/0" {
		t.Fatalf("copies[0].source.mount_path = %v", got)
	}

	ssh, ok := manifest["ssh"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected ssh shape: %#v", manifest["ssh"])
	}
	sshConfig, ok := ssh["config"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected ssh.config shape: %#v", ssh["config"])
	}
	if _, ok := sshConfig["source"]; ok {
		t.Fatalf("ssh.config still contains source: %#v", sshConfig)
	}
	if got := sshConfig["mount_path"]; got != "/opt/workcell/host-inputs/ssh/config" {
		t.Fatalf("ssh.config.mount_path = %v", got)
	}
	identities, ok := ssh["identities"].([]any)
	if !ok || len(identities) != 2 {
		t.Fatalf("unexpected ssh.identities shape: %#v", ssh["identities"])
	}
	firstIdentity, ok := identities[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected first identity: %#v", identities[0])
	}
	if _, ok := firstIdentity["source"]; ok {
		t.Fatalf("ssh.identities[0] still contains source: %#v", firstIdentity)
	}
	if got := firstIdentity["mount_path"]; got != "/opt/workcell/host-inputs/ssh/identity-0" {
		t.Fatalf("ssh.identities[0].mount_path = %v", got)
	}
	secondIdentity, ok := identities[1].(map[string]any)
	if !ok {
		t.Fatalf("unexpected second identity: %#v", identities[1])
	}
	if got := secondIdentity["source"]; got != "/host/id_b" {
		t.Fatalf("ssh.identities[1].source = %v", got)
	}

	var mounts []DirectMount
	if err := json.Unmarshal(gotMounts, &mounts); err != nil {
		t.Fatalf("json.Unmarshal mounts: %v", err)
	}
	wantMounts := []DirectMount{
		{Source: "/host/secret.txt", MountPath: "/opt/workcell/host-inputs/copies/0"},
		{Source: "/host/auth.json", MountPath: "/opt/workcell/host-inputs/credentials/codex-auth.json"},
		{Source: "/host/ssh-config", MountPath: "/opt/workcell/host-inputs/ssh/config"},
		{Source: "/host/id_a", MountPath: "/opt/workcell/host-inputs/ssh/identity-0"},
	}
	if len(mounts) != len(wantMounts) {
		t.Fatalf("unexpected mount count: got %d want %d\n%#v", len(mounts), len(wantMounts), mounts)
	}
	for i := range wantMounts {
		if mounts[i] != wantMounts[i] {
			t.Fatalf("mount %d mismatch: got %#v want %#v", i, mounts[i], wantMounts[i])
		}
	}
}

func TestRunExtractDirectMountsWritesMode0600(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	mountSpecPath := filepath.Join(root, "mounts.json")

	if err := os.WriteFile(manifestPath, []byte(`{"copies":[]}`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := RunExtractDirectMounts(manifestPath, mountSpecPath); err != nil {
		t.Fatalf("RunExtractDirectMounts returned error: %v", err)
	}

	assertFileMode(t, manifestPath, 0o600)
	assertFileMode(t, mountSpecPath, 0o600)
}

func TestRunExtractDirectMountsAtomicallyReplacesSwappedManifestLeaf(t *testing.T) {
	fixture := map[string]any{"copies": []any{}}
	expectedRoot := t.TempDir()
	expectedManifest := filepath.Join(expectedRoot, "manifest.json")
	expectedMounts := filepath.Join(expectedRoot, "mounts.json")
	writeFixtureManifest(t, expectedManifest, fixture)
	if err := RunExtractDirectMounts(expectedManifest, expectedMounts); err != nil {
		t.Fatalf("prepare expected output: %v", err)
	}
	wantManifest := readFile(t, expectedManifest)
	wantMounts := readFile(t, expectedMounts)

	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	mountSpecPath := filepath.Join(root, "mounts.json")
	writeFixtureManifest(t, manifestPath, fixture)
	outside := filepath.Join(root, "outside.json")
	originalOutside := []byte(`{"outside":true}` + "\n")
	if err := os.WriteFile(outside, originalOutside, 0o600); err != nil {
		t.Fatal(err)
	}

	err := runExtractDirectMounts(manifestPath, mountSpecPath, func() error {
		if err := os.Rename(manifestPath, manifestPath+".original"); err != nil {
			return err
		}
		return os.Symlink(filepath.Base(outside), manifestPath)
	})
	if err != nil {
		t.Fatalf("RunExtractDirectMounts after leaf swap: %v", err)
	}
	if got := readFile(t, manifestPath); !bytes.Equal(got, wantManifest) {
		t.Fatalf("manifest output changed after leaf swap:\n got %q\nwant %q", got, wantManifest)
	}
	if got := readFile(t, mountSpecPath); !bytes.Equal(got, wantMounts) {
		t.Fatalf("mount output changed after leaf swap:\n got %q\nwant %q", got, wantMounts)
	}
	if data := readFile(t, outside); !bytes.Equal(data, originalOutside) {
		t.Fatalf("outside symlink target changed: %q", data)
	}
	if info, err := os.Lstat(manifestPath); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("swapped manifest leaf was not atomically replaced: %v, %v", info, err)
	}
	assertFileMode(t, manifestPath, 0o600)
	assertFileMode(t, mountSpecPath, 0o600)
}

func TestRunExtractDirectMountsAtomicallyReplacesMountSpecSymlink(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	writeFixtureManifest(t, manifestPath, map[string]any{"copies": []any{}})

	outside := filepath.Join(root, "outside-mounts.json")
	originalOutside := []byte(`{"outside":true}` + "\n")
	if err := os.WriteFile(outside, originalOutside, 0o600); err != nil {
		t.Fatal(err)
	}
	mountSpecPath := filepath.Join(root, "mounts.json")
	if err := os.Symlink(filepath.Base(outside), mountSpecPath); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}

	if err := RunExtractDirectMounts(manifestPath, mountSpecPath); err != nil {
		t.Fatalf("RunExtractDirectMounts with symlinked mount output: %v", err)
	}
	if data := readFile(t, outside); !bytes.Equal(data, originalOutside) {
		t.Fatalf("outside mount-spec target changed: %q", data)
	}
	if info, err := os.Lstat(mountSpecPath); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("mount-spec leaf was not atomically replaced: %v, %v", info, err)
	}
	if got := readFile(t, mountSpecPath); !bytes.Equal(bytes.TrimSpace(got), []byte("null")) {
		t.Fatalf("mount specification = %q, want preserved JSON null", got)
	}
	assertFileMode(t, mountSpecPath, 0o600)
}

func TestRunExtractDirectMountsRejectsSymlinkedMountSpecParent(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	writeFixtureManifest(t, manifestPath, map[string]any{"copies": []any{}})

	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	mountSpecTarget := filepath.Join(realParent, "mounts.json")
	originalTarget := []byte(`{"outside":true}` + "\n")
	if err := os.WriteFile(mountSpecTarget, originalTarget, 0o600); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(filepath.Base(realParent), linkedParent); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}

	if err := RunExtractDirectMounts(manifestPath, filepath.Join(linkedParent, "mounts.json")); err == nil {
		t.Fatal("RunExtractDirectMounts accepted a symlinked mount-spec parent")
	}
	if data := readFile(t, mountSpecTarget); !bytes.Equal(data, originalTarget) {
		t.Fatalf("symlinked mount-spec parent changed outside target: %q", data)
	}
}

func TestRunExtractDirectMountsLeavesPlainCopySourcesInline(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	mountSpecPath := filepath.Join(root, "mounts.json")

	writeFixtureManifest(t, manifestPath, map[string]any{
		"copies": []any{
			map[string]any{
				"source": "copies/0",
				"target": "/state/injected/public.txt",
			},
		},
	})

	if err := RunExtractDirectMounts(manifestPath, mountSpecPath); err != nil {
		t.Fatalf("RunExtractDirectMounts returned error: %v", err)
	}

	var manifest map[string]any
	if err := json.Unmarshal(readFile(t, manifestPath), &manifest); err != nil {
		t.Fatalf("json.Unmarshal manifest: %v", err)
	}
	copies, ok := manifest["copies"].([]any)
	if !ok || len(copies) != 1 {
		t.Fatalf("unexpected manifest copies: %#v", manifest["copies"])
	}
	entry, ok := copies[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected copy entry: %#v", copies[0])
	}
	if got := entry["source"]; got != "copies/0" {
		t.Fatalf("source mutated unexpectedly: %v", got)
	}

	var mounts []DirectMount
	if err := json.Unmarshal(readFile(t, mountSpecPath), &mounts); err != nil {
		t.Fatalf("json.Unmarshal mounts: %v", err)
	}
	if len(mounts) != 0 {
		t.Fatalf("expected no direct mounts, got %#v", mounts)
	}
}

func TestRunExtractDirectMountsRejectsUnsafeManifestPaths(t *testing.T) {
	root := t.TempDir()
	original := []byte(`{"copies":[]}` + "\n")
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	leafLink := filepath.Join(root, "leaf-link.json")
	if err := os.Symlink(filepath.Base(target), leafLink); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if err := RunExtractDirectMounts(leafLink, filepath.Join(root, "mounts.json")); err == nil {
		t.Fatal("RunExtractDirectMounts accepted a leaf symlink")
	}
	if data, err := os.ReadFile(target); err != nil || !bytes.Equal(data, original) {
		t.Fatalf("leaf symlink changed target manifest: %q, %v", data, err)
	}

	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	parentTarget := filepath.Join(realParent, "manifest.json")
	if err := os.WriteFile(parentTarget, original, 0o600); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(filepath.Base(realParent), linkedParent); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if err := RunExtractDirectMounts(filepath.Join(linkedParent, "manifest.json"), filepath.Join(root, "mounts-parent.json")); err == nil {
		t.Fatal("RunExtractDirectMounts accepted a symlinked parent")
	}
	if data, err := os.ReadFile(parentTarget); err != nil || !bytes.Equal(data, original) {
		t.Fatalf("parent symlink changed target manifest: %q, %v", data, err)
	}

	fifo := filepath.Join(root, "manifest.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("unix.Mkfifo unavailable: %v", err)
	}
	if err := RunExtractDirectMounts(fifo, filepath.Join(root, "mounts-fifo.json")); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("RunExtractDirectMounts FIFO error = %v, want non-regular rejection", err)
	}
}

func TestRunExtractDirectMountsManifestByteLimit(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	mountSpecPath := filepath.Join(root, "mounts.json")
	exact := append([]byte(`{"copies":[]}`), bytes.Repeat([]byte{' '}, int(maxInjectionManifestBytes-int64(len(`{"copies":[]}`))))...)
	if err := os.WriteFile(manifestPath, exact, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunExtractDirectMounts(manifestPath, mountSpecPath); err != nil {
		t.Fatalf("RunExtractDirectMounts exact limit: %v", err)
	}
	if got := readFile(t, mountSpecPath); string(got) != "null\n" {
		t.Fatalf("exact-limit mount specification = %q, want null", got)
	}

	overLimit := append(exact, ' ')
	if err := os.WriteFile(manifestPath, overLimit, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(mountSpecPath); err != nil {
		t.Fatal(err)
	}
	if err := RunExtractDirectMounts(manifestPath, mountSpecPath); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("RunExtractDirectMounts over-limit error = %v, want byte-limit rejection", err)
	}
	if got := readFile(t, manifestPath); !bytes.Equal(got, overLimit) {
		t.Fatal("RunExtractDirectMounts changed an over-limit manifest")
	}
	if _, err := os.Stat(mountSpecPath); !os.IsNotExist(err) {
		t.Fatalf("RunExtractDirectMounts created mount specification after over-limit input: %v", err)
	}
}

func runGoExtractDirectMounts(t *testing.T, manifestFixture map[string]any) ([]byte, []byte) {
	t.Helper()

	root := t.TempDir()
	manifestPath := filepath.Join(root, "manifest.json")
	mountSpecPath := filepath.Join(root, "mounts.json")

	writeFixtureManifest(t, manifestPath, manifestFixture)
	if err := RunExtractDirectMounts(manifestPath, mountSpecPath); err != nil {
		t.Fatalf("RunExtractDirectMounts returned error: %v", err)
	}

	return readFile(t, manifestPath), readFile(t, mountSpecPath)
}

func writeFixtureManifest(t *testing.T, path string, fixture map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return data
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode mismatch for %s: got %04o want %04o", path, got, want)
	}
}
