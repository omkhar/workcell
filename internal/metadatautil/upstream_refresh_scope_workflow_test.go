// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestValidateUpstreamRefreshScopeWorkflow(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../.github/workflows/upstream-refresh-scope.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if err := metadatautil.ValidateUpstreamRefreshScopeWorkflow(workflow); err != nil {
		t.Fatalf("canonical workflow rejected: %v", err)
	}
	mutations := []struct{ name, old, replacement, want string }{
		{"pull_request_target", "  pull_request:\n", "  pull_request_target:\n", "only on pull_request"},
		{"second trigger", "  pull_request:\n", "  workflow_dispatch:\n  pull_request:\n", "only on pull_request"},
		{"head checked out first", "ref: ${{ github.event.pull_request.base.sha }}", "ref: ${{ github.event.pull_request.head.sha }}", "PR base first"},
		{"credentials persisted", "persist-credentials: false", "persist-credentials: true", "PR base first"},
		{"write token", "contents: read # Read", "contents: write # Read", "only contents: read"},
		{"check skipped", "      - name: Check merge-time scope\n", "      - name: Check merge-time scope\n        if: false\n", "must run only"},
		{"failure masked", "scope.sh pr-head", "scope.sh pr-head || true", "must run only"},
		{"renamed job", "name: Upstream refresh merge-time scope", "name: Upstream refresh scope", "named"},
		{"author type dropped", "          PR_AUTHOR_TYPE: ${{ github.event.pull_request.user.type }}\n", "", "must run only"},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			mutated := strings.Replace(workflow, m.old, m.replacement, 1)
			if mutated == workflow {
				t.Fatalf("mutation %q changed nothing", m.name)
			}
			err := metadatautil.ValidateUpstreamRefreshScopeWorkflow(mutated)
			if err == nil || !strings.Contains(err.Error(), m.want) {
				t.Fatalf("error = %v, want %q", err, m.want)
			}
		})
	}
}
