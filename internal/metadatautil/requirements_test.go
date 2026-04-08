// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRequirementsAcceptsCanonicalMatrix(t *testing.T) {
	root := t.TempDir()
	mustWriteRequirementFixture(t, root, "README.md")
	mustWriteRequirementFixture(t, root, "docs/guide.md")
	mustWriteRequirementFixture(t, root, "scripts/verify-example.sh")
	mustWriteRequirementFixture(t, root, "internal/example/example_test.go")

	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	if err := os.MkdirAll(filepath.Dir(requirementsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requirementsPath, []byte(`version = 1

[functional.FR-001]
title = "Managed launch"
summary = "Launch the provider inside the managed runtime."
evidence = ["scripts/verify-example.sh"]
docs = ["README.md", "docs/guide.md"]

[nonfunctional.NFR-001]
title = "Requirement traceability"
summary = "Every requirement cites automated evidence."
evidence = ["internal/example/example_test.go"]
docs = ["docs/guide.md"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateRequirements(root, requirementsPath); err != nil {
		t.Fatalf("ValidateRequirements() error = %v", err)
	}
}

func TestValidateRequirementsRejectsMissingEvidencePath(t *testing.T) {
	root := t.TempDir()
	mustWriteRequirementFixture(t, root, "README.md")
	mustWriteRequirementFixture(t, root, "docs/guide.md")
	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	if err := os.MkdirAll(filepath.Dir(requirementsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requirementsPath, []byte(`version = 1

[functional.FR-001]
title = "Managed launch"
summary = "Launch the provider inside the managed runtime."
evidence = ["scripts/missing.sh"]
docs = ["README.md"]

[nonfunctional.NFR-001]
title = "Requirement traceability"
summary = "Every requirement cites automated evidence."
evidence = ["docs/guide.md"]
docs = ["docs/guide.md"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateRequirements(root, requirementsPath)
	if err == nil {
		t.Fatal("ValidateRequirements() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "scripts/missing.sh") {
		t.Fatalf("ValidateRequirements() error = %v, want missing evidence path", err)
	}
}

func TestValidateRequirementsRejectsNonAutomatedEvidenceOnly(t *testing.T) {
	root := t.TempDir()
	mustWriteRequirementFixture(t, root, "README.md")
	mustWriteRequirementFixture(t, root, "docs/guide.md")
	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	if err := os.MkdirAll(filepath.Dir(requirementsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requirementsPath, []byte(`version = 1

[functional.FR-001]
title = "Managed launch"
summary = "Launch the provider inside the managed runtime."
evidence = ["README.md"]
docs = ["README.md"]

[nonfunctional.NFR-001]
title = "Requirement traceability"
summary = "Every requirement cites automated evidence."
evidence = ["docs/guide.md"]
docs = ["docs/guide.md"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateRequirements(root, requirementsPath)
	if err == nil {
		t.Fatal("ValidateRequirements() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "automated evidence") {
		t.Fatalf("ValidateRequirements() error = %v, want automated evidence failure", err)
	}
}

func TestValidateRequirementsRejectsRepoEscapingPaths(t *testing.T) {
	root := t.TempDir()
	mustWriteRequirementFixture(t, root, "README.md")
	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	if err := os.MkdirAll(filepath.Dir(requirementsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requirementsPath, []byte(`version = 1

[functional.FR-001]
title = "Managed launch"
summary = "Launch the provider inside the managed runtime."
evidence = ["../outside.sh"]
docs = ["README.md"]

[nonfunctional.NFR-001]
title = "Requirement traceability"
summary = "Every requirement cites automated evidence."
evidence = ["../outside.sh"]
docs = ["README.md"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateRequirements(root, requirementsPath)
	if err == nil {
		t.Fatal("ValidateRequirements() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "escapes repository root") {
		t.Fatalf("ValidateRequirements() error = %v, want repo root escape failure", err)
	}
}

func TestValidateRequirementsRejectsAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	mustWriteRequirementFixture(t, root, "README.md")
	requirementsPath := filepath.Join(root, "policy", "requirements.toml")
	if err := os.MkdirAll(filepath.Dir(requirementsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requirementsPath, []byte(`version = 1

[functional.FR-001]
title = "Managed launch"
summary = "Launch the provider inside the managed runtime."
evidence = ["/tmp/verify-example.sh"]
docs = ["README.md"]

[nonfunctional.NFR-001]
title = "Requirement traceability"
summary = "Every requirement cites automated evidence."
evidence = ["README.md"]
docs = ["README.md"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := ValidateRequirements(root, requirementsPath)
	if err == nil {
		t.Fatal("ValidateRequirements() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "must be repo-relative") {
		t.Fatalf("ValidateRequirements() error = %v, want absolute path failure", err)
	}
}

func mustWriteRequirementFixture(tb testing.TB, root, relativePath string) {
	tb.Helper()

	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
}
