// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package mutation

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const defaultMutantTimeout = 10 * time.Minute

// mutantTimeout bounds a single mutant's scoped test run so one hung mutant
// cannot consume the whole (CI-gated) mutation lane. Overridable via
// WORKCELL_MUTANT_TIMEOUT (a Go duration such as "15m") for slow/loaded runners.
func mutantTimeout() time.Duration {
	if raw := os.Getenv("WORKCELL_MUTANT_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultMutantTimeout
}

type mutationCase struct {
	relativePath string
	original     string
	replacement  string
	label        string
	command      commandSpec
}

// goCmd builds a commandSpec that invokes the host `go` binary with the
// given argv tail. The mutation table previously inlined a four-line
// commandSpec literal at each site; this helper compresses each to a
// single line.
func goCmd(args ...string) commandSpec {
	return commandSpec{
		Path: "go",
		Args: args,
	}
}

var goHelperMutations = []mutationCase{
	{
		relativePath: "internal/injection/render_documents_copies.go",
		original:     `if targetIsReserved(candidate) {`,
		replacement:  `if false && targetIsReserved(candidate) {`,
		label:        "reserved target protection",
		command:      goCmd("test", "./internal/injection"),
	},
	{
		relativePath: "internal/adapters/data.go",
		original:     `				"claude_mcp",`,
		replacement:  `				// "claude_mcp",`,
		label:        "claude mcp credential support",
		command:      goCmd("test", "./internal/injection"),
	},
	{
		relativePath: "internal/injection/render_validation.go",
		original:     `if info.Mode().Perm()&0o077 != 0 {`,
		replacement:  `if false && info.Mode().Perm()&0o077 != 0 {`,
		label:        "secret permission hygiene",
		command:      goCmd("test", "./internal/injection"),
	},
	{
		relativePath: "internal/transcript/transcript.go",
		original:     `if !isTerminal(stdin) || !isTerminal(stdout) {`,
		replacement:  `if false && (!isTerminal(stdin) || !isTerminal(stdout)) {`,
		label:        "interactive terminal requirement",
		command:      goCmd("test", "./internal/transcript"),
	},
	{
		relativePath: "internal/injection/extract_direct_mounts.go",
		original:     `delete(entry, "source")`,
		replacement:  `// delete(entry, "source")`,
		label:        "manifest source stripping",
		command:      goCmd("test", "./internal/injection"),
	},
	{
		relativePath: "internal/injection/render_injection_bundle.go",
		original:     `"forwardagent":        {},`,
		replacement:  `// "forwardagent":        {},`,
		label:        "forwardagent ssh directive blocking",
		command:      goCmd("test", "./internal/injection"),
	},
	{
		relativePath: "internal/injection/render_injection_bundle.go",
		original:     `"sendenv":             {},`,
		replacement:  `// "sendenv":             {},`,
		label:        "sendenv ssh directive blocking",
		command:      goCmd("test", "./internal/injection"),
	},
	{
		relativePath: "internal/metadatautil/operator_contract.go",
		original:     `if len(workflow.Evidence) == 0 {`,
		replacement:  `if false && len(workflow.Evidence) == 0 {`,
		label:        "workflow evidence requirement",
		command:      goCmd("test", "./internal/metadatautil", "-run", "Test(ValidateOperatorContract|LoadOperatorContract|StripManpageFormatting)", "-count=1"),
	},
	{
		relativePath: "internal/metadatautil/operator_contract.go",
		original:     `if _, ok := requirementPaths[canonicalPath]; !ok {`,
		replacement:  `if _, ok := requirementPaths[canonicalPath]; false && !ok {`,
		label:        "workflow requirement path parity",
		command:      goCmd("test", "./internal/metadatautil", "-run", "Test(ValidateOperatorContract|LoadOperatorContract|StripManpageFormatting)", "-count=1"),
	},
	{
		relativePath: "internal/metadatautil/operator_contract.go",
		original:     `if len(aliasProbes) == 0 {`,
		replacement:  `if false && len(aliasProbes) == 0 {`,
		label:        "alias probe requirement",
		command:      goCmd("test", "./internal/metadatautil", "-run", "Test(ValidateOperatorContract|LoadOperatorContract|StripManpageFormatting)", "-count=1"),
	},
	{
		relativePath: "internal/metadatautil/operator_contract.go",
		original:     `if !strings.Contains(output, workflow.Canonical) {`,
		replacement:  `if false && !strings.Contains(output, workflow.Canonical) {`,
		label:        "alias probe canonical parity",
		command:      goCmd("test", "./internal/metadatautil", "-run", "Test(ValidateOperatorContract|LoadOperatorContract|StripManpageFormatting)", "-count=1"),
	},
}

var rustMutations = []mutationCase{
	{
		relativePath: "runtime/container/rust/src/lib.rs",
		original:     `matches!(value, Some(candidate) if !candidate.is_empty() && !candidate.eq_ignore_ascii_case("strict"))`,
		replacement:  `matches!(value, Some(candidate) if !candidate.is_empty())`,
		label:        "strict-mode matcher",
	},
	{
		relativePath: "runtime/container/rust/src/lib.rs",
		original:     `path == root`,
		replacement:  `false`,
		label:        "root-prefix matcher",
	},
	{
		relativePath: "runtime/container/rust/src/gitpolicy.rs",
		original:     `            | "core.fsmonitor"` + "\n",
		replacement:  ``,
		label:        "core.fsmonitor git-config blocking",
	},
}

// Result summarizes a mutation run: how many mutants were killed (the scoped
// test suite failed, as it should) out of the total attempted, plus the labels
// of any survivors (mutants the tests failed to catch).
type Result struct {
	Killed    int
	Total     int
	Survivors []string
}

// Score returns the percentage of mutants killed, or 0 when no mutants ran.
func (r Result) Score() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Killed) / float64(r.Total) * 100
}

