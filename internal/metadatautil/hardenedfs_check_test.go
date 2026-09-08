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

func TestCheckHardenedFSAcceptsThisRepository(t *testing.T) {
	if err := metadatautil.CheckHardenedFS(filepath.Join("..", "..")); err != nil {
		t.Fatalf("CheckHardenedFS() error = %v", err)
	}
}

// The scan reads the syntax tree, so text that names a call is not a call.
// Every "no finding" row is a bypass a text scan admits: a comment, a string
// literal, a raw literal, an identifier that only ends with the banned name,
// and a reference with no argument list. Two rows are the bypasses the
// reviewer found against the first text-scan version of this check: an
// aliased os import, and the exemption words inside a string literal.
func TestHardenedFSFindings(t *testing.T) {
	t.Parallel()
	const header = "package host\n\nimport \"os\"\n\n"
	cases := []struct {
		name       string
		source     string
		want       int
		wantSymbol string
		wantErr    string
	}{
		{name: "plain call", source: header + "func f() { os.Open(path) }\n", want: 1},
		{name: "two calls on one line", source: header + "func f() { os.Stat(a); os.Stat(b) }\n", want: 2},
		{
			name: "every banned symbol",
			source: header + "func f() {\nos.Open(a)\nos.OpenFile(a)\nos.ReadFile(a)\nos.WriteFile(a)\n" +
				"os.Create(a)\nos.Mkdir(a)\nos.MkdirAll(a)\nos.Rename(a, b)\nos.Stat(a)\nos.ReadDir(a)\n" +
				"os.Readlink(a)\nos.Remove(a)\nos.RemoveAll(a)\nos.Chmod(a, b)\nos.Chown(a, b, c)\n" +
				"os.Symlink(a, b)\nos.Link(a, b)\nos.Truncate(a, b)\nos.CreateTemp(a, b)\nos.MkdirTemp(a, b)\n}\n",
			want: 20,
		},
		{
			name:   "an aliased import is still the os package",
			source: "package host\n\nimport stdos \"os\"\n\nfunc f() { stdos.Open(path) }\n",
			want:   1,
		},
		{
			name:   "a raw-string import path is still os",
			source: "package host\n\nimport os `os`\n\nfunc f() { os.Open(path) }\n",
			want:   1,
		},
		{
			name:   "an escaped import path is still os",
			source: "package host\n\nimport os \"o\\x73\"\n\nfunc f() { os.Open(path) }\n",
			want:   1,
		},
		{
			name:    "a dot import is refused",
			source:  "package host\n\nimport . \"os\"\n\nfunc f() { Open(path) }\n",
			wantErr: "hardened filesystem rule can read its calls",
		},

		{name: "line comment", source: header + "// os.Open(path) is banned\nfunc f() {}\n", want: 0},
		{name: "block comment", source: header + "/*\nos.Open(path)\n*/\nfunc f() {}\n", want: 0},
		{name: "interpreted literal", source: header + "var s = \"os.Open(\"\n", want: 0},
		{name: "raw literal", source: header + "var s = `os.Open(path)`\n", want: 0},
		{name: "a longer identifier is not the call", source: header + "func f() { workcellos.Open(path) }\n", want: 0},
		{
			// A function value carries the same authority as the direct call,
			// so a reference is a finding.
			name:   "a function value is a reference",
			source: header + "var open = os.Open\n",
			want:   1,
		},
		{
			name:   "a function value and its call are two references",
			source: header + "func f() {\nopen := os.Open\nopen(path)\n_ = os.Open\n}\n",
			want:   2,
		},
		{name: "another package with the same method", source: header + "func f() { rootio.Open(path) }\n", want: 0},
		{name: "os.Lstat is the answer, not the defect", source: header + "func f() { os.Lstat(path) }\n", want: 0},
		{
			name:   "the line states its reason",
			source: header + "func f() { os.Open(path) } // hardened-fs-exempt: the path is a build constant\n",
			want:   0,
		},
		{
			name:   "a bare tag states nothing",
			source: header + "func f() { os.Open(path) } // hardened-fs-exempt:\n",
			want:   1,
		},
		{
			name:   "the tag inside a string literal exempts nothing",
			source: header + "func f() { print(\"hardened-fs-exempt: x\"); os.Open(path) }\n",
			want:   1,
		},
		{
			name:   "an exemption clears only its own line",
			source: header + "func f() {\nos.Open(a) // hardened-fs-exempt: a build constant\nos.Open(b)\n}\n",
			want:   1,
		},
		{
			// One comment states the reason for one reference, so the other
			// reference on the line is still reported. The comment follows
			// os.ReadFile, so os.Open is the one that survives.
			name:       "one exemption clears the reference it follows",
			source:     header + "func f() { os.Open(a); os.ReadFile(b) } // hardened-fs-exempt: b is a build constant\n",
			want:       1,
			wantSymbol: "os.Open",
		},
		{
			name:   "a parenthesized call is still the call",
			source: header + "func f() { (os.Open)(path) }\n",
			want:   1,
		},
		{
			name:   "a parenthesized qualifier is still the os package",
			source: header + "func f() { (os).Open(path) }\n",
			want:   1,
		},
		{
			// The block comment carries its closing delimiter in its text, and
			// that is not a reason.
			name:   "a bare block comment states nothing",
			source: header + "func f() { os.Open(path) } /* hardened-fs-exempt: */\n",
			want:   1,
		},
		{
			name:   "a block comment with a reason clears the call",
			source: header + "func f() { os.Open(path) } /* hardened-fs-exempt: a build constant */\n",
			want:   0,
		},
		{
			// A //line directive renames a logical line, so a comment and an
			// unrelated call can report the same one.
			name: "a line directive does not move an exemption",
			source: header + "//line fixture.go:99\nfunc f() { os.Open(path) }\n" +
				"//line fixture.go:99\n// hardened-fs-exempt: a build constant\n",
			want: 1,
		},
		{
			name:   "the tag has to open the comment body",
			source: header + "func f() { os.Open(path) } // see hardened-fs-exempt: a build constant\n",
			want:   1,
		},
		{
			name:   "the temporary-path calls resolve a parent by name",
			source: header + "func f() {\nos.CreateTemp(dir, pattern)\nos.MkdirTemp(dir, pattern)\n}\n",
			want:   2,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			findings, err := metadatautil.HardenedFSFindings(testCase.source)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("expected an error containing %q, found %v", testCase.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("HardenedFSFindings() error = %v", err)
			}
			if len(findings) != testCase.want {
				t.Fatalf("HardenedFSFindings() = %v, want %d finding(s)", findings, testCase.want)
			}
			if testCase.wantSymbol != "" && findings[0].Symbol != testCase.wantSymbol {
				t.Fatalf("HardenedFSFindings() reported %q, want %q", findings[0].Symbol, testCase.wantSymbol)
			}
		})
	}
}

