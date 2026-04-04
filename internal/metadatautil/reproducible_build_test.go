// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTOMLSubsetAllowsMultilineArraysWithTrailingComma(t *testing.T) {
	parsed, err := ParseTOMLSubset(`
[required_status_checks]
contexts = [
  "Validate repository",
  "Reproducible build",
]
`, "/tmp/policy.toml")
	if err != nil {
		t.Fatalf("ParseTOMLSubset() error = %v", err)
	}
	table, ok := parsed["required_status_checks"].(map[string]any)
	if !ok {
		t.Fatalf("required_status_checks table missing or wrong type: %#v", parsed["required_status_checks"])
	}
	contexts, ok := table["contexts"].([]any)
	if !ok {
		t.Fatalf("contexts missing or wrong type: %#v", table["contexts"])
	}
	if len(contexts) != 2 || contexts[0] != "Validate repository" || contexts[1] != "Reproducible build" {
		t.Fatalf("contexts = %#v, want two entries", contexts)
	}
}

func TestVerifyReproducibleBuildWritesManifest(t *testing.T) {
	root := t.TempDir()
	layoutA := filepath.Join(root, "layout-a")
	layoutB := filepath.Join(root, "layout-b")
	writeSyntheticOCIExport(t, layoutA)
	writeSyntheticOCIExport(t, layoutB)

	manifestPath := filepath.Join(root, "repro.json")
	if err := VerifyReproducibleBuild(layoutA, layoutB, "linux/amd64,linux/arm64", manifestPath, 1700000000); err != nil {
		t.Fatalf("VerifyReproducibleBuild() error = %v", err)
	}

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var manifest struct {
		OCISubjectDigest string `json:"oci_subject_digest"`
		SourceDateEpoch  int64  `json:"source_date_epoch"`
		Platforms        map[string]struct {
			ImageManifestDigest string `json:"image_manifest_digest"`
			ConfigDigest        string `json:"config_digest"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if manifest.OCISubjectDigest != "sha256:wrapped-index" {
		t.Fatalf("subject digest = %q, want %q", manifest.OCISubjectDigest, "sha256:wrapped-index")
	}
	if manifest.SourceDateEpoch != 1700000000 {
		t.Fatalf("source_date_epoch = %d, want %d", manifest.SourceDateEpoch, 1700000000)
	}
	if got := manifest.Platforms["linux/amd64"]; got.ImageManifestDigest != "sha256:manifest-amd64" || got.ConfigDigest != "sha256:config-amd64" {
		t.Fatalf("amd64 platform digests = %+v, want manifest-amd64/config-amd64", got)
	}
	if got := manifest.Platforms["linux/arm64"]; got.ImageManifestDigest != "sha256:manifest-arm64" || got.ConfigDigest != "sha256:config-arm64" {
		t.Fatalf("arm64 platform digests = %+v, want manifest-arm64/config-arm64", got)
	}
}

func TestVerifyReproducibleBuildReportsMismatch(t *testing.T) {
	root := t.TempDir()
	layoutA := filepath.Join(root, "layout-a")
	layoutB := filepath.Join(root, "layout-b")
	writeSyntheticOCIExport(t, layoutA)
	writeSyntheticOCIExport(t, layoutB)

	badManifest := filepath.Join(layoutB, "blobs", "sha256", "manifest-arm64")
	if err := os.WriteFile(badManifest, []byte(`{"config":{"digest":"sha256:config-arm64-bad"}}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := VerifyReproducibleBuild(layoutA, layoutB, "linux/amd64,linux/arm64", "", 1700000000)
	if err == nil {
		t.Fatalf("VerifyReproducibleBuild() error = nil, want mismatch")
	}
	if !strings.Contains(err.Error(), "Config digests (linux/arm64):") {
		t.Fatalf("VerifyReproducibleBuild() error = %q, want config mismatch", err)
	}
}

func TestGenerateAndVerifyReproducibleBuildManifest(t *testing.T) {
	root := t.TempDir()
	layoutA := filepath.Join(root, "layout-a")
	layoutB := filepath.Join(root, "layout-b")
	writeSyntheticOCIExport(t, layoutA)
	writeSyntheticOCIExport(t, layoutB)

	manifestPath := filepath.Join(root, "repro.json")
	if err := GenerateReproducibleBuildManifest(layoutA, "linux/amd64,linux/arm64", manifestPath, 1700000000); err != nil {
		t.Fatalf("GenerateReproducibleBuildManifest() error = %v", err)
	}
	if err := VerifyReproducibleBuildManifest(layoutB, "linux/amd64,linux/arm64", manifestPath); err != nil {
		t.Fatalf("VerifyReproducibleBuildManifest() error = %v", err)
	}
}

func writeSyntheticOCIExport(t *testing.T, root string) {
	t.Helper()

	mustWriteJSONFile := func(path string, value any) {
		t.Helper()
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatalf("json.MarshalIndent() error = %v", err)
		}
		data = append(data, '\n')
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	mustWriteJSONFile(filepath.Join(root, "index.json"), map[string]any{
		"annotations": map[string]any{
			"ignored": "top-level",
		},
		"manifests": []any{
			map[string]any{
				"digest": "sha256:wrapped-index",
			},
		},
	})

	mustWriteJSONFile(filepath.Join(root, "blobs", "sha256", "wrapped-index"), map[string]any{
		"annotations": map[string]any{
			"ignored": "nested",
		},
		"manifests": []any{
			map[string]any{
				"digest": "sha256:manifest-amd64",
				"platform": map[string]any{
					"os":           "linux",
					"architecture": "amd64",
				},
			},
			map[string]any{
				"digest": "sha256:manifest-arm64",
				"platform": map[string]any{
					"os":           "linux",
					"architecture": "arm64",
				},
			},
		},
	})

	mustWriteJSONFile(filepath.Join(root, "blobs", "sha256", "manifest-amd64"), map[string]any{
		"config": map[string]any{
			"digest": "sha256:config-amd64",
		},
	})
	mustWriteJSONFile(filepath.Join(root, "blobs", "sha256", "manifest-arm64"), map[string]any{
		"config": map[string]any{
			"digest": "sha256:config-arm64",
		},
	})
}
