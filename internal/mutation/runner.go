// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package mutation

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type mutationCase struct {
	relativePath string
	original     string
	replacement  string
	label        string
	command      commandSpec
}

var goHelperMutations = []mutationCase{
	{
		relativePath: "internal/injection/render_injection_bundle.go",
		original:     `if targetIsReserved(candidate) {`,
		replacement:  `if false && targetIsReserved(candidate) {`,
		label:        "reserved target protection",
		command: commandSpec{
			Path: "go",
			Args: []string{"test", "./internal/injection"},
		},
	},
	{
		relativePath: "internal/injection/render_injection_bundle.go",
		original: strings.Join([]string{
			`if err := validateAllowedKeys(credentials, mapKeysSet([]string{`,
			`		"codex_auth",`,
			`		"claude_auth",`,
			`		"claude_api_key",`,
			`		"claude_mcp",`,
		}, "\n"),
		replacement: strings.Join([]string{
			`if err := validateAllowedKeys(credentials, mapKeysSet([]string{`,
			`		"codex_auth",`,
			`		"claude_auth",`,
			`		"claude_api_key",`,
			`		// "claude_mcp",`,
		}, "\n"),
		label: "claude mcp credential support",
		command: commandSpec{
			Path: "go",
			Args: []string{"test", "./internal/injection"},
		},
	},
	{
		relativePath: "internal/injection/render_injection_bundle.go",
		original:     `if info.Mode().Perm()&0o077 != 0 {`,
		replacement:  `if false && info.Mode().Perm()&0o077 != 0 {`,
		label:        "secret permission hygiene",
		command: commandSpec{
			Path: "go",
			Args: []string{"test", "./internal/injection"},
		},
	},
	{
		relativePath: "internal/transcript/transcript.go",
		original:     `if !isTerminal(stdin) || !isTerminal(stdout) {`,
		replacement:  `if false && (!isTerminal(stdin) || !isTerminal(stdout)) {`,
		label:        "interactive terminal requirement",
		command: commandSpec{
			Path: "go",
			Args: []string{"test", "./internal/transcript"},
		},
	},
	{
		relativePath: "internal/injection/extract_direct_mounts.go",
		original:     `delete(entry, "source")`,
		replacement:  `// delete(entry, "source")`,
		label:        "manifest source stripping",
		command: commandSpec{
			Path: "go",
			Args: []string{"test", "./internal/injection"},
		},
	},
	{
		relativePath: "internal/injection/render_injection_bundle.go",
		original:     `"forwardagent":        {},`,
		replacement:  `// "forwardagent":        {},`,
		label:        "forwardagent ssh directive blocking",
		command: commandSpec{
			Path: "go",
			Args: []string{"test", "./internal/injection"},
		},
	},
	{
		relativePath: "internal/injection/render_injection_bundle.go",
		original:     `"sendenv":             {},`,
		replacement:  `// "sendenv":             {},`,
		label:        "sendenv ssh directive blocking",
		command: commandSpec{
			Path: "go",
			Args: []string{"test", "./internal/injection"},
		},
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
}

func Run(repoRoot string) error {
	if err := runGoHelperMutations(repoRoot); err != nil {
		return err
	}
	if err := runRustGuardMutations(repoRoot); err != nil {
		return err
	}
	return nil
}

func runGoHelperMutations(repoRoot string) error {
	failures := make([]string, 0)
	for _, tc := range goHelperMutations {
		tempRoot, err := os.MkdirTemp("", "workcell-go-mutation.")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tempRoot)

		for _, relativePath := range []string{
			"cmd",
			"internal",
			"go.mod",
		} {
			if err := copyIntoTempRoot(repoRoot, tempRoot, relativePath); err != nil {
				return err
			}
		}
		if err := applyMutation(filepath.Join(tempRoot, tc.relativePath), tc.original, tc.replacement); err != nil {
			return err
		}
		exitCode, err := runCommand(tempRoot, tc.command)
		if err != nil {
			return err
		}
		if exitCode == 0 {
			failures = append(failures, tc.label)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("Go helper mutation coverage did not catch: %s", strings.Join(failures, ", "))
	}
	return nil
}

func runRustGuardMutations(repoRoot string) error {
	failures := make([]string, 0)
	for _, tc := range rustMutations {
		tempRoot, err := os.MkdirTemp("", "workcell-rust-mutation.")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tempRoot)

		if err := copyTree(
			filepath.Join(repoRoot, "runtime", "container", "rust"),
			filepath.Join(tempRoot, "runtime", "container", "rust"),
		); err != nil {
			return err
		}
		if err := applyMutation(filepath.Join(tempRoot, tc.relativePath), tc.original, tc.replacement); err != nil {
			return err
		}
		exitCode, err := runCommand(filepath.Join(tempRoot, "runtime", "container", "rust"), commandSpec{
			Path: "cargo",
			Args: []string{"test", "--locked", "--offline"},
		})
		if err != nil {
			return err
		}
		if exitCode == 0 {
			failures = append(failures, tc.label)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("Rust mutation coverage did not catch: %s", strings.Join(failures, ", "))
	}
	return nil
}

type commandSpec struct {
	Path string
	Args []string
	Env  map[string]string
}

func runCommand(cwd string, spec commandSpec) (int, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
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
	err := cmd.Run()
	if err == nil {
		return 0, nil
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
