// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	refreshSHA1 = "1111111111111111111111111111111111111111"
	refreshSHA2 = "2222222222222222222222222222222222222222"
)

// writeRefreshFixture writes one workflow and, unless table is "-", the
// inputs table.
func writeRefreshFixture(t *testing.T, workflow, table string) string {
	t.Helper()
	root := t.TempDir()
	for path, body := range map[string]string{".github/workflows/ci.yml": workflow, workflowActionInputsPath: table} {
		if body == "-" {
			continue
		}
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(workflowActionInputsPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func refreshWorkflow(uses ...string) string {
	body := "name: CI\non: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n"
	for _, u := range uses {
		body += "      - uses: " + u + "\n"
	}
	return body
}

type fakeFetcher struct {
	files map[string]string
	calls []string
}

func (f *fakeFetcher) fetch(_ context.Context, owner, repo, dir, sha string) ([]byte, error) {
	key := owner + "/" + repo
	if dir != "" {
		key += "/" + dir
	}
	key += "@" + sha
	f.calls = append(f.calls, key)
	body, ok := f.files[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return []byte(body), nil
}

func readTable(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, workflowActionInputsPath))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRefreshActionInputsBuildsTableFromWorkflows(t *testing.T) {
	root := writeRefreshFixture(t, refreshWorkflow(
		"o/b@"+refreshSHA2+" # v2",
		"o/a/sub@"+refreshSHA1,
		"o/b@"+refreshSHA2,
		"./local",
		"docker://alpine:3",
	), "-")
	fetcher := &fakeFetcher{files: map[string]string{
		"o/a/sub@" + refreshSHA1: "name: a\ninputs:\n  zeta: {}\n  Alpha_Key:\n    required: true\n  beta-key: {}\n",
		"o/b@" + refreshSHA2:     "name: b\nruns:\n  using: node24\n",
	}}
	changed, err := RefreshActionInputs(context.Background(), root, fetcher.fetch, false)
	if err != nil || !changed {
		t.Fatalf("RefreshActionInputs() = %v, %v; want changed", changed, err)
	}
	want := "o/a/sub@" + refreshSHA1 + "\tAlpha_Key,beta-key,zeta\n" + "o/b@" + refreshSHA2 + "\t\n"
	if got := readTable(t, root); got != want {
		t.Fatalf("table = %q, want %q", got, want)
	}
	if len(fetcher.calls) != 2 {
		t.Fatalf("fetch calls = %v, want one per unique ref", fetcher.calls)
	}
	if err := CheckWorkflowRefs(root); err != nil && strings.Contains(err.Error(), "uses-not-cached") {
		t.Fatalf("CheckWorkflowRefs after refresh: %v", err)
	}
}

func TestRefreshActionInputsKeepsCachedRowsAndDropsStaleRows(t *testing.T) {
	cached := "o/a@" + refreshSHA1 + "\tkept,as,is\n"
	stale := "o/gone@" + refreshSHA1 + "\tx\n"
	root := writeRefreshFixture(t, refreshWorkflow("o/a@"+refreshSHA1, "o/new@"+refreshSHA2), cached+stale)
	fetcher := &fakeFetcher{files: map[string]string{"o/new@" + refreshSHA2: "inputs:\n  token: {}\n"}}
	changed, err := RefreshActionInputs(context.Background(), root, fetcher.fetch, false)
	if err != nil || !changed {
		t.Fatalf("RefreshActionInputs() = %v, %v; want changed", changed, err)
	}
	if got, want := readTable(t, root), cached+"o/new@"+refreshSHA2+"\ttoken\n"; got != want {
		t.Fatalf("table = %q, want %q", got, want)
	}
	if strings.Join(fetcher.calls, " ") != "o/new@"+refreshSHA2 {
		t.Fatalf("fetch calls = %v, want only the new pin", fetcher.calls)
	}

	// A second run is a no-op and fetches nothing.
	fetcher.calls = nil
	changed, err = RefreshActionInputs(context.Background(), root, fetcher.fetch, false)
	if err != nil || changed || len(fetcher.calls) != 0 {
		t.Fatalf("second RefreshActionInputs() = %v, %v, calls %v; want unchanged with no fetch", changed, err, fetcher.calls)
	}
}

func TestRefreshActionInputsCheckWritesNothingAndFetchesNothing(t *testing.T) {
	table := "o/a@" + refreshSHA1 + "\tx\n"
	root := writeRefreshFixture(t, refreshWorkflow("o/a@"+refreshSHA1, "o/new@"+refreshSHA2), table)
	fetcher := &fakeFetcher{}
	changed, err := RefreshActionInputs(context.Background(), root, fetcher.fetch, true)
	if err != nil || !changed {
		t.Fatalf("check = %v, %v; want changed", changed, err)
	}
	if len(fetcher.calls) != 0 || readTable(t, root) != table {
		t.Fatalf("check fetched %v or wrote the table", fetcher.calls)
	}
	root = writeRefreshFixture(t, refreshWorkflow("o/a@"+refreshSHA1), table)
	if changed, err := RefreshActionInputs(context.Background(), root, fetcher.fetch, true); err != nil || changed {
		t.Fatalf("check on a current table = %v, %v; want unchanged", changed, err)
	}
}

func TestRefreshActionInputsRefusesUnpinnedOrEscapingRefs(t *testing.T) {
	for _, uses := range []string{
		"o/a@v4",
		"o/a@" + refreshSHA1[:39],
		"o/a@" + strings.Repeat("A", 40),
		"o/a/../b@" + refreshSHA1,
		"o/../x@" + refreshSHA1,
		"../a/b@" + refreshSHA1,
		"o/a/./b@" + refreshSHA1,
		"o/a//b@" + refreshSHA1,
		"o@" + refreshSHA1,
		"o/a b@" + refreshSHA1,
		"o/a%2f..@" + refreshSHA1,
	} {
		root := writeRefreshFixture(t, refreshWorkflow(`"`+uses+`"`), "-")
		fetcher := &fakeFetcher{}
		if _, err := RefreshActionInputs(context.Background(), root, fetcher.fetch, false); err == nil || len(fetcher.calls) != 0 {
			t.Fatalf("uses %q: err = %v, calls %v; want refusal before any fetch", uses, err, fetcher.calls)
		}
	}
}

func TestRefreshActionInputsRefusesNamesTheTableCannotHold(t *testing.T) {
	for _, name := range []string{`"a,b"`, `"a\tb"`, `"a\nb"`, "\"`x`\"", `"-x"`, `""`, `"a b"`} {
		root := writeRefreshFixture(t, refreshWorkflow("o/a@"+refreshSHA1), "-")
		fetcher := &fakeFetcher{files: map[string]string{"o/a@" + refreshSHA1: "inputs:\n  " + name + ": {}\n"}}
		if _, err := RefreshActionInputs(context.Background(), root, fetcher.fetch, false); err == nil {
			t.Fatalf("input name %s: want refusal", name)
		}
		if _, err := os.Stat(filepath.Join(root, workflowActionInputsPath)); !os.IsNotExist(err) {
			t.Fatalf("input name %s: the table was written", name)
		}
	}
	root := writeRefreshFixture(t, refreshWorkflow("o/a@"+refreshSHA1), "-")
	fetcher := &fakeFetcher{files: map[string]string{"o/a@" + refreshSHA1: "inputs: [a, b]\n"}}
	if _, err := RefreshActionInputs(context.Background(), root, fetcher.fetch, false); err == nil {
		t.Fatal("a sequence of inputs: want refusal")
	}
}

func TestRefreshActionInputsRefusesSymlinkedTable(t *testing.T) {
	root := writeRefreshFixture(t, refreshWorkflow("o/a@"+refreshSHA1), "-")
	target := filepath.Join(t.TempDir(), "outside.tsv")
	if err := os.WriteFile(target, []byte("o/a@"+refreshSHA1+"\tx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, workflowActionInputsPath)); err != nil {
		t.Fatal(err)
	}
	fetcher := &fakeFetcher{files: map[string]string{"o/a@" + refreshSHA1: "inputs:\n  y: {}\n"}}
	if _, err := RefreshActionInputs(context.Background(), root, fetcher.fetch, false); err == nil {
		t.Fatal("want refusal of a symlinked table")
	}
	if data, _ := os.ReadFile(target); string(data) != "o/a@"+refreshSHA1+"\tx\n" {
		t.Fatalf("symlink target changed to %q", data)
	}
}

func TestFetchActionMetadataRequestsOnlyThePinnedPaths(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "" {
			t.Errorf("request carried a credential")
		}
		switch r.URL.Path {
		case "/o/a/" + refreshSHA1 + "/sub/dir/action.yaml":
			_, _ = w.Write([]byte("inputs:\n  k: {}\n"))
		case "/o/r/" + refreshSHA1 + "/action.yml":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		case "/o/big/" + refreshSHA1 + "/action.yml":
			_, _ = w.Write([]byte(strings.Repeat("#", actionMetadataMaxBytes+1)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	data, err := fetchActionMetadata(context.Background(), client, server.URL, "o", "a", "sub/dir", refreshSHA1)
	if err != nil || string(data) != "inputs:\n  k: {}\n" {
		t.Fatalf("fetch = %q, %v", data, err)
	}
	if want := []string{"/o/a/" + refreshSHA1 + "/sub/dir/action.yml", "/o/a/" + refreshSHA1 + "/sub/dir/action.yaml"}; strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for _, repo := range []string{"r", "big", "none"} {
		if _, err := fetchActionMetadata(context.Background(), client, server.URL, "o", repo, "", refreshSHA1); err == nil {
			t.Fatalf("repo %s: want an error", repo)
		}
	}
	for _, args := range [][4]string{{"o", "a", "../x", refreshSHA1}, {"..", "a", "", refreshSHA1}, {"o", "a", "", "main"}, {"o", "a/b", "", refreshSHA1}} {
		paths = nil
		if _, err := fetchActionMetadata(context.Background(), client, server.URL, args[0], args[1], args[2], args[3]); err == nil || len(paths) != 0 {
			t.Fatalf("args %v: err = %v, paths %v; want refusal before any request", args, err, paths)
		}
	}
}
