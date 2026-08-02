// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package publishpr

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsTrustedHostToolPathAcceptsAllowedPrefix(t *testing.T) {
	t.Parallel()
	ctx := &BashContext{RootDir: "/work/root", WorkspaceRoot: "/work/ws"}
	cases := []string{
		"/usr/bin/git",
		"/opt/homebrew/bin/gh",
		"/Applications/Docker.app/Contents/Resources/bin/docker",
	}
	for _, p := range cases {
		if !IsTrustedHostToolPath(p, ctx) {
			t.Errorf("IsTrustedHostToolPath(%q) = false, want true", p)
		}
	}
}

func TestIsTrustedHostToolPathRejectsWorkspacePaths(t *testing.T) {
	t.Parallel()
	ctx := &BashContext{RootDir: "/work/root", WorkspaceRoot: "/work/ws"}
	cases := []string{
		"/work/root/bin/gh",
		"/work/ws/scripts/gh",
		"/tmp/gh",
		"relative/gh",
		"",
	}
	for _, p := range cases {
		if IsTrustedHostToolPath(p, ctx) {
			t.Errorf("IsTrustedHostToolPath(%q) = true, want false", p)
		}
	}
}

func TestIsTrustedHostToolPathRequiresExactOrSlashPrefix(t *testing.T) {
	t.Parallel()
	ctx := &BashContext{}
	if IsTrustedHostToolPath("/usr/bin", ctx) != true {
		t.Errorf("exact prefix match should be trusted")
	}
	if IsTrustedHostToolPath("/usr/bin-evil/git", ctx) != false {
		t.Errorf("/usr/bin-evil should not be trusted")
	}
}

func TestBashQuoteShellSafe(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                      "''",
		"abc":                   "abc",
		"a-b_c.d":               "a-b_c.d",
		"/usr/bin/git":          "/usr/bin/git",
		"with space":            `with\ space`,
		"feature/x":             "feature/x",
		"+refs/heads/x":         "+refs/heads/x",
		"Existing branch title": `Existing\ branch\ title`,
	}
	for in, want := range cases {
		got := bashQuote(in)
		if got != want {
			t.Errorf("bashQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmitCommandMatchesBashPrintfQ(t *testing.T) {
	t.Parallel()
	// tests/scenarios/shared/test-publish-pr-dry-run.sh greps for the
	// exact backslash-escape form `Existing\ branch\ title` against
	// the line `gh pr create ... --title Existing\ branch\ title
	// --draft`, so we must emit that byte-for-byte.
	var b bytes.Buffer
	EmitCommand(&b, []string{"gh", "pr", "create", "--title", "Existing branch title"})
	got := b.String()
	want := `  gh pr create --title Existing\ branch\ title ` + "\n"
	if got != want {
		t.Errorf("EmitCommand mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRemoteOriginPushURLUsesHostGitConfiguration(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	repository := filepath.Join(root, "repo")
	xdgConfigHome := filepath.Join(root, "xdg")
	if err := os.MkdirAll(filepath.Join(xdgConfigHome, "git"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(gitBin, args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	run("init", "-q", repository)
	run("-C", repository, "remote", "add", "origin", "https://push-alias.invalid/example/repo.git")
	run(
		"config",
		"--file", filepath.Join(xdgConfigHome, "git", "config"),
		"url.https://github.com/example/repo.git.pushInsteadOf",
		"https://push-alias.invalid/example/repo.git",
	)
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	ctx := &BashContext{
		HostGitBin:      gitBin,
		TrustedHostPath: filepath.Dir(gitBin) + ":/usr/bin:/bin",
		RealHome:        root,
	}
	got, err := remoteOriginPushURL(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://github.com/example/repo.git"; got != want {
		t.Errorf("remoteOriginPushURL() = %q, want %q", got, want)
	}
}

func TestParseBashContextFlagsConsumesPrefix(t *testing.T) {
	t.Parallel()
	args := []string{
		"--bash-root-dir=/r",
		"--bash-workspace-root=/w",
		"--bash-real-home=/h",
		"--bash-trusted-host-path=/usr/bin:/bin",
		"--bash-host-git-bin=/opt/homebrew/bin/git",
		"--bash-host-gh-bin=/opt/homebrew/bin/gh",
		"--branch", "feature/x",
		"--title", "T",
	}
	ctx, rest := parseBashContextFlags(args)
	if ctx.RootDir != "/r" || ctx.WorkspaceRoot != "/w" || ctx.RealHome != "/h" {
		t.Errorf("unexpected ctx: %+v", ctx)
	}
	if ctx.TrustedHostPath != "/usr/bin:/bin" {
		t.Errorf("TrustedHostPath = %q", ctx.TrustedHostPath)
	}
	if ctx.HostGitBin != "/opt/homebrew/bin/git" || ctx.HostGhBin != "/opt/homebrew/bin/gh" {
		t.Errorf("unexpected bin: git=%q gh=%q", ctx.HostGitBin, ctx.HostGhBin)
	}
	if len(rest) != 4 || rest[0] != "--branch" {
		t.Errorf("rest mismatch: %v", rest)
	}
}
