// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"bytes"
	"os"
	"os/exec"
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
	fixture.run("init", "--quiet", "--bare", fixture.remote)
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
	output, err := f.tryGit(nil, args...)
	if err != nil {
		f.t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(output)
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
	f.run("config", "gpg.format", "ssh")
	f.run("config", "gpg.ssh.allowedSignersFile", signers)
	f.run("config", "user.signingkey", keyPath)
}

func TestCommitMsgHookAcceptsNotationSubjects(t *testing.T) {
	fixture := newGitHooksFixture(t)
	for _, subject := range []string{
		"^F Add branch filter flag (tests pass; user-visible CLI flag)",
		".r Rename parser helper (rename proof and focused tests pass; internal refactor)",
		"!B Patch cache collision (validation covers only this change; user-visible defect)",
		"@d Draft support note (evidence incomplete; secondary docs)",
		"Merge branch 'topic' into main",
		"Revert \"^F Add branch filter flag (tests pass; user-visible CLI flag)\"",
		"fixup! ^F Add branch filter flag",
	} {
		fixture.commitFile("file.txt", subject+"\n", subject)
	}
}

func TestCommitMsgHookSkipsLeadingComments(t *testing.T) {
	fixture := newGitHooksFixture(t)
	messageFile := filepath.Join(fixture.root, "message.txt")
	message := "# Please enter the commit message for your changes.\n" +
		"^F Add branch filter flag (tests pass; user-visible CLI flag)\n"
	if err := os.WriteFile(messageFile, []byte(message), 0o644); err != nil {
		t.Fatalf("write message file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}
	fixture.run("add", "seed.txt")
	fixture.run("commit", "--quiet", "--cleanup=strip", "-F", messageFile)
}

func TestCommitMsgHookHonorsConfiguredCommentChar(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.run("config", "core.commentChar", ";")
	messageFile := filepath.Join(fixture.root, "message.txt")
	message := "; Please enter the commit message for your changes.\n" +
		"^F Add branch filter flag (tests pass; user-visible CLI flag)\n"
	if err := os.WriteFile(messageFile, []byte(message), 0o644); err != nil {
		t.Fatalf("write message file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}
	fixture.run("add", "seed.txt")
	fixture.run("commit", "--quiet", "--cleanup=strip", "-F", messageFile)
}

func TestCommitMsgHookNormalizesRetainedSubject(t *testing.T) {
	fixture := newGitHooksFixture(t)
	valid := "^F Add branch filter flag (tests pass; user-visible CLI flag)"
	// -m keeps comment lines, so the hook must make the retained subject the
	// subject it validated rather than leaving the comment in front of it.
	fixture.commitFile("seed.txt", "seed\n", "# invalid subject\n"+valid)
	if got := fixture.run("log", "-1", "--format=%s"); got != valid {
		t.Fatalf("retained subject %q is not the validated subject %q", got, valid)
	}
}

func TestCommitMsgHookKeepsCommentedBodyWhenSubjectIsFirst(t *testing.T) {
	fixture := newGitHooksFixture(t)
	valid := "^F Add branch filter flag (tests pass; user-visible CLI flag)"
	fixture.commitFile("seed.txt", "seed\n", valid+"\n\n# Summary\nDetail line.")
	if got := fixture.run("log", "-1", "--format=%B"); !strings.Contains(got, "# Summary") {
		t.Fatalf("hook discarded a body the contributor asked Git to keep:\n%s", got)
	}
}

func TestCommitMsgHookKeepsCommentedBodyWhenNormalizing(t *testing.T) {
	fixture := newGitHooksFixture(t)
	valid := "^F Add branch filter flag (tests pass; user-visible CLI flag)"
	// Leading comments are dropped so the retained subject is the validated
	// one, but comment-prefixed body lines are the contributor's content.
	fixture.commitFile("seed.txt", "seed\n", "# leading comment\n"+valid+"\n\n# Summary\nDetail line.")
	if got := fixture.run("log", "-1", "--format=%s"); got != valid {
		t.Fatalf("retained subject %q is not the validated subject %q", got, valid)
	}
	if got := fixture.run("log", "-1", "--format=%B"); !strings.Contains(got, "# Summary") {
		t.Fatalf("normalization discarded a comment-prefixed body line:\n%s", got)
	}
}

func TestCommitMsgHookNormalizesSubjectWithBackslash(t *testing.T) {
	fixture := newGitHooksFixture(t)
	valid := `^F Document C:\temp path (tests pass; user-visible docs)`
	// The subject is matched literally, so a backslash must not be read as an
	// escape while the leading comment is trimmed.
	fixture.commitFile("seed.txt", "seed\n", "# leading comment\n"+valid)
	if got := fixture.run("log", "-1", "--format=%s"); got != valid {
		t.Fatalf("retained subject %q is not the validated subject %q", got, valid)
	}
}

func TestCommitMsgHookAcceptsSubjectWhenCommentCharIsRiskSymbol(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.run("config", "core.commentChar", "^")
	valid := "^F Add branch filter flag (tests pass; user-visible CLI flag)"
	body := "Detail line."
	// Comment stripping would delete this subject, so the retained subject
	// has to be checked before stripping is used as a fallback.
	fixture.commitFile("seed.txt", "seed\n", valid+"\n\n"+body)
	if got := fixture.run("log", "-1", "--format=%s"); got != valid {
		t.Fatalf("retained subject %q is not the validated subject %q", got, valid)
	}
	if got := fixture.run("log", "-1", "--format=%B"); !strings.Contains(got, body) {
		t.Fatalf("comment stripping discarded the body:\n%s", got)
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
	if !strings.Contains(output, "unable to verify commit") {
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

func TestPrePushHookIgnoresReplacementRefs(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)", "-S")
	root := fixture.run("rev-parse", "HEAD")
	fixture.commitFile("file.txt", "two\n", "^B Correct fixture file (tests pass; fixture defect)")
	fixture.commitFile("file.txt", "three\n", "^B Correct fixture file again (tests pass; fixture defect)", "-S")
	// A signed graft that hides the unsigned middle commit must not hide it
	// from the hook: the push still sends the original history.
	head := fixture.run("rev-parse", "HEAD")
	graft := fixture.run(
		"commit-tree", "-S", "-p", root, "-m",
		"^B Graft fixture head (tests pass; fixture graft)", "HEAD^{tree}",
	)
	fixture.run("update-ref", "refs/replace/"+head, graft)
	output, err := fixture.tryGit(nil, "push", "--quiet", "origin", "main")
	if err == nil {
		t.Fatal("pre-push followed a replacement ref past an unsigned commit")
	}
	if !strings.Contains(output, "unable to verify commit") {
		t.Fatalf("pre-push rejection lacks signature guidance:\n%s", output)
	}
}

func TestPrePushHookAcceptsDirectURLPush(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)")
	if output, err := fixture.tryGit(
		[]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1"},
		"push", "--quiet", "origin", "main",
	); err != nil {
		t.Fatalf("bypassed seed push failed: %v\n%s", err, output)
	}
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "two\n", "^B Correct fixture file (tests pass; fixture defect)", "-S")
	// No remote-tracking ref matches a direct URL, so only the advertised
	// remote tip bounds the walk; the unsigned base must stay excluded.
	fixture.run("push", "--quiet", fixture.remote, "main")
}

func TestPrePushHookAcceptsNewRefOverDirectURL(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)")
	if output, err := fixture.tryGit(
		[]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1"},
		"push", "--quiet", "origin", "main",
	); err != nil {
		t.Fatalf("bypassed seed push failed: %v\n%s", err, output)
	}
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "two\n", "^B Correct fixture file (tests pass; fixture defect)", "-S")
	// A new ref pushed by URL reports a zero remote tip, so the published
	// unsigned base must still be excluded by the remote-tracking fallback.
	fixture.run("push", "--quiet", fixture.remote, "HEAD:refs/heads/topic")
}

func TestPrePushHookAcceptsNewRefOverUnfetchedRemote(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)")
	if output, err := fixture.tryGit(
		[]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1"},
		"push", "--quiet", "origin", "main",
	); err != nil {
		t.Fatalf("bypassed seed push failed: %v\n%s", err, output)
	}
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "two\n", "^B Correct fixture file (tests pass; fixture defect)", "-S")
	// A configured but never-fetched alias has an empty tracking namespace,
	// so the scoped exclusion matches nothing and must fall back.
	fixture.run("remote", "add", "mirror", fixture.remote)
	fixture.run("push", "--quiet", "mirror", "HEAD:refs/heads/topic")
}

