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
	cases := []struct {
		name     string
		doc      string
		lane     string // validate-repo.sh body; empty means it runs scripts/gate.sh
		baseline string
		base     string // committed baseline rows on main; empty means no commit
		want     string // empty means the fixture must pass
	}{
		{"clean", "The gate `scripts/gate.sh` runs.\n", "", "", "", ""},
		{"missing path", "See `scripts/nope.sh` here.\n", "", "", "", "missing-path"},
		{"missing dot-slash path", "See `./scripts/nope.sh` here.\n", "", "", "", "missing-path"},
		{"missing spaced path", "See ` scripts/nope.sh ` here.\n", "", "", "", "missing-path"},
		{"missing internal path", "See `internal/nope/x.go` here.\n", "", "", "", "missing-path"},
		{"unwired script", "See `scripts/orphan.sh` here.\n", "", "", "", "unwired-script"},
		{"unwired dot-slash script", "See `./scripts/orphan.sh` here.\n", "", "", "", "unwired-script"},
		{"wired by root variable", "See `scripts/orphan.sh` here.\n", "\"${ROOT_DIR}/scripts/orphan.sh\" --flag\n", "", "", ""},
		{"wired after keyword", "See `scripts/orphan.sh` here.\n", "if ! ./scripts/orphan.sh; then exit 1; fi\n", "", "", ""},
		{"comment is not wiring", "See `scripts/orphan.sh` here.\n", "# scripts/orphan.sh is intentionally not run\n", "", "", "unwired-script"},
		{"echo is not wiring", "See `scripts/orphan.sh` here.\n", "echo \"scripts/orphan.sh\"\n", "", "", "unwired-script"},
		{"longer name is not wiring", "See `scripts/orphan.sh` here.\n", "scripts/orphan.sh.bak\n", "", "", "unwired-script"},
		{"array member is not wiring", "See `scripts/orphan.sh` here.\n", "files=(\n  \"${ROOT_DIR}/scripts/orphan.sh\"\n)\n", "", "", "unwired-script"},
		{"case pattern is not wiring", "See `scripts/orphan.sh` here.\n", "case x in\n  scripts/orphan.sh | y)\n    true\n    ;;\nesac\n", "", "", "unwired-script"},
		{"continued argument is not wiring", "See `scripts/orphan.sh` here.\n", "shellcheck \\\n  scripts/orphan.sh\n", "", "", "unwired-script"},
		{"heredoc body is not wiring", "See `scripts/orphan.sh` here.\n", "cat <<EOF\nscripts/orphan.sh\nEOF\n", "", "", "unwired-script"},
		{"quoted separator is not wiring", "See `scripts/orphan.sh` here.\n", "echo \"a; scripts/orphan.sh\"\n", "", "", "unwired-script"},
		{"uncalled function is not wiring", "See `scripts/orphan.sh` here.\n", "f() {\n  scripts/orphan.sh\n}\n", "", "", "unwired-script"},
		{"called function is wiring", "See `scripts/orphan.sh` here.\n", "f() {\n  scripts/orphan.sh\n}\nf\n", "", "", ""},
		{"wired after an exit in an if", "See `scripts/orphan.sh` here.\n", "if [ -n \"$x\" ]; then\n  exit 1\nfi\nscripts/orphan.sh\n", "", "", ""},
		{"false && is not wiring", "See `scripts/orphan.sh` here.\n", "false && scripts/orphan.sh\n", "", "", "unwired-script"},
		{"if false is not wiring", "See `scripts/orphan.sh` here.\n", "if false; then\n  scripts/orphan.sh\nfi\n", "", "", "unwired-script"},
		{"after an exit is not wiring", "See `scripts/orphan.sh` here.\n", "exit 0\nscripts/orphan.sh\n", "", "", "unwired-script"},
		{"four-space tilde is not a fence", "    ~~~\nSee `scripts/nope.sh` here.\n", "", "", "", "missing-path"},
		{"tab-indented backticks are not a fence", "\t```\nSee [x](missing.md).\n", "", "", "", "broken link"},
		{"wired in a case branch", "See `scripts/orphan.sh` here.\n", "case x in\n  y) scripts/orphan.sh ;;\nesac\n", "", "", ""},
		{"symlink to outside file", "See `scripts/ext.sh` here.\n", "", "", "", "escaping-path"},
		{"symlinked parent directory", "See `scripts/extdir/outside.txt` here.\n", "", "", "", "escaping-path"},
		{"an info string closes no fence", "```\n```text\n```\nSee `scripts/nope.sh` here.\n", "", "", "", "missing-path"},
		{"tilde-fenced path is skipped", "~~~bash\nSee `scripts/nope.sh` here.\n~~~\n", "", "", "", ""},
		{"unreadable base baseline", "See `scripts/orphan.sh` here.\n", "", "README.md\tunwired-script\tscripts/orphan.sh\tx\n", "README.md\tunwired-script\tscripts/orphan.sh\tx\n", "cannot read policy/doc-claims-baseline.tsv"},
		{"escaping path", "See `scripts/../../outside.txt` here.\n", "", "", "", "escaping-path"},
		{"inline triple backticks are not a fence", "```inline``` text.\nSee `scripts/nope.sh` here.\n", "", "", "", "missing-path"},
		{"inline triple backticks keep links checked", "```inline``` text.\nSee [x](missing.md).\n", "", "", "", "broken link"},
		{"fenced path is skipped", "```bash\nSee `scripts/nope.sh` here.\n```\n", "", "", "", ""},
		{"baselined hit", "See `scripts/orphan.sh` here.\n", "", "README.md\tunwired-script\tscripts/orphan.sh\tx\n", "", ""},
		{"stale baseline row", "Nothing here.\n", "", "README.md\tunwired-script\tscripts/orphan.sh\tx\n", "", "stale doc-claims baseline"},
		{"baseline growth", "See `scripts/orphan.sh` here.\nSee `scripts/nope.sh` here.\n", "", "README.md\tmissing-path\tscripts/nope.sh\tx\nREADME.md\tunwired-script\tscripts/orphan.sh\tx\n", "README.md\tunwired-script\tscripts/orphan.sh\tx\n", "not at the merge base"},
		{"baseline replacement", "See `scripts/nope.sh` here.\n", "", "README.md\tmissing-path\tscripts/nope.sh\tx\n", "README.md\tunwired-script\tscripts/orphan.sh\tx\n", "not at the merge base"},
		{"baseline shrink", "See `scripts/orphan.sh` here.\n", "", "README.md\tunwired-script\tscripts/orphan.sh\tx\n", "README.md\tmissing-path\tscripts/nope.sh\tx\nREADME.md\tunwired-script\tscripts/orphan.sh\tx\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range sources {
				writeFixtureFile(t, dir, rel, body)
			}
			lane := tc.lane
			if lane == "" {
				lane = "scripts/gate.sh\n"
			}
			writeFixtureFile(t, dir, "scripts/validate-repo.sh", lane)
			writeFixtureFile(t, dir, "scripts/ci/job-x.sh", "true\n")
			writeFixtureFile(t, dir, ".github/workflows/x.yml", "name: x\n")
			writeFixtureFile(t, dir, "scripts/gate.sh", "true\n")
			writeFixtureFile(t, dir, "scripts/orphan.sh", "true\n")
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
			git("init", "-q", "-b", "main")
			if tc.base != "" {
				writeFixtureFile(t, dir, "policy/doc-claims-baseline.tsv", "# test\n"+tc.base)
				git("add", "-A")
				git("commit", "-q", "-m", "base")
			}
			writeFixtureFile(t, dir, "policy/doc-claims-baseline.tsv", "# test\n"+tc.baseline)
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
