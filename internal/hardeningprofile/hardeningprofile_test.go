// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hardeningprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRepo materializes a fake repo under a temp dir: policy holds the
// policy/hardening-profile.toml body (empty means "do not create it"), and
// files maps repo-relative target paths to their contents.
func writeRepo(t *testing.T, policy string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if policy != "" {
		writeFile(t, root, profileRelPath, policy)
	}
	for rel, body := range files {
		writeFile(t, root, rel, body)
	}
	return root
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// happyPolicy is a minimal but structurally faithful hardening profile: one
// required literal and one forbidden literal over a single target file.
const happyPolicy = `version = 1

[capabilities]
target = "scripts/workcell"
required = ["--cap-drop ALL"]
forbidden = ["--privileged"]
`

const happyLauncher = `#!/bin/bash
docker run --cap-drop ALL --security-opt no-new-privileges:true "$@"
`

func TestCheckHappy(t *testing.T) {
	root := writeRepo(t, happyPolicy, map[string]string{"scripts/workcell": happyLauncher})
	if err := Check(root); err != nil {
		t.Fatalf("Check(happy) = %v, want nil", err)
	}
}

func TestCheckMissingRequired(t *testing.T) {
	// Launcher drops the required --cap-drop ALL: a weakening drift.
	launcher := "#!/bin/bash\ndocker run --security-opt no-new-privileges:true \"$@\"\n"
	root := writeRepo(t, happyPolicy, map[string]string{"scripts/workcell": launcher})
	err := Check(root)
	if err == nil {
		t.Fatal("Check(missing required) = nil, want violation")
	}
	if !strings.Contains(err.Error(), "missing required posture literal \"--cap-drop ALL\"") {
		t.Fatalf("unexpected message: %v", err)
	}
	if !strings.Contains(err.Error(), "[capabilities]") {
		t.Fatalf("message must name the section: %v", err)
	}
}

func TestCheckForbiddenPresent(t *testing.T) {
	// Launcher gains the forbidden --privileged: a weakening drift.
	launcher := "#!/bin/bash\ndocker run --cap-drop ALL --privileged \"$@\"\n"
	root := writeRepo(t, happyPolicy, map[string]string{"scripts/workcell": launcher})
	err := Check(root)
	if err == nil {
		t.Fatal("Check(forbidden present) = nil, want violation")
	}
	if !strings.Contains(err.Error(), "contains forbidden posture literal \"--privileged\"") {
		t.Fatalf("unexpected message: %v", err)
	}
}

func TestCheckMissingTargetFile(t *testing.T) {
	// A missing target file is treated as empty content, so the required
	// literal fails.
	root := writeRepo(t, happyPolicy, nil)
	if err := Check(root); err == nil {
		t.Fatal("Check(missing target) = nil, want violation")
	}
}

func TestCheckMissingPolicy(t *testing.T) {
	root := writeRepo(t, "", map[string]string{"scripts/workcell": happyLauncher})
	err := Check(root)
	if err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("Check(missing policy) = %v, want read error", err)
	}
}

func TestCheckBadVersion(t *testing.T) {
	policy := "version = 2\n\n[capabilities]\ntarget = \"scripts/workcell\"\nrequired = [\"--cap-drop ALL\"]\n"
	root := writeRepo(t, policy, map[string]string{"scripts/workcell": happyLauncher})
	err := Check(root)
	if err == nil || !strings.Contains(err.Error(), "version = 1") {
		t.Fatalf("Check(bad version) = %v, want version error", err)
	}
}

func TestCheckEmptySection(t *testing.T) {
	policy := "version = 1\n\n[capabilities]\ntarget = \"scripts/workcell\"\n"
	root := writeRepo(t, policy, map[string]string{"scripts/workcell": happyLauncher})
	err := Check(root)
	if err == nil || !strings.Contains(err.Error(), "neither required nor forbidden") {
		t.Fatalf("Check(empty section) = %v, want empty-section error", err)
	}
}

func TestCheckMalformedPolicy(t *testing.T) {
	root := writeRepo(t, "this is not = = toml [[[", map[string]string{"scripts/workcell": happyLauncher})
	if err := Check(root); err == nil {
		t.Fatal("Check(malformed policy) = nil, want parse error")
	}
}

// TestCheckRealRepo asserts the shipped policy/hardening-profile.toml conforms
// to the real scripts/workcell and egress-endpoints.sh — the load-bearing A6
// gate. It is skipped when run outside the repo tree.
func TestCheckRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, profileRelPath)); err != nil {
		t.Skipf("real %s not found: %v", profileRelPath, err)
	}
	if err := Check(repoRoot); err != nil {
		t.Fatalf("Check(real repo) = %v, want nil", err)
	}
}