// Run executes every mutation case and returns an error if any mutant survived
// or the harness failed. It is the strict all-or-nothing variant (any survivor
// fails), intentionally independent of the reviewed baseline policy that
// CheckScore applies; callers wanting the baseline gate use RunScored +
// CheckScore instead.
func Run(repoRoot string) error {
	result, err := RunScored(repoRoot)
	if len(result.Survivors) > 0 {
		err = errors.Join(err, fmt.Errorf("mutation coverage did not catch: %s", strings.Join(result.Survivors, ", ")))
	}
	return err
}

// RunScored executes every mutation case and reports the kill counts. A non-nil
// error indicates a harness failure (a mutant that could not be evaluated), not
// a surviving mutant; survivors are reported in Result.Survivors.
func RunScored(repoRoot string) (Result, error) {
	goKilled, goTotal, goSurvivors, goErr := runGoHelperMutations(repoRoot)
	rustKilled, rustTotal, rustSurvivors, rustErr := runRustGuardMutations(repoRoot)
	survivors := make([]string, 0, len(goSurvivors)+len(rustSurvivors))
	survivors = append(survivors, goSurvivors...)
	survivors = append(survivors, rustSurvivors...)
	result := Result{
		Killed:    goKilled + rustKilled,
		Total:     goTotal + rustTotal,
		Survivors: survivors,
	}
	return result, errors.Join(goErr, rustErr)
}

// runGoHelperMutations runs all Go mutation cases in parallel. Each case
// operates in its own isolated temp directory so there is no shared state.
func runGoHelperMutations(repoRoot string) (killed, total int, survivors []string, err error) {
	type result struct {
		label string
		err   error
		pass  bool // true means mutation was NOT caught (test passed when it should fail)
	}

	total = len(goHelperMutations)
	results := make(chan result, len(goHelperMutations))
	var wg sync.WaitGroup

	for _, tc := range goHelperMutations {
		tc := tc
		wg.Add(1)
		go func() {
			defer wg.Done()

			tempRoot, err := os.MkdirTemp("", "workcell-go-mutation.")
			if err != nil {
				results <- result{label: tc.label, err: err}
				return
			}
			defer os.RemoveAll(tempRoot)

			for _, relativePath := range []string{"cmd", "internal", "go.mod", "go.sum"} {
				if err := copyIntoTempRoot(repoRoot, tempRoot, relativePath); err != nil {
					results <- result{label: tc.label, err: err}
					return
				}
			}
			if err := applyMutation(filepath.Join(tempRoot, tc.relativePath), tc.original, tc.replacement); err != nil {
				results <- result{label: tc.label, err: err}
				return
			}
			exitCode, err := runCommand(tempRoot, tc.command)
			if err != nil {
				results <- result{label: tc.label, err: err}
				return
			}
			results <- result{label: tc.label, pass: exitCode == 0}
		}()
	}

	wg.Wait()
	close(results)

	var harnessErrors []error
	for r := range results {
		if r.err != nil {
			harnessErrors = append(harnessErrors, fmt.Errorf("%s: %w", r.label, r.err))
			continue
		}
		if r.pass {
			survivors = append(survivors, r.label)
		} else {
			killed++
		}
	}
	return killed, total, survivors, errors.Join(harnessErrors...)
}