func TestPrePushHookRejectsUnsignedBaseSentToAnotherRepository(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)")
	if output, err := fixture.tryGit(
		[]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1"},
		"push", "--quiet", "origin", "main",
	); err != nil {
		t.Fatalf("bypassed seed push failed: %v\n%s", err, output)
	}
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "two\n", "^B Correct fixture file (tests pass; fixture defect)", "-S")
	// A second repository has never seen the unsigned base, so tracking refs
	// for the first one must not exclude it from the walk.
	elsewhere := filepath.Join(filepath.Dir(fixture.remote), "elsewhere.git")
	fixture.run("init", "--quiet", "--bare", elsewhere)
	output, err := fixture.tryGit(nil, "push", "--quiet", elsewhere, "HEAD:refs/heads/topic")
	if err == nil {
		t.Fatal("pre-push sent an unsigned base to a repository that had not seen it")
	}
	if !strings.Contains(output, "unable to verify commit") {
		t.Fatalf("pre-push rejection lacks signature guidance:\n%s", output)
	}
}

func TestPrePushHookFailsClosedWhenRangeWalkFails(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)")
	// A remote-tracking ref pointing at a missing object breaks the range
	// walk; the hook must refuse the push rather than see an empty range.
	broken := filepath.Join(fixture.root, ".git", "refs", "remotes", "origin", "broken")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatalf("mkdir for broken ref failed: %v", err)
	}
	missing := "24e6d5dc752f727899a698566b8933ff576aba47\n"
	if err := os.WriteFile(broken, []byte(missing), 0o644); err != nil {
		t.Fatalf("write broken ref failed: %v", err)
	}
	output, err := fixture.tryGit(nil, "push", "--quiet", "origin", "main")
	if err == nil {
		t.Fatal("pre-push accepted a push whose range walk failed")
	}
	if !strings.Contains(output, "could not enumerate the outgoing commits") {
		t.Fatalf("pre-push rejection lacks range-walk guidance:\n%s", output)
	}
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
	if !strings.Contains(output, "unable to verify commit") {
		t.Fatalf("pre-push rejection lacks signature guidance:\n%s", output)
	}
}
