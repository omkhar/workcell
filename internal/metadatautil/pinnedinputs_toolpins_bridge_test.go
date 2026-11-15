package metadatautil_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// TEMPORARY: these tests cover the bridge that keeps the inline-value checks
// running until the workflows move to the pins step. The cleanup change (PR 4
// of the tool-pin split) removes the bridge and these tests.

// TestToolPinBridgeEndsWhenAnyWorkflowUsesThePinsStep proves a partly moved
// repository fails closed: once one workflow names the pins step, every
// workflow gets the binding checks, so an inline pin left anywhere fails.
func TestToolPinBridgeEndsWhenAnyWorkflowUsesThePinsStep(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, workflow, old, new string }{
		{
			name:     "pins step in one job",
			workflow: "security.yml",
			old:      "      - name: Install actionlint\n",
			new:      "      - id: pins\n        name: Load tool pins\n        run: ./scripts/ci/load-tool-pins.sh\n\n      - name: Install actionlint\n",
		},
		{
			name:     "comment that names the pins step",
			workflow: "docs.yml",
			old:      "\nconcurrency:\n",
			new:      "\n# steps.pins.outputs.buildx\nconcurrency:\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := writePinnedInputsFixture(t)
			rewriteFile(t, filepath.Join(cfg.WorkflowsDir, tc.workflow), func(content string) string {
				if !strings.Contains(content, tc.old) {
					t.Fatalf("fixture %s does not contain %q", tc.workflow, tc.old)
				}
				return strings.Replace(content, tc.old, tc.new, 1)
			})
			requirePinnedInputsErrorContains(t, cfg, "inline; bind")
		})
	}
}
