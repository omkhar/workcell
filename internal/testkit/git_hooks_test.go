// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"bytes"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

type gitHooksFixture struct {
	t       *testing.T
	root    string
	homeDir string
	tmpDir  string
	remote  string
	git     string
}

func newGitHooksFixture(t *testing.T) *gitHooksFixture {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git lookup failed: %v", err)
	}
	stateRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("state root resolution failed: %v", err)
	}
	fixture := &gitHooksFixture{
		t:       t,
		root:    filepath.Join(stateRoot, "repo"),
		homeDir: filepath.Join(stateRoot, "home"),
		tmpDir:  filepath.Join(stateRoot, "tmp"),
		remote:  filepath.Join(stateRoot, "remote.git"),
		git:     gitBin,
	}
	for _, dir := range []string{fixture.root, fixture.homeDir, fixture.tmpDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s failed: %v", dir, err)
		}
	}
	repo := repoRoot(t)
	for _, relative := range []string{
		filepath.Join(".githooks", "commit-msg"),
		filepath.Join(".githooks", "pre-push"),
	} {
		source, err := os.ReadFile(filepath.Join(repo, relative))
		if err != nil {
			t.Fatalf("read %s failed: %v", relative, err)
		}
		target := filepath.Join(fixture.root, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir for %s failed: %v", relative, err)
		}
		if err := os.WriteFile(target, source, 0o755); err != nil {
			t.Fatalf("write %s failed: %v", relative, err)
		}
	}
	fixture.run("init", "--quiet", "--initial-branch=main")
	fixture.run("config", "core.hooksPath", ".githooks")
	fixture.run("config", "user.name", "Workcell Test")
	fixture.run("config", "user.email", "workcell-test@example.invalid")
	runGitHooksCommand(t, gitBin, stateRoot, fixture.env(), "init", "--quiet", "--bare", fixture.remote)
	fixture.run("remote", "add", "origin", fixture.remote)
	return fixture
}

func (f *gitHooksFixture) env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + f.homeDir,
		"TMPDIR=" + f.tmpDir,
		"LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1",
	}
}

func (f *gitHooksFixture) run(args ...string) string {
	f.t.Helper()
	return runGitHooksCommand(f.t, f.git, f.root, f.env(), args...)
}

func (f *gitHooksFixture) tryGit(extraEnv []string, args ...string) (string, error) {
	f.t.Helper()
	cmd := exec.Command(f.git, args...)
	cmd.Dir = f.root
	cmd.Env = append(f.env(), extraEnv...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

func (f *gitHooksFixture) commitFile(name string, content string, message string, extraArgs ...string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, name), []byte(content), 0o644); err != nil {
		f.t.Fatalf("write %s failed: %v", name, err)
	}
	f.run("add", name)
	args := append([]string{"commit", "--quiet", "-m", message}, extraArgs...)
	f.run(args...)
}

func (f *gitHooksFixture) configureSSHSigning() {
	f.t.Helper()
	if _, err := user.Current(); err != nil {
		f.t.Skipf("ssh-keygen needs a resolvable user: %v", err)
	}
	keyPath := filepath.Join(f.homeDir, "signing_key")
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		f.t.Skipf("ssh-keygen unavailable: %v", err)
	}
	cmd := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-C", "workcell-test", "-f", keyPath)
	cmd.Env = f.env()
	if output, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("ssh-keygen failed: %v\n%s", err, output)
	}
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		f.t.Fatalf("read public key failed: %v", err)
	}
	fields := strings.Fields(string(publicKey))
	if len(fields) < 2 {
		f.t.Fatalf("unexpected public key format: %q", string(publicKey))
	}
	signers := filepath.Join(f.homeDir, "allowed_signers")
	signerLine := "workcell-test@example.invalid " + fields[0] + " " + fields[1] + "\n"
	if err := os.WriteFile(signers, []byte(signerLine), 0o644); err != nil {
		f.t.Fatalf("write allowed signers failed: %v", err)
	}
	globalConfig := strings.Join([]string{
		"[gpg]",
		"\tformat = ssh",
		"[gpg \"ssh\"]",
		"\tallowedSignersFile = " + signers,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(f.homeDir, ".gitconfig"), []byte(globalConfig), 0o644); err != nil {
		f.t.Fatalf("write global config failed: %v", err)
	}
	f.run("config", "gpg.format", "ssh")
	f.run("config", "gpg.ssh.allowedSignersFile", signers)
	f.run("config", "user.signingkey", keyPath)
}

