// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestReleaseStateCommandDispatch(t *testing.T) {
	tmp := t.TempDir()
	draft := writeHostutilReleaseFixture(t, tmp, "draft.json", hostutilReleaseJSON("example/workcell", "v1.2.3", true, false, false))
	published := writeHostutilReleaseFixture(t, tmp, "published.json", hostutilReleaseJSON("example/workcell", "v1.2.3", false, false, true))
	page := writeHostutilReleaseFixture(t, tmp, "page.json", "["+hostutilReleaseJSON("example/workcell", "v1.2.3", true, false, false)+"]")
	selected := filepath.Join(tmp, "selected.json")

	for name, args := range map[string][]string{
		"repository":       {"validate-repository", "example/workcell"},
		"draft":            {"validate-draft-release", "example/workcell", "v1.2.3", draft},
		"published":        {"validate-published-state", "example/workcell", "v1.2.3", published},
		"latest":           {"validate-latest-response", "example/workcell", "v1.2.3", "200", published},
		"select exact tag": {"select-listed-release", "v1.2.3", selected, page},
	} {
		if err := runRelease(args); err != nil {
			t.Errorf("%s dispatch error = %v", name, err)
		}
	}
	if info, err := os.Stat(selected); err != nil || info.Size() == 0 {
		t.Fatalf("selected release info = %#v, error = %v; want nonempty output", info, err)
	}

	output, err := captureHostutilStdout(t, func() error {
		return runRelease([]string{"list-page-size", page})
	})
	if err != nil || output != "1\n" {
		t.Fatalf("list-page-size output = %q, error = %v; want %q", output, err, "1\n")
	}
}

func TestReleaseStateCommandUsageIsActionable(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"validate-repository"},
		{"validate-draft-release", "owner/repo", "v1.2.3"},
		{"validate-published-state", "owner/repo", "v1.2.3"},
		{"validate-latest-response", "owner/repo", "v1.2.3", "200"},
		{"select-listed-release", "v1.2.3", "output.json"},
	} {
		err := runRelease(args)
		var exitErr *cliexit.ExitCodeError
		if !errors.As(err, &exitErr) || exitErr.Code != 2 {
			t.Fatalf("runRelease(%q) error = %v, want exit code 2", args, err)
		}
		for _, required := range []string{
			"validate-draft-release OWNER/REPO TAG RESPONSE_JSON",
			"validate-latest-response OWNER/REPO TAG HTTP_STATUS RESPONSE_JSON",
			"select-listed-release TAG OUTPUT PAGE_JSON...",
		} {
			if !strings.Contains(exitErr.Message, required) {
				t.Fatalf("usage missing %q:\n%s", required, exitErr.Message)
			}
		}
	}
}

func hostutilReleaseJSON(repository, tag string, draft, prerelease, immutable bool) string {
	return `{"id":1,"upload_url":"https://uploads.github.com/repos/` + repository +
		`/releases/1/assets{?name,label}","tag_name":"` + tag +
		`","draft":` + strconv.FormatBool(draft) + `,"prerelease":` + strconv.FormatBool(prerelease) +
		`,"immutable":` + strconv.FormatBool(immutable) + `,"assets":[]}`
}

func writeHostutilReleaseFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func captureHostutilStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()
	runErr := run()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}
