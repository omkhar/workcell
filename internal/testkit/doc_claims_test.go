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
// A non-empty citools is the workcell-citools binary the script runs, for a
// fixture that holds no Go module.
func runDocLinks(t *testing.T, dir, citools string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(dir, "scripts", "check-doc-links.sh"))
	cmd.Dir = dir
	if citools != "" {
		cmd.Env = append(os.Environ(), "DOC_CLAIMS_CITOOLS="+citools)
	}
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
	out, err := runDocLinks(t, repoRoot(t), "")
	if err != nil {
		t.Fatalf("check-doc-links.sh failed on the checkout: %v\n%s", err, out)
	}
}

func TestDocClaimsNegativeControls(t *testing.T) {
	citools := filepath.Join(t.TempDir(), "workcell-citools")
	build := exec.Command("go", "build", "-o", citools, "./cmd/workcell-citools")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workcell-citools: %v\n%s", err, out)
	}
	sources := map[string]string{}
	for _, rel := range []string{"scripts/check-doc-links.sh", "scripts/lib/doc-claims.awk", "scripts/lib/md-unfenced.awk", "scripts/lib/go-run-env.sh"} {
		body, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
		if err != nil {
			t.Fatal(err)
		}
		sources[rel] = string(body)
	}
	// The comment lines of a baseline name the rules it governs.
	rules := "# Rules: missing-path escaping-path\n"
	cases := []struct {
		name     string
		doc      string
		baseline string
		base     string // committed baseline rows on main; empty means no baseline file there
		want     string // empty means the fixture must pass
	}{
		{"clean", "The gate `scripts/gate.sh` runs.\n", "", "", ""},
		{"missing path", "See `scripts/nope.sh` here.\n", "", "", "missing-path"},
		{"missing dot-slash path", "See `./scripts/nope.sh` here.\n", "", "", "missing-path"},
		{"missing spaced path", "See ` scripts/nope.sh ` here.\n", "", "", "missing-path"},
		{"missing internal path", "See `internal/nope/x.go` here.\n", "", "", "missing-path"},
		{"four-space tilde is not a fence", "    ~~~\nSee `scripts/nope.sh` here.\n", "", "", "missing-path"},
		{"tab-indented backticks are not a fence", "\t```\nSee [x](missing.md).\n", "", "", "broken link"},
		{"symlink to outside file", "See `scripts/ext.sh` here.\n", "", "", "escaping-path"},
		{"symlinked parent directory", "See `scripts/extdir/outside.txt` here.\n", "", "", "escaping-path"},
		{"an info string closes no fence", "```\n```text\n```\nSee `scripts/nope.sh` here.\n", "", "", "missing-path"},
		{"tilde-fenced path is skipped", "~~~bash\nSee `scripts/nope.sh` here.\n~~~\n", "", "", ""},
		{"a CRLF fence closes", "```\r\nx\r\n```\r\nSee `scripts/nope.sh` here.\r\n", "", "", "missing-path"},
		{"a multiline code span", "See `\nscripts/nope.sh\n` here.\n", "", "", "missing-path"},
		{"a code span does not cross a blank line", "stray `\n\nscripts/nope.sh` here.\n", "", "", ""},
		{"no main ref", "Nothing here.\n", "", "", "no origin/main or main ref"},
		{"unreadable base baseline", "See `scripts/nope.sh` here.\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "cannot read policy/doc-claims-baseline.tsv"},
		{"escaping path", "See `scripts/../../outside.txt` here.\n", "", "", "escaping-path"},
		{"traversal before the prefix", "See `./docs/../scripts/nope.sh` here.\n", "", "", "escaping-path"},
		{"traversal outside the checked areas", "See `./docs/../README.md` here.\n", "", "", ""},
		{"inline triple backticks are not a fence", "```inline``` text.\nSee `scripts/nope.sh` here.\n", "", "", "missing-path"},
		{"inline triple backticks keep links checked", "```inline``` text.\nSee [x](missing.md).\n", "", "", "broken link"},
		{"fenced path is skipped", "```bash\nSee `scripts/nope.sh` here.\n```\n", "", "", ""},
		{"baselined hit", "See `scripts/nope.sh` here.\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "", ""},
		{"stale baseline row", "Nothing here.\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "", "stale doc-claims baseline"},
		{"baseline growth", "See `scripts/nope.sh` here.\nSee `scripts/nope2.sh` here.\n", "README.md\tmissing-path\tscripts/nope.sh\tx\nREADME.md\tmissing-path\tscripts/nope2.sh\tx\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "not at the merge base"},
		{"baseline replacement", "See `scripts/nope2.sh` here.\n", "README.md\tmissing-path\tscripts/nope2.sh\tx\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "not at the merge base"},
		{"undeclared rule", "See `scripts/nope.sh` here.\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "", "rule missing-path is not named"},
		{"new rule rows need no base", "See `scripts/nope.sh` here.\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "README.md\tescaping-path\tscripts/ext.sh\tx\n", ""},
		{"baseline shrink", "See `scripts/nope.sh` here.\n", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "README.md\tmissing-path\tscripts/nope.sh\tx\nREADME.md\tmissing-path\tscripts/nope2.sh\tx\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range sources {
				writeFixtureFile(t, dir, rel, body)
			}
			writeFixtureFile(t, dir, "scripts/gate.sh", "true\n")
			writeFixtureFile(t, dir, "README.md", tc.doc)
			// A real file outside the fixture repository, which a traversal
			// span such as scripts/../../outside.txt reaches.
			writeFixtureFile(t, filepath.Dir(dir), "outside.txt", "x\n")
			// Symlinks that lead out of the fixture with no .. component.
			for link, target := range map[string]string{"scripts/ext.sh": "outside.txt", "scripts/extdir": ""} {
				if err := os.Symlink(filepath.Join(filepath.Dir(dir), target), filepath.Join(dir, link)); err != nil {
					t.Fatal(err)
				}
			}
			git := func(args ...string) {
				t.Helper()
				full := append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
				if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			branch := "main"
			if tc.name == "no main ref" {
				branch = "topic"
			}
			git("init", "-q", "-b", branch)
			// The base commit holds the baseline only when tc.base names rows.
			if tc.base != "" {
				header := rules
				if tc.name == "new rule rows need no base" {
					// The merge-base baseline predates the missing-path rule.
					header = strings.ReplaceAll(rules, "missing-path", "x")
				}
				writeFixtureFile(t, dir, "policy/doc-claims-baseline.tsv", header+tc.base)
			}
			git("add", "-A")
			git("commit", "-q", "-m", "base")
			baselineHeader := rules
			if tc.name == "undeclared rule" {
				baselineHeader = strings.ReplaceAll(rules, "missing-path", "x")
			}
			writeFixtureFile(t, dir, "policy/doc-claims-baseline.tsv", baselineHeader+tc.baseline)
			git("add", "-A")
			if tc.name == "unreadable base baseline" {
				// Drop the committed baseline blob so the merge-base read fails.
				blob, err := exec.Command("git", "-C", dir, "rev-parse", "main:policy/doc-claims-baseline.tsv").Output()
				if err != nil {
					t.Fatal(err)
				}
				id := strings.TrimSpace(string(blob))
				if err := os.Remove(filepath.Join(dir, ".git", "objects", id[:2], id[2:])); err != nil {
					t.Fatal(err)
				}
			}
			out, err := runDocLinks(t, dir, citools)
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
