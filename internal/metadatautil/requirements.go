// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ValidateRequirements(rootDir, requirementsPath string) error {
	text, err := readText(requirementsPath)
	if err != nil {
		return err
	}
	document, err := ParseTOMLSubset(text, requirementsPath)
	if err != nil {
		return err
	}

	version, ok := document["version"].(int)
	if !ok || version != 1 {
		return fmt.Errorf("%s must set version = 1", requirementsPath)
	}

	seenIDs := map[string]struct{}{}
	seenTitles := map[string]struct{}{}
	for category, prefix := range map[string]string{
		"functional":    "FR-",
		"nonfunctional": "NFR-",
	} {
		rawTable, ok := document[category].(map[string]any)
		if !ok || len(rawTable) == 0 {
			return fmt.Errorf("%s must define a non-empty [%s] requirement table", requirementsPath, category)
		}
		ids := make([]string, 0, len(rawTable))
		for id := range rawTable {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if !strings.HasPrefix(id, prefix) {
				return fmt.Errorf("%s requirement %s must use prefix %s", requirementsPath, id, prefix)
			}
			if _, ok := seenIDs[id]; ok {
				return fmt.Errorf("%s defines duplicate requirement id %s", requirementsPath, id)
			}
			seenIDs[id] = struct{}{}

			table, ok := rawTable[id].(map[string]any)
			if !ok {
				return fmt.Errorf("%s requirement %s must be a table", requirementsPath, id)
			}
			if err := validateRequirementTable(rootDir, requirementsPath, category, id, table, seenTitles); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRequirementTable(rootDir, requirementsPath, category, id string, table map[string]any, seenTitles map[string]struct{}) error {
	allowedKeys := map[string]struct{}{
		"title":    {},
		"summary":  {},
		"evidence": {},
		"docs":     {},
	}
	for key := range table {
		if _, ok := allowedKeys[key]; !ok {
			return fmt.Errorf("%s requirement %s contains unsupported key %s", requirementsPath, id, key)
		}
	}

	title, ok := MustString(table["title"])
	if !ok || strings.TrimSpace(title) == "" {
		return fmt.Errorf("%s requirement %s must define a non-empty title", requirementsPath, id)
	}
	if _, ok := seenTitles[title]; ok {
		return fmt.Errorf("%s requirement %s reuses title %q", requirementsPath, id, title)
	}
	seenTitles[title] = struct{}{}

	summary, ok := MustString(table["summary"])
	if !ok || strings.TrimSpace(summary) == "" {
		return fmt.Errorf("%s requirement %s must define a non-empty summary", requirementsPath, id)
	}

	evidence, ok, err := MustStringSlice(table["evidence"])
	if err != nil {
		return fmt.Errorf("%s requirement %s evidence: %w", requirementsPath, id, err)
	}
	if !ok || len(evidence) == 0 {
		return fmt.Errorf("%s requirement %s must define a non-empty evidence array", requirementsPath, id)
	}

	docs, ok, err := MustStringSlice(table["docs"])
	if err != nil {
		return fmt.Errorf("%s requirement %s docs: %w", requirementsPath, id, err)
	}
	if !ok || len(docs) == 0 {
		return fmt.Errorf("%s requirement %s must define a non-empty docs array", requirementsPath, id)
	}

	automatedEvidence := false
	for _, path := range evidence {
		if err := validateRequirementPath(rootDir, requirementsPath, category, id, "evidence", path); err != nil {
			return err
		}
		if isAutomatedEvidencePath(path) {
			automatedEvidence = true
		}
	}
	if !automatedEvidence {
		return fmt.Errorf("%s requirement %s must cite at least one automated evidence path", requirementsPath, id)
	}
	for _, path := range docs {
		if err := validateRequirementPath(rootDir, requirementsPath, category, id, "docs", path); err != nil {
			return err
		}
	}
	return nil
}

func validateRequirementPath(rootDir, requirementsPath, category, id, field, path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s requirement %s %s entries may not be empty", requirementsPath, id, field)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("%s requirement %s %s path %s must be repo-relative", requirementsPath, id, field, path)
	}

	target, err := resolveRequirementPath(rootDir, path)
	if err != nil {
		return fmt.Errorf("%s requirement %s %s path %s: %w", requirementsPath, id, field, path, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("%s requirement %s %s path %s: %w", requirementsPath, id, field, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s requirement %s %s path %s must point to a file", requirementsPath, id, field, path)
	}
	return nil
}

func resolveRequirementPath(rootDir, path string) (string, error) {
	root := filepath.Clean(rootDir)
	target := filepath.Join(root, filepath.FromSlash(path))
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository root")
	}
	return target, nil
}

func isAutomatedEvidencePath(path string) bool {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(path, "_test.go"):
		return true
	case strings.HasPrefix(base, "test-"):
		return true
	case strings.HasPrefix(base, "verify-"):
		return true
	case base == "container-smoke.sh", base == "build-and-test.sh", base == "dev-quick-check.sh", base == "pre-merge.sh", base == "provider-e2e.sh":
		return true
	default:
		return false
	}
}
