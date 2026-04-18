// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateOperatorContractAcceptsCanonicalSurface(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	if err := ValidateOperatorContract(root, contractPath, requirementsPath); err != nil {
		t.Fatalf("ValidateOperatorContract() error = %v", err)
	}
}

func TestValidateOperatorContractRejectsMissingCanonicalHelpEntry(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, false)

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	err := ValidateOperatorContract(root, contractPath, requirementsPath)
	if err == nil {
		t.Fatal("ValidateOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "canonical syntax") || !strings.Contains(err.Error(), "top-level-help") {
		t.Fatalf("ValidateOperatorContract() error = %v, want top-level help parity failure", err)
	}
}

func TestValidateOperatorContractRejectsUnknownWorkflowReference(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	if err := os.WriteFile(requirementsPath, []byte(`version = 1

[functional.FR-007]
title = "Durable session inventory"
summary = "Workcell exposes durable detached-session inventory and control."
workflows = ["session_commands_surface", "missing_workflow"]
evidence = ["tests/scenarios/shared/test-session-commands.sh"]
docs = ["README.md", "man/workcell.1"]

[nonfunctional.NFR-001]
title = "Requirement traceability"
summary = "Every requirement cites automated evidence."
evidence = ["internal/example/example_test.go"]
docs = ["README.md"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	err := ValidateOperatorContract(root, contractPath, requirementsPath)
	if err == nil {
		t.Fatal("ValidateOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "references unknown workflow missing_workflow") {
		t.Fatalf("ValidateOperatorContract() error = %v, want unknown workflow failure", err)
	}
}

func TestValidateOperatorContractRejectsWorkflowEvidenceOutsideRequirement(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	extraEvidencePath := filepath.Join(root, "tests", "scenarios", "shared", "test-extra-session-coverage.sh")
	if err := os.MkdirAll(filepath.Dir(extraEvidencePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraEvidencePath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	contract, err := readText(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contract = strings.Replace(
		contract,
		`evidence = ["tests/scenarios/shared/test-session-commands.sh"]`,
		`evidence = ["tests/scenarios/shared/test-extra-session-coverage.sh"]`,
		1,
	)
	if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}

	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	err = ValidateOperatorContract(root, contractPath, requirementsPath)
	if err == nil {
		t.Fatal("ValidateOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "must be cited by one of its referenced requirements") {
		t.Fatalf("ValidateOperatorContract() error = %v, want workflow evidence parity failure", err)
	}
}

func TestValidateOperatorContractRejectsWorkflowDocsOutsideRequirement(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	extraDocPath := filepath.Join(root, "docs", "session-extra.md")
	if err := os.MkdirAll(filepath.Dir(extraDocPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraDocPath, []byte("session extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	contract, err := readText(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contract = strings.Replace(
		contract,
		`docs = ["README.md", "man/workcell.1"]`,
		`docs = ["docs/session-extra.md"]`,
		1,
	)
	if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}

	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	err = ValidateOperatorContract(root, contractPath, requirementsPath)
	if err == nil {
		t.Fatal("ValidateOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "must be cited by one of its referenced requirements") {
		t.Fatalf("ValidateOperatorContract() error = %v, want workflow docs parity failure", err)
	}
}

func TestValidateOperatorContractRejectsAliasProbeMissingCanonicalOutput(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	workcellPath := filepath.Join(root, "scripts", "workcell")
	content, err := readText(workcellPath)
	if err != nil {
		t.Fatal(err)
	}
	content = strings.Replace(
		content,
		`printf 'Usage: workcell logs <audit|debug|file-trace|transcript>\nCompatibility alias for: workcell --logs audit|debug|file-trace|transcript\n'`,
		`printf 'Usage: workcell logs <audit|debug|file-trace|transcript>\nCompatibility alias for: workcell compatibility-only\n'`,
		1,
	)
	if err := os.WriteFile(workcellPath, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	err = ValidateOperatorContract(root, contractPath, requirementsPath)
	if err == nil {
		t.Fatal("ValidateOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "alias probe") || (!strings.Contains(err.Error(), "missing canonical syntax") && !strings.Contains(err.Error(), "missing alias usage")) {
		t.Fatalf("ValidateOperatorContract() error = %v, want alias probe parity failure", err)
	}
}

func TestValidateOperatorContractRejectsMissingWorkflowEvidence(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	contract, err := readText(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contract = strings.Replace(
		contract,
		`evidence = ["tests/scenarios/shared/test-session-commands.sh"]`,
		`evidence = []`,
		1,
	)
	if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}

	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	err = ValidateOperatorContract(root, contractPath, requirementsPath)
	if err == nil {
		t.Fatal("ValidateOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "must cite at least one evidence path") {
		t.Fatalf("ValidateOperatorContract() error = %v, want missing workflow evidence failure", err)
	}
}

func TestLoadOperatorContractRejectsAliasesWithoutAliasProbes(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	contract, err := readText(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contract = strings.Replace(contract, "alias_probes = [\"logs --help\"]\n", "", 1)
	if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = loadOperatorContract(contractPath)
	if err == nil {
		t.Fatal("loadOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "must define alias_probes for aliases") {
		t.Fatalf("loadOperatorContract() error = %v, want alias probe requirement failure", err)
	}
}

func TestLoadOperatorContractRejectsWrongTypedOptionalStringSlice(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	contract, err := readText(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contract = strings.Replace(
		contract,
		`aliases = ["workcell logs audit|debug|file-trace|transcript"]`,
		`aliases = "workcell logs audit|debug|file-trace|transcript"`,
		1,
	)
	if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = loadOperatorContract(contractPath)
	if err == nil {
		t.Fatal("loadOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "aliases") || !strings.Contains(err.Error(), "array of strings") {
		t.Fatalf("loadOperatorContract() error = %v, want typed aliases failure", err)
	}
}

func TestLoadOperatorContractRejectsPublicWorkflowWithoutDiscoverability(t *testing.T) {
	root := t.TempDir()
	writeOperatorContractFixture(t, root, true)

	contractPath := filepath.Join(root, "policy", "operator-contract.toml")
	contract, err := readText(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	contract = strings.Replace(
		contract,
		"discoverability = [\"top-level-help\", \"manpage\"]\n",
		"",
		1,
	)
	if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = loadOperatorContract(contractPath)
	if err == nil {
		t.Fatal("loadOperatorContract() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "discoverability") {
		t.Fatalf("loadOperatorContract() error = %v, want discoverability failure", err)
	}
}

func TestStripManpageFormattingRemovesBackspaceMarkup(t *testing.T) {
	input := "w\bwo\bor\brk\bkc\bce\bel\bll\bl\n"
	if got := stripManpageFormatting(input); got != "workcell\n" {
		t.Fatalf("stripManpageFormatting() = %q, want %q", got, "workcell\n")
	}
}

func writeOperatorContractFixture(t *testing.T, root string, includeDeleteInTopLevelHelp bool) {
	t.Helper()

	for _, path := range []string{
		"README.md",
		"man/workcell.1",
		"tests/scenarios/shared/test-session-commands.sh",
		"scripts/verify-invariants.sh",
		"internal/example/example_test.go",
	} {
		mustWriteRequirementFixture(t, root, path)
	}

	topLevelUsage := "Usage: workcell session <start|attach|send|stop|list|show|delete|logs|timeline|diff|export> [options]\n--logs audit|debug|file-trace|transcript"
	if !includeDeleteInTopLevelHelp {
		topLevelUsage = "Usage: workcell session <start|attach|send|stop|list|show|logs|timeline|diff|export> [options]\n--logs audit|debug|file-trace|transcript"
	}

	workcellPath := filepath.Join(root, "scripts", "workcell")
	if err := os.MkdirAll(filepath.Dir(workcellPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workcellPath, []byte("#!/bin/sh\nset -eu\nif [ \"$#\" -eq 1 ] && [ \"$1\" = \"--help\" ]; then\ncat <<'EOF'\n"+topLevelUsage+"\n  --session-workspace direct|isolated\nEOF\nexit 0\nfi\nif [ \"$#\" -eq 2 ] && [ \"$1\" = \"session\" ] && [ \"$2\" = \"--help\" ]; then\ncat <<'EOF'\nUsage: workcell session start [launch-options] [-- provider-args...]\n       workcell session delete --id SESSION_ID\n  --session-workspace direct|isolated\nEOF\nexit 0\nfi\nif [ \"$#\" -eq 2 ] && [ \"$1\" = \"logs\" ] && [ \"$2\" = \"--help\" ]; then\nprintf 'Usage: workcell logs <audit|debug|file-trace|transcript>\\nCompatibility alias for: workcell --logs audit|debug|file-trace|transcript\\n'\nexit 0\nfi\nprintf 'unsupported fixture invocation\\n' >&2\nexit 1\n# session start requires a clean source workspace for isolated session cloning:\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	manPath := filepath.Join(root, "man", "workcell.1")
	if err := os.WriteFile(manPath, []byte(topLevelUsage+"\n--session-workspace direct|isolated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("workcell session delete --id SESSION_ID\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	policyDir := filepath.Join(root, "policy")
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "requirements.toml"), []byte(`version = 1

[functional.FR-007]
title = "Durable session inventory"
summary = "Workcell exposes durable detached-session inventory and control."
workflows = ["session_commands_surface", "session_delete", "session_workspace_mode"]
evidence = ["tests/scenarios/shared/test-session-commands.sh"]
docs = ["README.md", "man/workcell.1"]

[functional.FR-006]
title = "Host-side operator commands"
summary = "Workcell exposes non-launch host-side operator commands."
workflows = ["launch_logs"]
evidence = ["scripts/verify-invariants.sh"]
docs = ["man/workcell.1"]

[nonfunctional.NFR-001]
title = "Requirement traceability"
summary = "Every requirement cites automated evidence."
evidence = ["internal/example/example_test.go"]
docs = ["README.md"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(policyDir, "operator-contract.toml"), []byte(`version = 1

[workflows.session_commands_surface]
title = "Session command surface"
support = "supported"
canonical = "workcell session <start|attach|send|stop|list|show|delete|logs|timeline|diff|export> [options]"
discoverability = ["top-level-help", "manpage"]
docs = ["man/workcell.1"]
evidence = ["tests/scenarios/shared/test-session-commands.sh"]
requirements = ["functional.FR-007"]
target_state = "retain"

[workflows.session_delete]
title = "Delete durable detached sessions"
support = "supported"
canonical = "workcell session delete --id SESSION_ID"
discoverability = ["subcommand-help:session", "readme"]
docs = ["README.md", "man/workcell.1"]
evidence = ["tests/scenarios/shared/test-session-commands.sh"]
requirements = ["functional.FR-007"]
target_state = "retain"

[workflows.session_workspace_mode]
title = "Detached session workspace mode"
support = "supported"
canonical = "--session-workspace direct|isolated"
discoverability = ["top-level-help", "subcommand-help:session", "manpage"]
docs = ["man/workcell.1"]
evidence = ["tests/scenarios/shared/test-session-commands.sh"]
requirements = ["functional.FR-007"]
remediation = ["session start requires a clean source workspace for isolated session cloning:"]
target_state = "retain"

[workflows.launch_logs]
title = "Read latest profile logs"
support = "supported"
canonical = "--logs audit|debug|file-trace|transcript"
aliases = ["workcell logs audit|debug|file-trace|transcript"]
alias_lifecycle = "compatibility-only"
alias_probes = ["logs --help"]
discoverability = ["top-level-help", "manpage"]
docs = ["man/workcell.1"]
evidence = ["scripts/verify-invariants.sh"]
requirements = ["functional.FR-006"]
target_state = "retain"
`), 0o644); err != nil {
		t.Fatal(err)
	}
}
