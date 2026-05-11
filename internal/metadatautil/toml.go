// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

// ParseTOMLSubset is a thin wrapper preserved for metadatautil's
// internal callers and existing test expectations.  See
// internal/tomlsubset for the canonical implementation.
func ParseTOMLSubset(content string, sourcePath string) (map[string]any, error) {
	return tomlsubset.Parse(content, sourcePath)
}

// MustString returns the string form of value when it is a string,
// reporting ok=false otherwise.  Used by callers that read TOML subset
// documents into typed shapes.
func MustString(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok
}

// MustStringSlice converts value into []string when value is a TOML
// array of strings.  Returns ok=false if value is not an array, and a
// non-nil error if the array contains non-string entries.
func MustStringSlice(value any) ([]string, bool, error) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false, nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false, errors.New("array value must contain only strings")
		}
		result = append(result, s)
	}
	return result, true, nil
}
