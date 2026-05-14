// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package publishpr

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
)

// BashContext carries the scripts/workcell publish_pr_main globals
// (ROOT_DIR, WORKSPACE, REAL_HOME, TRUSTED_HOST_PATH, HOST_GIT_BIN,
// HOST_GH_BIN). The bash shim that replaces publish_pr_main forwards
// these as --bash-* flags because `go_hostutil` runs the Go binary
// under `env -i` and would otherwise lose them.
type BashContext struct {
	RootDir          string
	WorkspaceRoot    string
	RealHome         string
	TrustedHostPath  string
	HostGitBin       string
	HostGhBin        string
	WorkcellSelfPath string
}

// trustedHostToolPrefixes mirrors the 14-entry allowlist embedded in
// scripts/workcell is_trusted_host_tool_path(). Any change here must
// stay in lockstep with that bash table.
var trustedHostToolPrefixes = []string{
	"/usr/bin",
	"/bin",
	"/usr/sbin",
	"/sbin",
	"/usr/local/bin",
	"/usr/local/Cellar",
	"/usr/local/aws-cli",
	"/usr/local/Caskroom/google-cloud-sdk",
	"/usr/local/google-cloud-sdk",
	"/usr/local/share/google-cloud-sdk",
	"/usr/local/sessionmanagerplugin/bin",
	"/opt/homebrew/bin",
	"/opt/homebrew/Cellar",
	"/opt/homebrew/Caskroom/google-cloud-sdk",
	"/opt/homebrew/share/google-cloud-sdk",
	"/Applications/Docker.app/Contents/Resources/bin",
}

// IsTrustedHostToolPath mirrors scripts/workcell is_trusted_host_tool_path:
// the candidate must be absolute, must not live under RootDir or
// WorkspaceRoot, and must sit under one of the trusted prefixes.
func IsTrustedHostToolPath(candidate string, ctx *BashContext) bool {
	if candidate == "" || !strings.HasPrefix(candidate, "/") {
		return false
	}
	if ctx != nil {
		if ctx.RootDir != "" && hasDirPrefix(candidate, ctx.RootDir) {
			return false
		}
		if ctx.WorkspaceRoot != "" && hasDirPrefix(candidate, ctx.WorkspaceRoot) {
			return false
		}
	}
	for _, prefix := range trustedHostToolPrefixes {
		if candidate == prefix || strings.HasPrefix(candidate, prefix+"/") {
			return true
		}
	}
	return false
}

func hasDirPrefix(candidate, prefix string) bool {
	return candidate == prefix || strings.HasPrefix(candidate, prefix+"/")
}

// CanonicalizeHostToolPath resolves symlinks the way scripts/workcell
// canonicalize_host_tool_path did (via `realpath`); when the path is
// missing it falls back to filepath.Clean so the trust-prefix check
// runs against a normalized form.
func CanonicalizeHostToolPath(candidate string) string {
	if candidate == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return filepath.Clean(candidate)
	}
	return resolved
}

// ResolveHostTool mirrors scripts/workcell resolve_host_tool: walk the
// explicit candidate list, fall back to a PATH lookup, and only accept
// a tool whose raw and canonical forms both pass IsTrustedHostToolPath.
// required=false matches resolve_host_tool_optional (returns "" without
// erroring when nothing is acceptable).
func ResolveHostTool(ctx *BashContext, name string, required bool, candidates []string) (string, error) {
	for _, candidate := range candidates {
		if resolved := acceptCandidate(ctx, candidate); resolved != "" {
			return resolved, nil
		}
	}
	if pathHit := lookPathIn(ctx.TrustedHostPath, name); pathHit != "" {
		if resolved := acceptCandidate(ctx, pathHit); resolved != "" {
			return resolved, nil
		}
	}
	if required {
		return "", &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("Missing trusted host tool: %s", name)}
	}
	return "", nil
}

func acceptCandidate(ctx *BashContext, candidate string) string {
	if candidate == "" || !isExecutable(candidate) {
		return ""
	}
	canonical := CanonicalizeHostToolPath(candidate)
	if !IsTrustedHostToolPath(candidate, ctx) || !IsTrustedHostToolPath(canonical, ctx) {
		return ""
	}
	return canonical
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// lookPathIn replicates `type -P name` evaluated under the trusted
// PATH only (scripts/workcell exports PATH=TRUSTED_HOST_PATH at script
// entry, then re-applies it inside env -i for every child command).
func lookPathIn(trustedPath, name string) string {
	if name == "" {
		return ""
	}
	for _, dir := range filepath.SplitList(trustedPath) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		if isExecutable(candidate) {
			return candidate
		}
	}
	return ""
}

