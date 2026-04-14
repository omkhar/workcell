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

	expected, err := canonicalizeConfigPath(workspace)
	if err != nil {
		return err
	}
	home, err := canonicalizeConfigPath(userHome())
	if err != nil {
		return err
	}
	cacheRoot, err := canonicalizeConfigPath(filepath.Join(home, "Library", "Caches", "colima"))
	if err != nil {
		return err
	}

	mounts := yamlSlice(config["mounts"])
	writableCount := 0
	writableMatches := 0
	for _, entry := range mounts {
		mount := yamlMap(entry)
		if mount == nil || !yamlBool(mount, "writable") {
			continue
		}
		writableCount++
		location, err := canonicalizeConfigPath(yamlString(mount, "location"))
		if err != nil {
			continue
		}
		if location == expected {
			writableMatches++
		}
	}

	if writableCount != 1 || writableMatches != 1 {
		return fmt.Errorf("colima profile %s must mount only %s as writable", profile, expected)
	}

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

		writable := yamlBool(mount, "writable")
		if !writable && (location == cacheRoot || strings.HasPrefix(location, cacheRoot+string(filepath.Separator))) {
			continue
		}
		if location == expected {
			continue
		}
		return fmt.Errorf("colima profile %s has an unexpected host mount: %s", profile, rawLocation)
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

	mountsRaw := config["mounts"]
	mounts := yamlSlice(mountsRaw)
	if len(mounts) != 1 {
		return fmt.Errorf("unexpected configured Colima mounts: %v", mountsRaw)
	}

	mount := yamlMap(mounts[0])
	if mount == nil {
		return fmt.Errorf("unexpected configured Colima mounts: %v", mountsRaw)
	}
	location, err := canonicalizeConfigPath(yamlString(mount, "location"))
	if err != nil {
		return fmt.Errorf("colima profile must mount only %s as writable in colima.yaml", expectedWorkspace)
	}
	if !yamlBool(mount, "writable") || location != expectedWorkspace {
		return fmt.Errorf("colima profile must mount only %s as writable in colima.yaml", expectedWorkspace)
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
