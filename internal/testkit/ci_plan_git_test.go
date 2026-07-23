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
	"sort"
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

func newCIPlanFixture(t *testing.T, objectFormat string, script []byte) *ciPlanFixture {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if script == nil {
		script, err = os.ReadFile(filepath.Join(repoRoot(t), "scripts", "ci-plan.sh"))
		if err != nil {
			t.Fatal(err)
		}
	}
	stateRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := &ciPlanFixture{
		t:       t,
		root:    filepath.Join(stateRoot, "repo"),
		binDir:  filepath.Join(stateRoot, "bin"),
		homeDir: filepath.Join(stateRoot, "home"),
		tmpDir:  filepath.Join(stateRoot, "tmp"),
		realGit: realGit,
	}
	for _, dir := range []string{fixture.root, fixture.binDir, fixture.homeDir, fixture.tmpDir,
		filepath.Join(fixture.root, "scripts"), filepath.Join(fixture.root, "policy")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture.writeFile("scripts/ci-plan.sh", script, 0o755)
	fixture.writeFile("policy/workflow-lanes.json", []byte("{}\n"), 0o644)
	fixture.writeExecutable(
		filepath.Join(fixture.binDir, "go"),
		`#!/bin/bash
set -euo pipefail
exec /bin/cat "${!#}"
`,
	)

	initArgs := []string{"init", "--quiet", "--initial-branch=main"}
	if objectFormat != "" {
		initArgs = append(initArgs, "--object-format="+objectFormat)
	}
	ciPlanRunGitAt(t, realGit, fixture.root, fixture.gitEnv(), initArgs...)
	fixture.git("add", "--all")
	fixture.git("commit", "--quiet", "-m", "fixture root")
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
		"PATH=" + os.Getenv("PATH"), "HOME=" + f.homeDir, "TMPDIR=" + f.tmpDir, "LC_ALL=C",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Workcell Test", "GIT_AUTHOR_EMAIL=workcell-test@example.invalid", "GIT_AUTHOR_DATE=2001-02-03T04:05:06Z",
		"GIT_COMMITTER_NAME=Workcell Test", "GIT_COMMITTER_EMAIL=workcell-test@example.invalid", "GIT_COMMITTER_DATE=2001-02-03T04:05:06Z",
	}
}

func (f *ciPlanFixture) planEnv() []string {
	return []string{
		"PATH=" + f.binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + f.homeDir, "TMPDIR=" + f.tmpDir, "LC_ALL=C", "BASH_ENV=", "ENV=",
	}
}

func (f *ciPlanFixture) git(args ...string) string {
	f.t.Helper()
	bound := []string{
		"--git-dir=" + filepath.Join(f.root, ".git"),
		"--work-tree=" + f.root,
		"-c", "core.bare=false",
		"-c", "core.worktree=" + f.root,
	}
	return ciPlanRunGitAt(f.t, f.realGit, f.root, f.gitEnv(), append(bound, args...)...)
}

func (f *ciPlanFixture) writeFile(relative string, content []byte, mode os.FileMode) {
	f.t.Helper()
	path := filepath.Join(f.root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		f.t.Fatal(err)
	}
}

func (f *ciPlanFixture) writeExecutable(path string, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *ciPlanFixture) initRepository(root string, relative string) {
	f.t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		f.t.Fatal(err)
	}
	ciPlanRunGitAt(f.t, f.realGit, root, f.gitEnv(),
		"init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(root, relative), []byte("base\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	ciPlanRunGitAt(f.t, f.realGit, root, f.gitEnv(), "add", relative)
	ciPlanRunGitAt(f.t, f.realGit, root, f.gitEnv(),
		"commit", "--quiet", "-m", "fixture root")
}

func (f *ciPlanFixture) addSubmodule(source string, path string) {
	f.t.Helper()
	f.git("-c", "protocol.file.allow=always",
		"submodule", "add", "--quiet", source, path)
}

