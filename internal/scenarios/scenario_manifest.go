// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package scenarios

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

var (
	validLanes     = map[string]struct{}{"secretless": {}, "provider-e2e": {}}
	validPlatforms = map[string]struct{}{"any": {}, "linux": {}, "macos": {}}
	validProviders = map[string]struct{}{"codex": {}, "claude": {}, "gemini": {}}
	personaPrefix  = "^[a-z][a-z0-9-]*$"
)

type Scenario struct {
	ID                  string
	Description         string
	Persona             string
	Providers           []string
	RequiresCredentials bool
	Manual              bool
	Lane                string
	Platform            string
	TestFile            string
}

func nonEmptyString(value any, label string) (string, error) {
	str, ok := value.(string)
	if !ok || strings.TrimSpace(str) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", label)
	}
	return str, nil
}

func optionalBool(scenario map[string]any, key, scenarioID string, defaultValue bool) (bool, error) {
	value, ok := scenario[key]
	if !ok {
		return defaultValue, nil
	}
	boolValue, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("scenario %s: %s must be boolean", scenarioID, key)
	}
	return boolValue, nil
}

func normalizeTestFile(value any, scenarioID string, manual bool) (string, error) {
	if value == "" || value == nil {
		if manual {
			return "", nil
		}
		return "", fmt.Errorf("scenario %s: test_file is required for automated scenarios", scenarioID)
	}

	raw, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("scenario %s: test_file must be a string", scenarioID)
	}

	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("scenario %s: test_file must stay under tests/scenarios without traversal", scenarioID)
	}

	parts := strings.Split(raw, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("scenario %s: test_file must stay under tests/scenarios without traversal", scenarioID)
		}
	}
	if path.Ext(raw) != ".sh" || !strings.HasPrefix(path.Base(raw), "test-") {
		return "", fmt.Errorf("scenario %s: test_file must reference a scenario shell script", scenarioID)
	}
	return path.Clean(raw), nil
}

func loadManifest(pathname string) (map[string]any, error) {
	content, err := os.ReadFile(pathname)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Scenario manifest does not exist: %s", pathname)
		}
		return nil, err
	}

	var manifest any
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, err
	}
	root, ok := manifest.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("scenario manifest must contain a JSON object")
	}
	return root, nil
}

func LoadScenarios(manifestPath string) ([]Scenario, error) {
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if manifest["version"] != float64(1) {
		return nil, fmt.Errorf("scenario manifest version must be 1")
	}

	rawScenarios, ok := manifest["scenarios"].([]any)
	if !ok || len(rawScenarios) == 0 {
		return nil, fmt.Errorf("scenario manifest must contain a non-empty scenarios array")
	}

	seenIDs := map[string]struct{}{}
	seenTestFiles := map[string]struct{}{}
	scenarios := make([]Scenario, 0, len(rawScenarios))
	for index, raw := range rawScenarios {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("scenario entry %d must be an object", index+1)
		}

		scenarioID, err := nonEmptyString(entry["id"], fmt.Sprintf("scenario entry %d id", index+1))
		if err != nil {
			return nil, err
		}
		if _, ok := seenIDs[scenarioID]; ok {
			return nil, fmt.Errorf("Duplicate scenario id: %s", scenarioID)
		}
		seenIDs[scenarioID] = struct{}{}

		description, err := nonEmptyString(entry["description"], fmt.Sprintf("scenario %s description", scenarioID))
		if err != nil {
			return nil, err
		}
		persona, err := nonEmptyString(entry["persona"], fmt.Sprintf("scenario %s persona", scenarioID))
		if err != nil {
			return nil, err
		}
		if !isPersonaValid(persona) {
			return nil, fmt.Errorf("scenario %s: persona must match %s", scenarioID, personaPrefix)
		}

		rawProviders, ok := entry["providers"].([]any)
		if !ok || len(rawProviders) == 0 {
			return nil, fmt.Errorf("scenario %s: providers must be a non-empty list", scenarioID)
		}
		normalizedProviders := make([]string, 0, len(rawProviders))
		for _, provider := range rawProviders {
			providerName, err := nonEmptyString(provider, fmt.Sprintf("scenario %s provider", scenarioID))
			if err != nil {
				return nil, err
			}
			if _, ok := validProviders[providerName]; !ok {
				return nil, fmt.Errorf(
					"scenario %s: provider must be one of: %s",
					scenarioID,
					strings.Join(sortedKeys(validProviders), ", "),
				)
			}
			for _, existing := range normalizedProviders {
				if existing == providerName {
					return nil, fmt.Errorf("scenario %s: duplicate provider %s", scenarioID, providerName)
				}
			}
			normalizedProviders = append(normalizedProviders, providerName)
		}

		requiresCredentials, err := optionalBool(entry, "requires_credentials", scenarioID, false)
		if err != nil {
			return nil, err
		}
		manual, err := optionalBool(entry, "manual", scenarioID, false)
		if err != nil {
			return nil, err
		}

		lane := "secretless"
		if requiresCredentials {
			lane = "provider-e2e"
		}
		if rawLane, ok := entry["lane"]; ok {
			laneValue, ok := rawLane.(string)
			if !ok || !containsKey(validLanes, laneValue) {
				return nil, fmt.Errorf("scenario %s: lane must be one of: %s", scenarioID, strings.Join(sortedKeys(validLanes), ", "))
			}
			lane = laneValue
		}
		if lane == "secretless" && requiresCredentials {
			return nil, fmt.Errorf("scenario %s: cannot be secretless and require credentials", scenarioID)
		}

		platform := "any"
		if rawPlatform, ok := entry["platform"]; ok {
			platformValue, ok := rawPlatform.(string)
			if !ok || !containsKey(validPlatforms, platformValue) {
				return nil, fmt.Errorf(
					"scenario %s: platform must be one of: %s",
					scenarioID,
					strings.Join(sortedKeys(validPlatforms), ", "),
				)
			}
			platform = platformValue
		}

		testFile, err := normalizeTestFile(entry["test_file"], scenarioID, manual)
		if err != nil {
			return nil, err
		}
		if testFile != "" {
			if _, ok := seenTestFiles[testFile]; ok {
				return nil, fmt.Errorf("duplicate scenario test_file: %s", testFile)
			}
			seenTestFiles[testFile] = struct{}{}
		}

		scenarios = append(scenarios, Scenario{
			ID:                  scenarioID,
			Description:         description,
			Persona:             persona,
			Providers:           normalizedProviders,
			RequiresCredentials: requiresCredentials,
			Manual:              manual,
			Lane:                lane,
			Platform:            platform,
			TestFile:            testFile,
		})
	}

	return scenarios, nil
}

