// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"os/exec"
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
		`"workspace_control_plane", `,
		``,
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
		`"head_digest="]`,
		`"head_digest=", "totally_bogus_prefix_zz="]`,
	)

	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a bogus output-line prefix")
	}
	if !strings.Contains(err.Error(), "totally_bogus_prefix_zz=") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of bogus prefix totally_bogus_prefix_zz=", err)
	}
}

func TestCheckPublicContractRejectsRemovalFromV1Freeze(t *testing.T) {
	root := publicContractRepoRoot(t)
	contractPath := mutatedContractCopy(t, root,
		`, "egress_enforcement="`,
		``,
	)

	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly accepted removal of a frozen v1 output prefix")
	}
	if !strings.Contains(err.Error(), "egress_enforcement=") || !strings.Contains(err.Error(), "v1 output-line prefixes") {
		t.Fatalf("CheckPublicContract() error = %v, want frozen-prefix diagnostic", err)
	}
}

func TestV1ContractFreezeRejectsCanonicalWorkflowChange(t *testing.T) {
	root := publicContractRepoRoot(t)
	operatorPath := filepath.Join(root, "policy", "operator-contract.toml")
	content, err := os.ReadFile(operatorPath)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(
		string(content),
		`canonical = "--dry-run"`,
		`canonical = "--preview-launch"`,
		1,
	)
	if mutated == string(content) {
		t.Fatal("operator-contract mutation did not apply")
	}
	mutatedPath := filepath.Join(t.TempDir(), "operator-contract.toml")
	if err := os.WriteFile(mutatedPath, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}

	err = checkV1ContractFreeze(
		filepath.Join(root, "policy", "public-contract.toml"),
		mutatedPath,
		defaultV1ContractFreezePath(root),
	)
	if err == nil {
		t.Fatal("checkV1ContractFreeze() unexpectedly accepted changed canonical workflow syntax")
	}
	if !strings.Contains(err.Error(), "launch_dry_run") || !strings.Contains(err.Error(), "--dry-run") {
		t.Fatalf("checkV1ContractFreeze() error = %v, want frozen workflow diagnostic", err)
	}
}

func TestV1ContractFreezeRejectsUnfrozenSupportedWorkflow(t *testing.T) {
	root := publicContractRepoRoot(t)
	freezePath := defaultV1ContractFreezePath(root)
	content, err := os.ReadFile(freezePath)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(
		string(content),
		`launch_dry_run = "--dry-run"`+"\n",
		"",
		1,
	)
	if mutated == string(content) {
		t.Fatal("v1 freeze mutation did not apply")
	}
	mutatedPath := filepath.Join(t.TempDir(), "v1-contract-freeze.toml")
	if err := os.WriteFile(mutatedPath, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}

	err = checkV1ContractFreeze(
		filepath.Join(root, "policy", "public-contract.toml"),
		filepath.Join(root, "policy", "operator-contract.toml"),
		mutatedPath,
	)
	if err == nil {
		t.Fatal("checkV1ContractFreeze() unexpectedly accepted an unfrozen supported workflow")
	}
	if !strings.Contains(err.Error(), "must append current supported workflow launch_dry_run") {
		t.Fatalf("checkV1ContractFreeze() error = %v, want append-only workflow diagnostic", err)
	}
}

func TestV1ContractFreezeRejectsUnfrozenPublicAddition(t *testing.T) {
	root := publicContractRepoRoot(t)
	publicContent, err := os.ReadFile(filepath.Join(root, "policy", "public-contract.toml"))
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(
		string(publicContent),
		`"head_digest="]`,
		`"head_digest=", "new_v1_prefix="]`,
		1,
	)
	if mutated == string(publicContent) {
		t.Fatal("public-contract addition did not apply")
	}
	publicPath := filepath.Join(t.TempDir(), "public-contract.toml")
	if err := os.WriteFile(publicPath, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}

	err = checkV1ContractFreeze(
		publicPath,
		filepath.Join(root, "policy", "operator-contract.toml"),
		defaultV1ContractFreezePath(root),
	)
	if err == nil {
		t.Fatal("checkV1ContractFreeze() unexpectedly accepted an unfrozen public addition")
	}
	if !strings.Contains(err.Error(), "must append current output-line prefixes") || !strings.Contains(err.Error(), "new_v1_prefix=") {
		t.Fatalf("checkV1ContractFreeze() error = %v, want append-only public diagnostic", err)
	}
}

