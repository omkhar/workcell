// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runDocLinks runs scripts/check-doc-links.sh in dir and returns its output.
func runDocLinks(t *testing.T, dir string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(dir, "scripts", "check-doc-links.sh"))
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFixtureFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDocClaimsPassOnCheckout(t *testing.T) {
	out, err := runDocLinks(t, repoRoot(t))
	if err != nil {
		t.Fatalf("check-doc-links.sh failed on the checkout: %v\n%s", err, out)
	}
}

func TestDocClaimsNegativeControls(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "check-doc-links.sh"))
	if err != nil {
		t.Fatal(err)
	}
	awkSrc, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "lib", "doc-claims.awk"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		doc      string
		baseline string
		want     string // empty means the fixture must pass
	}{
		{"clean", "The gate `scripts/gate.sh` runs.\n", "", ""},
		{"missing path", "See `scripts/nope.sh` here.\n", "", "missing-path"},
		{"missing internal path", "See `internal/nope/x.go` here.\n", "", "missing-path"},
		{"unwired script", "See `scripts/orphan.sh` here.\n", "", "unwired-script"},
		{"unanchored claim", "The launcher rejects bad input.\n", "", "unanchored-claim"},
		{"anchored claim", "The launcher rejects bad input.\nIt runs `scripts/gate.sh` first.\n", "", ""},
		{"claim three lines away", "The launcher rejects bad input.\n\n\nIt runs `scripts/gate.sh` first.\n", "", "unanchored-claim"},
		{"baselined claim", "The launcher rejects bad input.\n", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\n", ""},
		{"stale baseline row", "Nothing here.\n", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\n", "stale doc-claims baseline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixtureFile(t, dir, "scripts/check-doc-links.sh", string(script))
			writeFixtureFile(t, dir, "scripts/lib/doc-claims.awk", string(awkSrc))
			writeFixtureFile(t, dir, "scripts/validate-repo.sh", "scripts/gate.sh\n")
			writeFixtureFile(t, dir, "scripts/ci/job-x.sh", "true\n")
			writeFixtureFile(t, dir, ".github/workflows/x.yml", "name: x\n")
			writeFixtureFile(t, dir, "scripts/gate.sh", "true\n")
			writeFixtureFile(t, dir, "scripts/orphan.sh", "true\n")
			writeFixtureFile(t, dir, "README.md", tc.doc)
			writeFixtureFile(t, dir, "policy/doc-claims-baseline.tsv", "# test\n"+tc.baseline)
			if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, out)
			}
			if out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
				t.Fatalf("git add: %v\n%s", err, out)
			}
			out, err := runDocLinks(t, dir)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want pass, got %v\n%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("want failure containing %q, got err=%v\n%s", tc.want, err, out)
			}
		})
	}
}