// ResolveExistingExecutableOrDie mirrors resolve_existing_executable_or_die:
// the path must exist, be a regular executable file, and both raw and
// canonical forms must pass IsTrustedHostToolPath. label appears in the
// stderr message so HOST_GIT_BIN / HOST_GH_BIN / gh-bin failures stay
// byte-identical to the legacy bash.
func ResolveExistingExecutableOrDie(ctx *BashContext, rawPath, label string) (string, error) {
	info, err := os.Stat(rawPath)
	if err != nil || info.IsDir() {
		return "", &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("%s file does not exist: %s", label, rawPath)}
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("%s file is not executable: %s", label, rawPath)}
	}
	canonical := CanonicalizeHostToolPath(rawPath)
	if canonical == "" || !IsTrustedHostToolPath(rawPath, ctx) || !IsTrustedHostToolPath(canonical, ctx) {
		return "", &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("%s must point to a trusted host executable path: %s", label, rawPath)}
	}
	return canonical, nil
}

// EmitCommand mirrors scripts/workcell emit_command: two-space indent,
// `printf %q` per token, trailing space + newline.
func EmitCommand(w io.Writer, args []string) {
	fmt.Fprint(w, "  ")
	for _, arg := range args {
		fmt.Fprint(w, bashQuote(arg))
		fmt.Fprint(w, " ")
	}
	fmt.Fprint(w, "\n")
}

// bashQuote replicates `printf %q` as bash 3.2 (the macOS system bash
// scripts/workcell runs under) emits it: always backslash-escape unsafe
// characters; never wrap the whole token in single quotes when only
// printable characters are present; use ANSI-C `$'...'` only for non-
// printable bytes. The empty string becomes `”`. The dry-run scenario
// in tests/scenarios/shared/test-publish-pr-dry-run.sh greps for the
// exact backslash form, so any divergence surfaces there.
func bashQuote(s string) string {
	if s == "" {
		return "''"
	}
	if hasUnprintable(s) {
		var b strings.Builder
		b.WriteString("$'")
		for _, r := range s {
			switch r {
			case '\n':
				b.WriteString(`\n`)
			case '\t':
				b.WriteString(`\t`)
			case '\r':
				b.WriteString(`\r`)
			case '\\':
				b.WriteString(`\\`)
			case '\'':
				b.WriteString(`\'`)
			default:
				if r < 0x20 || r == 0x7f {
					fmt.Fprintf(&b, `\%03o`, r)
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteString("'")
		return b.String()
	}
	if isShellSafe(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isShellSafeRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('\\')
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isShellSafe(s string) bool {
	for _, r := range s {
		if !isShellSafeRune(r) {
			return false
		}
	}
	return true
}

func isShellSafeRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	switch r {
	case '%', '+', ',', '-', '.', '/', ':', '=', '@', '_':
		return true
	}
	return false
}

func hasUnprintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// PublishEnv captures the env -i sandbox baseline (PATH/HOME) that the
// bash run_publish_host_command_in_dir sets up before exec.
type PublishEnv struct {
	Path string
	Home string
}

// RunPublishHostCommandInDir mirrors run_publish_host_command_in_dir:
// the child runs under env -i with PATH/HOME/LC_ALL/LANG plus the
// well-known optional passthroughs (TERM, GPG_TTY, GNUPGHOME, SSH_*,
// GIT_ASKPASS, XDG_*, GH_*) when the parent process has them set.
func RunPublishHostCommandInDir(dir string, env *PublishEnv, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return nil
	}
	if env == nil {
		env = &PublishEnv{}
	}
	if env.Home == "" || !isDir(env.Home) {
		env.Home = "/"
	}
	if !isDir(dir) {
		return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("Missing host working directory: %s", dir)}
	}
	envv := []string{
		"PATH=" + env.Path,
		"HOME=" + env.Home,
		"LC_ALL=C",
		"LANG=C",
	}
	for _, key := range publishPassThroughEnvKeys {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			envv = append(envv, key+"="+value)
		}
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = envv
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &cliexit.ExitCodeError{Code: exitErr.ExitCode()}
		}
		return err
	}
	return nil
}

