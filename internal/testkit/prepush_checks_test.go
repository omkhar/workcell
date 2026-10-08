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

const fixtureSubject = "^F Add fixture file (tests pass; fixture seed)"

// prePushChecksFixture stages scripts/githooks/pre-push in a scratch repo with
// recording stubs for the repo gates and the real codespell.
type prePushChecksFixture struct {
	*gitHooksFixture
	calls string
}

func newPrePushChecksFixture(t *testing.T, failing string) *prePushChecksFixture {
	t.Helper()
	if _, err := exec.LookPath("codespell"); err != nil {
		t.Skipf("codespell unavailable: %v", err)
	}
	f := &prePushChecksFixture{gitHooksFixture: newGitHooksFixture(t)}
	f.calls = filepath.Join(f.tmpDir, "calls")
	for _, name := range []string{"check-generated-artifacts", "check-doc-links", "check-doc-language", "check-pr-shape"} {
		body := "#!/bin/bash\necho " + name + " >>\"" + f.calls + "\"\n"
		if name == failing {
			body += "exit 1\n"
		}
		path := filepath.Join(f.root, "scripts", name+".sh")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		writeExecFile(t, path, []byte(body), 0o755)
	}
	if err := os.WriteFile(filepath.Join(f.root, ".codespellrc"), []byte("[codespell]\nquiet-level = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codespellScript, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "ci", "run-codespell.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "scripts", "ci"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecFile(t, filepath.Join(f.root, "scripts", "ci", "run-codespell.sh"), codespellScript, 0o755)
	// The gates run from a checkout of the pushed commit, so commit them.
	f.run("add", ".")
	f.commitFile("base.txt", "base\n", fixtureSubject)
	f.run("update-ref", "refs/remotes/origin/main", "HEAD")
	f.run("checkout", "--quiet", "-b", "feature")
	return f
}

// hook runs the fast checks as Git would for a push of the checked-out branch.
func (f *prePushChecksFixture) hook(extraEnv ...string) (string, error) {
	f.t.Helper()
	return f.hookWithInput(pushLine(f.run("rev-parse", "HEAD")), extraEnv...)
}

func pushLine(oid string) string {
	return "refs/heads/feature " + oid + " refs/heads/feature " + strings.Repeat("0", 40) + "\n"
}

func (f *prePushChecksFixture) hookWithInput(stdin string, extraEnv ...string) (string, error) {
	f.t.Helper()
	cmd := exec.Command(filepath.Join(f.root, "scripts", "githooks", "pre-push"))
	cmd.Dir = f.root
	cmd.Env = append(append(f.env(), "WORKCELL_SKIP_PREPUSH_CHECKS=0"), extraEnv...)
	cmd.Stdin = strings.NewReader(stdin)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

func (f *prePushChecksFixture) ran() string {
	data, err := os.ReadFile(f.calls)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestFastPrePushRunsGatesOnChangedMarkdown(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("doc.md", "A clean sentence.\n", fixtureSubject)
	if output, err := f.hook(); err != nil {
		t.Fatalf("fast pre-push failed on a clean change: %v\n%s", err, output)
	}
	for _, want := range []string{"check-generated-artifacts", "check-doc-links", "check-doc-language", "check-pr-shape"} {
		if !strings.Contains(f.ran(), want) {
			t.Errorf("gate %s did not run; ran:\n%s", want, f.ran())
		}
	}
}

func TestFastPrePushSkipsDocGatesWithoutMarkdown(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("code.txt", "plain\n", fixtureSubject)
	if output, err := f.hook(); err != nil {
		t.Fatalf("fast pre-push failed: %v\n%s", err, output)
	}
	if strings.Contains(f.ran(), "check-doc-") {
		t.Errorf("doc gates ran without a changed .md:\n%s", f.ran())
	}
}

func TestFastPrePushRejectsPlantedMisspelling(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("doc.md", "We recieve input.\n", fixtureSubject)
	output, err := f.hook()
	if err == nil {
		t.Fatal("fast pre-push accepted a planted misspelling")
	}
	if !strings.Contains(output, "recieve") {
		t.Errorf("codespell hit missing from the output:\n%s", output)
	}
}

func TestFastPrePushSpellchecksOnlyCIDocFiles(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("planted_test.go", "// We recieve input.\n", fixtureSubject)
	if output, err := f.hook(); err != nil {
		t.Fatalf("fast pre-push spellchecked a file CI does not scan: %v\n%s", err, output)
	}
	f.commitFile("doc.md", "We recieve input.\n", fixtureSubject)
	if output, err := f.hook(); err == nil || !strings.Contains(output, "doc.md:1: recieve") {
		t.Fatalf("fast pre-push missed a misspelling in a changed .md: %v\n%s", err, output)
	}
}

func TestFastPrePushReportsEveryFailedGate(t *testing.T) {
	f := newPrePushChecksFixture(t, "check-generated-artifacts")
	f.commitFile("doc.md", "A clean sentence.\n", fixtureSubject)
	output, err := f.hook()
	if err == nil {
		t.Fatal("fast pre-push accepted a failing generated-artifacts gate")
	}
	if !strings.Contains(f.ran(), "check-pr-shape") {
		t.Errorf("later gates must still run after a failure; ran:\n%s", f.ran())
	}
	if !strings.Contains(output, "generated artifacts failed") {
		t.Errorf("failure not named:\n%s", output)
	}
}

func TestFastPrePushHonorsBypass(t *testing.T) {
	f := newPrePushChecksFixture(t, "check-generated-artifacts")
	f.commitFile("doc.md", "We recieve input.\n", fixtureSubject)
	if output, err := f.hook("WORKCELL_SKIP_PREPUSH_CHECKS=1"); err != nil {
		t.Fatalf("bypass failed: %v\n%s", err, output)
	}
}

func TestRepoPrePushChainsToFastChecks(t *testing.T) {
	f := newPrePushChecksFixture(t, "check-generated-artifacts")
	f.commitFile("doc.md", "A clean sentence.\n", fixtureSubject)
	output, err := f.tryGit([]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1", "WORKCELL_SKIP_PREPUSH_CHECKS=0"}, "push", "--quiet", "origin", "feature")
	if err == nil {
		t.Fatalf("push passed although the fast gate fails:\n%s", output)
	}
	if !strings.Contains(output, "generated artifacts failed") {
		t.Errorf("fast gate failure not shown:\n%s", output)
	}
}

func TestFastPrePushGatesPushedBranchOtherThanHead(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("doc.md", "We recieve input.\n", fixtureSubject)
	pushed := f.run("rev-parse", "HEAD")
	f.run("checkout", "--quiet", "main")
	output, err := f.hookWithInput(pushLine(pushed))
	if err == nil || !strings.Contains(output, "doc.md:1: recieve") {
		t.Fatalf("fast pre-push did not gate the pushed branch content: %v\n%s", err, output)
	}
}

func TestFastPrePushFailsClosedWhenCheckoutFails(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	output, err := f.hookWithInput(pushLine(strings.Repeat("1", 40)))
	if err == nil || !strings.Contains(output, "cannot check out") {
		t.Fatalf("fast pre-push did not fail closed on a failed checkout: %v\n%s", err, output)
	}
}

func TestFastPrePushSkipsDeletesAndTags(t *testing.T) {
	f := newPrePushChecksFixture(t, "check-generated-artifacts")
	head := f.run("rev-parse", "HEAD")
	zero := strings.Repeat("0", 40)
	input := "(delete) " + zero + " refs/heads/old " + head + "\nrefs/tags/v1 " + head + " refs/tags/v1 " + zero + "\n"
	if output, err := f.hookWithInput(input); err != nil || f.ran() != "" {
		t.Fatalf("gates ran for a delete and a tag push: %v\n%s\nran:\n%s", err, output, f.ran())
	}
}

func TestFastPrePushRunsDocLinksOnDeletedMarkdown(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("doc.md", "A clean sentence.\n", fixtureSubject)
	f.run("update-ref", "refs/remotes/origin/main", "HEAD")
	f.run("rm", "--quiet", "doc.md")
	f.run("commit", "--quiet", "-m", fixtureSubject)
	if output, err := f.hook(); err != nil {
		t.Fatalf("fast pre-push failed: %v\n%s", err, output)
	}
	if !strings.Contains(f.ran(), "check-doc-links") {
		t.Errorf("doc links did not run for a deleted .md; ran:\n%s", f.ran())
	}
}

func TestFastPrePushRejectsDeletedNonMarkdownLinkTarget(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	links, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "check-doc-links.sh"))
	if err != nil {
		t.Fatal(err)
	}
	writeExecFile(t, filepath.Join(f.root, "scripts", "check-doc-links.sh"), links, 0o755)
	f.run("add", "scripts/check-doc-links.sh")
	f.commitFile("img.png", "image\n", fixtureSubject)
	f.commitFile("doc.md", "See [the image](img.png).\n", fixtureSubject)
	f.run("update-ref", "refs/remotes/origin/main", "HEAD")
	f.run("rm", "--quiet", "img.png")
	f.run("commit", "--quiet", "-m", fixtureSubject)
	output, err := f.hook()
	if err == nil || !strings.Contains(output, "doc links failed") {
		t.Fatalf("deleting a non-Markdown link target skipped the link check: %v\n%s", err, output)
	}
}

func TestFastPrePushSkipsGoGatesWithoutGo(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("doc.md", "A clean sentence.\n", fixtureSubject)
	bin := filepath.Join(f.tmpDir, "nogo-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"git", "mktemp", "rm", "dirname", "bash", "env", "grep", "sort", "xargs", "tr", "awk"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s unavailable: %v", tool, err)
		}
		if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	writeExecFile(t, filepath.Join(bin, "codespell"), []byte("#!/bin/bash\nexit 0\n"), 0o755)
	output, err := f.hook("PATH=" + bin)
	if err != nil {
		t.Fatalf("fast pre-push failed without Go: %v\n%s", err, output)
	}
	if strings.Contains(f.ran(), "check-generated-artifacts") || strings.Contains(f.ran(), "check-doc-language") {
		t.Errorf("Go gates ran without Go; ran:\n%s", f.ran())
	}
	if !strings.Contains(f.ran(), "check-doc-links") || !strings.Contains(output, "go is missing") {
		t.Errorf("Go-free gates or the notice missing; ran:\n%s\noutput:\n%s", f.ran(), output)
	}
}

func TestFastPrePushIgnoresWorkingTreeEdits(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("doc.md", "We recieve input.\n", fixtureSubject)
	if err := os.WriteFile(filepath.Join(f.root, "doc.md"), []byte("We receive input.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := f.hook()
	if err == nil || !strings.Contains(output, "doc.md:1: recieve") {
		t.Fatalf("fast pre-push gated the edited worktree instead of the pushed commit: %v\n%s", err, output)
	}
}

func TestFastPrePushIgnoresUntrackedLinkTarget(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	links, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "check-doc-links.sh"))
	if err != nil {
		t.Fatal(err)
	}
	writeExecFile(t, filepath.Join(f.root, "scripts", "check-doc-links.sh"), links, 0o755)
	f.run("add", "scripts/check-doc-links.sh")
	f.commitFile("doc.md", "See [the target](target.md).\n", fixtureSubject)
	if err := os.WriteFile(filepath.Join(f.root, "target.md"), []byte("Untracked target.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := f.hook()
	if err == nil || !strings.Contains(output, "doc links failed") {
		t.Fatalf("an untracked target.md satisfied the link check: %v\n%s", err, output)
	}
}

func TestFastPrePushRunsDocLinksOnMarkdownRenamedAway(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.commitFile("old.md", "A clean sentence.\n", fixtureSubject)
	f.run("update-ref", "refs/remotes/origin/main", "HEAD")
	f.run("mv", "old.md", "old.txt")
	f.run("commit", "--quiet", "-m", fixtureSubject)
	if output, err := f.hook(); err != nil {
		t.Fatalf("fast pre-push failed: %v\n%s", err, output)
	}
	if !strings.Contains(f.ran(), "check-doc-links") {
		t.Errorf("doc links did not run for old.md renamed to old.txt; ran:\n%s", f.ran())
	}
}

func TestFastPrePushRejectsChangedSymlink(t *testing.T) {
	// With core.symlinks=false the checkout holds a regular file, so only the
	// tree check (mode 120000) can refuse it; an on-disk -L test cannot.
	for _, symlinks := range []string{"true", "false"} {
		t.Run("core.symlinks="+symlinks, func(t *testing.T) {
			f := newPrePushChecksFixture(t, "")
			links, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "check-doc-links.sh"))
			if err != nil {
				t.Fatal(err)
			}
			writeExecFile(t, filepath.Join(f.root, "scripts", "check-doc-links.sh"), links, 0o755)
			f.run("add", "scripts/check-doc-links.sh")
			host := filepath.Join(f.tmpDir, "host-secret.txt")
			if err := os.WriteFile(host, []byte("We recieve [secret](host-derived-token).\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(host, filepath.Join(f.root, "link.md")); err != nil {
				t.Fatal(err)
			}
			f.run("add", "link.md")
			f.run("commit", "--quiet", "-m", fixtureSubject)
			f.run("config", "core.symlinks", symlinks)
			output, err := f.hook()
			if err == nil {
				t.Fatalf("fast pre-push accepted a changed symlink:\n%s", output)
			}
			if strings.Contains(output, "recieve") || strings.Contains(output, "host-derived-token") || !strings.Contains(output, "link.md is a symlink") {
				t.Errorf("a gate read the host file or the refusal is missing:\n%s", output)
			}
		})
	}
}

func TestFastPrePushRunsOnlyTrustedCheckers(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	marker := filepath.Join(f.tmpDir, "pushed-code-ran")
	evil := []byte("#!/bin/bash\ntouch \"" + marker + "\"\n")
	for _, name := range []string{"check-generated-artifacts.sh", "check-doc-links.sh", "check-doc-language.sh", "check-pr-shape.sh", filepath.Join("ci", "run-codespell.sh")} {
		writeExecFile(t, filepath.Join(f.root, "scripts", name), evil, 0o755)
	}
	f.run("add", "scripts")
	f.commitFile("doc.md", "A clean sentence.\n", fixtureSubject)
	pushed := f.run("rev-parse", "HEAD")
	f.run("checkout", "--quiet", "main")
	output, err := f.hookWithInput(pushLine(pushed))
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatalf("the hook ran a checker from the pushed commit:\n%s", output)
	}
	if err != nil || !strings.Contains(f.ran(), "check-doc-links") || strings.Contains(f.ran(), "check-generated-artifacts") {
		t.Errorf("trusted gates did not run as expected: %v\n%s\nran:\n%s", err, output, f.ran())
	}
}

func TestFastPrePushSpellchecksOptionLikePath(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	name := "--skip=*.md"
	if err := os.WriteFile(filepath.Join(f.root, name), []byte("We recieve input.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.run("add", "--", name)
	f.run("commit", "--quiet", "-m", fixtureSubject)
	if output, err := f.hook(); err == nil || !strings.Contains(output, "recieve") {
		t.Fatalf("a path shaped like a codespell option escaped the scan: %v\n%s", err, output)
	}
}

func TestRepoPrePushGatesPushedBranchAfterSignatureWalk(t *testing.T) {
	f := newPrePushChecksFixture(t, "")
	f.configureSSHSigning()
	f.run("config", "commit.gpgsign", "true")
	if output, err := f.tryGit([]string{"WORKCELL_SKIP_PUSH_SIGNATURES=1"}, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("seed push failed: %v\n%s", err, output)
	}
	f.commitFile("doc.md", "We recieve input.\n", fixtureSubject)
	f.run("checkout", "--quiet", "main")
	output, err := f.tryGit([]string{"WORKCELL_SKIP_PREPUSH_CHECKS=0"}, "push", "--quiet", "origin", "feature")
	if err == nil {
		t.Fatalf("push of a non-checked-out branch passed the fast gates:\n%s", output)
	}
	if !strings.Contains(output, "doc.md:1: recieve") {
		t.Errorf("signature walk did not hand the ref lines to the fast gates:\n%s", output)
	}
}

func TestRunCodespellPrintsOffendingLines(t *testing.T) {
	if _, err := exec.LookPath("codespell"); err != nil {
		t.Skipf("codespell unavailable: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".codespellrc"), []byte("[codespell]\nquiet-level = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repoRoot(t), "scripts", "ci", "run-codespell.sh")
	run := func() (string, error) {
		cmd := exec.Command(script, root)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		return output.String(), err
	}
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("clean text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err != nil {
		t.Fatalf("clean tree failed: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("We recieve input.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := run()
	if err == nil {
		t.Fatal("planted misspelling passed")
	}
	if !strings.Contains(output, "doc.md:1: recieve ==> receive") {
		t.Errorf("offending line missing from the output:\n%s", output)
	}
}
