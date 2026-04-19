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

func writePublicRepoHygieneFixture(t *testing.T, readme string) string {
	t.Helper()

	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "scripts"),
		filepath.Join(root, ".agents"),
		filepath.Join(root, "docs"),
		filepath.Join(root, "man"),
		filepath.Join(root, "policy"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	scriptBytes, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "check-public-repo-hygiene.sh"))
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "scripts", "check-public-repo-hygiene.sh")
	if err := os.WriteFile(scriptPath, scriptBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}

	return scriptPath
}

func runPublicRepoHygieneFixture(t *testing.T, readme string) (string, error) {
	t.Helper()

	scriptPath := writePublicRepoHygieneFixture(t, readme)
	cmd := exec.Command("/bin/bash", scriptPath)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestCheckPublicRepoHygieneRejectsLeakedHomePaths(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		readme     string
		wantOutput string
	}{
		{name: "bare path", readme: "Leaked maintainer path: /Users/alice", wantOutput: "/Users/alice"},
		{name: "path with trailing text", readme: "Leaked maintainer path in prose: /Users/alice next-step", wantOutput: "/Users/alice next-step"},
		{name: "example prefixed username", readme: "Still machine specific: /Users/example-user/workcell", wantOutput: "/Users/example-user/workcell"},
		{name: "backtick wrapped path", readme: "Leaked markdown path: `/Users/alice/workcell`", wantOutput: "/Users/alice/workcell"},
		{name: "angle bracket wrapped path", readme: "Leaked markdown target: </Users/alice/workcell>", wantOutput: "/Users/alice/workcell"},
		{name: "mixed placeholder and real path", readme: "Mixed paths: `/Users/example` and `/Users/alice/workcell`", wantOutput: "/Users/alice/workcell"},
		{name: "file url", readme: "Leaked file URL: file:///Users/alice/workcell", wantOutput: "/Users/alice/workcell"},
		{name: "file url with authority", readme: "Leaked file URL: file://localhost/home/alice/workcell", wantOutput: "/home/alice/workcell"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			output, err := runPublicRepoHygieneFixture(t, tc.readme)
			if err == nil {
				t.Fatal("check-public-repo-hygiene unexpectedly accepted a leaked absolute home path")
			}
			if !strings.Contains(output, "machine-specific absolute home paths") {
				t.Fatalf("unexpected output: %s", output)
			}
			if !strings.Contains(output, tc.wantOutput) {
				t.Fatalf("output did not include the leaked path: %s", output)
			}
		})
	}
}

func TestCheckPublicRepoHygieneAllowsPortableExamples(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		readme string
	}{
		{name: "plain example path", readme: "Portable example path: /Users/example"},
		{name: "punctuation delimited example path", readme: "Portable markdown path: `/Users/example`"},
		{name: "example path with trailing text", readme: "Portable example path in prose: /Users/example next-step"},
		{name: "file url example path", readme: "Portable example file URL: file:///Users/example/workcell"},
		{name: "custom uri path", readme: "Portable custom URI: profile:///home/alice/docs"},
		{name: "custom uri with authority path", readme: "Portable custom URI with authority: notfile://localhost/home/alice/docs"},
		{name: "multiple home-like url segments", readme: "Legitimate URL with multiple segments: https://example.com/home/alice//Users/bob"},
		{name: "url path containing file scheme text", readme: "Legitimate URL path with embedded file text: https://example.com/path/file://localhost/home/alice/docs"},
		{name: "url segment", readme: "Legitimate URL: https://example.com/home/alice/docs"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			output, err := runPublicRepoHygieneFixture(t, tc.readme)
			if err != nil {
				t.Fatalf("check-public-repo-hygiene rejected portable input: %v\n%s", err, output)
			}
			if !strings.Contains(output, "Public repo hygiene check passed.") {
				t.Fatalf("unexpected output: %s", output)
			}
		})
	}
}
