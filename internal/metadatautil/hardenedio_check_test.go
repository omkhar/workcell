// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckHardenedIOAcceptsThisRepository(t *testing.T) {
	if err := metadatautil.CheckHardenedIO(filepath.Join("..", "..")); err != nil {
		t.Fatalf("CheckHardenedIO() error = %v", err)
	}
}

func TestHardenedIOFindings(t *testing.T) {
	t.Parallel()
	const header = "package x\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n)\n\n"
	cases := []struct {
		name    string
		source  string
		want    []string
		wantErr string
	}{
		{
			name: "every banned symbol",
			source: header + "func f() {\nos.ReadFile(a)\nos.WriteFile(a)\nos.Open(a)\nos.OpenFile(a)\n" +
				"os.Create(a)\nos.Stat(a)\nos.RemoveAll(a)\nfilepath.Glob(a)\n}\n",
			want: []string{"os.ReadFile", "os.WriteFile", "os.Open", "os.OpenFile", "os.Create", "os.Stat", "os.RemoveAll", "filepath.Glob"},
		},
		{name: "a permitted call", source: header + "func f() { os.Lstat(a); os.MkdirAll(a, 0); filepath.Join(a) }\n"},
		{
			name:   "an aliased import is named by its package",
			source: "package x\n\nimport (\n\tstdos \"os\"\n\tfp \"path/filepath\"\n)\n\nfunc f() { stdos.ReadFile(a); fp.Glob(a) }\n",
			want:   []string{"os.ReadFile", "filepath.Glob"},
		},
		{name: "a function value is a reference", source: header + "var read = os.ReadFile\n", want: []string{"os.ReadFile"}},
		{name: "a comment is not a call", source: header + "// os.ReadFile(a)\nfunc f() {}\n"},
		{name: "a string is not a call", source: header + "var s = \"filepath.Glob(a)\"\n"},
		{name: "an inline exemption clears nothing", source: header + "func f() { os.Stat(a) } // hardened-fs-exempt: a reason\n", want: []string{"os.Stat"}},
		{
			name: "a local value that shadows an import is not the package",
			source: header + "func f() { if os := statter(); os != nil { os.Stat(a) }; filepath := globber(); filepath.Glob(a) }\n" +
				"func g() { os.Stat(a) }\n",
			want: []string{"os.Stat"},
		},
		{name: "a different Glob is not filepath.Glob", source: header + "func f() { path.Glob(a); os.Glob(a) }\n"},
		{
			name:    "a dot import of path/filepath is refused",
			source:  "package x\n\nimport . \"path/filepath\"\n\nfunc f() { Glob(a) }\n",
			wantErr: "hardened filesystem rule can read its calls",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			findings, err := metadatautil.HardenedIOFindings(testCase.source)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("expected an error containing %q, found %v", testCase.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("HardenedIOFindings() error = %v", err)
			}
			var got []string
			for _, finding := range findings {
				got = append(got, finding.Symbol)
			}
			if strings.Join(got, ",") != strings.Join(testCase.want, ",") {
				t.Fatalf("findings = %v, want %v", got, testCase.want)
			}
		})
	}
}

