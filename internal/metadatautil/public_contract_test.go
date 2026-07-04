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

// TestExitCodeEmitterAnchor pins that an exit code is only satisfied by a
// real exit construct, not by an unrelated standalone numeric literal such
// as an arity check or slice index.
func TestExitCodeEmitterAnchor(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		code string
		want bool
	}{
		{"os.Exit", `os.Exit(3)`, "3", true},
		{"ExitCodeError", `return &cliexit.ExitCodeError{Code: 3}`, "3", true},
		{"shell exit", `  exit 2`, "2", true},
		{"return literal", `return 128 + sig`, "128", true},
		{"launcher branch", `if e == ENOENT { 127 } else { 126 }`, "126", true},
		{"timeout const", `const ColimaTimeoutExitCode = 124`, "124", true},
		{"arity constant is not an exit", `if len(args) == 3 {`, "3", false},
		{"slice index is not an exit", `parts[2]`, "2", false},
	} {
		if got := exitCodeEmitted(tc.src, tc.code); got != tc.want {
			t.Errorf("%s: exitCodeEmitted(%q, %q) = %v, want %v", tc.name, tc.src, tc.code, got, tc.want)
		}
	}
}

// TestOutputLinePrefixEmitterAnchor pins that a documented prefix is only
// satisfied by a quoted format-string emitter, not by a shell variable
// assignment or sed pattern that merely mentions the key.
func TestOutputLinePrefixEmitterAnchor(t *testing.T) {
	// A quoted emitter satisfies the prefix.
	if !outputLinePrefixEmitted([]string{`printf 'record_digest=%q ' "${d}"`}, "record_digest=") {
		t.Fatal("quoted emitter should satisfy record_digest=")
	}
	if !outputLinePrefixEmitted([]string{`fmt.Sprintf("assurance=%s", s)`}, "assurance=") {
		t.Fatal("quoted emitter should satisfy assurance=")
	}
	// A bare variable assignment or sed pattern must NOT satisfy it.
	for _, nonEmitter := range []string{
		`  local record_digest=""`,
		`prev_digest="$(sed -n 's/.*record_digest=\([^ ]*\).*/\1/p' "${p}")"`,
	} {
		if outputLinePrefixEmitted([]string{nonEmitter}, "record_digest=") {
			t.Fatalf("non-emitter reference %q must not satisfy record_digest=", nonEmitter)
		}
	}
	// A longer key ending in the prefix must NOT satisfy it.
	if outputLinePrefixEmitted([]string{`fmt.Sprintf("current_assurance=%s", s)`}, "assurance=") {
		t.Fatal("current_assurance= must not satisfy assurance=")
	}
}

// TestExcludeNonEmitterFilesDropsSelfAndTests pins the corpus exclusions
// that keep the output-prefix scan honest: the validator's own source (whose
// doc comments quote the contract prefixes) and _test.go fixtures must never
// be searched, or they would self-satisfy the very drift the check catches.
func TestExcludeNonEmitterFilesDropsSelfAndTests(t *testing.T) {
	in := []string{
		contractValidatorSourceFile,
		"internal/host/sessions/sessions.go",
		"internal/metadatautil/public_contract_test.go",
		"cmd/workcell-citools/main.go",
	}
	out := excludeNonEmitterFiles(in)
	for _, p := range out {
		if p == contractValidatorSourceFile || strings.HasSuffix(p, "_test.go") {
			t.Fatalf("excludeNonEmitterFiles kept a non-emitter file %q: %v", p, out)
		}
	}
	if len(out) != 2 {
		t.Fatalf("excludeNonEmitterFiles kept %v, want the 2 real emitter files", out)
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

// TestCheckPublicContractRejectsMissingScalarRootKey pins that the check
// compares against the authoritative allowedRootPolicyKeys gate: dropping a
// scalar root key (version/includes) the gate still accepts must fail as a
// stale entry, catching drift the later table-name scrape would have missed.
func TestCheckPublicContractRejectsMissingScalarRootKey(t *testing.T) {
	root := publicContractRepoRoot(t)
	contractPath := mutatedContractCopy(t, root,
		`scalar_root_keys = ["version", "includes"]`,
		`scalar_root_keys = ["includes"]`,
	)

	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a missing scalar root key")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of the missing gate key version", err)
	}
}