func (f *ciPlanFixture) configureFilter(name string, driver string) string {
	f.t.Helper()
	marker := filepath.Join(f.t.TempDir(), "filter-ran")
	filter := filepath.Join(f.t.TempDir(), "filter")
	body := "#!/bin/bash\n: >" + strconv.Quote(marker) + "\n" + map[string]string{"clean": "exec /bin/cat\n", "process": "exit 1\n"}[driver]
	f.writeExecutable(filter, body)
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
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := ciPlanResult{stdout: stdout.String(), stderr: stderr.String()}
	if err == nil {
		return result
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.code = exitErr.ExitCode()
		return result
	}
	f.t.Fatalf("ci-plan execution failed: %v", err)
	return ciPlanResult{}
}

func (f *ciPlanFixture) runConfig(args ...string) ciPlanCapturedConfig {
	f.t.Helper()
	result := f.run(args...)
	if result.code != 0 {
		f.t.Fatalf("ci-plan failed: code=%d\nstdout=%s\nstderr=%s", result.code, result.stdout, result.stderr)
	}
	var config ciPlanCapturedConfig
	if err := json.Unmarshal([]byte(result.stdout), &config); err != nil {
		f.t.Fatalf("decode ci-plan capture: %v\n%s", err, result.stdout)
	}
	return config
}

func (f *ciPlanFixture) installGitWrapper(body string) {
	f.t.Helper()
	f.writeExecutable(
		filepath.Join(f.binDir, "git"),
		"#!/bin/bash\nset -euo pipefail\nreal_git="+strconv.Quote(f.realGit)+"\n"+body,
	)
}

