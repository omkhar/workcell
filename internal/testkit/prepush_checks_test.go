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
	f.commitFile("base.txt", "base\n", fixtureSubject)
	f.run("update-ref", "refs/remotes/origin/main", "HEAD")
	f.run("checkout", "--quiet", "-b", "feature")
	return f
}

func (f *prePushChecksFixture) hook(extraEnv ...string) (string, error) {
	f.t.Helper()
	cmd := exec.Command(filepath.Join(f.root, "scripts", "githooks", "pre-push"))
	cmd.Dir = f.root
	cmd.Env = append(append(f.env(), "WORKCELL_SKIP_PREPUSH_CHECKS=0"), extraEnv...)
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
