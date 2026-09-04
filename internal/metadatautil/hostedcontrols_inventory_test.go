// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedNamedValuesPreservesMalformedAndDuplicateEntries(t *testing.T) {
	payload := map[string]any{"variables": []any{
		"malformed",
		map[string]any{"name": ""},
		map[string]any{"name": "DUPLICATE", "value": "first"},
		map[string]any{"name": "DUPLICATE", "value": "last"},
	}}
	actual := hostedNamedValues(payload)
	if len(actual) != 1 || actual["DUPLICATE"] != "last" {
		t.Fatalf("hostedNamedValues() = %#v, want last duplicate value only", actual)
	}
}

func TestHostedEnvironmentNamesPreservesMalformedAndDuplicateEntries(t *testing.T) {
	index := map[string]any{"environments": []any{
		"malformed",
		map[string]any{"name": ""},
		map[string]any{"name": "release"},
		map[string]any{"name": "release"},
	}}
	actual := hostedEnvironmentNames(index)
	if len(actual) != 1 {
		t.Fatalf("hostedEnvironmentNames() = %#v, want release only", actual)
	}
	if _, ok := actual["release"]; !ok {
		t.Fatalf("hostedEnvironmentNames() = %#v, want release", actual)
	}
}

func TestHostedSecretNamesPreservesMalformedAndDuplicateEntries(t *testing.T) {
	payload := map[string]any{"secrets": []any{
		"malformed",
		map[string]any{"name": ""},
		map[string]any{"name": "TOKEN"},
		map[string]any{"name": "TOKEN"},
	}}
	actual := hostedSecretNames(payload)
	if len(actual) != 1 {
		t.Fatalf("hostedSecretNames() = %#v, want TOKEN only", actual)
	}
	if _, ok := actual["TOKEN"]; !ok {
		t.Fatalf("hostedSecretNames() = %#v, want TOKEN", actual)
	}
}

func TestVerifyHostedRepositoryVariablesReportsMissingBeforeMismatch(t *testing.T) {
	payload := map[string]any{"variables": []any{map[string]any{"name": "WRONG", "value": "actual"}}}
	expected := map[string]any{"MISSING": "required", "WRONG": "expected"}
	err := verifyHostedRepositoryVariables(payload, expected, "omkhar/workcell")
	want := "repository variables missing on omkhar/workcell: MISSING"
	if err == nil || err.Error() != want {
		t.Fatalf("verifyHostedRepositoryVariables() error = %v, want %q", err, want)
	}
}

func TestVerifyHostedWorkflowEnvironmentsReportsMissingBeforeArtifactReads(t *testing.T) {
	expected := map[string]WorkflowEnvironmentPolicy{"release": {}}
	err := verifyHostedWorkflowEnvironments(t.TempDir(), map[string]any{"environments": []any{}}, expected, "omkhar/workcell")
	want := "workflow environments missing on omkhar/workcell: release"
	if err == nil || err.Error() != want {
		t.Fatalf("verifyHostedWorkflowEnvironments() error = %v, want %q", err, want)
	}
}

func TestVerifyHostedWorkflowEnvironmentChecksMetadataBeforeVariables(t *testing.T) {
	root := t.TempDir()
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release.json"), map[string]any{"name": "wrong"})
	err := verifyHostedWorkflowEnvironment(root, "release", WorkflowEnvironmentPolicy{}, "omkhar/workcell")
	want := "workflow environment metadata for release on omkhar/workcell resolved to wrong"
	if err == nil || err.Error() != want {
		t.Fatalf("verifyHostedWorkflowEnvironment() error = %v, want %q", err, want)
	}
}

func TestVerifyHostedEnvironmentVariablesReportsMissingBeforeOtherFailures(t *testing.T) {
	root := t.TempDir()
	payload := map[string]any{"variables": []any{
		map[string]any{"name": "WRONG", "value": "actual"},
		map[string]any{"name": "UNEXPECTED", "value": "value"},
	}}
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release-variables.json"), payload)
	expected := map[string]string{"MISSING": "required", "WRONG": "expected"}
	err := verifyHostedEnvironmentVariables(root, "release", expected, "omkhar/workcell")
	want := "workflow environment variables missing on omkhar/workcell/release: MISSING"
	if err == nil || err.Error() != want {
		t.Fatalf("verifyHostedEnvironmentVariables() error = %v, want %q", err, want)
	}
}

func TestVerifyHostedEnvironmentVariablesReportsMismatchBeforeUnexpected(t *testing.T) {
	root := t.TempDir()
	payload := map[string]any{"variables": []any{
		map[string]any{"name": "WRONG", "value": "actual"},
		map[string]any{"name": "UNEXPECTED", "value": "value"},
	}}
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release-variables.json"), payload)
	err := verifyHostedEnvironmentVariables(root, "release", map[string]string{"WRONG": "expected"}, "omkhar/workcell")
	want := `workflow environment variables on omkhar/workcell/release do not match policy: WRONG="actual" (expected "expected")`
	if err == nil || err.Error() != want {
		t.Fatalf("verifyHostedEnvironmentVariables() error = %v, want %q", err, want)
	}
}

func TestVerifyHostedEnvironmentSecretsReportsMissingBeforeUnexpected(t *testing.T) {
	root := t.TempDir()
	payload := map[string]any{"secrets": []any{map[string]any{"name": "UNEXPECTED"}}}
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release-secrets.json"), payload)
	err := verifyHostedEnvironmentSecrets(root, "release", []string{"MISSING"}, "omkhar/workcell")
	want := "workflow environment secrets missing on omkhar/workcell/release: MISSING"
	if err == nil || err.Error() != want {
		t.Fatalf("verifyHostedEnvironmentSecrets() error = %v, want %q", err, want)
	}
}

func TestVerifyHostedWorkflowEnvironmentChecksVariablesBeforeSecrets(t *testing.T) {
	root := t.TempDir()
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release.json"), map[string]any{"name": "release"})
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release-variables.json"), map[string]any{
		"variables": []any{map[string]any{"name": "UNEXPECTED", "value": "value"}},
	})
	err := verifyHostedWorkflowEnvironment(root, "release", WorkflowEnvironmentPolicy{}, "omkhar/workcell")
	if err == nil || !strings.Contains(err.Error(), "workflow environment variables on omkhar/workcell/release include unexpected entries") {
		t.Fatalf("verifyHostedWorkflowEnvironment() error = %v, want unexpected-variable rejection", err)
	}
}

func TestVerifyHostedWorkflowEnvironmentChecksSecretsBeforeDeployment(t *testing.T) {
	root := t.TempDir()
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release.json"), map[string]any{"name": "release"})
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release-variables.json"), map[string]any{"variables": []any{}})
	writeHostedInventoryJSON(t, filepath.Join(root, "environment-release-secrets.json"), map[string]any{
		"secrets": []any{map[string]any{"name": "UNEXPECTED"}},
	})
	err := verifyHostedWorkflowEnvironment(root, "release", WorkflowEnvironmentPolicy{}, "omkhar/workcell")
	if err == nil || !strings.Contains(err.Error(), "workflow environment secrets on omkhar/workcell/release include unexpected entries") {
		t.Fatalf("verifyHostedWorkflowEnvironment() error = %v, want unexpected-secret rejection", err)
	}
}

func writeHostedInventoryJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
