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

// skillCopyMismatches reports every file under root/.claude/skills that is not
// a regular file, that has no regular-file twin at the matching path under
// root/.agents/skills, or whose bytes differ from that twin.  The `.claude`
// tree is a verbatim copy of the `.agents` tree for harnesses that only read
// `.claude`; nothing else in the repository asserts the two stay identical.
//
// Both sides must be regular files.  A symlink compares byte-equal to its own
// target, so following one would let a link stand in for a committed copy and
// satisfy the comparison without two copies existing, and a link can resolve
// to content outside the repository.  filepath.WalkDir does not follow
// symlinks, so entry.Type() reports the link itself; the twin needs an
// explicit os.Lstat for the same reason.
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
		if !entry.Type().IsRegular() {
			mismatches = append(mismatches, path+" is not a regular file ("+entry.Type().String()+")")
			return nil
		}
		twinInfo, err := os.Lstat(twin)
		if err != nil {
			mismatches = append(mismatches, path+" has no twin at "+twin)
			return nil //nolint:nilerr // a missing twin is a reported mismatch, not a walk failure
		}
		if !twinInfo.Mode().IsRegular() {
			mismatches = append(mismatches, twin+" is not a regular file ("+twinInfo.Mode().Type().String()+")")
			return nil
		}
		copied, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source, err := os.ReadFile(twin)
		if err != nil {
			return err
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

	// A symlink is byte-equal to whatever it points at, so it must be rejected
	// on its own rather than compared: it is not the second copy the invariant
	// requires, and it can point outside the repository.
	if err := os.WriteFile(agentsFile, []byte("drifted\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", agentsFile, err)
	}
	if err := os.Remove(claudeFile); err != nil {
		t.Fatalf("remove %s: %v", claudeFile, err)
	}
	if err := os.Symlink(agentsFile, claudeFile); err != nil {
		t.Fatalf("symlink %s -> %s: %v", claudeFile, agentsFile, err)
	}
	linked, err := skillCopyMismatches(fixture)
	if err != nil {
		t.Fatalf("skillCopyMismatches(%q) error = %v", fixture, err)
	}
	if len(linked) != 1 {
		t.Fatalf("symlinked copy reported %d mismatches, want 1: %v", len(linked), linked)
	}
}
