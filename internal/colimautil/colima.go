// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package colimautil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/pathutil"
	"gopkg.in/yaml.v3"
)

func ValidateRuntimeMounts(configPath, workspace, profile string) error {
	config, err := loadYAMLMap(configPath)
	if err != nil {
		return err
	}

	return validateManagedMounts(config["mounts"], workspace, profile)
}

func expectedReadOnlyCacheMounts() (map[string]bool, error) {
	home, err := canonicalizeConfigPath(userHome())
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Join(home, "Library", "Caches", "colima")
	roots := []string{
		filepath.Join(cacheRoot, "workcell-host-inputs"),
		filepath.Join(cacheRoot, "workcell-shadow"),
	}
	expected := make(map[string]bool, len(roots))
	for _, root := range roots {
		canonical, err := canonicalizeConfigPath(root)
		if err != nil {
			return nil, err
		}
		expected[canonical] = false
	}
	return expected, nil
}

func validateManagedMounts(mountsRaw any, workspace, profile string) error {
	expected, err := canonicalizeConfigPath(workspace)
	if err != nil {
		return err
	}
	cacheMounts, err := expectedReadOnlyCacheMounts()
	if err != nil {
		return err
	}

	mounts := yamlSlice(mountsRaw)
	if len(mounts) == 0 {
		return fmt.Errorf("unexpected configured Colima mounts: %v", mountsRaw)
	}
	writableCount := 0
	writableMatches := 0
	for _, entry := range mounts {
		mount := yamlMap(entry)
		if mount == nil {
			return fmt.Errorf("colima profile %s has an unexpected host mount: %v", profile, entry)
		}
		rawLocation := yamlString(mount, "location")
		location, err := canonicalizeConfigPath(rawLocation)
		if err != nil {
			return fmt.Errorf("colima profile %s has an unexpected host mount: %s", profile, rawLocation)
		}
		if mountPoint := yamlFirstString(mount, "mountPoint", "mount_point"); mountPoint != "" {
			canonicalMountPoint, err := canonicalizeConfigPath(mountPoint)
			if err != nil || canonicalMountPoint != location {
				return fmt.Errorf("colima profile %s has an unexpected host mount: %s", profile, rawLocation)
			}
		}

		if yamlBool(mount, "writable") {
			writableCount++
			if location == expected {
				writableMatches++
			}
			continue
		}
		if _, ok := cacheMounts[location]; ok {
			cacheMounts[location] = true
			continue
		}
		return fmt.Errorf("colima profile %s has an unexpected host mount: %s", profile, rawLocation)
	}

	if writableCount != 1 || writableMatches != 1 {
		return fmt.Errorf("colima profile %s must mount only %s as writable", profile, expected)
	}
	for location, seen := range cacheMounts {
		if !seen {
			return fmt.Errorf("colima profile %s is missing read-only Workcell cache mount: %s", profile, location)
		}
	}

	return nil
}

func ValidateProfileConfig(configPath, workspace, expectedCPU, expectedMemory, expectedDisk string) error {
	config, err := loadYAMLMap(configPath)
	if err != nil {
		return err
	}

	expectedWorkspace, err := canonicalizeConfigPath(workspace)
	if err != nil {
		return err
	}

	if yamlBool(config, "forwardAgent", "forward_agent") {
		return fmt.Errorf("colima profile must not forward the SSH agent")
	}

	if err := validateManagedMounts(config["mounts"], workspace, "managed"); err != nil {
		if strings.HasPrefix(err.Error(), "colima profile managed must mount only ") {
			return fmt.Errorf("colima profile must mount only %s as writable in colima.yaml", expectedWorkspace)
		}
		return err
	}

	vmType := yamlFirstString(config, "vmType", "vm_type")
	if vmType != "vz" {
		return fmt.Errorf("unexpected Colima vmType for managed profile: %v", yamlFirst(config, "vmType", "vm_type"))
	}
	mountType := yamlFirstString(config, "mountType", "mount_type")
	if mountType != "virtiofs" {
		return fmt.Errorf("unexpected Colima mountType for managed profile: %v", yamlFirst(config, "mountType", "mount_type"))
	}
	runtimeName := yamlRuntimeName(config["runtime"])
	if runtimeName != "docker" {
		return fmt.Errorf("unexpected Colima runtime for managed profile: %v", config["runtime"])
	}
	if fmt.Sprint(config["cpu"]) != expectedCPU {
		return fmt.Errorf("unexpected Colima CPU count for managed profile: %v", config["cpu"])
	}
	if fmt.Sprint(config["memory"]) != expectedMemory {
		return fmt.Errorf("unexpected Colima memory size for managed profile: %v", config["memory"])
	}
	if fmt.Sprint(config["disk"]) != expectedDisk {
		return fmt.Errorf("unexpected Colima disk size for managed profile: %v", config["disk"])
	}

	return nil
}

func loadYAMLMap(path string) (map[string]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := yaml.Unmarshal(content, &value); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

func canonicalizeConfigPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("missing path")
	}
	expanded, err := pathutil.ExpandUserPathBestEffort(raw)
	if err != nil {
		return "", err
	}
	return pathutil.CanonicalizeExpandedPath(expanded)
}

func yamlMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	default:
		return nil
	}
}

func yamlSlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	default:
		return nil
	}
}

func yamlString(values map[string]any, key string) string {
	return yamlFirstString(values, key)
}

func yamlFirstString(values map[string]any, keys ...string) string {
	value := yamlFirst(values, keys...)
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func yamlBool(values map[string]any, keys ...string) bool {
	value := yamlFirst(values, keys...)
	typed, ok := value.(bool)
	return ok && typed
}

func yamlFirst(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func yamlRuntimeName(value any) string {
	if value == nil {
		return ""
	}
	if name, ok := value.(string); ok {
		return name
	}
	mapping := yamlMap(value)
	if mapping == nil {
		return ""
	}
	return yamlFirstString(mapping, "name")
}

func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
