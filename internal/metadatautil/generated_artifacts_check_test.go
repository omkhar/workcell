// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckGeneratedArtifactsAcceptsThisRepository(t *testing.T) {
	if err := metadatautil.CheckGeneratedArtifacts(filepath.Join("..", "..")); err != nil {
		t.Fatalf("CheckGeneratedArtifacts() error = %v", err)
	}
}

func TestCheckGeneratedArtifacts(t *testing.T) {
	t.Parallel()
	const fresh = "generated\n"
	cases := []struct {
		name      string
		header    string
		body      string
		committed string
		caller    string
		wantErr   string
	}{
		{
			name:      "fresh artifact passes",
			header:    "# generated-artifact: policy/fixture.json\n",
			body:      "printf 'generated\\n' >\"$1\"\n",
			committed: fresh,
		},
		{
			name:      "stale artifact fails",
			header:    "# generated-artifact: policy/fixture.json\n",
			body:      "printf 'generated\\n' >\"$1\"\n",
			committed: "committed by hand\n",
			wantErr:   "is stale",
		},
		{
			name:    "orphan generator fails",
			header:  "# generated-artifact: none (nothing to commit)\n",
			body:    "true\n",
			wantErr: "no tracked script invokes this generator",
		},
		{
			name:   "called generator passes",
			header: "# generated-artifact: none (nothing to commit)\n",
			body:   "true\n",
			caller: "#!/usr/bin/env bash\nset -euo pipefail\nROOT_DIR=\"$(pwd)\"\n\"${ROOT_DIR}/scripts/generate-fixture.sh\" out\n",
		},
		{
			name:    "missing declaration fails",
			header:  "",
			body:    "true\n",
			wantErr: "add a # generated-artifact: header line",
		},
		{
			name:    "empty declaration fails",
			header:  "# generated-artifact:\n",
			body:    "true\n",
			wantErr: "carries no value",
		},
		{
			name:    "a path with a reason is not a path",
			header:  "# generated-artifact: policy/fixture.json (maybe)\n",
			body:    "true\n",
			wantErr: "must name one path or none",
		},
		{
			// A lint-list entry names the generator without running it. The
			// reviewer found this exact shape: the only reference to
			// scripts/generate-workflow-lane-manifest.sh was its row in the
			// shell_files array of scripts/validate-repo.sh.
			name:    "a lint-list entry is not a caller",
			header:  "# generated-artifact: none (nothing to commit)\n",
			body:    "true\n",
			caller:  "#!/usr/bin/env bash\nshell_files=(\n  \"${ROOT_DIR}/scripts/generate-fixture.sh\"\n)\n",
			wantErr: "no tracked script invokes this generator",
		},
		{
			name:   "a comment is not a caller",
			header: "# generated-artifact: none (nothing to commit)\n",
			body:   "true\n",
			caller: "#!/usr/bin/env bash\n# run \"${ROOT_DIR}/scripts/generate-fixture.sh\" by hand\n",
			// The reference sits in a comment, so nothing runs the generator.
			wantErr: "no tracked script invokes this generator",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGeneratorFixture(t, filepath.Join(root, "scripts", "generate-fixture.sh"),
				"#!/usr/bin/env bash\n"+testCase.header+"set -euo pipefail\n"+testCase.body)
			if testCase.committed != "" {
				writeFileFixture(t, filepath.Join(root, "policy", "fixture.json"), testCase.committed)
			}
			if testCase.caller != "" {
				writeGeneratorFixture(t, filepath.Join(root, "scripts", "caller.sh"), testCase.caller)
			}
			initGitFixture(t, root)
			err := metadatautil.CheckGeneratedArtifacts(root)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("expected a clean result, found %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("expected an error containing %q, found %v", testCase.wantErr, err)
			}
		})
	}
}

// An empty generator set must not pass: a gate derived from a list it never
// read reports a clean result over generators it never saw.
func TestCheckGeneratedArtifactsRejectsEmptyGeneratorSet(t *testing.T) {
	t.Parallel()
	if err := metadatautil.CheckGeneratedArtifacts(t.TempDir()); err == nil {
		t.Fatal("expected an empty generator set to fail")
	}
}

// Each row is a shape the repository really carries. A list entry and a
// comment name the generator without running it; the reviewer found the first
// shape in scripts/validate-repo.sh and it made the orphan invisible.
func TestValidateGeneratorCaller(t *testing.T) {
	t.Parallel()
	const generator = "generate-workflow-lane-manifest.sh"
	cases := []struct {
		name   string
		script string
		want   bool
	}{
		{"root-relative invocation", "\"${ROOT_DIR}/scripts/" + generator + "\" \"${OUTPUT_PATH}\"\n", true},
		{"working-directory invocation", "./scripts/" + generator + " dist/lanes.json\n", true},
		{"shell_files lint array", "shell_files=(\n  \"${ROOT_DIR}/scripts/" + generator + "\"\n)\n", false},
		{"HOST_GATE_SCRIPTS array", "HOST_GATE_SCRIPTS=(\n  \"${ROOT_DIR}/scripts/" + generator + "\"\n)\n", false},
		{"single-quoted list entry", "files=(\n  'scripts/" + generator + "'\n)\n", false},
		{"line comment", "# run scripts/" + generator + " by hand\n", false},
		{"indented comment", "  # scripts/" + generator + "\n", false},
		{"unrelated generator", "\"${ROOT_DIR}/scripts/generate-release-checksums.sh\" out\n", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := metadatautil.ValidateGeneratorCaller(testCase.script, generator)
			if (err == nil) != testCase.want {
				t.Fatalf("ValidateGeneratorCaller() error = %v, want caller = %v", err, testCase.want)
			}
		})
	}
}

func writeGeneratorFixture(t *testing.T, path, body string) {
	t.Helper()
	writeFileFixture(t, path, body)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("mark the fixture executable: %v", err)
	}
}

func writeFileFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create the fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the fixture: %v", err)
	}
}

// initGitFixture makes the fixture tree a repository, because the caller
// search reads the tracked shell scripts.
func initGitFixture(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
}
