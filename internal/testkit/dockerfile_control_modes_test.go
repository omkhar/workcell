// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	runtimeBuilderPermissionsStart = "RUN mkdir -p /etc/claude-code \\\n"
	runtimeBuilderPermissionsEnd   = "  && find /opt/workcell/adapters /etc/claude-code /usr/local/libexec/workcell"
	readOnlyModeStart              = "  && chmod 0444 \\\n"
	executableModeStart            = "  && chmod +x \\\n"
)

var executableControlScripts = []string{
	"/usr/local/libexec/workcell/home-control-plane.sh",
	"/usr/local/libexec/workcell/public-node-guard.mjs",
	"/usr/local/libexec/workcell/provider-policy.sh",
}

// splitControlScriptModes cuts the runtime-builder permission block into the
// text preceding the last read-only chmod, that read-only chmod, and the
// executable chmod that must follow it.
func splitControlScriptModes(dockerfile string) (prefix string, readOnly string, executable string, err error) {
	start := strings.Index(dockerfile, runtimeBuilderPermissionsStart)
	if start < 0 {
		return "", "", "", fmt.Errorf("runtime-builder permission block start is missing")
	}
	permissions := dockerfile[start:]
	end := strings.Index(permissions, runtimeBuilderPermissionsEnd)
	if end < 0 {
		return "", "", "", fmt.Errorf("runtime-builder permission block end is missing")
	}
	permissions = permissions[:end]
	executableAt := strings.Index(permissions, executableModeStart)
	if executableAt < 0 {
		return "", "", "", fmt.Errorf("executable mode command is missing")
	}
	readOnlyAt := strings.LastIndex(permissions[:executableAt], readOnlyModeStart)
	if readOnlyAt < 0 {
		return "", "", "", fmt.Errorf("read-only mode command is missing before the executable mode command")
	}
	return permissions[:readOnlyAt], permissions[readOnlyAt:executableAt], permissions[executableAt:], nil
}

func validateControlScriptModes(dockerfile string) error {
	_, readOnly, executable, err := splitControlScriptModes(dockerfile)
	if err != nil {
		return err
	}
	for _, path := range executableControlScripts {
		line := "    " + path + " \\\n"
		if count := strings.Count(readOnly, line); count != 1 {
			return fmt.Errorf("read-only mode includes %s %d times, want 1", path, count)
		}
		if count := strings.Count(executable, line); count != 1 {
			return fmt.Errorf("executable mode includes %s %d times, want 1", path, count)
		}
	}
	return nil
}

func reorderControlScriptModeCommands(dockerfile string) (string, error) {
	prefix, readOnly, executable, err := splitControlScriptModes(dockerfile)
	if err != nil {
		return "", err
	}
	return strings.Replace(dockerfile, prefix+readOnly+executable, prefix+executable+readOnly, 1), nil
}

func readRuntimeDockerfile(t *testing.T) string {
	t.Helper()
	dockerfilePath := filepath.Join(repoRoot(t), "runtime", "container", "Dockerfile")
	dockerfileBytes, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatal(err)
	}
	return string(dockerfileBytes)
}

func TestDockerfileNormalizesControlScriptModesBeforeMakingThemExecutable(t *testing.T) {
	if err := validateControlScriptModes(readRuntimeDockerfile(t)); err != nil {
		t.Fatal(err)
	}
}

func TestDockerfileControlScriptModeValidationRejectsMissingReadOnlyMode(t *testing.T) {
	dockerfile := readRuntimeDockerfile(t)
	for _, path := range executableControlScripts {
		mutant := strings.Replace(dockerfile, "    "+path+" \\\n", "", 1)
		if err := validateControlScriptModes(mutant); err == nil {
			t.Fatalf("validation accepted %s without read-only mode normalization", path)
		}
	}
}

func TestDockerfileControlScriptModeValidationRejectsDuplicateReadOnlyPath(t *testing.T) {
	dockerfile := readRuntimeDockerfile(t)
	for _, path := range executableControlScripts {
		line := "    " + path + " \\\n"
		mutant := strings.Replace(dockerfile, line, line+line, 1)
		if err := validateControlScriptModes(mutant); err == nil {
			t.Fatalf("validation accepted duplicate read-only path %s", path)
		}
	}
}

func TestDockerfileControlScriptModeValidationRejectsDuplicateExecutablePath(t *testing.T) {
	dockerfile := readRuntimeDockerfile(t)
	for _, path := range executableControlScripts {
		line := "    " + path + " \\\n"
		mutant := strings.Replace(dockerfile, executableModeStart, executableModeStart+line, 1)
		if err := validateControlScriptModes(mutant); err == nil {
			t.Fatalf("validation accepted duplicate executable path %s", path)
		}
	}
}

func TestDockerfileControlScriptModeValidationRejectsReorderedCommands(t *testing.T) {
	dockerfile := readRuntimeDockerfile(t)
	mutant, err := reorderControlScriptModeCommands(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateControlScriptModes(mutant); err == nil {
		t.Fatal("validation accepted executable mode before read-only mode normalization")
	}
}
