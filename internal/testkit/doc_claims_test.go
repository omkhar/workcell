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
	// The old scanner cut a claim to its first 100 characters, so a suffix
	// change kept the old key. The key here is that cut form.
	longClaim := "The launcher rejects every unsafe input that reaches the provider wrapper from any operator-supplied flag or file and every MCP file."
	cutKey := longClaim[:100]
	cutKey = cutKey[:strings.LastIndex(cutKey, " ")]
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
		{"wired after an exit in an if", "See `scripts/orphan.sh` here.\n", "if true; then\n  exit 1\nfi\nscripts/orphan.sh\n", "", "", ""},
		{"wired in a case branch", "See `scripts/orphan.sh` here.\n", "case x in\n  y) scripts/orphan.sh ;;\nesac\n", "", "", ""},
		{"escaping path", "The launcher rejects bad input.\nSee `scripts/../../outside.txt` here.\n", "", "", "", "escaping-path"},
		{"unanchored claim", "The launcher rejects bad input.\n", "", "", "", "unanchored-claim"},
		{"blocks claim", "Workcell blocks operator launch.\n", "", "", "", "unanchored-claim"},
		{"denies claim", "Workcell denies repository MCP files.\n", "", "", "", "unanchored-claim"},
		{"prevents claim", "The mount prevents writes.\n", "", "", "", "unanchored-claim"},
		{"enforces claim", "The wrapper enforces the policy.\n", "", "", "", "unanchored-claim"},
		{"fails claim", "The check fails closed.\n", "", "", "", "unanchored-claim"},
		{"requires claim", "The launcher requires a pinned image.\n", "", "", "", "unanchored-claim"},
		{"guarantees claim", "The seal guarantees integrity.\n", "", "", "", "unanchored-claim"},
		{"claim wrapped across lines", "The policy is enforced\nby the launcher.\n", "", "", "", "unanchored-claim"},
		{"inline triple backticks are not a fence", "```inline``` text.\nSee `scripts/nope.sh` here.\n", "", "", "", "missing-path"},
		{"inline triple backticks keep links checked", "```inline``` text.\nSee [x](missing.md).\n", "", "", "", "broken link"},
		{"fenced claim is skipped", "```bash\nThe launcher rejects bad input.\n```\n", "", "", "", ""},
		{"anchored claim", "The launcher rejects bad input.\nIt runs `scripts/gate.sh` first.\n", "", "", "", ""},
		{"claim three lines away", "The launcher rejects bad input.\n\n\nIt runs `scripts/gate.sh` first.\n", "", "", "", "unanchored-claim"},
		{"baselined claim", "The launcher rejects bad input.\n", "", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\n", "", ""},
		{"long claim keeps its full text", longClaim + "\n", "", "README.md\tunanchored-claim\t" + cutKey + "\tx\n", "", "unbaselined doc claim hit"},
		{"stale baseline row", "Nothing here.\n", "", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\n", "", "stale doc-claims baseline"},
		{"baseline growth", "The launcher rejects bad input.\nWorkcell blocks operator launch.\n", "", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\nREADME.md\tunanchored-claim\tWorkcell blocks operator launch.\tx\n", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\n", "not at the merge base"},
		{"baseline replacement", "Workcell blocks operator launch.\n", "", "README.md\tunanchored-claim\tWorkcell blocks operator launch.\tx\n", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\n", "not at the merge base"},
		{"baseline shrink", "The launcher rejects bad input.\n", "", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\n", "README.md\tunanchored-claim\tThe launcher rejects bad input.\tx\nREADME.md\tunanchored-claim\tWorkcell blocks operator launch.\tx\n", ""},
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
