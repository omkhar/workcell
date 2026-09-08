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

// The scan reads the calls the source makes, not the text that names them.
// Every "no finding" row is a bypass the reviewer found against a validator
// that read a source as text: a comment, a string literal, a raw literal, and
// a longer identifier that ends with the banned name.
func TestHardenedFSFindings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   int
	}{
		{"plain call", "func f() { os.Open(path) }\n", 1},
		{"two calls on one line", "func f() { os.Stat(a); os.Stat(b) }\n", 2},
		{"every banned symbol", "func f() {\nos.Open(a)\nos.OpenFile(a)\nos.ReadFile(a)\nos.WriteFile(a)\nos.Create(a)\nos.MkdirAll(a)\nos.Rename(a, b)\nos.Stat(a)\n}\n", 8},

		{"line comment", "// os.Open(path) is banned\n", 0},
		{"block comment", "/*\nos.Open(path)\n*/\n", 0},
		{"interpreted literal", "var s = \"os.Open(\"\n", 0},
		{"raw literal", "var s = `os.Open(path)`\n", 0},
		{"a longer identifier is not the call", "func f() { workcellos.Open(path) }\n", 0},
		{"a method of another value", "func f() { logos.Open(path) }\n", 0},
		{"no argument list", "var f = os.Open\n", 0},
		{"the line states its reason", "func f() { os.Open(path) } // hardened-fs-exempt: the path is a build constant\n", 0},
		{"rootio is not raw", "func f() { rootio.ReadFileNoFollow(path, label, limit) }\n", 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			findings := metadatautil.HardenedFSFindings(testCase.source)
			if len(findings) != testCase.want {
				t.Fatalf("HardenedFSFindings() = %v, want %d finding(s)", findings, testCase.want)
			}
		})
	}
}

func TestCheckHardenedFSRatchet(t *testing.T) {
	t.Parallel()
	const oneCall = "package host\n\nfunc read() { os.ReadFile(path) }\n"
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
			source:   "package host\n\nfunc read() { os.ReadFile(path) } // hardened-fs-exempt: a build constant\n",
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
			for _, pkg := range []string{"applecontainer", "authpolicy", "authresolve", "injection", "runtimeutil"} {
				writeHardenedFSFixture(t, filepath.Join(root, "internal", pkg, "doc.go"), "package "+pkg+"\n")
			}
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

func writeHardenedFSFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}
}
