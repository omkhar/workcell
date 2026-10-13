package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestHostedJobsStageGoBeforeTheResolver is the gate on the hosted-runner Go
// rule: the runner's tool-cache go fails the trusted-go storage rule, so every
// job step that reaches resolve_exact_toolchain_go_bin must follow a step that
// runs scripts/ci/stage-trusted-go.sh.
func TestHostedJobsStageGoBeforeTheResolver(t *testing.T) {
	resolverScripts := []string{"run-hosted-controls-audit.sh", "verify-github-hosted-controls.sh", "publish-github-release.sh"}
	for _, name := range []string{"hosted-controls.yml", "release.yml"} {
		data, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct {
				Steps []struct {
					Run string `yaml:"run"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			t.Fatal(err)
		}
		for job, spec := range workflow.Jobs {
			staged := false
			for _, step := range spec.Steps {
				if strings.Contains(step.Run, "scripts/ci/stage-trusted-go.sh") {
					staged = true
				}
				for _, script := range resolverScripts {
					if strings.Contains(step.Run, script) && !staged {
						t.Errorf("%s job %s runs %s before scripts/ci/stage-trusted-go.sh", name, job, script)
					}
				}
			}
		}
	}
}
