// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"strings"
	"testing"
)

func TestNamedWorkflowStepScopesToRequestedStep(t *testing.T) {
	t.Parallel()
	workflow := `steps:
  - name: Confirm attestation environment policy
    run: |
      echo "check policy"
  - name: Later step
    run: |
      if [[ "${ENABLE_GITHUB_ATTESTATIONS_SUPPORTED}" != "true" ]]; then
        exit 1
      fi
`

	step := namedWorkflowStep(workflow, "Confirm attestation environment policy")
	if !strings.Contains(step, `echo "check policy"`) {
		t.Fatalf("namedWorkflowStep() = %q, want requested step body", step)
	}
	if strings.Contains(step, "Later step") || strings.Contains(step, "exit 1") {
		t.Fatalf("namedWorkflowStep() included a later step: %q", step)
	}
}
