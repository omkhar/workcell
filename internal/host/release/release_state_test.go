// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubReleaseListPageSize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		body    string
		want    int
		wantErr string
	}{
		{name: "empty", body: `[]`},
		{name: "two", body: `[{},{}]`, want: 2},
		{name: "null", body: `null`, wantErr: "expected JSON array"},
		{name: "object", body: `{}`, wantErr: "decode GitHub releases list page"},
		{name: "malformed", body: `[`, wantErr: "decode GitHub releases list page"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := GitHubReleaseListPageSize(writeReleaseStateFixture(t, tc.body))
			assertReleaseStateResult(t, err, tc.wantErr)
			if err == nil && got != tc.want {
				t.Fatalf("page size = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWriteGitHubListedRelease(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	other := releaseStateJSON(t, "example/workcell", "v1.2.30", true, false, false, 0)
	match := releaseStateJSON(t, "example/workcell", "v1.2.3", true, false, false, 0)
	first := filepath.Join(tmp, "first.json")
	second := filepath.Join(tmp, "second.json")
	output := filepath.Join(tmp, "selected.json")
	writeReleaseStatePage(t, first, other)
	writeReleaseStatePage(t, second, match)

	if err := WriteGitHubListedRelease("v1.2.3", []string{first, second}, output); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != match {
		t.Fatalf("selected release = %s, want %s", got, match)
	}

	if err := WriteGitHubListedRelease("v1.2.4", []string{first, second}, output); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(output); err != nil || info.Size() != 0 {
		t.Fatalf("absent exact tag output info = %#v, error = %v; want empty file", info, err)
	}
}

func TestWriteGitHubListedReleaseFailsClosed(t *testing.T) {
	t.Parallel()
	valid := releaseStateJSON(t, "example/workcell", "v1.2.3", true, false, false, 0)
	for _, tc := range []struct {
		name    string
		pages   []string
		wantErr string
	}{
		{name: "duplicate exact tag", pages: []string{"[" + valid + "]", "[" + valid + "]"}, wantErr: "duplicate exact tag"},
		{name: "null page", pages: []string{"null"}, wantErr: "expected JSON array"},
		{name: "malformed page", pages: []string{"["}, wantErr: "decode GitHub releases list page"},
		{
			name:    "missing upload URL",
			pages:   []string{`[{"id":2,"tag_name":"v1.2.4","draft":true,"prerelease":false,"immutable":false,"assets":[]}]`},
			wantErr: "missing upload_url",
		},
		{
			name:    "missing assets",
			pages:   []string{`[{"id":2,"upload_url":"https://uploads.example/2","tag_name":"v1.2.4","draft":true,"prerelease":false,"immutable":false}]`},
			wantErr: "missing assets",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			paths := make([]string, 0, len(tc.pages))
			for index, body := range tc.pages {
				path := filepath.Join(tmp, fmt.Sprintf("page-%d.json", index))
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, path)
			}
			err := WriteGitHubListedRelease("v1.2.3", paths, filepath.Join(tmp, "selected.json"))
			assertReleaseStateResult(t, err, tc.wantErr)
		})
	}
}

func TestValidateGitHubDraftRelease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		repository string
		tag        string
		body       string
		wantErr    string
	}{
		{name: "final", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.3", true, false, false, 1)},
		{name: "release candidate", repository: "example/workcell", tag: "v1.2.3-rc.4", body: releaseStateJSON(t, "example/workcell", "v1.2.3-rc.4", true, true, false, 0)},
		{name: "wrong tag", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.4", true, false, false, 0), wantErr: `tag_name = "v1.2.4"`},
		{name: "published", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.3", false, false, false, 0), wantErr: "draft = false"},
		{name: "wrong class", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.3", true, true, false, 0), wantErr: "prerelease = true"},
		{name: "immutable", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.3", true, false, true, 0), wantErr: "immutable = true"},
		{name: "wrong repository", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "other/workcell", "v1.2.3", true, false, false, 0), wantErr: "upload_url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateGitHubDraftRelease(tc.repository, tc.tag, writeReleaseStateFixture(t, tc.body))
			assertReleaseStateResult(t, err, tc.wantErr)
		})
	}
}

func TestValidateGitHubPublishedReleaseState(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		repository string
		tag        string
		body       string
		wantErr    string
	}{
		{name: "final", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.3", false, false, true, 1)},
		{name: "release candidate", repository: "example/workcell", tag: "v1.2.3-rc.4", body: releaseStateJSON(t, "example/workcell", "v1.2.3-rc.4", false, true, true, 0)},
		{name: "mutable", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.3", false, false, false, 0), wantErr: "immutable = false"},
		{name: "draft", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.3", true, false, true, 0), wantErr: "draft = true"},
		{name: "wrong class", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "example/workcell", "v1.2.3", false, true, true, 0), wantErr: "prerelease = true"},
		{name: "wrong repository", repository: "example/workcell", tag: "v1.2.3", body: releaseStateJSON(t, "other/workcell", "v1.2.3", false, false, true, 0), wantErr: "upload_url"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateGitHubPublishedReleaseState(tc.repository, tc.tag, writeReleaseStateFixture(t, tc.body))
			assertReleaseStateResult(t, err, tc.wantErr)
		})
	}
}

func TestValidateGitHubLatestReleaseResponse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		repository string
		tag        string
		status     string
		body       string
		wantErr    string
	}{
		{name: "final points to itself", repository: "example/workcell", tag: "v1.2.3", status: "200", body: releaseStateJSON(t, "example/workcell", "v1.2.3", false, false, true, 0)},
		{name: "release candidate leaves final latest", repository: "example/workcell", tag: "v1.2.3-rc.4", status: "200", body: releaseStateJSON(t, "example/workcell", "v1.2.2", false, false, true, 0)},
		{name: "release candidate with no final", repository: "example/workcell", tag: "v1.2.3-rc.4", status: "404", body: `{}`},
		{name: "final is not latest", repository: "example/workcell", tag: "v1.2.3", status: "200", body: releaseStateJSON(t, "example/workcell", "v1.2.2", false, false, true, 0), wantErr: "want final tag"},
		{name: "final latest is missing", repository: "example/workcell", tag: "v1.2.3", status: "404", body: `{}`, wantErr: "want 200"},
		{name: "release candidate lookup fails", repository: "example/workcell", tag: "v1.2.3-rc.4", status: "500", body: `{}`, wantErr: "want 200 or 404"},
		{name: "release candidate became latest", repository: "example/workcell", tag: "v1.2.3-rc.4", status: "200", body: releaseStateJSON(t, "example/workcell", "v1.2.3-rc.4", false, true, true, 0), wantErr: "want a final release tag"},
		{name: "another release candidate is latest", repository: "example/workcell", tag: "v1.2.3-rc.4", status: "200", body: releaseStateJSON(t, "example/workcell", "v1.2.2-rc.9", false, true, true, 0), wantErr: "want a final release tag"},
		{name: "wrong repository", repository: "example/workcell", tag: "v1.2.3", status: "200", body: releaseStateJSON(t, "other/workcell", "v1.2.3", false, false, true, 0), wantErr: "upload_url"},
		{name: "latest is mutable", repository: "example/workcell", tag: "v1.2.3", status: "200", body: releaseStateJSON(t, "example/workcell", "v1.2.3", false, false, false, 0), wantErr: "immutable = false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateGitHubLatestReleaseResponse(tc.repository, tc.tag, tc.status, writeReleaseStateFixture(t, tc.body))
			assertReleaseStateResult(t, err, tc.wantErr)
		})
	}
}

