// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package release

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteGitHubReleaseCreatePayload(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	outputPath := filepath.Join(tmp, "create.json")
	if err := WriteGitHubReleaseCreatePayload("v1.2.3", outputPath); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != `{"tag_name":"v1.2.3","draft":true,"generate_release_notes":true}` {
		t.Fatalf("unexpected payload: %s", data)
	}
}

func TestWriteGitHubReleaseMetadata(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	releaseJSONPath := filepath.Join(tmp, "release.json")
	outputPath := filepath.Join(tmp, "metadata.bin")
	releaseJSON := map[string]any{
		"id":         123,
		"upload_url": "https://uploads.github.com/repos/example/workcell/releases/123/assets{?name,label}",
		"draft":      true,
		"immutable":  false,
		"assets": []map[string]any{
			{"name": "workcell-linux-amd64.tar.gz", "id": 11},
			{"name": "workcell-linux-arm64.tar.gz"},
		},
	}
	data, err := json.Marshal(releaseJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(releaseJSONPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteGitHubReleaseMetadata(releaseJSONPath, []string{
		filepath.Join("/tmp", "workcell-linux-amd64.tar.gz"),
		filepath.Join("/tmp", "workcell-linux-arm64.tar.gz"),
	}, outputPath); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	records := bytes.Split(got, []byte{0})
	if len(records) > 0 && len(records[len(records)-1]) == 0 {
		records = records[:len(records)-1]
	}
	want := [][]byte{
		[]byte("123"),
		[]byte("https://uploads.github.com/repos/example/workcell/releases/123/assets"),
		[]byte("true"),
		[]byte("false"),
		[]byte("workcell-linux-amd64.tar.gz"),
		[]byte("11"),
		[]byte("workcell-linux-arm64.tar.gz"),
		[]byte(""),
	}
	if len(records) != len(want) {
		t.Fatalf("unexpected record count: got %d want %d", len(records), len(want))
	}
	for i := range want {
		if !bytes.Equal(records[i], want[i]) {
			t.Fatalf("record %d = %q, want %q", i, records[i], want[i])
		}
	}
}

func TestEncodeReleaseAssetName(t *testing.T) {
	t.Parallel()
	got := EncodeReleaseAssetName("workcell a+b.tar.gz")
	want := "workcell%20a%2Bb.tar.gz"
	if got != want {
		t.Fatalf("EncodeReleaseAssetName() = %q, want %q", got, want)
	}
}

func TestWriteReleaseBundleManifest(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	outputPath := filepath.Join(tmp, "bundle", "manifest.json")
	if err := WriteReleaseBundleManifest(outputPath, "HEAD", "workcell.tar.gz", "workcell/", 123, "sha256:aaa", "sha256:bbb"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		"{",
		`  "archive_ref": "HEAD",`,
		`  "bundle_name": "workcell.tar.gz",`,
		`  "bundle_prefix": "workcell/",`,
		`  "bundle_sha256": "sha256:aaa",`,
		`  "checksums_sha256": "sha256:bbb",`,
		`  "source_date_epoch": 123`,
		"}",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("unexpected manifest:\n%s", got)
	}
}
