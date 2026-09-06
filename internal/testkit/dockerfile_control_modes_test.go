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
	readOnlyControlScriptsStart    = "  && chmod 0444 \\\n    /usr/local/libexec/workcell/assurance.sh \\\n"
	executableControlScriptsStart  = "  && chmod +x \\\n"
	runtimeBuilderPermissionsEnd   = "  && find /opt/workcell/adapters /etc/claude-code /usr/local/libexec/workcell"
)

var executableControlScripts = []string{
	"/usr/local/libexec/workcell/home-control-plane.sh",
	"/usr/local/libexec/workcell/public-node-guard.mjs",
	"/usr/local/libexec/workcell/provider-policy.sh",
}

func runtimeBuilderPermissions(dockerfile string) (string, error) {
	start := strings.Index(dockerfile, runtimeBuilderPermissionsStart)
	if start < 0 {
		return "", fmt.Errorf("runtime-builder permission block start is missing")
	}
	remainder := dockerfile[start:]
	end := strings.Index(remainder, runtimeBuilderPermissionsEnd)
	if end < 0 {
		return "", fmt.Errorf("runtime-builder permission block end is missing")
	}
	return remainder[:end], nil
}

func validateControlScriptModes(dockerfile string) error {
	permissions, err := runtimeBuilderPermissions(dockerfile)
	if err != nil {
		return err
	}
	readOnly := strings.Index(permissions, readOnlyControlScriptsStart)
	executable := strings.Index(permissions, executableControlScriptsStart)
	if readOnly < 0 || executable < 0 || readOnly >= executable {
		return fmt.Errorf("control script mode commands are missing or out of order")
	}
	return requireControlScripts(permissions[readOnly:executable], permissions[executable:])
}

func requireControlScripts(readOnly string, executable string) error {
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
	permissions, err := runtimeBuilderPermissions(dockerfile)
	if err != nil {
		return "", err
	}
	readOnly := strings.Index(permissions, readOnlyControlScriptsStart)
	executable := strings.Index(permissions, executableControlScriptsStart)
	if readOnly < 0 || executable < 0 || readOnly >= executable {
		return "", fmt.Errorf("control script mode commands cannot be reordered")
	}
	reordered := permissions[:readOnly] + permissions[executable:] + permissions[readOnly:executable]
	return strings.Replace(dockerfile, permissions, reordered, 1), nil
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
		t.Run(filepath.Base(path)+" missing read-only mode", func(t *testing.T) {
			mutant := strings.Replace(dockerfile, "    "+path+" \\\n", "", 1)
			if err := validateControlScriptModes(mutant); err == nil {
				t.Fatal("validation accepted a control script without read-only mode normalization")
			}
		})
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
		mutant := strings.Replace(dockerfile, executableControlScriptsStart, executableControlScriptsStart+line, 1)
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