func TestCheckHardenedFSRatchet(t *testing.T) {
	t.Parallel()
	const oneCall = "package host\n\nimport \"os\"\n\nfunc read() { os.ReadFile(path) }\n"
	cases := []struct {
		name     string
		source   string
		baseline string
		wantErr  string
	}{
		{
			name:     "at baseline",
			source:   oneCall,
			baseline: "internal/host/state.go\tos.ReadFile\t1\n",
		},
		{
			name:     "growth fails",
			source:   oneCall + "func more() { os.ReadFile(other) }\n",
			baseline: "internal/host/state.go\tos.ReadFile\t1\n",
			wantErr:  "baseline allows 1",
		},
		{
			name:     "an unlisted call fails",
			source:   oneCall,
			baseline: "",
			wantErr:  "baseline allows 0",
		},
		{
			name:     "a stale row fails",
			source:   "package host\n\nfunc read() { rootio.ReadFileNoFollow(path, label, limit) }\n",
			baseline: "internal/host/state.go\tos.ReadFile\t1\n",
			wantErr:  "stale baseline row",
		},
		{
			name:     "an inline reason clears the call",
			source:   "package host\n\nimport \"os\"\n\nfunc read() { os.ReadFile(path) } // hardened-fs-exempt: a build constant\n",
			baseline: "",
		},
		{
			name:     "a test source is out of scope",
			source:   oneCall,
			baseline: "",
			wantErr:  "", // written to state_test.go below
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			name := "state.go"
			if testCase.name == "a test source is out of scope" {
				name = "state_test.go"
			}
			writeHardenedFSFixture(t, filepath.Join(root, "internal", "host", name), testCase.source)
			if name == "state_test.go" {
				// The walk needs one non-test source, or the empty-inventory
				// guard fires instead of the rule under test.
				writeHardenedFSFixture(t, filepath.Join(root, "internal", "host", "doc.go"), "package host\n")
			}
			writeHardenedFSFixture(t, filepath.Join(root, "policy", "hardened-fs-baseline.tsv"), testCase.baseline)
			writeHardenedFSPackageFixtures(t, root, "internal/host")
			err := metadatautil.CheckHardenedFS(root)
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

// A package name that no directory matches must fail: a list that outlives its
// subject reports a clean result over source it never read.
func TestCheckHardenedFSRejectsAMissingPackage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHardenedFSFixture(t, filepath.Join(root, "policy", "hardened-fs-baseline.tsv"), "")
	if err := metadatautil.CheckHardenedFS(root); err == nil {
		t.Fatal("expected a missing trust-boundary package to fail")
	}
}

// A symlinked package root must fail. filepath.WalkDir reports the link entry
// and returns success, so every source below it would escape the scan while
// the check still reported a clean result.
func TestCheckHardenedFSRejectsASymlinkedPackageRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHardenedFSFixture(t, filepath.Join(root, "policy", "hardened-fs-baseline.tsv"), "")
	writeHardenedFSFixture(t, filepath.Join(root, "host_impl", "state.go"),
		"package host\n\nimport \"os\"\n\nfunc read() { os.ReadFile(path) }\n")
	writeHardenedFSPackageFixtures(t, root, "internal/host")
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatalf("create the internal directory: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "host_impl"), filepath.Join(root, "internal", "host")); err != nil {
		t.Fatalf("create the symlinked package root: %v", err)
	}
	if err := metadatautil.CheckHardenedFS(root); err == nil {
		t.Fatal("expected a symlinked trust-boundary package root to fail")
	}
}

// writeHardenedFSPackageFixtures gives every trust-boundary package one source,
// because the check requires each one to contribute a scanned file. The list
// comes from the check itself, so a new package needs no fixture edit.
func writeHardenedFSPackageFixtures(t *testing.T, root string, skip ...string) {
	t.Helper()
	skipped := map[string]bool{}
	for _, name := range skip {
		skipped[name] = true
	}
	for _, pkg := range metadatautil.HardenedFSPackages() {
		if skipped[pkg] {
			continue
		}
		writeHardenedFSFixture(t, filepath.Join(root, filepath.FromSlash(pkg), "doc.go"),
			"package "+filepath.Base(pkg)+"\n")
	}
}

func writeHardenedFSFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}
}
