// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBuildInputFixture(t *testing.T, root, relative string, data []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func buildInputFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, relative := range []string{
		"runtime/container/Dockerfile",
		DebianBootstrapManifestRelPath,
		"runtime/container/providers/package.json",
		"runtime/container/providers/package-lock.json",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		writeBuildInputFixture(t, root, relative, data)
	}
	for _, relative := range []string{
		".dockerignore", "adapters/example/config.json", "go.mod", "go.sum",
		"internal/aptbroker/protocol.go", "internal/aptbroker/peer_linux.go",
		"cmd/workcell-apt-broker-client/main.go", "cmd/workcell-apt-broker-server/main.go",
		"internal/unrelated/example.go",
	} {
		writeBuildInputFixture(t, root, relative, []byte("initial fixture\n"))
	}
	return root
}

type buildInputTestManifest struct {
	Runtime struct {
		Inputs map[string]string `json:"context_inputs"`
	} `json:"runtime"`
	Verification struct {
		Inputs map[string]string `json:"inputs"`
	} `json:"verification"`
}

func generateBuildInputFixture(t *testing.T, root string) buildInputTestManifest {
	t.Helper()
	output := filepath.Join(t.TempDir(), "manifest.json")
	err := GenerateBuildInputManifest(
		filepath.Join(root, "runtime/container/Dockerfile"),
		filepath.Join(root, "runtime/container/providers/package.json"),
		filepath.Join(root, "runtime/container/providers/package-lock.json"),
		output, "fixture", 0, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	var manifest buildInputTestManifest
	if err := readJSONFile(output, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestBuildInputManifestBindsBrokerSources(t *testing.T) {
	root := buildInputFixture(t)
	before := generateBuildInputFixture(t, root)
	for _, relative := range []string{
		"go.mod", "go.sum", "internal/aptbroker/protocol.go", "internal/aptbroker/peer_linux.go",
		"cmd/workcell-apt-broker-client/main.go", "cmd/workcell-apt-broker-server/main.go",
	} {
		t.Run(relative, func(t *testing.T) {
			if before.Runtime.Inputs[relative] != sha256HexString("initial fixture\n") {
				t.Fatalf("runtime input missing or incorrect: %s", relative)
			}
			if _, exists := before.Verification.Inputs[relative]; exists {
				t.Fatalf("runtime input also classified as verification: %s", relative)
			}
			writeBuildInputFixture(t, root, relative, []byte("changed fixture\n"))
			after := generateBuildInputFixture(t, root)
			if after.Runtime.Inputs[relative] != sha256HexString("changed fixture\n") {
				t.Fatalf("runtime input digest did not follow source: %s", relative)
			}
		})
	}
	const unrelated = "internal/unrelated/example.go"
	if _, exists := before.Runtime.Inputs[unrelated]; exists {
		t.Fatal("unrelated Go source entered runtime context")
	}
	if before.Verification.Inputs[unrelated] != sha256HexString("initial fixture\n") {
		t.Fatal("unrelated Go source lost verification binding")
	}
}
