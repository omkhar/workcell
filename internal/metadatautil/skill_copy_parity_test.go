// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// skillCopyMismatches reports every file under root/.claude/skills whose bytes
// differ from, or that has no twin at, the matching path under
// root/.agents/skills.  The `.claude` tree is a verbatim copy of the `.agents`
// tree for harnesses that only read `.claude`; nothing else in the repository
// asserts the two stay identical.
func skillCopyMismatches(root string) ([]string, error) {
	claudeRoot := filepath.Join(root, ".claude", "skills")
	agentsRoot := filepath.Join(root, ".agents", "skills")

	var mismatches []string
	err := filepath.WalkDir(claudeRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(claudeRoot, path)
		if err != nil {
			return err
		}
		twin := filepath.Join(agentsRoot, relative)
		copied, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source, err := os.ReadFile(twin)
		if err != nil {
			mismatches = append(mismatches, path+" has no twin at "+twin)
			return nil //nolint:nilerr // a missing twin is a reported mismatch, not a walk failure
		}
		if !bytes.Equal(copied, source) {
			mismatches = append(mismatches, path+" differs from "+twin)
		}
		return nil
	})
	return mismatches, err
}

func TestSkillCopyParity(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	mismatches, err := skillCopyMismatches(root)
	if err != nil {
		t.Fatalf("skillCopyMismatches(%q) error = %v", root, err)
	}
	for _, mismatch := range mismatches {
		t.Errorf("skill copy drift: %s; copy the .agents file over the .claude one", mismatch)
	}

	// Negative control: the assertion must fail on drift, so perturb a copy of
	// the tree in a temporary directory rather than in the repository.
	fixture := t.TempDir()
	claudeFile := filepath.Join(fixture, ".claude", "skills", "commit", "SKILL.md")
	agentsFile := filepath.Join(fixture, ".agents", "skills", "commit", "SKILL.md")
	for file, content := range map[string]string{claudeFile: "drifted\n", agentsFile: "original\n"} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(file), err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}
	drifted, err := skillCopyMismatches(fixture)
	if err != nil {
		t.Fatalf("skillCopyMismatches(%q) error = %v", fixture, err)
	}
	if len(drifted) != 1 {
		t.Fatalf("perturbed fixture reported %d mismatches, want 1: %v", len(drifted), drifted)
	}

	if err := os.Remove(agentsFile); err != nil {
		t.Fatalf("remove %s: %v", agentsFile, err)
	}
	missing, err := skillCopyMismatches(fixture)
	if err != nil {
		t.Fatalf("skillCopyMismatches(%q) error = %v", fixture, err)
	}
	if len(missing) != 1 {
		t.Fatalf("fixture without a twin reported %d mismatches, want 1: %v", len(missing), missing)
	}
}