// publishPassThroughEnvKeys mirrors the conditional `env_args+=...`
// entries in run_publish_host_command_in_dir. Order matches the bash so
// reviewers can diff the two sides without re-sorting.
var publishPassThroughEnvKeys = []string{
	"TERM",
	"GPG_TTY",
	"GNUPGHOME",
	"SSH_AUTH_SOCK",
	"SSH_AGENT_PID",
	"SSH_ASKPASS",
	"GIT_ASKPASS",
	"XDG_CONFIG_HOME",
	"XDG_STATE_HOME",
	"XDG_CACHE_HOME",
	"XDG_DATA_HOME",
	"XDG_RUNTIME_DIR",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GH_HOST",
	"GH_CONFIG_DIR",
}

func isDir(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// resolveExistingDirectoryOrDie mirrors resolve_existing_directory_or_die:
// canonicalize against WorkspaceRoot, require an existing directory.
func resolveExistingDirectoryOrDie(ctx *BashContext, rawPath string) (string, error) {
	resolved := rawPath
	if !filepath.IsAbs(resolved) && ctx != nil && ctx.WorkspaceRoot != "" {
		resolved = filepath.Join(ctx.WorkspaceRoot, resolved)
	}
	resolved = filepath.Clean(resolved)
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = eval
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("Workspace path does not exist: %s\nResolve it to an existing directory, then rerun with --workspace %s", rawPath, resolved)}
	}
	if !info.IsDir() {
		return "", &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("Workspace path is not a directory: %s", resolved)}
	}
	return resolved, nil
}

// resolveExistingFileOrDie mirrors resolve_existing_file_or_die.
func resolveExistingFileOrDie(ctx *BashContext, rawPath, label string) (string, error) {
	resolved := rawPath
	if !filepath.IsAbs(resolved) && ctx != nil && ctx.WorkspaceRoot != "" {
		resolved = filepath.Join(ctx.WorkspaceRoot, resolved)
	}
	resolved = filepath.Clean(resolved)
	if eval, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = eval
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		return "", &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("%s file does not exist: %s", label, rawPath)}
	}
	return resolved, nil
}

// runCleanGit invokes the workspace-safe git form
// (run_workspace_safe_git_command_in_dir) and returns the captured
// stdout with trailing newlines trimmed the way `$(...)` does in bash.
func runCleanGit(ctx *BashContext, dir string, args []string) (string, error) {
	full := append([]string{
		"env",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		ctx.HostGitBin,
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
		"-c", "color.ui=false",
	}, args...)
	var stdout, stderr bytes.Buffer
	err := RunPublishHostCommandInDir(dir, &PublishEnv{Path: ctx.TrustedHostPath, Home: ctx.RealHome}, full, nil, &stdout, &stderr)
	return strings.TrimRight(stdout.String(), "\n"), err
}

func branchExists(ctx *BashContext, workspace, branch string) bool {
	_, err := runCleanGit(ctx, workspace, []string{"show-ref", "--verify", "--quiet", "refs/heads/" + branch})
	return err == nil
}

func currentBranch(ctx *BashContext, workspace string) string {
	out, _ := runCleanGit(ctx, workspace, []string{"branch", "--show-current"})
	return out
}

func hasRemoteOrigin(ctx *BashContext, workspace string) bool {
	_, err := runCleanGit(ctx, workspace, []string{"remote", "get-url", "origin"})
	return err == nil
}

func hasWorktreeChanges(ctx *BashContext, workspace string) bool {
	out, _ := runCleanGit(ctx, workspace, []string{"status", "--short", "--untracked-files=all"})
	return strings.TrimSpace(out) != ""
}

func hasStagedChanges(ctx *BashContext, workspace string) bool {
	_, err := runCleanGit(ctx, workspace, []string{"diff", "--cached", "--quiet", "--exit-code"})
	return err != nil
}

func workspaceIsGitWorkTree(ctx *BashContext, workspace string) bool {
	_, err := runCleanGit(ctx, workspace, []string{"rev-parse", "--is-inside-work-tree"})
	return err == nil
}

// checkRefFormatHook returns a CheckRefFormatFunc backed by the trusted
// HOST_GIT_BIN, matching the bash validators that shelled out to
// `${HOST_GIT_BIN} check-ref-format --branch`.
func checkRefFormatHook(ctx *BashContext) CheckRefFormatFunc {
	return func(branch string) bool {
		_, err := runCleanGit(ctx, ctx.WorkspaceRoot, []string{"check-ref-format", "--branch", branch})
		return err == nil
	}
}
