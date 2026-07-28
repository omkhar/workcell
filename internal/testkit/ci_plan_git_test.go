// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam
package testkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type ciPlanCapturedConfig struct {
	Profile      string   `json:"profile"`
	Event        string   `json:"event"`
	BaseBranch   string   `json:"base_branch"`
	Labels       []string `json:"labels"`
	ChangedFiles []string `json:"changed_files"`
}
type ciPlanResult struct {
	code           int
	stdout, stderr string
}
type ciPlanFixture struct {
	t                                      *testing.T
	root, binDir, homeDir, tmpDir, realGit string
}

func ciPlanMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func ciPlanFileState(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	ciPlanMust(t, err)
	info, err := os.Stat(path)
	ciPlanMust(t, err)
	return fmt.Sprintf("%x:%d:%s", content, info.ModTime().UnixNano(), info.Mode())
}
func newCIPlanFixture(t *testing.T, objectFormat string) *ciPlanFixture {
	t.Helper()
	realGit, err := exec.LookPath("git")
	ciPlanMust(t, err)
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "ci-plan.sh"))
	ciPlanMust(t, err)
	stateRoot, err := filepath.EvalSymlinks(t.TempDir())
	ciPlanMust(t, err)
	fixture := &ciPlanFixture{t: t, root: filepath.Join(stateRoot, "repo"), binDir: filepath.Join(stateRoot, "bin"), homeDir: filepath.Join(stateRoot, "home"), tmpDir: filepath.Join(stateRoot, "tmp"), realGit: realGit}
	for _, dir := range []string{fixture.root, fixture.binDir, fixture.homeDir, fixture.tmpDir, filepath.Join(fixture.root, "scripts"), filepath.Join(fixture.root, "policy")} {
		ciPlanMust(t, os.MkdirAll(dir, 0o755))
	}
	fixture.writeFile("scripts/ci-plan.sh", script, 0o755)
	fixture.writeFile("policy/workflow-lanes.json", []byte("{}\n"), 0o644)
	fixture.writeExecutable(filepath.Join(fixture.binDir, "go"), "#!/bin/bash\nset -euo pipefail\nexec /bin/cat \"${!#}\"\n")
	ciPlanRunGitAt(t, realGit, fixture.root, fixture.gitEnv(), "init", "--quiet", "--initial-branch=main", "--object-format="+objectFormat)
	fixture.git("add", "--all")
	fixture.git("commit", "--quiet", "-m", "fixture root")
	return fixture
}
func newCIPlanTopicFixture(t *testing.T, files ...string) *ciPlanFixture {
	fixture := newCIPlanFixture(t, "sha1")
	fixture.writeTextFiles(files...)
	if len(files) != 0 {
		fixture.commit("tracked fixture files")
	}
	fixture.git("checkout", "--quiet", "-b", "topic")
	return fixture
}
func ciPlanRunGitAt(t *testing.T, realGit string, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(realGit, append([]string{"-C", dir}, args...)...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
func (f *ciPlanFixture) gitEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + f.homeDir, "TMPDIR=" + f.tmpDir, "LC_ALL=C", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Workcell Test", "GIT_AUTHOR_EMAIL=workcell-test@example.invalid", "GIT_AUTHOR_DATE=2001-02-03T04:05:06Z",
		"GIT_COMMITTER_NAME=Workcell Test", "GIT_COMMITTER_EMAIL=workcell-test@example.invalid", "GIT_COMMITTER_DATE=2001-02-03T04:05:06Z",
	}
}
func (f *ciPlanFixture) planEnv() []string {
	return []string{"PATH=" + f.binDir + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + f.homeDir, "TMPDIR=" + f.tmpDir, "LC_ALL=C", "BASH_ENV=", "ENV="}
}
func (f *ciPlanFixture) git(args ...string) string {
	f.t.Helper()
	bound := []string{"--git-dir=" + filepath.Join(f.root, ".git"), "--work-tree=" + f.root, "-c", "core.bare=false", "-c", "core.worktree=" + f.root}
	return ciPlanRunGitAt(f.t, f.realGit, f.root, f.gitEnv(), append(bound, args...)...)
}
func (f *ciPlanFixture) writeFile(relative string, content []byte, mode os.FileMode) {
	f.t.Helper()
	path := filepath.Join(f.root, filepath.FromSlash(relative))
	ciPlanMust(f.t, os.MkdirAll(filepath.Dir(path), 0o755))
	ciPlanMust(f.t, os.WriteFile(path, content, mode))
}
func (f *ciPlanFixture) writeTextFiles(files ...string) {
	f.t.Helper()
	if len(files)%2 != 0 {
		f.t.Fatal("writeTextFiles requires path/content pairs")
	}
	for index := 0; index < len(files); index += 2 {
		f.writeFile(files[index], []byte(files[index+1]), 0o644)
	}
}
func (f *ciPlanFixture) writeExecutable(path string, content string) {
	f.t.Helper()
	ciPlanMust(f.t, os.MkdirAll(filepath.Dir(path), 0o755))
	ciPlanMust(f.t, os.WriteFile(path, []byte(content), 0o755))
}
func (f *ciPlanFixture) initRepository(root string, relative string) {
	f.t.Helper()
	ciPlanMust(f.t, os.MkdirAll(root, 0o755))
	ciPlanRunGitAt(f.t, f.realGit, root, f.gitEnv(), "init", "--quiet", "--initial-branch=main")
	ciPlanMust(f.t, os.WriteFile(filepath.Join(root, relative), []byte("base\n"), 0o644))
	ciPlanRunGitAt(f.t, f.realGit, root, f.gitEnv(), "add", relative)
	ciPlanRunGitAt(f.t, f.realGit, root, f.gitEnv(), "commit", "--quiet", "-m", "fixture root")
}
func (f *ciPlanFixture) configureFilter(name string, driver string) string {
	f.t.Helper()
	marker := filepath.Join(f.t.TempDir(), "filter-ran")
	filter := filepath.Join(f.t.TempDir(), "filter")
	f.writeExecutable(filter, "#!/bin/bash\n: >"+strconv.Quote(marker)+"\n"+map[string]string{"clean": "exec /bin/cat\n", "process": "exit 1\n"}[driver])
	f.git("config", "filter."+name+"."+driver, filter)
	f.git("config", "filter."+name+".required", "true")
	return marker
}
func (f *ciPlanFixture) commit(message string) string {
	f.t.Helper()
	f.git("add", "--all")
	f.git("commit", "--quiet", "-m", message)
	return f.git("rev-parse", "HEAD")
}
func (f *ciPlanFixture) run(args ...string) ciPlanResult {
	f.t.Helper()
	return f.runCommand(filepath.Join(f.root, "scripts", "ci-plan.sh"), args...)
}
func (f *ciPlanFixture) runCommand(command string, args ...string) ciPlanResult {
	f.t.Helper()
	argv := append([]string{}, args...)
	argv = append(argv, "--format", "json")
	cmd := exec.Command(command, argv...)
	cmd.Env = f.planEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := ciPlanResult{stdout: stdout.String(), stderr: stderr.String()}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.code = exitErr.ExitCode()
			return result
		}
		f.t.Fatalf("ci-plan execution failed: %v", err)
	}
	return result
}
func (f *ciPlanFixture) runCommandEnv(command string, extraEnv []string, args ...string) ciPlanResult {
	return f.runCommand("/usr/bin/env", append(append(append([]string{}, extraEnv...), command), args...)...)
}
func (f *ciPlanFixture) runConfig(args ...string) ciPlanCapturedConfig {
	return f.runConfigEnv(nil, args...)
}
func (f *ciPlanFixture) runConfigEnv(extraEnv []string, args ...string) ciPlanCapturedConfig {
	f.t.Helper()
	return f.requireConfig(f.runCommandEnv(filepath.Join(f.root, "scripts", "ci-plan.sh"), extraEnv, args...))
}
func (f *ciPlanFixture) requireConfig(result ciPlanResult) ciPlanCapturedConfig {
	f.t.Helper()
	if result.code != 0 {
		f.t.Fatalf("ci-plan failed: code=%d\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
	}
	var config ciPlanCapturedConfig
	if err := json.Unmarshal([]byte(result.stdout), &config); err != nil {
		f.t.Fatalf("decode ci-plan capture: %v\n%s", err, result.stdout)
	}
	return config
}
func (f *ciPlanFixture) requireMutantPaths(original, replacement string, env []string, expected ...string) {
	f.t.Helper()
	f.replaceScript(original, replacement)
	requireCIPlanPaths(f.t, f.runConfigEnv(env, "--base", "main").ChangedFiles, expected...)
}
func (f *ciPlanFixture) installGitWrapper(body string) {
	f.t.Helper()
	f.writeExecutable(filepath.Join(f.binDir, "git"), "#!/bin/bash\nset -euo pipefail\nreal_git="+strconv.Quote(f.realGit)+"\n"+body)
}
func (f *ciPlanFixture) stubGit(command string, output string, status int) {
	f.t.Helper()
	f.installGitWrapper("case \" $* \" in *" + strconv.Quote(command) + "*) printf '%b' " + strconv.Quote(output) + "; exit " + strconv.Itoa(status) + ";; esac\nexec \"${real_git}\" \"$@\"\n")
}
func (f *ciPlanFixture) replaceScript(original string, replacement string) {
	f.t.Helper()
	path := filepath.Join(f.root, "scripts", "ci-plan.sh")
	content, err := os.ReadFile(path)
	ciPlanMust(f.t, err)
	if count := strings.Count(string(content), original); count == 0 {
		f.t.Fatalf("ci-plan mutation anchor missing: %q", original)
	}
	mutated := strings.ReplaceAll(string(content), original, replacement)
	ciPlanMust(f.t, os.WriteFile(path, []byte(mutated), 0o755))
}
func requireCIPlanPaths(t *testing.T, actual []string, expected ...string) {
	t.Helper()
	actual, expected = append([]string{}, actual...), append([]string{}, expected...)
	slices.Sort(actual)
	slices.Sort(expected)
	actual = slices.Compact(actual)
	if fmt.Sprint(actual) != fmt.Sprint(expected) {
		t.Fatalf("changed files = %#v, want %#v", actual, expected)
	}
}
func requireCIPlanError(t *testing.T, result ciPlanResult, code int, message string) {
	t.Helper()
	if result.code == 0 || (code >= 0 && result.code != code) || !strings.Contains(result.stderr, message) {
		t.Fatalf("ci-plan error = code %d, stderr %q; want code %d containing %q", result.code, result.stderr, code, message)
	}
}
func requireCIPlanExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Stat(path)
	if (want && err != nil) || (!want && !os.IsNotExist(err)) {
		t.Fatalf("path existence = %v, error %v; want %v for %s", err == nil, err, want, path)
	}
}
func TestCIPlanRejectsHiddenGitStateAuthorities(t *testing.T) {
	const rootCheck = "  reject_hidden_index_entries || return $?\n"
	for name, flag := range map[string]string{"assume-unchanged": "--assume-unchanged", "skip-worktree": "--skip-worktree"} {
		t.Run(name, func(t *testing.T) {
			fixture := newCIPlanTopicFixture(t, "docs/hidden.md", "base\n", "runtime/visible.go", "base\n")
			fixture.git("update-index", flag, "docs/hidden.md")
			fixture.writeTextFiles("docs/hidden.md", "hidden\n", "runtime/visible.go", "visible\n")
			result := fixture.run("--base", "main")
			requireCIPlanError(t, result, -1, "hidden-worktree flag")
			fixture.requireMutantPaths(rootCheck, "  :\n", nil, "docs/hidden.md", "runtime/visible.go", "scripts/ci-plan.sh")
		})
	}
	t.Run("inherited-index", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1")
		fixture.writeTextFiles("docs/hidden.md", "base\n", "runtime/visible.go", "base\n")
		fixture.commit("tracked files")
		alternate := filepath.Join(t.TempDir(), "index")
		ciPlanRunGitAt(t, fixture.realGit, fixture.root, append(fixture.gitEnv(), "GIT_INDEX_FILE="+alternate), "read-tree", "HEAD")
		fixture.git("checkout", "--quiet", "-b", "topic")
		fixture.git("update-index", "--skip-worktree", "docs/hidden.md")
		fixture.writeTextFiles("docs/hidden.md", "hidden\n", "runtime/visible.go", "visible\n")
		env := []string{"PLANNER_INDEX_FILE=" + alternate}
		requireCIPlanError(t, fixture.runCommandEnv(filepath.Join(fixture.root, "scripts", "ci-plan.sh"), env, "--base", "main"), -1, "hidden-worktree flag")
		fixture.requireMutantPaths(`PLANNER_INDEX_FILE=""`, `:`, env, "docs/hidden.md", "runtime/visible.go", "scripts/ci-plan.sh")
	})
	t.Run("info-exclude", func(t *testing.T) {
		fixture := newCIPlanTopicFixture(t, ".gitignore", "generated/\n")
		ciPlanMust(t, os.WriteFile(filepath.Join(fixture.root, ".git", "info", "exclude"), []byte("docs/hidden.md\n"), 0o600))
		fixture.writeTextFiles("docs/hidden.md", "untracked\n", "runtime/visible.go", "untracked\n", "generated/cache", "ignored\n")
		requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "docs/hidden.md", "runtime/visible.go")
		fixture.requireMutantPaths("--exclude-per-directory=.gitignore", "--exclude-standard", nil, "runtime/visible.go", "scripts/ci-plan.sh")
	})
	for _, testCase := range []struct {
		name, path, rules string
		tracked, staged   bool
		expected          []string
	}{
		{"modified", ".gitignore", "generated/\ndocs/hidden.md\n", true, false, []string{".gitignore", "runtime/visible.go", "scripts/ci-plan.sh"}},
		{"staged", ".gitignore", "generated/\ndocs/hidden.md\n", true, true, []string{".gitignore", "runtime/visible.go", "scripts/ci-plan.sh"}},
		{"untracked-root", ".gitignore", ".gitignore\ndocs/hidden.md\n", false, false, []string{"runtime/visible.go", "scripts/ci-plan.sh"}},
		{"untracked-nested", "docs/.gitignore", ".gitignore\nhidden.md\n", false, false, []string{"runtime/visible.go", "scripts/ci-plan.sh"}},
		{"case-variant", ".GITIGNORE", ".GITIGNORE\ndocs/hidden.md\n", false, false, []string{"runtime/visible.go", "scripts/ci-plan.sh"}},
	} {
		t.Run(testCase.name+"-ignore", func(t *testing.T) {
			fixture := newCIPlanFixture(t, "sha1")
			if testCase.tracked {
				fixture.writeFile(testCase.path, []byte("generated/\n"), 0o644)
				fixture.commit("tracked ignore rules")
			}
			fixture.git("checkout", "--quiet", "-b", "topic")
			fixture.writeFile(testCase.path, []byte(testCase.rules), 0o644)
			if testCase.staged {
				fixture.git("add", testCase.path)
			}
			fixture.writeTextFiles("docs/hidden.md", "hidden\n", "runtime/visible.go", "visible\n")
			requireCIPlanError(t, fixture.run("--base", "main"), -1, ".gitignore")
			if _, err := os.Stat(filepath.Join(fixture.root, ".gitignore")); testCase.name == "case-variant" && err != nil {
				return
			}
			fixture.replaceScript("  reject_mutable_ignore_files \"${mutable_ignore_start}\" || return $?\n", "")
			requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, testCase.expected...)
		})
	}
	t.Run("committed-ignore", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1")
		fixture.git("checkout", "--quiet", "-b", "topic")
		fixture.writeFile(".gitignore", []byte("generated/\n"), 0o644)
		fixture.commit("topic ignore rules")
		fixture.writeTextFiles("generated/cache", "ignored\n", "runtime/visible.go", "visible\n")
		requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, ".gitignore", "runtime/visible.go")
		fixture.writeFile(".gitignore", []byte("generated/\ndocs/hidden.md\n"), 0o644)
		requireCIPlanError(t, fixture.run("--base", "main"), -1, ".gitignore")
	})
}
func TestCIPlanGitCollectorPreservesRepositoryState(t *testing.T) {
	fixture := newCIPlanTopicFixture(t, "committed.txt", "base\n", "modified.txt", "base\n", "deleted.txt", "base\n", "staged.txt", "base\n")
	requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles)
	fixture.writeFile("committed.txt", []byte("topic\n"), 0o644)
	fixture.commit("topic commit")
	fixture.writeFile("modified.txt", []byte("modified\n"), 0o644)
	ciPlanMust(t, os.Remove(filepath.Join(fixture.root, "deleted.txt")))
	fixture.writeTextFiles("staged.txt", "staged\n", "untracked.txt", "untracked\n", "docs/review\n.md", "newline path\n")
	fixture.git("add", "staged.txt")
	indexBefore := ciPlanFileState(t, filepath.Join(fixture.root, ".git", "index"))
	config := fixture.runConfig("--base", "main")
	if after := ciPlanFileState(t, filepath.Join(fixture.root, ".git", "index")); after != indexBefore {
		t.Fatal("planner mutated the real index")
	}
	requireCIPlanPaths(t, config.ChangedFiles, "committed.txt", "deleted.txt", "docs/review\n.md", "modified.txt", "staged.txt", "untracked.txt")
}
func TestCIPlanGitCollectorAcceptsSHA256Repository(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha256")
	if head := fixture.git("rev-parse", "HEAD"); len(head) != 64 {
		t.Fatalf("sha256 object id length = %d, want 64", len(head))
	}
	fixture.git("checkout", "--quiet", "-b", "topic")
	fixture.writeFile("untracked.txt", []byte("untracked\n"), 0o644)
	requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "untracked.txt")
}
func TestCIPlanSystemBashDefaultLabelsAndExplicitPaths(t *testing.T) {
	fixture := newCIPlanTopicFixture(t)
	fixture.writeFile("untracked.txt", []byte("untracked\n"), 0o644)
	script := filepath.Join(fixture.root, "scripts", "ci-plan.sh")
	marker := filepath.Join(t.TempDir(), "imported-function-ran")
	invalidPath := string([]byte{'x', '/', 0xff, '.', 'm', 'd'})
	ciPlanMust(t, os.Symlink("/bin/bash", filepath.Join(fixture.binDir, "bash")))
	result := fixture.runCommand("/usr/bin/env", "SHELLOPTS=braceexpand:noclobber:onecmd", "BASHOPTS=nocasematch",
		"BASH_COMPAT=foo", "BASH_XTRACEFD=foo", "FUNCNEST=2", "LC_ALL=bogus.invalid", "POSIXLY_CORRECT=y", "POSIX_PEDANTIC=y",
		"BASH_FUNC_mktemp%%=() { : > "+strconv.Quote(marker)+"; }", script, "--base", "main")
	if result.stderr != "" {
		t.Fatalf("privileged shebang failed: code=%d stderr=%q", result.code, result.stderr)
	}
	config := fixture.requireConfig(result)
	requireCIPlanExists(t, marker, false)
	requireCIPlanPaths(t, config.ChangedFiles, "untracked.txt")
	fixture.stubGit(" ls-files --others --exclude-per-directory=.gitignore -z ", "x/\\377.md\\0", 0)
	requireCIPlanError(t, fixture.run("--base", "main"), -1, "valid UTF-8")
	fixture.installGitWrapper("exit 97\n")
	result = fixture.runCommand("/bin/bash", script, "--profile", "release-preflight",
		"--event", "workflow_dispatch", "--base", "release", "--label", "one", "--label", "two",
		"--no-auto-changed-files", "--changed-file", "docs/one\n.md", "--changed-file", "runtime/two.go")
	config = fixture.requireConfig(result)
	if config.Profile != "release-preflight" ||
		config.Event != "workflow_dispatch" ||
		config.BaseBranch != "release" ||
		fmt.Sprint(config.Labels) != "[one two]" {
		t.Fatalf("explicit planner inputs changed unexpectedly: %#v", config)
	}
	requireCIPlanPaths(t, config.ChangedFiles, "docs/one\n.md", "runtime/two.go")
	for _, invalid := range []string{invalidPath, "x/\xf4\x90\x80\x80.md", "x/\xed\xa0\x80.md", "x/\xc0\x80.md"} {
		requireCIPlanError(t, fixture.runCommand("/bin/bash", script, "--no-auto-changed-files", "--changed-file", invalid), -1, "valid UTF-8")
	}
	fixture.replaceScript("/usr/bin/cmp -s", "/usr/bin/true")
	config = fixture.requireConfig(fixture.runCommand("/bin/bash", script, "--no-auto-changed-files", "--changed-file", invalidPath))
	requireCIPlanPaths(t, config.ChangedFiles, "x/�.md")
	fixture.replaceScript(`printf '%s\0'`, `printf '%s\n'`)
	config = fixture.requireConfig(fixture.runCommand("/bin/bash", script, "--no-auto-changed-files", "--changed-file", "docs/one\n.md"))
	requireCIPlanPaths(t, config.ChangedFiles)
}
func TestCIPlanGitCollectorRejectsBaseFiltersBeforeExecution(t *testing.T) {
	const rejectionAnchor = "  reject_conversion_filters || return $?\n"
	t.Run("uncommitted-attributes-stay-pinned", func(t *testing.T) {
		fixture := newCIPlanTopicFixture(t, "tracked.txt", "base\n")
		marker := fixture.configureFilter("hostile", "clean")
		fixture.writeTextFiles(".gitattributes", "*.txt filter=hostile\n", "tracked.txt", "dirty\n")
		_ = fixture.runConfig("--base", "main")
		requireCIPlanExists(t, marker, false)
	})
	t.Run("inherited-index-cannot-hide-filter", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1")
		rootOID := fixture.git("rev-parse", "HEAD")
		fixture.writeTextFiles("tracked.txt", "base\n", ".gitattributes", "*.txt filter=hostile\n")
		fixture.commit("base filter fixture")
		fixture.git("checkout", "--quiet", "-b", "topic")
		marker := fixture.configureFilter("hostile", "clean")
		fixture.writeFile("tracked.txt", []byte("worktree\n"), 0o644)
		alternate := filepath.Join(t.TempDir(), "index")
		ciPlanRunGitAt(t, fixture.realGit, fixture.root, append(fixture.gitEnv(), "GIT_INDEX_FILE="+alternate), "read-tree", rootOID)
		env := []string{"PLANNER_INDEX_FILE=" + alternate}
		requireCIPlanError(t, fixture.runCommandEnv(filepath.Join(fixture.root, "scripts", "ci-plan.sh"), env, "--base", "main"), -1, "select conversion filter hostile")
		fixture.replaceScript(`PLANNER_INDEX_FILE=""`, `:`)
		if result := fixture.runCommandEnv(filepath.Join(fixture.root, "scripts", "ci-plan.sh"), env, "--base", "main"); result.code != 0 {
			t.Fatalf("inherited-index mutant remained fail closed: %s", result.stderr)
		}
		requireCIPlanExists(t, marker, false)
	})
	docs, docsErr := os.ReadFile(filepath.Join(repoRoot(t), "docs", "validation-scenarios.md"))
	workflowDocs, workflowDocsErr := os.ReadFile(filepath.Join(repoRoot(t), "docs", "github-workflows.md"))
	for _, testCase := range []string{"hostile/clean", "hostile/process", "unset/clean", "unset/process", "unspecified/clean", "unspecified/process"} {
		driverName, driver, _ := strings.Cut(testCase, "/")
		t.Run(testCase, func(t *testing.T) {
			fixture := newCIPlanTopicFixture(t, "tracked.txt", "base\n", ".gitattributes", "*.txt filter="+driverName+"\n")
			marker := fixture.configureFilter(driverName, driver)
			fixture.writeFile("tracked.txt", []byte("worktree\n"), 0o644)
			result := fixture.run("--base", "main")
			if result.code == 0 || docsErr != nil || workflowDocsErr != nil || !bytes.Contains(docs, []byte("rejects nonregular or split-index state")) || !bytes.Contains(docs, []byte("conversion filters")) || !bytes.Contains(workflowDocs, []byte("fail-closed resident Git discovery")) ||
				!strings.Contains(result.stderr, "effective pinned attributes select conversion filter "+driverName+" for tracked.txt") ||
				strings.Contains(result.stderr, marker) {
				t.Fatalf("base %s/%s filter or scope docs failed: code=%d stderr=%q docs=%v/%v", driverName, driver, result.code, result.stderr, docsErr, workflowDocsErr)
			}
			requireCIPlanExists(t, marker, false)
			fixture.replaceScript(rejectionAnchor, "")
			_ = fixture.run("--base", "main")
			requireCIPlanExists(t, marker, true)
		})
	}
}
func TestCIPlanRejectsPresentGitlinkPathsBeforeInspection(t *testing.T) {
	for _, authority := range []string{"info-exclude", "gitignore"} {
		t.Run(authority, func(t *testing.T) {
			fixture := newCIPlanFixture(t, "sha1")
			moduleRoot := filepath.Join(t.TempDir(), "module")
			fixture.initRepository(moduleRoot, "module.txt")
			fixture.git("-c", "protocol.file.allow=always", "submodule", "add", "--quiet", moduleRoot, "modules/demo")
			fixture.commit("tracked submodule")
			fixture.git("checkout", "--quiet", "-b", "topic")
			checkout := filepath.Join(fixture.root, "modules", "demo")
			if authority == "info-exclude" {
				gitDir := ciPlanRunGitAt(t, fixture.realGit, checkout, fixture.gitEnv(), "rev-parse", "--absolute-git-dir")
				ciPlanMust(t, os.WriteFile(filepath.Join(gitDir, "info", "exclude"), []byte("hidden.txt\n"), 0o600))
			} else {
				ciPlanMust(t, os.WriteFile(filepath.Join(checkout, ".gitignore"), []byte(".gitignore\nhidden.txt\n"), 0o644))
			}
			ciPlanMust(t, os.WriteFile(filepath.Join(checkout, "hidden.txt"), []byte("hidden\n"), 0o644))
			fixture.writeFile("runtime/visible.go", []byte("visible\n"), 0o644)
			requireCIPlanError(t, fixture.run("--base", "main"), -1, "gitlink worktree path is present")
			fixture.requireMutantPaths("  prepare_planner_index_snapshot || return $?\n", "", nil, "runtime/visible.go", "scripts/ci-plan.sh")
		})
	}
	t.Run("absent-gitlink", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1")
		moduleRoot := filepath.Join(t.TempDir(), "module")
		fixture.initRepository(moduleRoot, "module.txt")
		fixture.git("-c", "protocol.file.allow=always", "submodule", "add", "--quiet", moduleRoot, "modules/demo")
		fixture.commit("tracked submodule")
		fixture.git("checkout", "--quiet", "-b", "topic")
		fixture.git("submodule", "deinit", "--force", "modules/demo")
		requireCIPlanExists(t, filepath.Join(fixture.root, "modules", "demo"), true)
		requireCIPlanError(t, fixture.run("--base", "main"), -1, "gitlink worktree path is present")
		ciPlanMust(t, os.RemoveAll(filepath.Join(fixture.root, "modules", "demo")))
		fixture.writeFile("runtime/visible.go", []byte("visible\n"), 0o644)
		requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "modules/demo", "runtime/visible.go")
	})
}
func TestCIPlanRejectsSymlinkedTrackedAncestryBeforeRawRead(t *testing.T) {
	fixture := newCIPlanTopicFixture(t, "tracked/file", "base\n")
	external, marker := t.TempDir(), filepath.Join(t.TempDir(), "hash-ran")
	ciPlanMust(t, os.WriteFile(filepath.Join(external, "file"), []byte("base\n"), 0o644))
	ciPlanMust(t, os.RemoveAll(filepath.Join(fixture.root, "tracked")))
	ciPlanMust(t, os.Symlink(external, filepath.Join(fixture.root, "tracked")))
	fixture.installGitWrapper(`case " $* " in *" hash-object --no-filters -- "*) : >` + strconv.Quote(marker) + `;; esac
	exec "${real_git}" "$@"`)
	requireCIPlanError(t, fixture.run("--base", "main"), -1, "tracked-file ancestry is not a regular directory")
	ciPlanMust(t, os.Remove(filepath.Join(fixture.root, "tracked")))
	fixture.git("reset", "--quiet", "--hard", "main")
	ciPlanMust(t, os.Remove(filepath.Join(fixture.root, "tracked", "file")))
	ciPlanMust(t, os.Symlink("target", filepath.Join(fixture.root, "tracked", "link")))
	fixture.commit("tracked symlink ancestry")
	ciPlanMust(t, os.RemoveAll(filepath.Join(fixture.root, "tracked")))
	ciPlanMust(t, os.Symlink(external, filepath.Join(fixture.root, "tracked")))
	requireCIPlanError(t, fixture.run("--base", "main"), -1, "tracked-file ancestry is not a regular directory")
	requireCIPlanExists(t, marker, false)
}
func TestCIPlanRejectsRepositoryMetadataRedirection(t *testing.T) {
	fixture := newCIPlanTopicFixture(t)
	fixture.writeFile(".git/commondir", []byte("../../.git\n"), 0o600)
	requireCIPlanError(t, fixture.run("--base", "main"), -1, "must not redirect the common Git directory")
	ciPlanMust(t, os.Remove(filepath.Join(fixture.root, ".git", "commondir")))
	parentGit := filepath.Join(filepath.Dir(fixture.root), ".git")
	ciPlanMust(t, os.Rename(filepath.Join(fixture.root, ".git"), parentGit))
	fixture.writeFile(".git", []byte("gitdir: "+parentGit+"\n"), 0o600)
	requireCIPlanError(t, fixture.run("--base", "main"), -1, "anchored by the script root .git directory")
}
func TestCIPlanGitCollectorBindsCanonicalWorktreeAndOverridesLocalConfig(t *testing.T) {
	fixture := newCIPlanTopicFixture(t)
	fixture.writeFile("real-worktree.txt", []byte("real\n"), 0o644)
	decoy := filepath.Join(t.TempDir(), "decoy")
	ciPlanMust(t, os.MkdirAll(decoy, 0o755))
	ciPlanMust(t, os.WriteFile(filepath.Join(decoy, "decoy-only.txt"), []byte("decoy\n"), 0o644))
	fixture.git("config", "core.worktree", decoy)
	fixture.git("config", "core.bare", "true")
	requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "real-worktree.txt")
	fixture.replaceScript("--git-dir=\"${git_dir}\" ", "")
	fixture.replaceScript("--work-tree=\"${work_tree}\" ", "")
	fixture.replaceScript("-c core.bare=false ", "")
	fixture.replaceScript("-c \"core.worktree=${work_tree}\" ", "")
	mutant := fixture.run("--base", "main")
	if mutant.code == 0 {
		var captured ciPlanCapturedConfig
		if err := json.Unmarshal([]byte(mutant.stdout), &captured); err == nil {
			for _, path := range captured.ChangedFiles {
				if path == "real-worktree.txt" {
					t.Fatalf("unbound Git mutant retained the canonical worktree: %#v", captured.ChangedFiles)
				}
			}
		}
	} else if !strings.Contains(mutant.stderr, "Unable to derive changed files") {
		t.Fatalf("unbound Git mutant failed unexpectedly: code=%d stderr=%q", mutant.code, mutant.stderr)
	}
}
func TestCIPlanOverridesStatCacheOmissionConfig(t *testing.T) {
	fixture := newCIPlanTopicFixture(t, ".gitattributes", "* text=auto\n", ".gitignore", "generated/A\n")
	fixture.git("config", "core.trustctime", "false")
	fixture.git("config", "core.checkStat", "minimal")
	fixture.git("config", "core.ignoreStat", "true")
	ignorePath := filepath.Join(fixture.root, ".gitignore")
	info, err := os.Stat(ignorePath)
	ciPlanMust(t, err)
	stamp := info.ModTime().AddDate(-1, 0, 0)
	ciPlanMust(t, os.Chtimes(ignorePath, stamp, stamp))
	fixture.git("update-index", "--refresh")
	fixture.writeFile(".gitignore", []byte("generated/A\r\n"), 0o644)
	ciPlanMust(t, os.Chtimes(ignorePath, stamp, stamp))
	fixture.writeFile("runtime/visible.go", []byte("visible\n"), 0o644)
	fixture.git("diff", "--quiet")
	requireCIPlanError(t, fixture.run("--base", "main"), -1, ".gitignore")
	fixture.replaceScript("    -c core.ignoreStat=false -c core.protectHFS=true -c core.protectNTFS=true \\\n", "    -c core.protectHFS=true -c core.protectNTFS=true \\\n")
	fixture.replaceScript(`    [[ "${actual}" == "${tracked_oids[${index}]}" ]] || CHANGED_FILES[${#CHANGED_FILES[@]}]="${tracked_paths[${index}]}"`, "    :")
	requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "runtime/visible.go")
	fixture.replaceScript("    -c core.protectHFS=true -c core.protectNTFS=true \\\n", "    -c core.ignoreStat=false -c core.protectHFS=true -c core.protectNTFS=true \\\n")
	fixture.requireMutantPaths(`planner_git update-index -z --index-info <"${inventory}"`, `/bin/cp "${PLANNER_GIT_DIR}/index" "${PLANNER_INDEX_FILE}"`, nil, "runtime/visible.go", "scripts/ci-plan.sh")
}
func TestCIPlanOverridesTrackedWorktreeOmissionConfig(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1")
	fixture.writeTextFiles(".gitattributes", "scripts/crlf-probe.sh text=auto\nscripts/eol-probe.sh text eol=crlf\nscripts/ident-probe.sh ident\nscripts/encoding-probe.txt working-tree-encoding=UTF-16\n", "scripts/ident-probe.sh", "$Id$\n")
	fixture.writeFile("scripts/mode-probe.sh", []byte("#!/bin/sh\nexit 0\n"), 0o755)
	fixture.writeFile("scripts/crlf-probe.sh", []byte("#!/bin/sh\necho x\n"), 0o755)
	fixture.writeFile("scripts/eol-probe.sh", []byte("#!/bin/sh\necho x\n"), 0o755)
	fixture.writeFile("scripts/encoding-probe.txt", []byte{0xff, 0xfe, 'x', 0, '\n', 0}, 0o644)
	ciPlanMust(t, os.Symlink("mode-probe.sh", filepath.Join(fixture.root, "scripts", "link-probe")))
	fixture.commit("tracked config probes")
	fixture.git("checkout", "--quiet", "-b", "topic")
	fixture.git("config", "core.fileMode", "false")
	fixture.git("config", "core.symlinks", "false")
	ciPlanMust(t, os.Chmod(filepath.Join(fixture.root, "scripts", "mode-probe.sh"), 0o644))
	ciPlanMust(t, os.Remove(filepath.Join(fixture.root, "scripts", "link-probe")))
	fixture.writeTextFiles("scripts/link-probe", "mode-probe.sh", "scripts/ident-probe.sh", "$Id: arbitrary $\n", "runtime/visible.go", "visible\n")
	fixture.writeFile("scripts/crlf-probe.sh", []byte("#!/bin/sh\r\necho x\r\n"), 0o755)
	fixture.writeFile("scripts/eol-probe.sh", []byte("#!/bin/sh\r\necho x\r\n"), 0o755)
	fixture.git("diff", "--quiet")
	requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "runtime/visible.go", "scripts/crlf-probe.sh", "scripts/encoding-probe.txt", "scripts/eol-probe.sh", "scripts/ident-probe.sh", "scripts/link-probe", "scripts/mode-probe.sh")
	fixture.requireMutantPaths("-c core.fileMode=true ", "", nil, "runtime/visible.go", "scripts/ci-plan.sh", "scripts/crlf-probe.sh", "scripts/encoding-probe.txt", "scripts/eol-probe.sh", "scripts/ident-probe.sh", "scripts/link-probe")
	fixture.requireMutantPaths("-c core.symlinks=true ", "", nil, "runtime/visible.go", "scripts/ci-plan.sh", "scripts/crlf-probe.sh", "scripts/encoding-probe.txt", "scripts/eol-probe.sh", "scripts/ident-probe.sh")
	fixture.requireMutantPaths(`    [[ "${actual}" == "${tracked_oids[${index}]}" ]] || CHANGED_FILES[${#CHANGED_FILES[@]}]="${tracked_paths[${index}]}"`, "    :", nil, "runtime/visible.go", "scripts/ci-plan.sh")
}
func TestCIPlanRejectsSplitIndexWithoutMutation(t *testing.T) {
	fixture := newCIPlanTopicFixture(t)
	index := filepath.Join(fixture.root, ".git", "index")
	fixture.git("config", "core.splitIndex", "true")
	flatBefore := ciPlanFileState(t, index)
	requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles)
	sharedFiles, err := filepath.Glob(filepath.Join(fixture.root, ".git", "sharedindex.*"))
	ciPlanMust(t, err)
	if flatBefore != ciPlanFileState(t, index) || len(sharedFiles) != 0 {
		t.Fatal("planner inherited split-index config or mutated the real index")
	}
	fixture.git("config", "--unset", "core.splitIndex")
	fixture.git("update-index", "--split-index")
	shared := fixture.git("rev-parse", "--shared-index-path")
	if !filepath.IsAbs(shared) {
		shared = filepath.Join(fixture.root, shared)
	}
	alias, hop := filepath.Join(filepath.Dir(shared), "ſharedindex."+strings.TrimPrefix(filepath.Base(shared), "sharedindex.")), shared+".hop"
	ciPlanMust(t, os.Rename(shared, hop))
	ciPlanMust(t, os.Rename(hop, alias))
	if _, err := os.Stat(shared); err == nil {
		shared = alias
	} else {
		ciPlanMust(t, os.Rename(alias, shared))
	}
	indexBefore, sharedBefore := ciPlanFileState(t, index), ciPlanFileState(t, shared)
	env := []string{"SHELLOPTS=braceexpand:noclobber:noglob", "BASHOPTS=nocasematch", "GLOBIGNORE=sharedindex.*"}
	requireCIPlanError(t, fixture.runCommandEnv(filepath.Join(fixture.root, "scripts", "ci-plan.sh"), env, "--base", "main"), -1, "split-index state is not accepted")
	if indexAfter, sharedAfter := ciPlanFileState(t, index), ciPlanFileState(t, shared); indexBefore != indexAfter || sharedBefore != sharedAfter {
		t.Fatalf("split-index rejection mutated state: index %s -> %s; shared %s -> %s", indexBefore, indexAfter, sharedBefore, sharedAfter)
	}
	fixture.requireMutantPaths("  reject_split_index || return $?\n", "", env, "scripts/ci-plan.sh")
}
func TestCIPlanRejectsShallowGraphThatChangesMergeBase(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1")
	fixture.writeFile("runtime/critical.go", []byte("topic\n"), 0o644)
	commitA := fixture.commit("A: topic value")
	fixture.writeFile("runtime/critical.go", []byte("base\n"), 0o644)
	fixture.commit("B: main value")
	fixture.git("checkout", "--quiet", "-b", "topic")
	fixture.writeFile("docs/topic.md", []byte("topic docs\n"), 0o644)
	commitP := fixture.commit("P: topic docs")
	fixture.writeFile("runtime/critical.go", []byte("topic\n"), 0o644)
	fixture.git("add", "runtime/critical.go")
	commitM := fixture.git("commit-tree", fixture.git("write-tree"), "-p", commitP, "-p", commitA, "-m", "M")
	fixture.git("reset", "--quiet", "--hard", commitM)
	requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "docs/topic.md", "runtime/critical.go")
	ciPlanMust(t, os.WriteFile(filepath.Join(fixture.root, ".git", "shallow"), []byte(commitP+"\n"), 0o600))
	requireCIPlanError(t, fixture.run("--base", "main"), -1, "shallow repositories are not accepted")
	fixture.requireMutantPaths("  reject_split_index || return $?\n", "", nil, "docs/topic.md", "scripts/ci-plan.sh")
}
func TestCIPlanRejectsMultipleMergeBases(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1")
	root := fixture.git("rev-parse", "HEAD")
	fixture.writeFile("runtime/critical.go", []byte("A\n"), 0o644)
	a1 := fixture.commit("A1")
	fixture.git("reset", "--quiet", "--hard", root)
	fixture.writeFile("policy/critical.go", []byte("B\n"), 0o644)
	b1 := fixture.commit("B1")
	a2 := fixture.git("commit-tree", fixture.git("rev-parse", a1+"^{tree}"), "-p", a1, "-p", b1, "-m", "A2")
	fixture.git("checkout", a1, "--", "runtime/critical.go")
	fixture.writeFile("docs/topic.md", []byte("topic\n"), 0o644)
	fixture.git("add", ".")
	b2 := fixture.git("commit-tree", fixture.git("write-tree"), "-p", b1, "-p", a1, "-m", "B2")
	fixture.git("update-ref", "refs/heads/main", a2)
	fixture.git("checkout", "--quiet", "-B", "topic", b2)
	chosen := fixture.git("merge-base", "main", "topic")
	requireCIPlanError(t, fixture.run("--base", "main"), -1, "exactly one resident merge base")
	want := map[string]string{a1: "policy/critical.go", b1: "runtime/critical.go"}[chosen]
	fixture.requireMutantPaths("merge-base --all", "merge-base", nil, "docs/topic.md", "scripts/ci-plan.sh", want)
}
func TestCIPlanBaseRefPresenceAndRetrievalFailClosed(t *testing.T) {
	for _, testCase := range []struct {
		name, command, output, want string
		status, code                int
		remote, dropLocal           bool
	}{
		{"unexpected-presence-status", " show-ref --exists refs/remotes/origin/main ", "", "Unable to inspect resident base ref refs/remotes/origin/main", 7, 7, false, false},
		{"retrieval-after-presence", " show-ref --verify --hash refs/remotes/origin/main ", "", "present base ref refs/remotes/origin/main could not be read", 8, 8, true, false},
		{"malformed-object-id", " show-ref --verify --hash refs/remotes/origin/main ", "ABCDEF0123456789ABCDEF0123456789ABCDEF01\n", "resident base ref returned a malformed object ID", 0, -1, true, false},
		{"both-refs-absent", "", "", "neither the resident origin/main ref nor local main branch exists", 0, -1, false, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newCIPlanFixture(t, "sha1")
			if testCase.remote {
				fixture.git("update-ref", "refs/remotes/origin/main", fixture.git("rev-parse", "main"))
			}
			fixture.git("checkout", "--quiet", "-b", "topic")
			if testCase.dropLocal {
				fixture.git("branch", "-D", "main")
			}
			if testCase.command != "" {
				fixture.stubGit(testCase.command, testCase.output, testCase.status)
			}
			requireCIPlanError(t, fixture.run("--base", "main"), testCase.code, testCase.want)
		})
	}
}
func TestCIPlanBaseRefRemoteAppearanceWinsLocalFallbackRace(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1")
	remoteOID := fixture.git("rev-parse", "HEAD")
	fixture.writeFile("local-main-only.txt", []byte("local main\n"), 0o644)
	fixture.commit("local main advance")
	fixture.git("checkout", "--quiet", "-b", "topic")
	counter := filepath.Join(t.TempDir(), "remote-created")
	fixture.installGitWrapper(`case " $* " in *" show-ref --exists refs/remotes/origin/main "*) if [[ ! -e ` + strconv.Quote(counter) + ` ]]; then
: >` + strconv.Quote(counter) + `
"${real_git}" --git-dir=` + strconv.Quote(filepath.Join(fixture.root, ".git")) + ` update-ref refs/remotes/origin/main ` + strconv.Quote(remoteOID) + `
exit 2; fi;; esac
exec "${real_git}" "$@"`)
	requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "local-main-only.txt")
	requireCIPlanExists(t, counter, true)
}
func TestCIPlanRejectsMalformedGitMetadata(t *testing.T) {
	for _, testCase := range []struct{ name, command, output, want string }{
		{"attributes", " check-attr -z --all --stdin ", "tracked-without-nul", "incomplete attribute record"},
		{"index-flags", " ls-files -v -z ", "H tracked-without-nul", "incomplete tracked-index record"},
		{"unknown-index-tag", " ls-files -v -z ", "X tracked\\0", "malformed tracked-index record"},
		{"shallow-path", " rev-parse --path-format=absolute --git-path shallow ", "relative\n", "resolve shallow-repository state"},
		{"staged-files", " ls-files --stage -z ", "malformed\\0", "malformed staged-file record"},
		{"staged-metadata", " ls-files --stage -z ", "16000x 0123456789012345678901234567890123456789 0\\tmodules/demo\\0", "malformed staged-file record"},
		{"tracked-content", " hash-object --no-filters -- ", "malformed\n", "malformed tracked-content hashes"},
		{"untracked-files", " ls-files --others -z ", "partial", "incomplete untracked-file record"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newCIPlanTopicFixture(t)
			fixture.stubGit(testCase.command, testCase.output, 0)
			requireCIPlanError(t, fixture.run("--base", "main"), -1, testCase.want)
		})
	}
	t.Run("disappearing-output", func(t *testing.T) {
		fixture := newCIPlanTopicFixture(t)
		fixture.installGitWrapper("case \" $* \" in *\" ls-files -v -z \"*) \"${real_git}\" \"$@\"; status=$?; /bin/rm -f -- \"${TMPDIR}\"/workcell-ci-plan-git.*/output.*; exit \"${status}\";; esac\nexec \"${real_git}\" \"$@\"\n")
		requireCIPlanError(t, fixture.run("--base", "main"), -1, "Unable to derive changed files")
		fixture.requireMutantPaths(`done <"${inventory}" || return $?`, `done <"${inventory}"`, nil, "scripts/ci-plan.sh")
	})
}
func TestCIPlanGitDiscoveryNeverFetches(t *testing.T) {
	fixture := newCIPlanTopicFixture(t)
	marker := filepath.Join(t.TempDir(), "remote-ran")
	remote := filepath.Join(t.TempDir(), "remote")
	fixture.writeExecutable(remote, "#!/bin/bash\n: >"+strconv.Quote(marker)+"\nexit 1\n")
	fixture.git("remote", "add", "origin", "ext::"+remote)
	fixture.git("config", "protocol.ext.allow", "always")
	fixture.installGitWrapper(`case " $* " in *" rev-parse --absolute-git-dir "*) exec "${real_git}" "$@";; esac
[[ "${GIT_ATTR_GLOBAL-}|${GIT_ATTR_NOSYSTEM-}|${GIT_ATTR_SYSTEM-}|${GIT_GRAFT_FILE-}|${GIT_LITERAL_PATHSPECS-}|${GIT_NO_LAZY_FETCH-}|${GIT_NO_REPLACE_OBJECTS-}|${GIT_OPTIONAL_LOCKS-}|${GIT_TERMINAL_PROMPT-}|${GIT_CONFIG_GLOBAL-}|${GIT_CONFIG_NOSYSTEM-}|${GIT_CONFIG_SYSTEM-}|${GCM_INTERACTIVE-}" == "/dev/null|1|/dev/null|/dev/null|1|1|1|0|0|/dev/null|1|/dev/null|never" && -n "${GIT_ATTR_SOURCE-}" ]] || exit 96
[[ " $* " == *" --git-dir="*" --work-tree="*" --no-pager "*" core.askPass= "*" core.attributesFile=/dev/null "*" core.bare=false "*" core.excludesFile=/dev/null "*" core.fsmonitor=false "*" core.hooksPath=/dev/null "*" core.worktree="*" credential.helper= "*" credential.interactive=never "*" diff.ignoreSubmodules=none "* && ( " $* " != *" diff "* || " $* " == *" --no-ext-diff --no-textconv --no-renames --ignore-submodules=none "* ) ]] || exit 97
exec "${real_git}" "$@"`)
	if result := fixture.run("--base", "main"); result.code != 0 {
		t.Fatalf("resident planner failed: code=%d stderr=%q", result.code, result.stderr)
	}
	requireCIPlanExists(t, marker, false)
	anchor := "  base_oid=\"$(resolve_base_oid)\" || return $?\n"
	fixture.replaceScript(anchor, "  planner_git fetch origin main || return $?\n"+anchor)
	_ = fixture.run("--base", "main")
	requireCIPlanExists(t, marker, true)
}
func TestCIPlanRejectsNonBranchBaseSpellings(t *testing.T) {
	for _, base := range []string{"-main", "refs/heads/main", "main..topic", "main.lock"} {
		t.Run(strings.NewReplacer("/", "-", ".", "-").Replace(base), func(t *testing.T) {
			fixture := newCIPlanTopicFixture(t)
			result := fixture.run("--base", base)
			requireCIPlanError(t, result, 2, "Invalid --base branch name")
		})
	}
}
func newCIPlanHistoryFixture(t *testing.T) (*ciPlanFixture, string, string) {
	t.Helper()
	fixture := newCIPlanFixture(t, "sha1")
	rootOID := fixture.git("rev-parse", "HEAD")
	fixture.writeFile("local-main-only.txt", []byte("main\n"), 0o644)
	fixture.commit("local main")
	fixture.git("checkout", "--quiet", "-b", "topic")
	fixture.writeFile("topic-only.txt", []byte("topic\n"), 0o644)
	topicOID := fixture.commit("topic")
	return fixture, rootOID, topicOID
}
func TestCIPlanNeutralizesGraftsAndReplacementObjects(t *testing.T) {
	t.Run("graft-file", func(t *testing.T) {
		fixture, rootOID, topicOID := newCIPlanHistoryFixture(t)
		infoDir := filepath.Join(fixture.root, ".git", "info")
		ciPlanMust(t, os.MkdirAll(infoDir, 0o755))
		grafts := []byte(topicOID + " " + rootOID + "\n")
		ciPlanMust(t, os.WriteFile(filepath.Join(infoDir, "grafts"), grafts, 0o600))
		requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "topic-only.txt")
		fixture.requireMutantPaths("GIT_GRAFT_FILE=/dev/null \\\n", "", nil, "local-main-only.txt", "scripts/ci-plan.sh", "topic-only.txt")
	})
	t.Run("replacement-object", func(t *testing.T) {
		fixture, rootOID, topicOID := newCIPlanHistoryFixture(t)
		topicTree := fixture.git("rev-parse", topicOID+"^{tree}")
		replacementOID := fixture.git("commit-tree", topicTree, "-p", rootOID, "-m", "replacement topic")
		fixture.git("replace", topicOID, replacementOID)
		requireCIPlanPaths(t, fixture.runConfig("--base", "main").ChangedFiles, "topic-only.txt")
		fixture.requireMutantPaths("GIT_NO_REPLACE_OBJECTS=1 ", "", nil, "local-main-only.txt", "scripts/ci-plan.sh", "topic-only.txt")
	})
}
