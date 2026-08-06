package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedInstallEvidenceDocumentation(t *testing.T) {
	root := repoRoot(t)
	evidencePaths := []string{
		"README.md",
		"SUPPORT.md",
		"docs/enterprise-rollout.md",
		"docs/github-workflows.md",
		"docs/provenance.md",
		"docs/release-posture.md",
		"docs/use-case-matrix.md",
		"policy/requirements.toml",
	}
	for _, path := range evidencePaths {
		content, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(content)), " ")
		for _, want := range []string{
			"bundle installation",
			"launcher-link removal",
			"man-page-link removal",
			"Homebrew installation",
			"formula removal",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s must describe hosted %s evidence", path, want)
			}
		}
	}
	for _, path := range append(evidencePaths, "ROADMAP.md") {
		content, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(content))
		for _, stale := range []string{
			"bundle install/uninstall",
			"bundle install and uninstall",
			"bundle installation and removal",
			"install/uninstall behavior",
		} {
			if strings.Contains(text, stale) {
				t.Fatalf("%s overstates hosted bundle evidence with %q", path, stale)
			}
		}
	}
}