func (f *ciPlanFixture) replaceScript(original string, replacement string) {
	f.t.Helper()
	path := filepath.Join(f.root, "scripts", "ci-plan.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatal(err)
	}
	if count := strings.Count(string(content), original); count == 0 {
		f.t.Fatalf("ci-plan mutation anchor missing: %q", original)
	}
	mutated := strings.ReplaceAll(string(content), original, replacement)
	if err := os.WriteFile(path, []byte(mutated), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func requireCIPlanPaths(t *testing.T, actual []string, expected ...string) {
	t.Helper()
	actual = append([]string{}, actual...)
	expected = append([]string{}, expected...)
	sort.Strings(actual)
	sort.Strings(expected)
	if fmt.Sprint(actual) != fmt.Sprint(expected) {
		t.Fatalf("changed files = %#v, want %#v", actual, expected)
	}
}

func TestCIPlanGitCollectorPreservesRepositoryState(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1", nil)
	fixture.writeFile("committed.txt", []byte("base\n"), 0o644)
	fixture.writeFile("modified.txt", []byte("base\n"), 0o644)
	fixture.writeFile("deleted.txt", []byte("base\n"), 0o644)
	fixture.writeFile("staged.txt", []byte("base\n"), 0o644)
	fixture.commit("tracked fixture files")

	submoduleRoot := filepath.Join(t.TempDir(), "module")
	fixture.initRepository(submoduleRoot, "module.txt")
	fixture.addSubmodule(submoduleRoot, "modules/demo")
	fixture.commit("tracked submodule")
	fixture.git("checkout", "--quiet", "-b", "topic")

	clean := fixture.runConfig("--base", "main")
	requireCIPlanPaths(t, clean.ChangedFiles)

	fixture.writeFile("committed.txt", []byte("topic\n"), 0o644)
	fixture.commit("topic commit")
	fixture.writeFile("modified.txt", []byte("modified\n"), 0o644)
	if err := os.Remove(filepath.Join(fixture.root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	fixture.writeFile("staged.txt", []byte("staged\n"), 0o644)
	fixture.git("add", "staged.txt")
	fixture.writeFile("untracked.txt", []byte("untracked\n"), 0o644)
	fixture.writeFile("modules/demo/module.txt", []byte("dirty submodule\n"), 0o644)

	config := fixture.runConfig("--base", "main")
	requireCIPlanPaths(
		t,
		config.ChangedFiles,
		"committed.txt",
		"deleted.txt",
		"modified.txt",
		"modules/demo",
		"staged.txt",
		"untracked.txt",
	)
	external := t.TempDir()
	ciPlanRunGitAt(t, fixture.realGit, external, fixture.gitEnv(),
		"clone", "--quiet", "--no-hardlinks", submoduleRoot, "demo")
	if err := os.RemoveAll(filepath.Join(fixture.root, "modules")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(fixture.root, "modules")); err != nil {
		t.Fatal(err)
	}
	result := fixture.run("--base", "main")
	if result.code == 0 || !strings.Contains(result.stderr, "submodule traverses symlinked ancestry") {
		t.Fatalf("symlinked submodule ancestry was accepted: code=%d stderr=%q", result.code, result.stderr)
	}
	fixture.replaceScript("    if [[ \"${subroot}\" != \"${candidate}\" ]]; then\n", "    if false; then\n")
	if mutant := fixture.run("--base", "main"); mutant.code != 0 {
		t.Fatalf("symlink-ancestry mutant was rejected: %s", mutant.stderr)
	}
}

func TestCIPlanGitCollectorAcceptsSHA1AndSHA256Repositories(t *testing.T) {
	for format, length := range map[string]int{"sha1": 40, "sha256": 64} {
		t.Run(format, func(t *testing.T) {
			fixture := newCIPlanFixture(t, format, nil)
			head := fixture.git("rev-parse", "HEAD")
			if len(head) != length {
				t.Fatalf("%s object id length = %d, want %d", format, len(head), length)
			}
			fixture.git("checkout", "--quiet", "-b", "topic")
			fixture.writeFile("untracked.txt", []byte("untracked\n"), 0o644)
			config := fixture.runConfig("--base", "main")
			requireCIPlanPaths(t, config.ChangedFiles, "untracked.txt")
		})
	}
}

func TestCIPlanSystemBashDefaultLabelsAndExplicitPaths(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1", nil)
	fixture.git("checkout", "--quiet", "-b", "topic")
	fixture.writeFile("untracked.txt", []byte("untracked\n"), 0o644)
	script := filepath.Join(fixture.root, "scripts", "ci-plan.sh")
	result := fixture.runCommand("/bin/bash", script, "--base", "main")
	if result.code != 0 {
		t.Fatalf("system bash failed: code=%d stderr=%q", result.code, result.stderr)
	}
	var config ciPlanCapturedConfig
	if err := json.Unmarshal([]byte(result.stdout), &config); err != nil {
		t.Fatal(err)
	}
	requireCIPlanPaths(t, config.ChangedFiles, "untracked.txt")

	fixture.installGitWrapper("exit 97\n")
	result = fixture.runCommand(
		"/bin/bash", script,
		"--profile", "release-preflight",
		"--event", "workflow_dispatch",
		"--base", "release",
		"--label", "one",
		"--label", "two",
		"--no-auto-changed-files",
		"--changed-file", "docs/one.md",
		"--changed-file", "runtime/two.go",
	)
	if result.code != 0 || json.Unmarshal([]byte(result.stdout), &config) != nil {
		t.Fatalf("explicit system bash input failed: code=%d stderr=%q", result.code, result.stderr)
	}
	if config.Profile != "release-preflight" ||
		config.Event != "workflow_dispatch" ||
		config.BaseBranch != "release" ||
		fmt.Sprint(config.Labels) != "[one two]" {
		t.Fatalf("explicit planner inputs changed unexpectedly: %#v", config)
	}
	requireCIPlanPaths(t, config.ChangedFiles, "docs/one.md", "runtime/two.go")
}

func TestCIPlanGitCollectorRejectsBaseFiltersBeforeExecution(t *testing.T) {
	const rejectionAnchor = "  reject_conversion_filters || return $?\n"
	t.Run("uncommitted-attributes-stay-pinned", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1", nil)
		fixture.writeFile("tracked.txt", []byte("base\n"), 0o644)
		fixture.commit("tracked filter fixture")
		fixture.git("checkout", "--quiet", "-b", "topic")
		marker := fixture.configureFilter("hostile", "clean")
		fixture.writeFile(".gitattributes", []byte("*.txt filter=hostile\n"), 0o644)
		fixture.writeFile("tracked.txt", []byte("dirty\n"), 0o644)
		_ = fixture.runConfig("--base", "main")
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("uncommitted filter attributes executed a clean filter: %v", err)
		}
	})
	docs, docsErr := os.ReadFile(filepath.Join(repoRoot(t), "docs", "validation-scenarios.md"))
	workflowDocs, workflowDocsErr := os.ReadFile(filepath.Join(repoRoot(t), "docs", "github-workflows.md"))
	for _, testCase := range []string{"hostile/clean", "hostile/process", "unset/clean", "unset/process", "unspecified/clean", "unspecified/process"} {
		driverName, driver, _ := strings.Cut(testCase, "/")
		t.Run(testCase, func(t *testing.T) {
			fixture := newCIPlanFixture(t, "sha1", nil)
			fixture.writeFile("tracked.txt", []byte("base\n"), 0o644)
			fixture.writeFile(".gitattributes", []byte("*.txt filter="+driverName+"\n"), 0o644)
			fixture.commit("base filter fixture")
			fixture.git("checkout", "--quiet", "-b", "topic")

			marker := fixture.configureFilter(driverName, driver)
			fixture.writeFile("tracked.txt", []byte("worktree\n"), 0o644)

			result := fixture.run("--base", "main")
			if result.code == 0 || docsErr != nil || workflowDocsErr != nil || !bytes.Contains(docs, []byte("rejects symlinked submodule ancestry")) || !bytes.Contains(docs, []byte("Any clean or process filter stops planning")) || !bytes.Contains(workflowDocs, []byte("fail-closed, resident-only Git discovery")) ||
				!strings.Contains(result.stderr, "effective pinned attributes select conversion filter "+driverName+" for tracked.txt") ||
				strings.Contains(result.stderr, marker) {
				t.Fatalf("base %s/%s filter or scope docs failed: code=%d stderr=%q docs=%v/%v", driverName, driver, result.code, result.stderr, docsErr, workflowDocsErr)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("rejected %s filter ran; stat error=%v", driver, err)
			}

			fixture.replaceScript(rejectionAnchor, "")
			_ = fixture.run("--base", "main")
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("filter-rejection mutant did not execute %s marker: %v", driver, err)
			}
		})
	}
}

func TestCIPlanRejectsPopulatedSubmoduleFiltersBeforeDirtyInspection(t *testing.T) {
	const filterRejection = "    reject_conversion_filters_bound \"${subgit}\" \"${subroot}\" \"${subhead}\" || return $?\n"
	const recursivePreflight = "    preflight_populated_submodules \"${subgit}\" \"${subroot}\" \"${subhead}\" || return $?\n"
	for _, testCase := range []string{"direct/hostile/clean", "direct/hostile/process", "direct/unset/clean", "direct/unspecified/process", "nested/unset/process", "nested/unspecified/clean"} {
		depth, filterCase, _ := strings.Cut(testCase, "/")
		driverName, driver, _ := strings.Cut(filterCase, "/")
		t.Run(testCase, func(t *testing.T) {
			fixture := newCIPlanFixture(t, "sha1", nil)
			moduleRoot := filepath.Join(t.TempDir(), "module")
			fixture.initRepository(moduleRoot, "module.txt")
			source := moduleRoot
			if depth == "nested" {
				middleRoot := filepath.Join(t.TempDir(), "middle")
				fixture.initRepository(middleRoot, "middle.txt")
				ciPlanRunGitAt(t, fixture.realGit, middleRoot, fixture.gitEnv(),
					"-c", "protocol.file.allow=always", "submodule", "add", "--quiet", moduleRoot, "nested/leaf")
				ciPlanRunGitAt(t, fixture.realGit, middleRoot, fixture.gitEnv(), "commit", "--quiet", "-am", "nested leaf")
				source = middleRoot
			}
			fixture.addSubmodule(source, "modules/demo")
			fixture.commit("tracked submodule")
			if depth == "nested" {
				fixture.git("-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")
			}
			fixture.git("checkout", "--quiet", "-b", "topic")

			checkout := filepath.Join(fixture.root, "modules", "demo")
			if depth == "nested" {
				checkout = filepath.Join(checkout, "nested", "leaf")
			}
			marker := filepath.Join(t.TempDir(), "filter-ran")
			filter := filepath.Join(t.TempDir(), "filter")
			filterBody := "#!/bin/bash\n: >" + strconv.Quote(marker) + "\n"
			if driver == "clean" {
				filterBody += "exec /bin/cat\n"
			} else {
				filterBody += "exit 1\n"
			}
			fixture.writeExecutable(filter, filterBody)
			ciPlanRunGitAt(t, fixture.realGit, checkout, fixture.gitEnv(),
				"config", "filter."+driverName+"."+driver, filter)
			ciPlanRunGitAt(t, fixture.realGit, checkout, fixture.gitEnv(),
				"config", "filter."+driverName+".required", "true")
			gitDir := ciPlanRunGitAt(t, fixture.realGit, checkout, fixture.gitEnv(),
				"rev-parse", "--absolute-git-dir")
			if err := os.WriteFile(filepath.Join(gitDir, "info", "attributes"),
				[]byte("*.txt filter="+driverName+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			blob := ciPlanRunGitAt(t, fixture.realGit, checkout, fixture.gitEnv(),
				"rev-parse", "HEAD:module.txt")
			ciPlanRunGitAt(t, fixture.realGit, checkout, fixture.gitEnv(),
				"update-index", "--cacheinfo", "100644,"+blob+",module.txt")
			_ = os.Remove(marker)
			if err := os.WriteFile(filepath.Join(checkout, "module.txt"), []byte("dirty\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			result := fixture.run("--base", "main")
			if result.code == 0 || !strings.Contains(result.stderr, "select conversion filter "+driverName) {
				t.Fatalf("submodule %s/%s filter was accepted: code=%d stderr=%q",
					driverName, driver, result.code, result.stderr)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("rejected submodule %s filter ran: %v", driver, err)
			}

			if depth == "nested" {
				fixture.replaceScript(recursivePreflight, "")
				_ = fixture.run("--base", "main")
				if _, err := os.Stat(marker); err != nil {
					t.Fatalf("recursive-preflight mutant did not execute the nested filter: %v", err)
				}
				return
			}
			fixture.replaceScript(filterRejection, "")
			_ = fixture.run("--base", "main")
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("nested-filter-preflight mutant did not execute %s marker: %v", driver, err)
			}
		})
	}
}

func TestCIPlanGitCollectorBindsCanonicalWorktreeAndOverridesLocalConfig(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1", nil)
	fixture.git("checkout", "--quiet", "-b", "topic")
	fixture.writeFile("real-worktree.txt", []byte("real\n"), 0o644)

	decoy := filepath.Join(t.TempDir(), "decoy")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "decoy-only.txt"), []byte("decoy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.git("config", "core.worktree", decoy)
	fixture.git("config", "core.bare", "true")

	config := fixture.runConfig("--base", "main")
	requireCIPlanPaths(t, config.ChangedFiles, "real-worktree.txt")

	fixture.replaceScript("    --git-dir=\"${git_dir}\" \\\n", "")
	fixture.replaceScript("    --work-tree=\"${work_tree}\" \\\n", "")
	fixture.replaceScript("    -c core.bare=false \\\n", "")
	fixture.replaceScript("    -c \"core.worktree=${work_tree}\" \\\n", "")
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

func TestCIPlanBaseRefPresenceAndRetrievalFailClosed(t *testing.T) {
	t.Run("unexpected-presence-status", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1", nil)
		fixture.git("checkout", "--quiet", "-b", "topic")
		fixture.installGitWrapper(`
case " $* " in
  *" show-ref --exists refs/remotes/origin/main "*) exit 7 ;;
esac
exec "${real_git}" "$@"
`)
		result := fixture.run("--base", "main")
		if result.code != 7 ||
			!strings.Contains(result.stderr, "Unable to inspect resident base ref refs/remotes/origin/main") {
			t.Fatalf("unexpected show-ref status did not fail closed: code=%d stderr=%q", result.code, result.stderr)
		}
	})

	t.Run("retrieval-after-presence", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1", nil)
		fixture.git("update-ref", "refs/remotes/origin/main", fixture.git("rev-parse", "main"))
		fixture.git("checkout", "--quiet", "-b", "topic")
		fixture.installGitWrapper(`
case " $* " in
  *" show-ref --verify --hash refs/remotes/origin/main "*) exit 8 ;;
esac
exec "${real_git}" "$@"
`)
		result := fixture.run("--base", "main")
		if result.code != 8 ||
			!strings.Contains(result.stderr, "present base ref refs/remotes/origin/main could not be read") {
			t.Fatalf("failed ref retrieval fell back: code=%d stderr=%q", result.code, result.stderr)
		}
	})

	t.Run("malformed-object-id", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1", nil)
		fixture.git("update-ref", "refs/remotes/origin/main", fixture.git("rev-parse", "main"))
		fixture.git("checkout", "--quiet", "-b", "topic")
		fixture.installGitWrapper(`
case " $* " in
  *" show-ref --verify --hash refs/remotes/origin/main "*)
    printf 'ABCDEF0123456789ABCDEF0123456789ABCDEF01\n'
    exit 0
    ;;
esac
exec "${real_git}" "$@"
`)
		result := fixture.run("--base", "main")
		if result.code == 0 ||
			!strings.Contains(result.stderr, "resident base ref returned a malformed object ID") {
			t.Fatalf("malformed OID was accepted: code=%d stderr=%q", result.code, result.stderr)
		}
	})

	t.Run("both-refs-absent", func(t *testing.T) {
		fixture := newCIPlanFixture(t, "sha1", nil)
		fixture.git("checkout", "--quiet", "-b", "topic")
		fixture.git("branch", "-D", "main")
		result := fixture.run("--base", "main")
		if result.code == 0 ||
			!strings.Contains(result.stderr, "neither the resident origin/main ref nor local main branch exists") {
			t.Fatalf("absent base refs did not fail closed: code=%d stderr=%q", result.code, result.stderr)
		}
	})
}

func TestCIPlanBaseRefRemoteAppearanceWinsLocalFallbackRace(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1", nil)
	remoteOID := fixture.git("rev-parse", "HEAD")
	fixture.writeFile("local-main-only.txt", []byte("local main\n"), 0o644)
	fixture.commit("local main advance")
	fixture.git("checkout", "--quiet", "-b", "topic")

	counter := filepath.Join(t.TempDir(), "remote-created")
	fixture.installGitWrapper(`
case " $* " in
  *" show-ref --exists refs/remotes/origin/main "*)
    if [[ ! -e ` + strconv.Quote(counter) + ` ]]; then
      : >` + strconv.Quote(counter) + `
      "${real_git}" --git-dir=` + strconv.Quote(filepath.Join(fixture.root, ".git")) + ` \
        update-ref refs/remotes/origin/main ` + strconv.Quote(remoteOID) + `
      exit 2
    fi
    ;;
esac
exec "${real_git}" "$@"
`)
	config := fixture.runConfig("--base", "main")
	requireCIPlanPaths(t, config.ChangedFiles, "local-main-only.txt")
	if _, err := os.Stat(counter); err != nil {
		t.Fatalf("remote-ref race was not exercised: %v", err)
	}
}

func TestCIPlanRejectsIncompleteAttributeOutput(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1", nil)
	fixture.git("checkout", "--quiet", "-b", "topic")
	fixture.installGitWrapper(`
case " $* " in
  *" check-attr -z --all --stdin "*)
    printf 'tracked-without-nul'
    exit 0
    ;;
esac
exec "${real_git}" "$@"
`)
	result := fixture.run("--base", "main")
	if result.code == 0 || !strings.Contains(result.stderr, "incomplete attribute record") {
		t.Fatalf("incomplete attribute output was accepted: code=%d stderr=%q", result.code, result.stderr)
	}
}

func TestCIPlanGitDiscoveryNeverFetches(t *testing.T) {
	fixture := newCIPlanFixture(t, "sha1", nil)
	fixture.git("checkout", "--quiet", "-b", "topic")
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
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("resident planner contacted origin; stat error=%v", err)
	}

	anchor := "  base_oid=\"$(resolve_base_oid)\" || return $?\n"
	fixture.replaceScript(anchor, "  planner_git fetch origin main || return $?\n"+anchor)
	_ = fixture.run("--base", "main")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("fetch mutant did not contact origin: %v", err)
	}
}

