// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestLaneScripts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"scripts/validate-repo.sh": "\"${ROOT_DIR}/scripts/a.sh\"\necho scripts/b.sh\n\"${ARTIFACT_DIR}/scripts/g.sh\"\n$ROOT_DIR/scripts/h.sh\n",
		"scripts/ci/job-x.sh":      "./scripts/c.sh\n",
		".github/workflows/x.yml": "jobs:\n  x:\n    steps:\n      - run: ./scripts/d.sh --flag\n      - run: echo scripts/e.sh\n" +
			"      - run: scripts/j.sh\n        working-directory: other\n      - run: |\n          cd other\n          scripts/l.sh\n" +
			"  w:\n    defaults:\n      run:\n        working-directory: other\n    steps:\n      - run: scripts/k.sh\n",
		".github/workflows/z.yaml": "defaults:\n  run:\n    working-directory: sub\njobs:\n  z:\n    steps:\n      - run: ../scripts/m.sh\n" +
			"      - run: scripts/n.sh\n        working-directory: .\n",
		".github/workflows/notes.md": "./scripts/f.sh\n",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := metadatautil.LaneScripts(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"scripts/a.sh", "scripts/c.sh", "scripts/ci/job-x.sh", "scripts/d.sh", "scripts/h.sh", "scripts/m.sh", "scripts/n.sh", "scripts/validate-repo.sh"}
	if !slices.Equal(got, want) {
		t.Fatalf("LaneScripts() = %q, want %q", got, want)
	}
	if _, err := metadatautil.LaneScripts(t.TempDir()); err == nil {
		t.Fatal("LaneScripts() on a tree with no validate-repo.sh: want an error")
	}
	outside := filepath.Join(t.TempDir(), "job-y.sh")
	if err := os.WriteFile(outside, []byte("scripts/z.sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "scripts", "ci", "job-y.sh")); err != nil {
		t.Fatal(err)
	}
	if _, err := metadatautil.LaneScripts(root); err == nil {
		t.Fatal("LaneScripts() with a symlinked lane file: want an error")
	}
}

// TestLaneScriptsRejectsEvasions runs the shared corpus against a gate a workflow step runs. A row that turns the command into text must unwire the gate, and a gated row must keep it wired.
func TestLaneScriptsRejectsEvasions(t *testing.T) {
	t.Parallel()
	const head = "name: w\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - name: s\n        run: |\n          true\n"
	const anchor = "          ./scripts/gate.sh"
	validate := func(workflow string) error {
		root := t.TempDir()
		for rel, body := range map[string]string{"scripts/validate-repo.sh": "true\n", "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": workflow} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := metadatautil.LaneScripts(root)
		if err != nil {
			return err
		}
		if !slices.Contains(got, "scripts/gate.sh") {
			return errors.New("scripts/gate.sh has no lane wiring")
		}
		return nil
	}
	if err := validate(head + anchor + "\n"); err != nil {
		t.Fatalf("LaneScripts() on the wired step: %v", err)
	}
	RequireRejectsTextEvasions(t, head+anchor+"\n", anchor, "wiring", validate)
}