// runRustGuardMutations runs all Rust mutation cases in parallel. Each case
// operates in its own isolated temp directory so there is no shared state.
func runRustGuardMutations(repoRoot string) (killed, total int, survivors []string, err error) {
	type result struct {
		label string
		err   error
		pass  bool
	}

	total = len(rustMutations)
	results := make(chan result, len(rustMutations))
	var wg sync.WaitGroup

	for _, tc := range rustMutations {
		tc := tc
		wg.Add(1)
		go func() {
			defer wg.Done()

			tempRoot, err := os.MkdirTemp("", "workcell-rust-mutation.")
			if err != nil {
				results <- result{label: tc.label, err: err}
				return
			}
			defer os.RemoveAll(tempRoot)

			if err := copyTree(
				filepath.Join(repoRoot, "runtime", "container", "rust"),
				filepath.Join(tempRoot, "runtime", "container", "rust"),
			); err != nil {
				results <- result{label: tc.label, err: err}
				return
			}
			if err := applyMutation(filepath.Join(tempRoot, tc.relativePath), tc.original, tc.replacement); err != nil {
				results <- result{label: tc.label, err: err}
				return
			}
			exitCode, err := runCommand(filepath.Join(tempRoot, "runtime", "container", "rust"), commandSpec{
				Path: "cargo",
				Args: []string{"test", "--locked", "--offline"},
			})
			if err != nil {
				results <- result{label: tc.label, err: err}
				return
			}
			results <- result{label: tc.label, pass: exitCode == 0}
		}()
	}

	wg.Wait()
	close(results)

	var harnessErrors []error
	for r := range results {
		if r.err != nil {
			harnessErrors = append(harnessErrors, fmt.Errorf("%s: %w", r.label, r.err))
			continue
		}
		if r.pass {
			survivors = append(survivors, r.label)
		} else {
			killed++
		}
	}
	return killed, total, survivors, errors.Join(harnessErrors...)
}

type commandSpec struct {
	Path string
	Args []string
	Env  map[string]string
}

func runCommand(cwd string, spec commandSpec) (int, error) {
	timeout := mutantTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = cwd
	if spec.Env != nil {
		env := os.Environ()
		for key, value := range spec.Env {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Put the child in its own process group and, on timeout, kill the whole
	// group so a hung `go test`/`cargo test` cannot orphan its compiled test
	// binary (a grandchild) that would keep burning CI resources.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return 0, fmt.Errorf("mutation command timed out after %s: %s %s", timeout, spec.Path, strings.Join(spec.Args, " "))
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func applyMutation(path string, original string, replacement string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(content)
	if !strings.Contains(text, original) {
		return fmt.Errorf("mutation anchor not found in %s: %s", path, original)
	}
	updated := strings.Replace(text, original, replacement, 1)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(updated), info.Mode().Perm())
}

func copyTree(sourceRoot string, destinationRoot string) error {
	return filepath.WalkDir(sourceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(destinationRoot, relative)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, targetPath)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(targetPath, content, info.Mode().Perm())
	})
}

func copyIntoTempRoot(repoRoot string, tempRoot string, relativePath string) error {
	sourcePath := filepath.Join(repoRoot, relativePath)
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	targetPath := filepath.Join(tempRoot, relativePath)
	if info.IsDir() {
		return copyTree(sourcePath, targetPath)
	}
	return copyFile(sourcePath, targetPath, info.Mode())
}

func copyFile(sourcePath string, targetPath string, mode fs.FileMode) error {
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(targetPath, content, mode.Perm())
}