func TestReleaseStateValidatorsRejectInvalidInputsBeforeFileReads(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing.json")
	for name, err := range map[string]error{
		"draft repository":  ValidateGitHubDraftRelease("../repo", "v1.2.3", missing),
		"draft tag":         ValidateGitHubDraftRelease("example/workcell", "v1.2.3-beta.1", missing),
		"published repo":    ValidateGitHubPublishedReleaseState("../repo", "v1.2.3", missing),
		"published tag":     ValidateGitHubPublishedReleaseState("example/workcell", "v1.2.3-beta.1", missing),
		"latest repository": ValidateGitHubLatestReleaseResponse("../repo", "v1.2.3", "200", missing),
		"latest tag":        ValidateGitHubLatestReleaseResponse("example/workcell", "v1.2.3-beta.1", "200", missing),
		"listed exact tag":  WriteGitHubListedRelease("v1.2.3-beta.1", []string{missing}, filepath.Join(t.TempDir(), "out")),
		"no list pages":     WriteGitHubListedRelease("v1.2.3", nil, filepath.Join(t.TempDir(), "out")),
		"repository export": ValidateGitHubRepository("../repo"),
	} {
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s error = %v, want ErrInvalidInput", name, err)
		}
	}
}

func releaseStateJSON(t *testing.T, repository, tag string, draft, prerelease, immutable bool, assetCount int) string {
	t.Helper()
	assets := make([]json.RawMessage, 0, assetCount)
	for index := 0; index < assetCount; index++ {
		assets = append(assets, json.RawMessage(fmt.Sprintf(`{"opaque_asset_index":%d}`, index)))
	}
	id := int64(1)
	uploadURL := "https://uploads.github.com/repos/" + repository + "/releases/1/assets{?name,label}"
	item := listedRelease{
		ID:         &id,
		UploadURL:  &uploadURL,
		TagName:    &tag,
		Draft:      &draft,
		Prerelease: &prerelease,
		Immutable:  &immutable,
		Assets:     &assets,
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeReleaseStateFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "response.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeReleaseStatePage(t *testing.T, path string, items ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("["+strings.Join(items, ",")+"]"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertReleaseStateResult(t *testing.T, err error, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("error = %v, want substring %q", err, wantErr)
	}
}
