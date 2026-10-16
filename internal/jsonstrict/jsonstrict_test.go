// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package jsonstrict

import (
	"math"
	"strings"
	"testing"
)

func TestRejectDuplicateKeys(t *testing.T) {
	for _, good := range []string{`{"a":1,"b":{"c":2},"d":[{"e":3}]}`, `[1,"x",null]`, `"x"`, `{"a":{"a":1}}`} {
		if err := RejectDuplicateKeys([]byte(good), math.MaxInt); err != nil {
			t.Errorf("RejectDuplicateKeys(%s) = %v, want nil", good, err)
		}
	}
	for _, bad := range []string{`{"a":1,"a":2}`, `{"o":{"k":1,"k":2}}`, `[{"y":1,"y":2}]`, `{"a":1,"a":2}`, `{"a":1`, `{"a":}`} {
		if err := RejectDuplicateKeys([]byte(bad), math.MaxInt); err == nil {
			t.Errorf("RejectDuplicateKeys(%s) = nil, want an error", bad)
		}
	}
}

// TestRejectDuplicateKeysCapsTheDepth admits maxDepth nested levels and
// refuses one more.
func TestRejectDuplicateKeysCapsTheDepth(t *testing.T) {
	nest := func(n int) []byte { return []byte(strings.Repeat("[", n) + "0" + strings.Repeat("]", n)) }
	if err := RejectDuplicateKeys(nest(3), 3); err != nil {
		t.Fatalf("depth 3 under cap 3 = %v, want nil", err)
	}
	if err := RejectDuplicateKeys(nest(4), 3); err == nil || err.Error() != "JSON nesting exceeds 3 levels" {
		t.Fatalf("depth 4 under cap 3 = %v, want the nesting error", err)
	}
}
