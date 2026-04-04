// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