func runGitHooksCommand(t *testing.T, gitBin string, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitBin, args...)
	cmd.Dir = dir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestCommitMsgHookAcceptsNotationSubjects(t *testing.T) {
	fixture := newGitHooksFixture(t)
	for index, subject := range []string{
		"^F Add branch filter flag (tests pass; user-visible CLI flag)",
		".r Rename parser helper (rename proof and focused tests pass; internal refactor)",
		"!B Patch cache collision (validation covers only this change; user-visible defect)",
		"@d Draft support note (evidence incomplete; secondary docs)",
		"Merge branch 'topic' into main",
		"Revert \"^F Add branch filter flag (tests pass; user-visible CLI flag)\"",
		"fixup! ^F Add branch filter flag",
	} {
		fixture.commitFile("file.txt", subject+"\n", subject)
		_ = index
	}
}

func TestCommitMsgHookRejectsInvalidSubjects(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("seed.txt", "seed\n", "^F Seed fixture history (tests pass; fixture seed)")
	for _, subject := range []string{
		"^S Resolve toolchain drift (dev quick check; review dry)",
		"Fix the cache bug",
		"^F Missing case reason (tests pass)",
		"F^ Swapped prefix order (tests pass; user-visible flag)",
	} {
		if err := os.WriteFile(filepath.Join(fixture.root, "seed.txt"), []byte(subject+"\n"), 0o644); err != nil {
			t.Fatalf("write seed failed: %v", err)
		}
		fixture.run("add", "seed.txt")
		output, err := fixture.tryGit(nil, "commit", "--quiet", "-m", subject)
		if err == nil {
			t.Fatalf("commit-msg accepted invalid subject %q", subject)
		}
		if !strings.Contains(output, "Risk-Aware Commit Notation") {
			t.Fatalf("commit-msg rejection lacks guidance for %q:\n%s", subject, output)
		}
		fixture.run("reset", "--quiet")
	}
}

func TestCommitMsgHookHonorsBypass(t *testing.T) {
	fixture := newGitHooksFixture(t)
	if err := os.WriteFile(filepath.Join(fixture.root, "seed.txt"), []byte("bypass\n"), 0o644); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}
	fixture.run("add", "seed.txt")
	output, err := fixture.tryGit(
		[]string{"WORKCELL_SKIP_COMMIT_NOTATION=1"},
		"commit", "--quiet", "-m", "temporary bypass subject",
	)
	if err != nil {
		t.Fatalf("bypassed commit failed: %v\n%s", err, output)
	}
}

func TestPrePushHookRejectsUnsignedCommits(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)")
	output, err := fixture.tryGit(nil, "push", "--quiet", "origin", "main")
	if err == nil {
		t.Fatal("pre-push accepted an unsigned commit")
	}
	if !strings.Contains(output, "unable to verify commit") && !strings.Contains(output, "signed") {
		t.Fatalf("pre-push rejection lacks signature guidance:\n%s", output)
	}
}

func TestPrePushHookAcceptsSignedCommits(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)", "-S")
	fixture.run("push", "--quiet", "origin", "main")
	fixture.commitFile("file.txt", "two\n", "^B Correct fixture file (tests pass; fixture defect)", "-S")
	fixture.run("push", "--quiet", "origin", "main")
}

func TestPrePushHookAllowsEmptyRangeAndDeletes(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)")
	if output, err := fixture.tryGit(
		[]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1"},
		"push", "--quiet", "origin", "main",
	); err != nil {
		t.Fatalf("bypassed seed push failed: %v\n%s", err, output)
	}
	fixture.run("fetch", "--quiet", "origin")
	fixture.run("push", "--quiet", "origin", "main")
	fixture.run("push", "--quiet", "origin", "main:refs/heads/topic")
	fixture.run("push", "--quiet", "origin", ":refs/heads/topic")
}

func TestPrePushHookRejectsUnsignedTailBehindSignedHead(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)")
	fixture.commitFile("file.txt", "two\n", "^B Correct fixture file (tests pass; fixture defect)", "-S")
	output, err := fixture.tryGit(nil, "push", "--quiet", "origin", "main")
	if err == nil {
		t.Fatal("pre-push accepted an unsigned commit behind a signed head")
	}
	if !strings.Contains(output, "unable to verify commit") && !strings.Contains(output, "signed") {
		t.Fatalf("pre-push rejection lacks signature guidance:\n%s", output)
	}
}