func containsKey(values map[string]struct{}, key string) bool {
	_, ok := values[key]
	return ok
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isPersonaValid(persona string) bool {
	if persona == "" || persona[0] < 'a' || persona[0] > 'z' {
		return false
	}
	for _, r := range persona[1:] {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '-' {
			continue
		}
		return false
	}
	return true
}

func ScenarioShellTests(scenarioRoot string) ([]string, error) {
	absRoot, err := filepath.Abs(scenarioRoot)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(absRoot); err != nil || !info.IsDir() {
		return []string{}, nil
	}

	paths := []string{}
	if err := filepath.WalkDir(absRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "test-") || filepath.Ext(name) != ".sh" {
			return nil
		}
		rel, err := filepath.Rel(absRoot, current)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Strings(paths)
	return paths, nil
}

func VerifyCoverage(scenarioRoot, manifestPath string) error {
	absRoot, err := filepath.Abs(scenarioRoot)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return err
	}
	defer root.Close()

	scenarios, err := LoadScenarios(manifestPath)
	if err != nil {
		return err
	}

	manifestTestFiles := map[string]struct{}{}
	for _, scenario := range scenarios {
		if scenario.TestFile != "" {
			manifestTestFiles[scenario.TestFile] = struct{}{}
		}
	}
	keys := make([]string, 0, len(manifestTestFiles))
	for key := range manifestTestFiles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, testFile := range keys {
		info, err := root.Stat(filepath.FromSlash(testFile))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("Missing test file: tests/scenarios/%s", testFile)
		}
	}

	shellTests, err := ScenarioShellTests(absRoot)
	if err != nil {
		return err
	}
	orphaned := make([]string, 0)
	for _, testFile := range shellTests {
		if _, ok := manifestTestFiles[testFile]; !ok {
			orphaned = append(orphaned, testFile)
		}
	}
	if len(orphaned) > 0 {
		return fmt.Errorf("Scenario scripts missing from manifest: %s", strings.Join(orphaned, ", "))
	}
	return nil
}

func ListTSV(manifestPath string, w io.Writer) error {
	scenarios, err := LoadScenarios(manifestPath)
	if err != nil {
		return err
	}
	for _, scenario := range scenarios {
		requires := "0"
		if scenario.RequiresCredentials {
			requires = "1"
		}
		manual := "0"
		if scenario.Manual {
			manual = "1"
		}
		if _, err := fmt.Fprintln(w, strings.Join([]string{
			scenario.ID,
			scenario.TestFile,
			requires,
			scenario.Lane,
			scenario.Platform,
			manual,
		}, "\t")); err != nil {
			return err
		}
	}
	return nil
}

func Usage(program string) string {
	if program == "" {
		program = "workcell-scenario-manifest"
	}
	return fmt.Sprintf(
		"Usage: %s [list-tsv|verify-coverage] ...",
		program,
	)
}

func Run(program string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, Usage(program))
		return 2
	}

	switch args[0] {
	case "list-tsv":
		if len(args) != 2 {
			fmt.Fprintln(stderr, Usage(program))
			return 2
		}
		if err := ListTSV(args[1], stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "verify-coverage":
		if len(args) != 3 {
			fmt.Fprintln(stderr, Usage(program))
			return 2
		}
		if err := VerifyCoverage(args[1], args[2]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, Usage(program))
		fmt.Fprintf(stderr, "%s: unsupported command: %s\n", program, args[0])
		return 2
	}
}