func TestV1ContractHistoryRejectsSynchronizedPublicRemoval(t *testing.T) {
	root := publicContractRepoRoot(t)
	publicContent, err := os.ReadFile(filepath.Join(root, "policy", "public-contract.toml"))
	if err != nil {
		t.Fatal(err)
	}
	freezeContent, err := os.ReadFile(defaultV1ContractFreezePath(root))
	if err != nil {
		t.Fatal(err)
	}
	mutatedPublic := strings.Replace(string(publicContent), `, "egress_enforcement="`, "", 1)
	mutatedFreeze := strings.Replace(string(freezeContent), `, "egress_enforcement="`, "", 1)
	if mutatedPublic == string(publicContent) || mutatedFreeze == string(freezeContent) {
		t.Fatal("synchronized public-contract mutation did not apply")
	}
	dir := t.TempDir()
	publicPath := filepath.Join(dir, "public-contract.toml")
	freezePath := filepath.Join(dir, "v1-contract-freeze.toml")
	if err := os.WriteFile(publicPath, []byte(mutatedPublic), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freezePath, []byte(mutatedFreeze), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkV1ContractFreeze(
		publicPath,
		filepath.Join(root, "policy", "operator-contract.toml"),
		freezePath,
	); err != nil {
		t.Fatalf("current-state parity should accept the synchronized fixture, got %v", err)
	}
	err = CheckV1ContractFreezeHistory(defaultV1ContractFreezePath(root), freezePath)
	if err == nil {
		t.Fatal("history check unexpectedly accepted synchronized removal of a historical v1 prefix")
	}
	if !strings.Contains(err.Error(), "egress_enforcement=") || !strings.Contains(err.Error(), "removed historical") {
		t.Fatalf("CheckV1ContractFreezeHistory() error = %v, want historical-removal diagnostic", err)
	}
}

func TestV1ContractHistoryRejectsSynchronizedCanonicalRewrite(t *testing.T) {
	root := publicContractRepoRoot(t)
	operatorContent, err := os.ReadFile(filepath.Join(root, "policy", "operator-contract.toml"))
	if err != nil {
		t.Fatal(err)
	}
	freezeContent, err := os.ReadFile(defaultV1ContractFreezePath(root))
	if err != nil {
		t.Fatal(err)
	}
	mutatedOperator := strings.Replace(string(operatorContent), `canonical = "--dry-run"`, `canonical = "--preview-launch"`, 1)
	mutatedFreeze := strings.Replace(string(freezeContent), `launch_dry_run = "--dry-run"`, `launch_dry_run = "--preview-launch"`, 1)
	if mutatedOperator == string(operatorContent) || mutatedFreeze == string(freezeContent) {
		t.Fatal("synchronized operator-contract mutation did not apply")
	}
	dir := t.TempDir()
	operatorPath := filepath.Join(dir, "operator-contract.toml")
	freezePath := filepath.Join(dir, "v1-contract-freeze.toml")
	if err := os.WriteFile(operatorPath, []byte(mutatedOperator), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freezePath, []byte(mutatedFreeze), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkV1ContractFreeze(
		filepath.Join(root, "policy", "public-contract.toml"),
		operatorPath,
		freezePath,
	); err != nil {
		t.Fatalf("current-state parity should accept the synchronized fixture, got %v", err)
	}
	err = CheckV1ContractFreezeHistory(defaultV1ContractFreezePath(root), freezePath)
	if err == nil {
		t.Fatal("history check unexpectedly accepted synchronized rewrite of historical v1 syntax")
	}
	if !strings.Contains(err.Error(), "launch_dry_run") || !strings.Contains(err.Error(), "rewrote historical") {
		t.Fatalf("CheckV1ContractFreezeHistory() error = %v, want historical-rewrite diagnostic", err)
	}
}

func TestV1ContractGitHistoryIncludesRemovedMergeParent(t *testing.T) {
	root := publicContractRepoRoot(t)
	original, err := os.ReadFile(defaultV1ContractFreezePath(root))
	if err != nil {
		t.Fatal(err)
	}
	mainVersion := strings.Replace(
		string(original),
		`"head_digest="]`,
		`"head_digest=", "history_merge_prefix="]`,
		1,
	)
	staleVersion := strings.Replace(
		string(original),
		`"head_digest="]`,
		`"head_digest=", "stale_branch_prefix="]`,
		1,
	)
	if mainVersion == string(original) || staleVersion == string(original) {
		t.Fatal("merge-topology fixture mutation did not apply")
	}

	repo := t.TempDir()
	runGitFixture(t, repo, "init", "-q")
	runGitFixture(t, repo, "config", "user.name", "Workcell Test")
	runGitFixture(t, repo, "config", "user.email", "workcell-test@example.invalid")
	runGitFixture(t, repo, "config", "commit.gpgsign", "false")
	freezePath := filepath.Join(repo, "policy", "v1-contract-freeze.toml")
	if err := os.MkdirAll(filepath.Dir(freezePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freezePath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, repo, "add", "policy/v1-contract-freeze.toml")
	runGitFixture(t, repo, "commit", "-q", "-m", "initial floor")
	runGitFixture(t, repo, "branch", "stale")

	if err := os.WriteFile(freezePath, []byte(mainVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, repo, "commit", "-qam", "append main commitment")

	runGitFixture(t, repo, "switch", "-q", "stale")
	if err := os.WriteFile(freezePath, []byte(staleVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, repo, "commit", "-qam", "change stale branch")
	runGitFixture(t, repo, "switch", "-q", "-")
	merge := exec.Command("git", "-C", repo, "merge", "--no-ff", "stale", "-m", "merge stale branch")
	if output, err := merge.CombinedOutput(); err == nil {
		t.Fatalf("merge fixture unexpectedly avoided a conflict:\n%s", output)
	}
	// Resolve to the first parent's version. Default path simplification can
	// then hide the second parent's stale_branch_prefix commitment; the
	// production --full-history enumeration must still inspect and reject it.
	if err := os.WriteFile(freezePath, []byte(mainVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, repo, "add", "policy/v1-contract-freeze.toml")
	runGitFixture(t, repo, "commit", "-q", "--no-edit")
	runGitFixture(t, repo, "config", "log.showSignature", "true")

	err = CheckV1ContractFreezeGitHistory(repo, freezePath)
	if err == nil {
		t.Fatal("history check unexpectedly accepted a merge that removed a parent commitment")
	}
	if !strings.Contains(err.Error(), "stale_branch_prefix=") {
		t.Fatalf("CheckV1ContractFreezeGitHistory() error = %v, want removed merge-parent commitment", err)
	}
}

func TestV1ContractHistoryLogArgsAreCompleteAndSignatureFree(t *testing.T) {
	got := strings.Join(v1ContractHistoryLogArgs("policy/v1-contract-freeze.toml"), " ")
	for _, want := range []string{"-c log.showSignature=false", "--full-history", "--format=%H", "-- policy/v1-contract-freeze.toml"} {
		if !strings.Contains(got, want) {
			t.Fatalf("v1ContractHistoryLogArgs() = %q, want %q", got, want)
		}
	}
}

func runGitFixture(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
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
	// shellproto fallback: a real Field{Key: "..."} in a shellproto file
	// satisfies the prefix; a bare quoted key (e.g. an error-message string)
	// does not.
	if !outputLinePrefixEmitted([]string{`import "…/shellproto"` + "\n" + `w.WriteFields(shellproto.Field{Key: "publish_pr_url", Value: u})`}, "publish_pr_url=") {
		t.Fatal("shellproto Field{Key: \"publish_pr_url\"} should satisfy publish_pr_url=")
	}
	if outputLinePrefixEmitted([]string{`import "…/shellproto"` + "\n" + `missing := "publish_pr_url"`}, "publish_pr_url=") {
		t.Fatal("a bare quoted key in a shellproto file must not satisfy publish_pr_url=")
	}
}

// TestStripCommentsRemovesCommentProse pins that comment forms are dropped so
// they cannot satisfy the emitter/exit-site scans, while real code survives.
func TestStripCommentsRemovesCommentProse(t *testing.T) {
	got := stripComments("os.Exit(2) // fallback exit 3 here\n/* exit 4 */\n# exit 5\n#!/bin/bash\n\tcode := \"${#a[@]}\"\n")
	if !strings.Contains(got, "os.Exit(2)") {
		t.Fatalf("stripComments dropped real code: %q", got)
	}
	for _, ghost := range []string{"exit 3", "exit 4", "exit 5"} {
		if strings.Contains(got, ghost) {
			t.Fatalf("stripComments left comment prose %q: %q", ghost, got)
		}
	}
	if !strings.Contains(got, "#!/bin/bash") || !strings.Contains(got, "${#a[@]}") {
		t.Fatalf("stripComments corrupted shebang or inline #: %q", got)
	}
}

func TestCheckPublicContractRejectsMissingSessionShowPrefix(t *testing.T) {
	root := publicContractRepoRoot(t)
	contractPath := mutatedContractCopy(t, root,
		`, "display_workspace="`,
		``,
	)
	err := CheckPublicContract(root, contractPath)
	if err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a missing session-show prefix")
	}
	if !strings.Contains(err.Error(), "display_workspace=") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of display_workspace=", err)
	}
}

func TestCheckPublicContractRejectsTableMovedToScalar(t *testing.T) {
	root := publicContractRepoRoot(t)
	// Move a real table (copies) into scalar_root_keys: the flattened union
	// still equals the gate, but the table-specific check must fail. Two
	// independent line edits (a comment separates them in the real policy).
	original, err := os.ReadFile(filepath.Join(root, "policy", "public-contract.toml"))
	if err != nil {
		t.Fatal(err)
	}
	mutated := string(original)
	for _, sub := range []struct{ from, to string }{
		{`"copies", "network"]`, `"network"]`},
		{`scalar_root_keys = ["version", "includes"]`, `scalar_root_keys = ["version", "includes", "copies"]`},
	} {
		if !strings.Contains(mutated, sub.from) {
			t.Fatalf("mutation substring %q not found", sub.from)
		}
		mutated = strings.Replace(mutated, sub.from, sub.to, 1)
	}
	contractPath := filepath.Join(t.TempDir(), "public-contract.toml")
	if err := os.WriteFile(contractPath, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CheckPublicContract(root, contractPath); err == nil {
		t.Fatal("CheckPublicContract() unexpectedly succeeded with a table moved into scalar_root_keys")
	} else if !strings.Contains(err.Error(), "copies") {
		t.Fatalf("CheckPublicContract() error = %v, want mention of copies", err)
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
		`"copies", "network"]`,
		`"bogus_injection_table", "network"]`,
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
