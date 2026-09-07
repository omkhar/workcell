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

func TestCommitMsgHookNormalizesSubjectWithTrailingSpace(t *testing.T) {
	fixture := newGitHooksFixture(t)
	valid := "^F Add branch filter flag (tests pass; user-visible CLI flag)"
	messageFile := filepath.Join(fixture.root, "message.txt")
	// stripspace drops the trailing space, so the validated subject no longer
	// appears verbatim in the file the trim reads.
	message := "# leading comment\n" + valid + "   \n\nDetail line.\n"
	if err := os.WriteFile(messageFile, []byte(message), 0o644); err != nil {
		t.Fatalf("write message file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}
	fixture.run("add", "seed.txt")
	fixture.run("commit", "--quiet", "--cleanup=verbatim", "-F", messageFile)
	if got := fixture.run("log", "-1", "--format=%s"); got != valid {
		t.Fatalf("retained subject %q is not the validated subject %q", got, valid)
	}
	if got := fixture.run("log", "-1", "--format=%B"); !strings.Contains(got, "Detail line.") {
		t.Fatalf("normalization discarded the body:\n%s", got)
	}
}

func TestCommitMsgHookNormalizesRetainedSubjectWithTrailingSpace(t *testing.T) {
	fixture := newGitHooksFixture(t)
	valid := "^F Add branch filter flag (tests pass; user-visible CLI flag)"
	messageFile := filepath.Join(fixture.root, "message.txt")
	// stripspace drops the trailing space before the check, so the stored
	// subject has to be the checked one and not the raw line. %s trims the
	// difference away, so the raw message is the assertion.
	if err := os.WriteFile(messageFile, []byte(valid+"   \n\nDetail line.\n"), 0o644); err != nil {
		t.Fatalf("write message file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}
	fixture.run("add", "seed.txt")
	fixture.run("commit", "--quiet", "--cleanup=verbatim", "-F", messageFile)
	raw := fixture.run("log", "-1", "--format=%B")
	rawSubject := strings.SplitN(raw, "\n", 2)[0]
	if rawSubject != valid {
		t.Fatalf("stored subject %q is not the checked subject %q", rawSubject, valid)
	}
	if !strings.Contains(raw, "Detail line.") {
		t.Fatalf("normalization discarded the body:\n%s", raw)
	}
}

func TestCommitMsgHookHonorsNumberedGitConfigEnvironment(t *testing.T) {
	fixture := newGitHooksFixture(t)
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
	// GIT_CONFIG_COUNT and its numbered pairs also select configuration, so
	// the sanitized re-exec has to forward every GIT_CONFIG variable.
	env := []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.commentChar",
		"GIT_CONFIG_VALUE_0=;",
	}
	if output, err := fixture.tryGit(env, "commit", "--quiet", "--cleanup=strip", "-F", messageFile); err != nil {
		t.Fatalf("commit with numbered configuration failed: %v\n%s", err, output)
	}
}

func TestCommitMsgHookHonorsXDGCommentChar(t *testing.T) {
	fixture := newGitHooksFixture(t)
	xdgDir := filepath.Join(fixture.homeDir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdgDir, "git"), 0o755); err != nil {
		t.Fatalf("mkdir xdg config failed: %v", err)
	}
	config := "[core]\n\tcommentChar = \";\"\n"
	if err := os.WriteFile(filepath.Join(xdgDir, "git", "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write xdg config failed: %v", err)
	}
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
	// Git reads $XDG_CONFIG_HOME/git/config, so the sanitized re-exec has to
	// forward that variable for stripspace to agree with Git.
	env := []string{"XDG_CONFIG_HOME=" + xdgDir}
	if output, err := fixture.tryGit(env, "commit", "--quiet", "--cleanup=strip", "-F", messageFile); err != nil {
		t.Fatalf("commit with XDG comment character failed: %v\n%s", err, output)
	}
}

func TestCommitMsgHookRejectsSubjectWhenCommentCharIsRiskSymbol(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.run("config", "core.commentChar", "^")
	messageFile := filepath.Join(fixture.root, "message.txt")
	message := "^F Add branch filter flag (tests pass; user-visible CLI flag)\n" +
		"INVALID BODY SUBJECT\n"
	if err := os.WriteFile(messageFile, []byte(message), 0o644); err != nil {
		t.Fatalf("write message file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}
	fixture.run("add", "seed.txt")
	// Git applies --cleanup after the hook, so accepting this subject lets
	// Git strip it and commit the next line as an unvalidated subject.
	output, err := fixture.tryGit(nil, "commit", "--quiet", "--cleanup=strip", "-F", messageFile)
	if err == nil {
		t.Fatalf("hook accepted a subject Git then removed:\n%s", output)
	}
	if !strings.Contains(output, "core.commentChar deletes") {
		t.Fatalf("rejection lacks comment character guidance:\n%s", output)
	}
}

func TestCommitMsgHookNormalizesCRLFMessages(t *testing.T) {
	fixture := newGitHooksFixture(t)
	valid := "^F Add branch filter flag (tests pass; user-visible CLI flag)"
	messageFile := filepath.Join(fixture.root, "message.txt")
	// stripspace removes the carriage return before the check, so the trim
	// has to find the subject on a line that still carries one.
	message := "# leading comment\r\n" + valid + "\r\n\r\nDetail line.\r\n"
	if err := os.WriteFile(messageFile, []byte(message), 0o644); err != nil {
		t.Fatalf("write message file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}
	fixture.run("add", "seed.txt")
	if output, err := fixture.tryGit(nil, "commit", "--quiet", "--cleanup=verbatim", "-F", messageFile); err != nil {
		t.Fatalf("commit with a CRLF message failed: %v\n%s", err, output)
	}
	if got := fixture.run("log", "-1", "--format=%s"); got != valid {
		t.Fatalf("stored subject %q is not the checked subject %q", got, valid)
	}
}

func TestCommitMsgHookHonorsCommandScopedCommentChar(t *testing.T) {
	fixture := newGitHooksFixture(t)
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
	// -c settings reach the hook through GIT_CONFIG_PARAMETERS, which the
	// sanitized re-exec has to forward for stripspace to agree with Git.
	fixture.run("-c", "core.commentChar=;", "commit", "--quiet", "--cleanup=strip", "-F", messageFile)
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

func TestPrePushHookRejectsUnsignedBaseWhenPushURLDiffers(t *testing.T) {
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
	// Tracking refs record what the fetch URL held, so they cannot vouch for a
	// separately configured push destination.
	elsewhere := filepath.Join(filepath.Dir(fixture.remote), "pushurl.git")
	fixture.run("init", "--quiet", "--bare", elsewhere)
	fixture.run("remote", "set-url", "--push", "origin", elsewhere)
	output, err := fixture.tryGit(nil, "push", "--quiet", "origin", "main")
	if err == nil {
		t.Fatal("pre-push let fetch tracking refs vouch for a different push destination")
	}
	if !strings.Contains(output, "unable to verify commit") {
		t.Fatalf("pre-push rejection lacks signature guidance:\n%s", output)
	}
}

func TestPrePushHookHonorsCommandScopedVerificationConfig(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)", "-S")
	signers := filepath.Join(fixture.homeDir, "allowed_signers")
	fixture.run("config", "--unset", "gpg.ssh.allowedSignersFile")
	// The allowed-signers file is supplied per command, so the sanitized
	// re-exec has to forward it for verify-commit to see the same settings.
	fixture.run("-c", "gpg.ssh.allowedSignersFile="+signers, "push", "--quiet", "origin", "main")
}

func TestPrePushHookHonorsXDGVerificationConfig(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)", "-S")
	signers := filepath.Join(fixture.homeDir, "allowed_signers")
	fixture.run("config", "--unset", "gpg.ssh.allowedSignersFile")
	xdgDir := filepath.Join(fixture.homeDir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdgDir, "git"), 0o755); err != nil {
		t.Fatalf("mkdir xdg config failed: %v", err)
	}
	config := "[gpg \"ssh\"]\n\tallowedSignersFile = " + signers + "\n"
	if err := os.WriteFile(filepath.Join(xdgDir, "git", "config"), []byte(config), 0o644); err != nil {
		t.Fatalf("write xdg config failed: %v", err)
	}
	// Git reads $XDG_CONFIG_HOME/git/config, so the sanitized re-exec has to
	// forward that variable for verify-commit to see the same settings.
	env := []string{"XDG_CONFIG_HOME=" + xdgDir}
	if output, err := fixture.tryGit(env, "push", "--quiet", "origin", "main"); err != nil {
		t.Fatalf("push with XDG signing configuration failed: %v\n%s", err, output)
	}
}

func TestPrePushHookRejectsUnsignedBaseBehindStaleTrackingRefs(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configureSSHSigning()
	fixture.commitFile("base.txt", "base\n", "^F Add unsigned base (tests pass; fixture seed)")
	fixture.tryGit([]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1"}, "push", "--quiet", "origin", "main")
	fixture.commitFile("child.txt", "child\n", "^F Add signed child (tests pass; fixture seed)", "-S")
	// origin now points at a second repository that holds nothing, but its
	// tracking refs still name the unsigned base published to the first one.
	other := filepath.Join(filepath.Dir(fixture.remote), "other.git")
	fixture.run("init", "--quiet", "--bare", other)
	fixture.run("remote", "set-url", "origin", other)
	output, err := fixture.tryGit(nil, "push", "--quiet", "origin", "main:refs/heads/published")
	if err == nil {
		t.Fatalf("stale tracking refs excused an unsigned commit:\n%s", output)
	}
	if !strings.Contains(output, "unable to verify commit") {
		t.Fatalf("rejection lacks signature guidance:\n%s", output)
	}
}

func TestPrePushHookForwardsTransportAuthentication(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configureSSHSigning()
	fixture.commitFile("base.txt", "base\n", "^F Add unsigned base (tests pass; fixture seed)")
	fixture.tryGit([]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1"}, "push", "--quiet", "origin", "main")
	fixture.commitFile("child.txt", "child\n", "^F Add signed child (tests pass; fixture seed)", "-S")
	// The destination is reachable only through GIT_SSH_COMMAND, so the hook
	// cannot read the advertisement without it. The walk then reaches the
	// published unsigned base and refuses an otherwise valid new-ref push.
	sshCommand := filepath.Join(fixture.homeDir, "fake-ssh")
	script := "#!/bin/bash\nexec /bin/sh -c \"${@: -1}\"\n"
	if err := os.WriteFile(sshCommand, []byte(script), 0o755); err != nil {
		t.Fatalf("write ssh command failed: %v", err)
	}
	fixture.run("remote", "set-url", "origin", "ssh://host"+fixture.remote)
	env := []string{"GIT_SSH_COMMAND=" + sshCommand}
	if output, err := fixture.tryGit(env, "push", "--quiet", "origin", "main:refs/heads/published"); err != nil {
		t.Fatalf("push over a transport needing GIT_SSH_COMMAND failed: %v\n%s", err, output)
	}
}

func TestPrePushHookHonorsGlobalConfigSelector(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.configureSSHSigning()
	fixture.commitFile("file.txt", "one\n", "^F Add fixture file (tests pass; fixture seed)", "-S")
	signers := filepath.Join(fixture.homeDir, "allowed_signers")
	fixture.run("config", "--unset", "gpg.ssh.allowedSignersFile")
	globalConfig := filepath.Join(fixture.homeDir, "selected_config")
	config := "[gpg \"ssh\"]\n\tallowedSignersFile = " + signers + "\n"
	if err := os.WriteFile(globalConfig, []byte(config), 0o644); err != nil {
		t.Fatalf("write selected config failed: %v", err)
	}
	// GIT_CONFIG_GLOBAL selects the global configuration file, so the
	// sanitized re-exec has to forward it for verify-commit to read it.
	env := []string{"GIT_CONFIG_GLOBAL=" + globalConfig}
	if output, err := fixture.tryGit(env, "push", "--quiet", "origin", "main"); err != nil {
		t.Fatalf("push with a selected global configuration failed: %v\n%s", err, output)
	}
}

func TestPrePushHookFailsClosedWhenRangeWalkFails(t *testing.T) {
	fixture := newGitHooksFixture(t)
	fixture.commitFile("base.txt", "one\n", "^F Add fixture base (tests pass; fixture seed)")
	fixture.commitFile("file.txt", "two\n", "^F Add fixture file (tests pass; fixture seed)")
	// A missing parent object breaks the range walk; the hook must refuse the
	// push rather than see an empty range.
	parent := fixture.run("rev-parse", "HEAD~1")
	object := filepath.Join(fixture.root, ".git", "objects", parent[:2], parent[2:])
	if err := os.Remove(object); err != nil {
		t.Fatalf("remove parent object failed: %v", err)
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
