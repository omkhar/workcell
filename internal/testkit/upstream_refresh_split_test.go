// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// splitFixtureFiles are the repository files a mixed refresh changes.
var splitFixtureFiles = []string{
	".github/workflows/release.yml",
	"policy/provider-bumps.toml",
	"runtime/container/Dockerfile",
	"runtime/container/debian-bootstrap.env",
	"runtime/container/providers/package-lock.json",
	"runtime/container/providers/package.json",
}

// splitEdit rewrites the first match of pattern in one fixture file.
func splitEdit(t *testing.T, repo, rel, pattern, replacement string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(pattern)
	loc := re.FindIndex(data)
	if loc == nil {
		t.Fatalf("%s has no match for %q", rel, pattern)
	}
	edited := string(data[:loc[0]]) + re.ReplaceAllString(string(data[loc[0]:loc[1]]), replacement) + string(data[loc[1]:])
	mustWrite(t, path, []byte(edited), 0o644)
}

// splitApplyProviderBump edits the files the provider updater owns, the way a
// Claude and Gemini bump does.
func splitApplyProviderBump(t *testing.T, repo string) {
	t.Helper()
	splitEdit(t, repo, "runtime/container/Dockerfile", `(?m)^ARG CLAUDE_VERSION=.*$`, "ARG CLAUDE_VERSION=9.9.9")
	for range 2 {
		splitEdit(t, repo, "runtime/container/Dockerfile", `(?m)^(\s+)CLAUDE_SHA256="[0-9a-f]{64}"`, `${1}CLAUDE_SHA256="`+strings.Repeat("e", 64)+`"`)
	}
	splitEdit(t, repo, "runtime/container/providers/package.json", `"@google/gemini-cli": "[^"]+"`, `"@google/gemini-cli": "9.9.9"`)
	splitEdit(t, repo, "runtime/container/providers/package-lock.json", `"@google/gemini-cli": "[^"]+"`, `"@google/gemini-cli": "9.9.9"`)
	splitEdit(t, repo, "runtime/container/providers/package-lock.json",
		`("node_modules/@google/gemini-cli": \{\n\s+"version": )"[^"]+",(\n\s+"resolved": "[^"]+/gemini-cli-)[^"]+\.tgz",(\n\s+"integrity": )"[^"]+"`,
		`${1}"9.9.9",${2}9.9.9.tgz",${3}"sha512-`+strings.Repeat("A", 86)+`=="`)
}

// splitApplyToolchainBump edits files the toolchain updater owns, one of them
// in the same Dockerfile as the provider pins.
func splitApplyToolchainBump(t *testing.T, repo string, workflow bool) {
	t.Helper()
	splitEdit(t, repo, "runtime/container/Dockerfile", `(?m)^ARG GO_VERSION=.*$`, "ARG GO_VERSION=9.9.9")
	splitEdit(t, repo, "runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_SNAPSHOT=.*$`, "DEBIAN_SNAPSHOT=20991231T000000Z")
	if workflow {
		splitEdit(t, repo, ".github/workflows/release.yml", `(?m)^  WORKCELL_COSIGN_VERSION: .*$`, "  WORKCELL_COSIGN_VERSION: v9.9.9")
	}
}

type splitCandidate struct {
	Candidate    string   `json:"candidate"`
	BaseSHA      string   `json:"base_sha"`
	TreeOID      string   `json:"tree_oid"`
	ChangedFiles []string `json:"changed_files"`
}

// splitChangedLines returns the removed and added lines of a patch, keyed by
// file, so two patches can be checked for a shared line.
func splitChangedLines(patch string) map[string]bool {
	lines := map[string]bool{}
	file := ""
	for line := range strings.Lines(patch) {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			file = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			lines[file+"\x00"+line] = true
		}
	}
	return lines
}