// Each failing row is a planted violation: the gate must refuse it. The clean
// rows show the same tree passes once the baseline states the call.
func TestCheckHardenedIORatchet(t *testing.T) {
	t.Parallel()
	const glob = "package tool\n\nimport \"path/filepath\"\n\nfunc f() { filepath.Glob(p) }\n"
	findings, err := metadatautil.HardenedIOFindings(glob)
	if err != nil || len(findings) != 1 {
		t.Fatalf("HardenedIOFindings() = %v, %v", findings, err)
	}
	row := "cmd/tool/main.go\tfilepath.Glob\t" + findings[0].Call + "\trepository sources only\n"
	cases := []struct {
		name     string
		path     string
		source   string
		baseline string
		wantErr  string
	}{
		{name: "a planted call fails and names the replacement", path: "cmd/tool/main.go", source: glob, wantErr: "use ReadDir on a parent from rootio.OpenParentDirectoryNoFollow"},
		{name: "a planted call under internal fails", path: "internal/x/x.go", source: "package x\n\nimport \"os\"\n\nfunc f() { os.ReadFile(p) }\n", wantErr: "use rootio.ReadFileNoFollow"},
		{name: "a planted OpenFile names the write-capable replacement", path: "internal/x/x.go", source: "package x\n\nimport \"os\"\n\nfunc f() { os.OpenFile(p, os.O_WRONLY, 0) }\n", wantErr: "a write, create or truncate needs rootio.StageAndPublishAt"},
		{name: "a reasoned row admits the call", path: "cmd/tool/main.go", source: glob, baseline: row},
		{name: "a planted directory Open names the directory replacement", path: "internal/x/x.go", source: "package x\n\nimport \"os\"\n\nfunc f() { os.Open(p) }\n", wantErr: "a directory needs rootio.OpenDirectoryAtNoFollow"},
		{name: "a planted RemoveAll names the no-follow replacement", path: "internal/x/x.go", source: "package x\n\nimport \"os\"\n\nfunc f() { os.RemoveAll(p) }\n", wantErr: "use rootio.RemoveAllAtNoFollow"},
		{name: "growth past the row fails", path: "cmd/tool/main.go", source: glob + "func g() { filepath.Glob(q) }\n", baseline: row, wantErr: "call not in the baseline row"},
		{name: "a call swapped for another under the same total fails", path: "cmd/tool/main.go", source: "package tool\n\nimport \"path/filepath\"\n\nfunc f() { filepath.Glob(q) }\n", baseline: row, wantErr: "stale baseline entry"},
		{name: "a call moved to another function fails", path: "cmd/tool/main.go", source: "package tool\n\nimport \"path/filepath\"\n\nfunc g() { filepath.Glob(p) }\n", baseline: row, wantErr: "call not in the baseline row"},
		{name: "an edit above the call keeps its identity", path: "cmd/tool/main.go", source: "package tool\n\nimport \"path/filepath\"\n\nvar x = 1\n\nfunc f() {\n\tfilepath.Glob(\n\t\tp)\n}\n", baseline: row},
		{name: "a malformed identity fails", path: "cmd/tool/main.go", source: glob, baseline: "cmd/tool/main.go\tfilepath.Glob\t1\tcount rows are gone\n", wantErr: "call identity must be 8 lowercase hex digits"},
		{name: "a row without a reason fails", path: "cmd/tool/main.go", source: glob, baseline: "cmd/tool/main.go\tfilepath.Glob\t1\t \n", wantErr: "expected 4 tab-separated fields"},
		{name: "a duplicate row fails", path: "cmd/tool/main.go", source: glob, baseline: row + row, wantErr: "duplicate row"},
		{name: "a stale row fails", path: "cmd/tool/main.go", source: "package tool\n", baseline: row, wantErr: "stale baseline row"},
		{name: "internal/rootio is allowed", path: "internal/rootio/io.go", source: "package rootio\n\nimport \"os\"\n\nfunc f() { os.Open(p) }\n"},
		{name: "a test source is out of scope", path: "cmd/tool/main_test.go", source: glob},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			// Each tree needs one non-test source, or the vacuous-pass guard
			// fires instead of the rule under test.
			writeHardenedFSFixture(t, filepath.Join(root, "cmd", "doc.go"), "package cmd\n")
			writeHardenedFSFixture(t, filepath.Join(root, "internal", "doc.go"), "package internal\n")
			writeHardenedFSFixture(t, filepath.Join(root, filepath.FromSlash(testCase.path)), testCase.source)
			writeHardenedFSFixture(t, filepath.Join(root, "policy", "hardened-io-baseline.tsv"), testCase.baseline)
			err := metadatautil.CheckHardenedIO(root)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("expected a clean result, found %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("expected an error containing %q, found %v", testCase.wantErr, err)
			}
		})
	}
}

// A missing tree must fail rather than pass over source it never read.
func TestCheckHardenedIORejectsAMissingTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHardenedFSFixture(t, filepath.Join(root, "internal", "doc.go"), "package internal\n")
	writeHardenedFSFixture(t, filepath.Join(root, "policy", "hardened-io-baseline.tsv"), "")
	if err := metadatautil.CheckHardenedIO(root); err == nil {
		t.Fatal("expected a missing cmd tree to fail")
	}
	if err := os.Symlink(filepath.Join(root, "internal"), filepath.Join(root, "cmd")); err != nil {
		t.Fatalf("create the symlinked tree: %v", err)
	}
	if err := metadatautil.CheckHardenedIO(root); err == nil {
		t.Fatal("expected a symlinked cmd tree to fail")
	}
}
