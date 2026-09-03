// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"testing"
)

func TestRulesetSummaryIDRejectsInvalidJSONNumbers(t *testing.T) {
	for _, literal := range []string{"42.0", "42e0", "42.000000000000001", "9223372036854775808"} {
		_, err := rulesetSummaryID(map[string]any{"id": json.Number(literal)})
		if err == nil {
			t.Fatalf("rulesetSummaryID(%q) unexpectedly accepted a noncanonical ID", literal)
		}
	}
}