func TestCIPlanRejectsNonBranchBaseSpellings(t *testing.T) {
	for _, base := range []string{"-main", "refs/heads/main", "main..topic", "main.lock"} {
		t.Run(strings.NewReplacer("/", "-", ".", "-").Replace(base), func(t *testing.T) {
			fixture := newCIPlanFixture(t, "sha1", nil)
			fixture.git("checkout", "--quiet", "-b", "topic")
			result := fixture.run("--base", base)
			if result.code != 2 || !strings.Contains(result.stderr, "Invalid --base branch name") {
				t.Fatalf("invalid base %q was accepted: code=%d stderr=%q", base, result.code, result.stderr)
			}
		})
	}
}

func newCIPlanHistoryFixture(t *testing.T) (*ciPlanFixture, string, string) {
	t.Helper()
	fixture := newCIPlanFixture(t, "sha1", nil)
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
		if err := os.MkdirAll(infoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		grafts := []byte(topicOID + " " + rootOID + "\n")
		if err := os.WriteFile(filepath.Join(infoDir, "grafts"), grafts, 0o600); err != nil {
			t.Fatal(err)
		}

		control := fixture.runConfig("--base", "main")
		requireCIPlanPaths(t, control.ChangedFiles, "topic-only.txt")
		fixture.replaceScript("GIT_GRAFT_FILE=/dev/null \\\n", "")
		mutant := fixture.runConfig("--base", "main")
		if !containsCIPlanPath(mutant.ChangedFiles, "local-main-only.txt") {
			t.Fatalf("graft-file mutant was not killed: %#v", mutant.ChangedFiles)
		}
	})

	t.Run("replacement-object", func(t *testing.T) {
		fixture, rootOID, topicOID := newCIPlanHistoryFixture(t)
		topicTree := fixture.git("rev-parse", topicOID+"^{tree}")
		replacementOID := fixture.git(
			"commit-tree", topicTree,
			"-p", rootOID,
			"-m", "replacement topic",
		)
		fixture.git("replace", topicOID, replacementOID)

		control := fixture.runConfig("--base", "main")
		requireCIPlanPaths(t, control.ChangedFiles, "topic-only.txt")
		fixture.replaceScript("GIT_NO_REPLACE_OBJECTS=1 \\\n", "")
		mutant := fixture.runConfig("--base", "main")
		if !containsCIPlanPath(mutant.ChangedFiles, "local-main-only.txt") {
			t.Fatalf("replacement-object mutant was not killed: %#v", mutant.ChangedFiles)
		}
	})
}

func containsCIPlanPath(paths []string, expected string) bool {
	for _, path := range paths {
		if path == expected {
			return true
		}
	}
	return false
}
