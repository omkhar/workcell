// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func publicContractRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine repo root")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// mutatedContractCopy copies the real repo's policy/public-contract.toml
// into a temp file with exactly one string substitution applied, so
// negative-control tests exercise CheckPublicContract against a contract
// that is byte-for-byte identical to production except for the one
// mutation under test.
func mutatedContractCopy(t *testing.T, root, oldSubstring, newSubstring string) string {
	t.Helper()
	original := filepath.Join(root, "policy", "public-contract.toml")
	content, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), oldSubstring) {
		t.Fatalf("mutation substring %q not found in %s", oldSubstring, original)
	}
	mutated := strings.Replace(string(content), oldSubstring, newSubstring, 1)

	contractPath := filepath.Join(t.TempDir(), "public-contract.toml")
	if err := os.WriteFile(contractPath, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	return contractPath
}

func TestCheckPublicContractAcceptsRealRepo(t *testing.T) {
	root := publicContractRepoRoot(t)
	contractPath := filepath.Join(root, "policy", "public-contract.toml")

	if err := CheckPublicContract(root, contractPath); err != nil {
		t.Fatalf("CheckPublicContract() error = %v", err)
	}
}

func TestCheckPublicContractRejectsBogusExitCode(t *testing.T) {
	root := publicContractRepoRoot(t)
	contractPath := mutatedContractCopy(t, root,
		`"128"]`,
		`"128", "999"]`,
	)

	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a bogus exit code")
	}
	if !strings.Contains(err.Error(), "999") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of bogus exit code 999", err)
	}
}

func TestCheckPublicContractRejectsMissingSessionRecordField(t *testing.T) {
	root := publicContractRepoRoot(t)
	contractPath := mutatedContractCopy(t, root,
		`, "workspace_control_plane"]`,
		`]`,
	)

	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a missing SessionRecord field")
	}
	if !strings.Contains(err.Error(), "workspace_control_plane") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of orphaned field workspace_control_plane", err)
	}
}

func TestCheckPublicContractRejectsBogusOutputPrefix(t *testing.T) {
	root := publicContractRepoRoot(t)
	contractPath := mutatedContractCopy(t, root,
		`"prev_digest="]`,
		`"prev_digest=", "totally_bogus_prefix_zz="]`,
	)

	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a bogus output-line prefix")
	}
	if !strings.Contains(err.Error(), "totally_bogus_prefix_zz=") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of bogus prefix totally_bogus_prefix_zz=", err)
	}
}

// TestCheckPublicContractRejectsSubstringOnlyPrefix pins the key-boundary
// requirement: a prefix that appears in the source only as the tail of a
// longer key (e.g. "surance=" inside "assurance="/"current_assurance=") is
// never emitted at a real boundary and must be rejected, even though a plain
// substring search would have spuriously accepted it.
func TestCheckPublicContractRejectsSubstringOnlyPrefix(t *testing.T) {
	root := publicContractRepoRoot(t)
	contractPath := mutatedContractCopy(t, root,
		`"assurance=",`,
		`"assurance=", "surance=",`,
	)

	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a substring-only prefix")
	}
	if !strings.Contains(err.Error(), "surance=") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of substring-only prefix surance=", err)
	}
}

func TestCheckPublicContractRejectsBogusInjectionTable(t *testing.T) {
	root := publicContractRepoRoot(t)
	// Rename a real accepted table to one the parser does not accept: the
	// set-equality check must report it both as stale (in contract, not in
	// code) and orphaned (in code, not in contract).
	contractPath := mutatedContractCopy(t, root,
		`"copies"]`,
		`"bogus_injection_table"]`,
	)

	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a bogus injection table")
	}
	if !strings.Contains(err.Error(), "bogus_injection_table") || !strings.Contains(err.Error(), "copies") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of both bogus_injection_table and copies", err)
	}
}
