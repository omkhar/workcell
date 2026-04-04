// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package scenarios

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(tb testing.TB, root string, payload any) string {
	tb.Helper()
	manifestDir := filepath.Join(root, "tests", "scenarios")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		tb.Fatal(err)
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		tb.Fatal(err)
	}
	data = append(data, '\n')
	manifestPath := filepath.Join(manifestDir, "manifest.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		tb.Fatal(err)
	}
	return manifestPath
}

func writeScript(tb testing.TB, root, relPath string) {
	tb.Helper()
	pathname := filepath.Join(root, "tests", "scenarios", filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(pathname), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(pathname, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		tb.Fatal(err)
	}
}

type runResult struct {
	code   int
	stdout string
	stderr string
}

func runGo(program string, args []string) runResult {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(program, args, &stdout, &stderr)
	return runResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestLoadScenariosAndListTSV(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeManifest(t, root, map[string]any{
		"version": 1,
		"scenarios": []any{
			map[string]any{
				"id":                   "shared/example",
				"description":          "Example scenario",
				"persona":              "developer",
				"providers":            []any{"codex", "claude"},
				"requires_credentials": false,
				"manual":               false,
				"test_file":            "shared/test-example.sh",
			},
			map[string]any{
				"id":                   "shared/manual",
				"description":          "Manual scenario",
				"persona":              "tester",
				"providers":            []any{"gemini"},
				"requires_credentials": false,
				"manual":               true,
				"lane":                 "provider-e2e",
				"platform":             "macos",
				"test_file":            "",
			},
		},
	})

	scenarios, err := LoadScenarios(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 2 {
		t.Fatalf("expected 2 scenarios, got %d", len(scenarios))
	}
	if scenarios[0].Lane != "secretless" || scenarios[0].Platform != "any" || scenarios[0].Manual || scenarios[0].RequiresCredentials {
		t.Fatalf("unexpected defaults for first scenario: %#v", scenarios[0])
	}
	if scenarios[1].TestFile != "" || !scenarios[1].Manual || scenarios[1].Lane != "provider-e2e" || scenarios[1].Platform != "macos" {
		t.Fatalf("unexpected manual scenario normalization: %#v", scenarios[1])
	}

	got := runGo("scenario_manifest", []string{"list-tsv", manifestPath})
	if got.code != 0 {
		t.Fatalf("Run(list-tsv) = %d stderr=%q", got.code, got.stderr)
	}
	if got.stderr != "" {
		t.Fatalf("unexpected stderr: %q", got.stderr)
	}
	want := "shared/example\tshared/test-example.sh\t0\tsecretless\tany\t0\nshared/manual\t\t0\tprovider-e2e\tmacos\t1\n"
	if got.stdout != want {
		t.Fatalf("unexpected list-tsv output: %q", got.stdout)
	}
}

func TestRunRejectsInvalidManifestsAndCoverage(t *testing.T) {
	cases := []struct {
		name         string
		manifest     map[string]any
		command      []string
		setup        func(testing.TB, string)
		wantContains string
	}{
		{
			name: "duplicate-id",
			manifest: map[string]any{
				"version": 1,
				"scenarios": []any{
					map[string]any{
						"id":          "shared/duplicate",
						"description": "One",
						"persona":     "developer",
						"providers":   []any{"codex"},
						"test_file":   "shared/test-one.sh",
					},
					map[string]any{
						"id":          "shared/duplicate",
						"description": "Two",
						"persona":     "developer",
						"providers":   []any{"claude"},
						"test_file":   "shared/test-two.sh",
					},
				},
			},
			command:      []string{"list-tsv"},
			wantContains: "Duplicate scenario id: shared/duplicate",
		},
		{
			name: "missing-test-file",
			manifest: map[string]any{
				"version": 1,
				"scenarios": []any{
					map[string]any{
						"id":                   "shared/missing",
						"description":          "Missing script",
						"persona":              "developer",
						"providers":            []any{"codex"},
						"requires_credentials": false,
						"manual":               false,
						"test_file":            "shared/test-missing.sh",
					},
				},
			},
			command:      []string{"verify-coverage"},
			wantContains: "Missing test file: tests/scenarios/shared/test-missing.sh",
			setup: func(tb testing.TB, root string) {
				writeScript(tb, root, "shared/test-orphan.sh")
			},
		},
		{
			name: "orphan-test-file",
			manifest: map[string]any{
				"version": 1,
				"scenarios": []any{
					map[string]any{
						"id":                   "shared/present",
						"description":          "Present script",
						"persona":              "developer",
						"providers":            []any{"codex"},
						"requires_credentials": false,
						"manual":               false,
						"test_file":            "shared/test-present.sh",
					},
				},
			},
			command:      []string{"verify-coverage"},
			wantContains: "Scenario scripts missing from manifest: shared/test-orphan.sh",
			setup: func(tb testing.TB, root string) {
				writeScript(tb, root, "shared/test-present.sh")
				writeScript(tb, root, "shared/test-orphan.sh")
			},
		},
		{
			name: "path-traversal",
			manifest: map[string]any{
				"version": 1,
				"scenarios": []any{
					map[string]any{
						"id":          "shared/traversal",
						"description": "Traversal",
						"persona":     "developer",
						"providers":   []any{"codex"},
						"test_file":   "../escape.sh",
					},
				},
			},
			command:      []string{"list-tsv"},
			wantContains: "test_file must stay under tests/scenarios without traversal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			manifestPath := writeManifest(t, root, tc.manifest)
			if tc.setup != nil {
				tc.setup(t, root)
			}

			var goArgs []string
			switch tc.command[0] {
			case "list-tsv":
				goArgs = []string{"list-tsv", manifestPath}
			case "verify-coverage":
				goArgs = []string{"verify-coverage", filepath.Join(root, "tests", "scenarios"), manifestPath}
			default:
				t.Fatalf("unsupported command in test: %s", tc.command[0])
			}

			got := runGo("scenario_manifest", goArgs)
			if got.code != 1 {
				t.Fatalf("exit code mismatch: got %d want 1\nstdout=%q\nstderr=%q", got.code, got.stdout, got.stderr)
			}
			if !strings.Contains(got.stderr, tc.wantContains) {
				t.Fatalf("stderr %q does not contain %q", got.stderr, tc.wantContains)
			}
		})
	}
}

func TestVerifyCoverageWithoutRoot(t *testing.T) {
	root := t.TempDir()
	manifestPath := writeManifest(t, root, map[string]any{
		"version": 1,
		"scenarios": []any{
			map[string]any{
				"id":                   "shared/example",
				"description":          "Example scenario",
				"persona":              "developer",
				"providers":            []any{"codex"},
				"requires_credentials": false,
				"manual":               false,
				"test_file":            "shared/test-example.sh",
			},
		},
	})
	writeScript(t, root, "shared/test-example.sh")

	got := runGo("scenario_manifest", []string{"verify-coverage", filepath.Join(root, "tests", "scenarios"), manifestPath})
	if got.code != 0 {
		t.Fatalf("Run(verify-coverage) = %d stderr=%q", got.code, got.stderr)
	}
	if got.stderr != "" {
		t.Fatalf("unexpected stderr: %q", got.stderr)
	}
}