// TestUpstreamRefreshSplitDryRun runs the refresh split locally. It applies a
// mixed provider and toolchain refresh in the order the workflow does, builds
// both candidates, and checks them with the real scope guard and the real
// publish script against a fake gh.
func TestUpstreamRefreshSplitDryRun(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"jq", "shasum", "base64"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	for _, tc := range []struct {
		name     string
		workflow bool
	}{
		{name: "toolchain changes a workflow", workflow: true},
		{name: "toolchain changes no workflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			mustMkdir(t, repo)
			publishGit(t, repo, nil, "init", "-q")
			for _, rel := range splitFixtureFiles {
				data, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
				if err != nil {
					t.Fatal(err)
				}
				mustMkdir(t, filepath.Dir(filepath.Join(repo, filepath.FromSlash(rel))))
				mustWrite(t, filepath.Join(repo, filepath.FromSlash(rel)), data, 0o644)
			}
			ident := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
			publishGit(t, repo, nil, "add", "-A")
			publishGit(t, repo, ident, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
			base := publishGit(t, repo, nil, "rev-parse", "HEAD")

			out := filepath.Join(root, "candidate")
			env := []string{"GITHUB_REPOSITORY=o/r", "GITHUB_RUN_ID=42", "GITHUB_REF=refs/heads/main", "TMPDIR=" + root, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}
			build := func(kind string) {
				t.Helper()
				cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-candidate.sh"), kind, out)
				cmd.Dir = repo
				cmd.Env = append(os.Environ(), env...)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("build %s candidate: %v\n%s", kind, err, output)
				}
			}
			splitApplyProviderBump(t, repo)
			build("provider")
			// The builder restores HEAD, so the toolchain refresh starts clean.
			if status := publishGit(t, repo, nil, "status", "--porcelain"); status != "" {
				t.Fatalf("builder left the tree dirty:\n%s", status)
			}
			splitApplyToolchainBump(t, repo, tc.workflow)
			build("toolchain")

			patches := map[string]string{}
			metas := map[string]splitCandidate{}
			for _, kind := range []string{"provider", "toolchain"} {
				patch, err := os.ReadFile(filepath.Join(out, kind, "patch"))
				if err != nil {
					t.Fatal(err)
				}
				patches[kind] = string(patch)
				raw, err := os.ReadFile(filepath.Join(out, kind, "metadata.json"))
				if err != nil {
					t.Fatal(err)
				}
				var meta splitCandidate
				if err := json.Unmarshal(raw, &meta); err != nil {
					t.Fatal(err)
				}
				if meta.Candidate != kind || meta.BaseSHA != base {
					t.Fatalf("%s metadata = %+v, want candidate %s on base %s", kind, meta, kind, base)
				}
				metas[kind] = meta
				// Each candidate applies to the base alone and gives its tree.
				publishGit(t, repo, nil, "apply", "--index", "--binary", filepath.Join(out, kind, "patch"))
				if tree := publishGit(t, repo, nil, "write-tree"); tree != meta.TreeOID {
					t.Fatalf("%s patch applied alone gives tree %s, want %s", kind, tree, meta.TreeOID)
				}
				publishGit(t, repo, nil, "reset", "-q", "--hard", base)
			}

			// The two patches share no changed line. Their paths are disjoint,
			// except the Dockerfile that holds pins of both kinds.
			providerLines := splitChangedLines(patches["provider"])
			for line := range splitChangedLines(patches["toolchain"]) {
				if providerLines[line] {
					t.Fatalf("both candidates change %q", line)
				}
			}
			for _, path := range metas["toolchain"].ChangedFiles {
				for _, other := range metas["provider"].ChangedFiles {
					if path == other && path != "runtime/container/Dockerfile" {
						t.Fatalf("both candidates change %s", path)
					}
				}
			}
			wantWorkflow := false
			for _, path := range metas["toolchain"].ChangedFiles {
				wantWorkflow = wantWorkflow || path == ".github/workflows/release.yml"
			}
			if wantWorkflow != tc.workflow {
				t.Fatalf("toolchain changed files = %q, workflow change %v", metas["toolchain"].ChangedFiles, tc.workflow)
			}

			guard := filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-scope-guard.sh")
			if output, err := exec.Command(guard, filepath.Join(out, "provider", "patch")).CombinedOutput(); err != nil {
				t.Fatalf("scope guard rejected the provider candidate: %v\n%s", err, output)
			}
			if output, err := exec.Command(guard, filepath.Join(out, "toolchain", "patch")).CombinedOutput(); err == nil {
				t.Fatalf("scope guard accepted the toolchain candidate:\n%s", output)
			}

			bin := writePublishFakeGH(t, root)
			ghLog := filepath.Join(root, "gh.log")
			audit := filepath.Join(root, "audit.md")
			cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-publish.sh"), out, "passed", audit)
			cmd.Dir = repo
			cmd.Env = append(append(os.Environ(), env...),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GH_TOKEN=unused", "FAKE_MAIN_SHA="+base, "FAKE_PARENT="+base, "FAKE_GH_LOG="+ghLog,
				"FAKE_ALLOW_WRITES=1", "FAKE_COMMIT="+strings.Repeat("c", 40))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("publish failed: %v\n%s", err, output)
			}
			logData, _ := os.ReadFile(ghLog)
			auditData, _ := os.ReadFile(audit)
			gh, auditText := string(logData), string(auditData)
			for _, want := range []string{
				"--head codex/upstream-refresh-provider-42 --title Refresh pinned upstreams (provider)",
				"pr merge --repo o/r --auto --merge --match-head-commit",
			} {
				if !strings.Contains(gh, want) {
					t.Fatalf("gh calls = %q, want %q", gh, want)
				}
			}
			if strings.Count(gh, "pr merge") != 1 {
				t.Fatalf("gh calls = %q, want auto-merge for the provider PR only", gh)
			}
			if tc.workflow {
				if strings.Contains(gh, "upstream-refresh-toolchain-42") {
					t.Fatalf("the App published a toolchain candidate that changes a workflow:\n%s", gh)
				}
				if want := "./scripts/publish-upstream-refresh-pr.sh --run-id 42 --candidate toolchain"; !strings.Contains(auditText, want) {
					t.Fatalf("audit = %q, want the host command %q", auditText, want)
				}
				return
			}
			for _, want := range []string{
				"--head codex/upstream-refresh-toolchain-42 --title Refresh pinned upstreams (toolchain)",
				"--add-label needs-human-review",
			} {
				if !strings.Contains(gh, want) {
					t.Fatalf("gh calls = %q, want %q", gh, want)
				}
			}
			// The guard and the verify scripts cover the provider candidate only.
			bodies, _ := os.ReadFile(ghLog + ".bodies")
			if strings.Count(string(bodies), "scope guard: passed") != 1 || strings.Count(string(bodies), "scope guard: not-run") != 1 {
				t.Fatalf("PR bodies = %q, want scope guard passed for provider and not-run for toolchain", bodies)
			}
			if strings.Count(auditText, "cool-off:") != 1 {
				t.Fatalf("audit = %q, want the cool-off claim for the provider candidate only", auditText)
			}
			if strings.Count(auditText, "verify-upstream-*-release.sh") != 1 {
				t.Fatalf("audit = %q, want the release verify claim for the provider candidate only", auditText)
			}
		})
	}
}
