// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// skillTreeFiles reads every regular file under root and returns the contents
// keyed by path relative to root, plus the relative paths of entries that are
// neither a regular file nor a directory.
//
// filepath.WalkDir never follows a symlink: it reports one as a non-directory
// entry and does not descend into it.  Every path handed to os.ReadFile here
// was therefore reached through directory components the walk itself opened as
// real directories, so a symlink at any depth in either tree surfaces as an
// irregular entry instead of being traversed.  That is what keeps a symlinked
// parent from standing in for a committed file, and it is why the twin is
// discovered by its own walk rather than built by joining strings and stat-ing
// only the leaf.
func skillTreeFiles(root string) (map[string][]byte, []string, error) {
	files := map[string][]byte{}
	var irregular []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			irregular = append(irregular, relative+" ("+entry.Type().String()+")")
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = content
		return nil
	})
	return files, irregular, err
}

// skillCopyMismatches reports every way the `.claude/skills` tree under root
// fails to be a verbatim regular-file copy of the matching part of
// `.agents/skills`.  The `.claude` tree exists for harnesses that only read
// `.claude`; nothing else in the repository asserts the two stay identical, so
// either file can drift silently.
//
// `.agents/skills` may hold skills that `.claude/skills` does not copy, so the
// requirement is one-directional: every `.claude` file needs a byte-identical
// twin, not the reverse.  Neither tree may contain a symlink, because a
// symlink compares equal to whatever it points at and can resolve outside the
// repository, so it would satisfy the comparison without a second copy
// existing.
func skillCopyMismatches(root string) ([]string, error) {
	claudeRoot := filepath.Join(root, ".claude", "skills")
	agentsRoot := filepath.Join(root, ".agents", "skills")

	copies, irregularCopies, err := skillTreeFiles(claudeRoot)
	if err != nil {
		return nil, err
	}
	sources, irregularSources, err := skillTreeFiles(agentsRoot)
	if err != nil {
		return nil, err
	}

	var mismatches []string
	for tree, irregular := range map[string][]string{claudeRoot: irregularCopies, agentsRoot: irregularSources} {
		for _, entry := range irregular {
			mismatches = append(mismatches, filepath.Join(tree, entry)+" is not a regular file")
		}
	}
	for relative, copied := range copies {
		copyPath := filepath.Join(claudeRoot, relative)
		twinPath := filepath.Join(agentsRoot, relative)
		source, exists := sources[relative]
		switch {
		case !exists:
			mismatches = append(mismatches, copyPath+" has no regular-file twin at "+twinPath)
		case !bytes.Equal(copied, source):
			mismatches = append(mismatches, copyPath+" differs from "+twinPath)
		}
	}
	sort.Strings(mismatches)
	return mismatches, nil
}

func TestSkillCopyParity(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	mismatches, err := skillCopyMismatches(root)
	if err != nil {
		t.Fatalf("skillCopyMismatches(%q) error = %v", root, err)
	}
	for _, mismatch := range mismatches {
		t.Errorf("skill copy drift: %s; .claude/skills must stay a plain regular-file copy of .agents/skills", mismatch)
	}
}

// TestSkillCopyMismatchesDetectsDrift is the negative control for
// TestSkillCopyParity: a passing parity run only means something if the same
// assertion still fails on drift.  Each case perturbs a temporary fixture
// rather than the repository.
func TestSkillCopyMismatchesDetectsDrift(t *testing.T) {
	const (
		copyRelPath   = ".claude/skills/commit/SKILL.md"
		sourceRelPath = ".agents/skills/commit/SKILL.md"
	)

	for name, perturb := range map[string]func(t *testing.T, fixture string){
		"differing bytes": func(t *testing.T, fixture string) {
			writeFixtureFile(t, filepath.Join(fixture, copyRelPath), "drifted\n")
		},
		"missing twin": func(t *testing.T, fixture string) {
			if err := os.Remove(filepath.Join(fixture, sourceRelPath)); err != nil {
				t.Fatalf("remove twin: %v", err)
			}
		},
		"symlinked copy": func(t *testing.T, fixture string) {
			copyPath := filepath.Join(fixture, copyRelPath)
			if err := os.Remove(copyPath); err != nil {
				t.Fatalf("remove copy: %v", err)
			}
			if err := os.Symlink(filepath.Join(fixture, sourceRelPath), copyPath); err != nil {
				t.Fatalf("symlink copy: %v", err)
			}
		},
		"symlinked twin parent": func(t *testing.T, fixture string) {
			// The leaf is a regular file and its bytes match; only the parent
			// directory of the twin is a link, so a leaf-only stat would miss
			// this and the comparison would pass without a committed twin.
			elsewhere := filepath.Join(fixture, "elsewhere")
			writeFixtureFile(t, filepath.Join(elsewhere, "SKILL.md"), "identical\n")
			twinParent := filepath.Dir(filepath.Join(fixture, sourceRelPath))
			if err := os.RemoveAll(twinParent); err != nil {
				t.Fatalf("remove twin parent: %v", err)
			}
			if err := os.Symlink(elsewhere, twinParent); err != nil {
				t.Fatalf("symlink twin parent: %v", err)
			}
		},
		"symlinked copy parent": func(t *testing.T, fixture string) {
			elsewhere := filepath.Join(fixture, "elsewhere")
			writeFixtureFile(t, filepath.Join(elsewhere, "SKILL.md"), "identical\n")
			copyParent := filepath.Dir(filepath.Join(fixture, copyRelPath))
			if err := os.RemoveAll(copyParent); err != nil {
				t.Fatalf("remove copy parent: %v", err)
			}
			if err := os.Symlink(elsewhere, copyParent); err != nil {
				t.Fatalf("symlink copy parent: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := t.TempDir()
			writeFixtureFile(t, filepath.Join(fixture, copyRelPath), "identical\n")
			writeFixtureFile(t, filepath.Join(fixture, sourceRelPath), "identical\n")

			clean, err := skillCopyMismatches(fixture)
			if err != nil {
				t.Fatalf("skillCopyMismatches(unperturbed) error = %v", err)
			}
			if len(clean) != 0 {
				t.Fatalf("unperturbed fixture reported mismatches: %v", clean)
			}

			perturb(t, fixture)

			mismatches, err := skillCopyMismatches(fixture)
			if err != nil {
				t.Fatalf("skillCopyMismatches(perturbed) error = %v", err)
			}
			if len(mismatches) == 0 {
				t.Fatal("perturbed fixture reported no mismatch; the parity assertion cannot fail")
			}
		})
	}
}

func writeFixtureFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
