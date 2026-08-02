// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteGitHubReleaseCreatePayload(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tag  string
		want string
	}{
		{
			tag:  "v1.2.3",
			want: `{"tag_name":"v1.2.3","draft":true,"prerelease":false,"make_latest":"false","generate_release_notes":true}`,
		},
		{
			tag:  "v1.2.3-rc.4",
			want: `{"tag_name":"v1.2.3-rc.4","draft":true,"prerelease":true,"make_latest":"false","generate_release_notes":true}`,
		},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			t.Parallel()
			outputPath := filepath.Join(t.TempDir(), "create.json")
			if err := WriteGitHubReleaseCreatePayload(tc.tag, outputPath); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Fatalf("payload = %s, want %s", data, tc.want)
			}
		})
	}
}

func TestWriteGitHubReleasePublishPayload(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tag  string
		want string
	}{
		{tag: "v1.2.3", want: `{"draft":false,"prerelease":false,"make_latest":"true"}`},
		{tag: "v1.2.3-rc.4", want: `{"draft":false,"prerelease":true,"make_latest":"false"}`},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			t.Parallel()
			outputPath := filepath.Join(t.TempDir(), "publish.json")
			if err := WriteGitHubReleasePublishPayload(tc.tag, outputPath); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.want {
				t.Fatalf("payload = %s, want %s", data, tc.want)
			}
		})
	}
}

func TestReleasePayloadsRejectUnsupportedTags(t *testing.T) {
	t.Parallel()
	for _, write := range []func(string, string) error{
		WriteGitHubReleaseCreatePayload,
		WriteGitHubReleasePublishPayload,
	} {
		outputPath := filepath.Join(t.TempDir(), "payload.json")
		err := write("v1.2.3-beta.1", outputPath)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("write unsupported tag error = %v, want ErrInvalidInput", err)
		}
		if _, statErr := os.Stat(outputPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unsupported tag created output file: %v", statErr)
		}
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
