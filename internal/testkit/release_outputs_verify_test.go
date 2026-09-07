// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func verifyReleaseOutputsScript(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "scripts", "verify-release-outputs.sh")
}

const (
	releaseOutputTag    = "v1.2.3"
	releaseOutputBundle = "workcell-" + releaseOutputTag + ".tar.gz"
	releaseOutputImage  = "ghcr.io/omkhar/workcell"
	slsaPredicate       = "https://slsa.dev/provenance/v1"
	spdxPredicate       = "https://spdx.dev/Document/v2.3"

	releaseOutputIssuer   = "https://token.actions.githubusercontent.com"
	releaseOutputIdentity = "https://github.com/omkhar/workcell/.github/workflows/release.yml@refs/heads/main"
)

// releaseOutputAssets mirrors DATA_ASSETS in scripts/verify-release-outputs.sh,
// in the order the verifier walks it.
func releaseOutputAssets() []string {
	return []string{
		releaseOutputBundle,
		"workcell.rb",
		"workcell-image.digest",
		"workcell-build-inputs.json",
		"workcell-control-plane.json",
		"workcell-builder-environment.json",
		"SHA256SUMS",
		"workcell-source.spdx.json",
		"workcell-image.spdx.json",
	}
}

func releaseOutputFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	assets := releaseOutputAssets()
	if err := os.WriteFile(filepath.Join(dir, "workcell-image.digest"), []byte("ghcr.io/omkhar/workcell@sha256:"+strings.Repeat("a", 64)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := make([]string, 0, len(assets))
	for _, asset := range assets {
		if asset == "SHA256SUMS" {
			continue
		}
		path := filepath.Join(dir, asset)
		if asset != "workcell-image.digest" {
			content := []byte("fixture:" + asset + "\n")
			if asset == "workcell-build-inputs.json" {
				content = []byte(`{"build":{"ref":"` + strings.Repeat("c", 40) + `"}}`)
			}
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(content)
		lines = append(lines, hex.EncodeToString(digest[:])+"  "+asset)
		if err := os.WriteFile(path+".sigstore.json", []byte(`{"fixture":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.sigstore.json"), []byte(`{"fixture":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// releaseOutputStubBin builds a driver that sources the real verifier and
// replaces cosign and gh with logging stubs. Each fail glob selects which
// invocations of that tool fail: "" fails none, "*" fails every one, and a
// narrower glob fails a single call, so a negative control cannot be satisfied
// by an earlier check rejecting first. An empty tag digest makes the matching
// `cosign verify` return an empty result array instead of failing, which is the
// case a vacuously-true `all` predicate would accept.
func releaseOutputStubBin(t *testing.T, cosignFailGlob, ghFailGlob string) (string, string, string) {
	t.Helper()
	return releaseOutputStubDriver(t, verifyReleaseOutputsScript(t), cosignFailGlob, ghFailGlob, strings.Repeat("a", 64), strings.Repeat("a", 64))
}

func releaseOutputStubDriver(t *testing.T, scriptPath, cosignFailGlob, ghFailGlob, releaseTagDigest, commitTagDigest string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	cosignLog := filepath.Join(dir, "cosign.log")
	ghLog := filepath.Join(dir, "gh.log")
	driver := filepath.Join(dir, "verify-release-outputs-test-driver.sh")
	script := fmt.Sprintf(`#!/bin/bash
source %q
cosign_fail_glob=%q
gh_fail_glob=%q
log_call() {
  local log="$1"
  shift
  { printf '%%s\x1f' "$@"; printf '\n'; } >>"${log}"
}
cosign() {
  [[ -z "${GITHUB_TOKEN+x}" && -z "${GH_TOKEN+x}" && -z "${ATTESTATION_TOKEN+x}" ]] || return 96
  log_call %q "$@"
  if [[ "${1:-}" == "verify" ]]; then
    tag_digest=%q
    if [[ "$*" == *":sha-%s"* ]]; then
      tag_digest=%q
    fi
    if [[ -z "${tag_digest}" ]]; then
      printf '[]\n'
    else
      printf '[{"critical":{"image":{"docker-manifest-digest":"sha256:%%s"}}}]\n' "${tag_digest}"
    fi
  fi
  [[ -n "${cosign_fail_glob}" && "$*" == ${cosign_fail_glob} ]] && return 1
  return 0
}
gh() {
  [[ -z "${GITHUB_TOKEN+x}" && -z "${ATTESTATION_TOKEN+x}" ]] || return 98
  [[ "${GH_TOKEN:-}" == "test-token" ]] || return 97
  [[ "${GH_HOST:-}" == "github.com" ]] || return 95
  [[ -z "${GH_ENTERPRISE_TOKEN:-}" && -z "${GITHUB_ENTERPRISE_TOKEN:-}" ]] || return 94
  for arg in "$@"; do
    [[ "${arg}" != "--hostname" && "${arg}" != --hostname=* ]] || return 93
  done
  log_call %q "$@"
  [[ -n "${gh_fail_glob}" && "$*" == ${gh_fail_glob} ]] && return 1
  return 0
}
main "$@"
`, scriptPath, cosignFailGlob, ghFailGlob, cosignLog, releaseTagDigest, strings.Repeat("c", 40), commitTagDigest, ghLog)
	if err := os.WriteFile(driver, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return driver, cosignLog, ghLog
}

func releaseOutputArgs(assets, tag, imageRepository, sourceDigest, workflowDigest string, attestations bool) []string {
	args := []string{
		"--assets-dir", assets,
		"--repo", "omkhar/workcell",
		"--tag", tag,
		"--image-repository", imageRepository,
		"--source-digest", sourceDigest,
		"--workflow-digest", workflowDigest,
	}
	if attestations {
		args = append(args, "--attestations")
	}
	return args
}

func runVerifyDriver(t *testing.T, driver string, args, env []string) (int, string) {
	t.Helper()
	// Bash startup files are cleared unless the caller sets them deliberately,
	// so a test can hand the verifier hostile startup state on purpose.
	full := append([]string{}, env...)
	for _, name := range []string{"BASH_ENV", "ENV"} {
		if !slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, name+"=") }) {
			full = append(full, name+"=")
		}
	}
	cmd := exec.Command(driver, args...)
	cmd.Env = full
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("running release-output verifier: %v\n%s", err, out)
	return -1, string(out)
}

func runVerifyReleaseOutputs(t *testing.T, driver, assets string, attestations bool) (int, string) {
	return runVerifyReleaseOutputsWithDigests(t, driver, assets, strings.Repeat("c", 40), strings.Repeat("c", 40), attestations)
}

func runVerifyReleaseOutputsWithDigests(t *testing.T, driver, assets, sourceDigest, workflowDigest string, attestations bool) (int, string) {
	t.Helper()
	args := releaseOutputArgs(assets, releaseOutputTag, releaseOutputImage, sourceDigest, workflowDigest, attestations)
	// The hostile GitHub host and enterprise credentials must never reach gh:
	// the verifier pins GH_HOST and clears both enterprise aliases per call, and
	// the stub rejects any invocation where that pinning did not happen.
	return runVerifyDriver(t, driver, args, []string{
		"GITHUB_TOKEN=test-token",
		"GH_HOST=attacker.example.com",
		"GH_ENTERPRISE_TOKEN=attacker-enterprise-token",
		"GITHUB_ENTERPRISE_TOKEN=attacker-enterprise-token",
	})
}

// TestVerifyReleaseOutputsRunsFromItsEntryPoint executes the shipped script
// directly rather than through the sourcing driver every other test uses. A
// script whose entry-point guard no longer calls main would exit 0 having
// verified nothing, and one that calls `main` without "$@" would drop the
// verify_args the release workflow passes, so both the no-argument fail-closed
// path and argument forwarding are checked.
func TestVerifyReleaseOutputsRunsFromItsEntryPoint(t *testing.T) {
	t.Parallel()
	script := verifyReleaseOutputsScript(t)
	if code, out := runVerifyDriver(t, script, nil, nil); code == 0 || !strings.Contains(out, "assets directory is required") {
		t.Fatalf("release verifier did not run from its entry point, got %d\n%s", code, out)
	}
	// --help is only reachable if the guard forwarded "$@" to main.
	code, out := runVerifyDriver(t, script, []string{"--help"}, nil)
	if code != 0 || !strings.Contains(out, "Usage: verify-release-outputs.sh") {
		t.Fatalf("release verifier entry point does not forward its arguments, got %d\n%s", code, out)
	}
}

// TestVerifyReleaseOutputsIgnoresHostileBashStartup hands the shipped script a
// BASH_ENV pointing at attacker-controlled startup code. The `#!/bin/bash -p`
// shebang must make Bash ignore it; without privileged mode that file runs
// before the verifier pins PATH and could replace cosign or gh outright.
func TestVerifyReleaseOutputsIgnoresHostileBashStartup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "startup-ran")
	startup := filepath.Join(dir, "startup.sh")
	if err := os.WriteFile(startup, []byte("printf ran >\""+marker+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostile := []string{"BASH_ENV=" + startup, "ENV=" + startup}

	code, out := runVerifyDriver(t, verifyReleaseOutputsScript(t), nil, hostile)
	if code == 0 || !strings.Contains(out, "assets directory is required") {
		t.Fatalf("release verifier did not run under hostile startup state, got %d\n%s", code, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("release verifier executed caller-selected Bash startup code (%v)", err)
	}

	// Negative fixture: the same startup file does run when the privileged
	// shebang is dropped, so the check above is not passing on an inert path.
	content, err := os.ReadFile(verifyReleaseOutputsScript(t))
	if err != nil {
		t.Fatal(err)
	}
	unprivileged := strings.Replace(string(content), "#!/bin/bash -p\n", "#!/bin/bash\n", 1)
	if unprivileged == string(content) {
		t.Fatal("release verifier no longer uses a privileged Bash shebang")
	}
	copyPath := filepath.Join(t.TempDir(), "unprivileged-verify-release-outputs.sh")
	if err := os.WriteFile(copyPath, []byte(unprivileged), 0o755); err != nil {
		t.Fatal(err)
	}
	runVerifyDriver(t, copyPath, nil, hostile)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("dropping the privileged shebang did not run the startup file, so the control is vacuous (%v)", err)
	}
}

// TestVerifyReleaseOutputsIsolatesTokensFromCosign supplies GH_TOKEN, which the
// attestation runs never exercise because they pass GITHUB_TOKEN. The stub
// cosign refuses any invocation that can still see a token, so the run only
// succeeds if run_cosign cleared this alias too.
func TestVerifyReleaseOutputsIsolatesTokensFromCosign(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, cosignLog, _ := releaseOutputStubBin(t, "", "")
	args := releaseOutputArgs(assets, releaseOutputTag, releaseOutputImage, strings.Repeat("c", 40), strings.Repeat("c", 40), false)
	code, out := runVerifyDriver(t, bin, args, []string{"GH_TOKEN=test-token"})
	if code != 0 {
		t.Fatalf("Cosign saw a caller-supplied GH_TOKEN, got %d\n%s", code, out)
	}
	if got := len(logLines(t, cosignLog)); got != 12 {
		t.Fatalf("Cosign calls = %d, want the full signature set to have run", got)
	}
}

// TestVerifyReleaseOutputsAcceptsGHTokenForAttestations covers the documented
// GITHUB_TOKEN-or-GH_TOKEN credential fallback with attestations enabled, which
// every other attestation run misses by supplying GITHUB_TOKEN.
func TestVerifyReleaseOutputsAcceptsGHTokenForAttestations(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, ghLog := releaseOutputStubBin(t, "", "")
	args := releaseOutputArgs(assets, releaseOutputTag, releaseOutputImage, strings.Repeat("c", 40), strings.Repeat("c", 40), true)
	code, out := runVerifyDriver(t, bin, args, []string{
		"GH_TOKEN=test-token",
		"GH_HOST=attacker.example.com",
		"GH_ENTERPRISE_TOKEN=attacker-enterprise-token",
		"GITHUB_ENTERPRISE_TOKEN=attacker-enterprise-token",
	})
	if code != 0 {
		t.Fatalf("release verifier rejected GH_TOKEN as the attestation credential, got %d\n%s", code, out)
	}
	if got := len(logLines(t, ghLog)); got != 10 {
		t.Fatalf("gh attestation calls = %d, want the full attestation set to have run", got)
	}
}

// TestVerifyReleaseOutputsRejectsUnexpectedReleaseFile leaves SHA256SUMS intact
// so only the directory inventory walk can reject the extra file.
func TestVerifyReleaseOutputsRejectsUnexpectedReleaseFile(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	if err := os.WriteFile(filepath.Join(assets, "attacker.bin"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, _, _ := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "unexpected release file: attacker.bin") {
		t.Fatalf("expected unexpected release file rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsDifferentSourceAndWorkflowCommits(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputsWithDigests(
		t,
		bin,
		assets,
		strings.Repeat("b", 40),
		strings.Repeat("c", 40),
		false,
	)
	if code == 0 || !strings.Contains(out, "source and trusted workflow digests must match") {
		t.Fatalf("expected release/workflow digest mismatch rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsUnrelatedImageRepository(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBin(t, "", "")
	args := releaseOutputArgs(assets, releaseOutputTag, "ghcr.io/attacker/workcell", strings.Repeat("c", 40), strings.Repeat("c", 40), false)
	code, out := runVerifyDriver(t, bin, args, nil)
	if code == 0 || !strings.Contains(out, "image repository must match release repository") {
		t.Fatalf("expected unrelated image repository rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsUnreviewedTagClass(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBin(t, "", "")
	args := releaseOutputArgs(assets, "v1.2.3-beta.1", releaseOutputImage, strings.Repeat("c", 40), strings.Repeat("c", 40), false)
	code, out := runVerifyDriver(t, bin, args, nil)
	if code == 0 || !strings.Contains(out, "invalid release tag") {
		t.Fatalf("expected unreviewed tag class rejection, got %d\n%s", code, out)
	}
}

func logLines(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// mutatedVerifierDriver stubs a copy of the verifier with one textual change
// applied, so a negative fixture can show the assertion under test really does
// fail against the regression it claims to catch.
func mutatedVerifierDriver(t *testing.T, source, old, replacement string) string {
	t.Helper()
	mutated := strings.Replace(source, old, replacement, 1)
	if mutated == source {
		t.Fatalf("release verifier no longer contains %q", old)
	}
	path := filepath.Join(t.TempDir(), "mutated-verify-release-outputs.sh")
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	driver, _, _ := releaseOutputStubDriver(t, path, "", "", strings.Repeat("a", 64), strings.Repeat("a", 64))
	return driver
}

// poisonedToolPath returns a directory whose every tool the verifier shells out
// to exits non-zero, so any run that resolves a tool from it cannot complete.
func poisonedToolPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"jq", "sha256sum", "awk", "find", "wc", "cat", "sort", "sed", "grep"} {
		if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/bash\nexit 3\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestVerifyReleaseOutputsPinsToolPath runs the verifier with every plausible
// caller-supplied tool-path variable aimed at a directory of failing tools. The
// run must still succeed, which is only possible if the script resolved its
// tools from its own fixed trusted path and honoured no caller override. The
// assertion is entirely execution-based: no source scan decides any part of it,
// so an override read from a differently named variable is caught by the
// environment below rather than by a name this test would have to know.
func TestVerifyReleaseOutputsPinsToolPath(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(verifyReleaseOutputsScript(t))
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)

	poisoned := poisonedToolPath(t)
	env := []string{"GITHUB_TOKEN=test-token"}
	for _, name := range []string{
		"PATH",
		"TRUSTED_PATH",
		"WORKCELL_TRUSTED_PATH",
		"WORKCELL_RELEASE_VERIFY_TRUSTED_PATH",
		"WORKCELL_RELEASE_VERIFY_PATH",
	} {
		env = append(env, name+"="+poisoned)
	}

	assets := releaseOutputFixture(t)
	args := releaseOutputArgs(assets, releaseOutputTag, releaseOutputImage, strings.Repeat("c", 40), strings.Repeat("c", 40), false)
	bin, _, _ := releaseOutputStubBin(t, "", "")
	if code, out := runVerifyDriver(t, bin, args, env); code != 0 {
		t.Fatalf("release verifier honoured a caller-selected tool path, got %d\n%s", code, out)
	}

	// Negative fixtures: both regression shapes this guards against do fail
	// under exactly the environment above, so the success is not vacuous.
	for _, tc := range []struct {
		name        string
		old         string
		replacement string
	}{
		{
			name:        "inherits the caller PATH",
			old:         `export PATH="${TRUSTED_PATH}"`,
			replacement: `export PATH="${PATH}"`,
		},
		{
			name:        "honours a caller override variable",
			old:         `readonly TRUSTED_PATH="`,
			replacement: `readonly TRUSTED_PATH="${WORKCELL_RELEASE_VERIFY_TRUSTED_PATH:+${WORKCELL_RELEASE_VERIFY_TRUSTED_PATH}:}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driver := mutatedVerifierDriver(t, source, tc.old, tc.replacement)
			if code, out := runVerifyDriver(t, driver, args, env); code == 0 {
				t.Fatalf("a verifier that %s passed under a poisoned tool path, got %d\n%s", tc.name, code, out)
			}
		})
	}
}

// callArgs splits one logged stub call back into its argument vector. The stub
// separates arguments with a unit separator rather than logging "$*", so a path
// containing whitespace stays one argument.
func callArgs(line string) []string {
	return strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f")
}

// flagOccurrences counts every appearance of flag, in both the separate-argument
// and the --flag=value forms that gh and cosign accept. Aliases name alternate
// spellings of the same flag (gh's -R for --repo), which the tools treat
// identically, so an occurrence of any spelling counts.
func flagOccurrences(args []string, flag string, aliases ...string) int {
	seen := 0
	for _, spelling := range append([]string{flag}, aliases...) {
		for _, arg := range args {
			if arg == spelling || strings.HasPrefix(arg, spelling+"=") {
				seen++
			}
		}
	}
	return seen
}

// flagValue returns the argument bound to flag, and only when the flag appears
// exactly once in either accepted form. Both tools take the last occurrence of a
// repeated flag, so a weaker second --cert-identity or --predicate-type would
// otherwise pass an assertion made against the first.
func flagValue(args []string, flag string, aliases ...string) string {
	if flagOccurrences(args, flag, aliases...) != 1 {
		return ""
	}
	for _, spelling := range append([]string{flag}, aliases...) {
		for i, arg := range args {
			if arg == spelling && i+1 < len(args) {
				return args[i+1]
			}
			if value, ok := strings.CutPrefix(arg, spelling+"="); ok {
				return value
			}
		}
	}
	return ""
}

// callSubjects reduces each logged stub call to the subject it verified, plus
// the bundle and predicate type that select what is being proved about it.
// Comparing the whole multiset means a verifier that checks one subject
// repeatedly cannot pass by call count alone.
func callSubjects(t *testing.T, path string) []string {
	t.Helper()
	subjects := make([]string, 0)
	for _, line := range logLines(t, path) {
		args := callArgs(line)
		subject := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "--") {
				break
			}
			subject += arg + " "
		}
		if bundle := flagValue(args, "--bundle"); bundle != "" {
			subject += "bundle=" + bundle + " "
		}
		if predicate := flagValue(args, "--predicate-type"); predicate != "" {
			subject += "predicate=" + predicate + " "
		}
		subjects = append(subjects, strings.TrimSpace(subject))
	}
	slices.Sort(subjects)
	return subjects
}

func TestVerifyReleaseOutputsChecksSignaturesAndAttestations(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, cosignLog, ghLog := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputs(t, bin, assets, true)
	if code != 0 {
		t.Fatalf("expected release output verification success, got %d\n%s", code, out)
	}

	imageRef := releaseOutputImage + "@sha256:" + strings.Repeat("a", 64)
	wantCosign := make([]string, 0, 12)
	for _, asset := range releaseOutputAssets() {
		path := filepath.Join(assets, asset)
		wantCosign = append(wantCosign, "verify-blob "+path+" bundle="+path+".sigstore.json")
	}
	wantCosign = append(wantCosign,
		"verify "+imageRef,
		"verify "+releaseOutputImage+":"+releaseOutputTag,
		"verify "+releaseOutputImage+":sha-"+strings.Repeat("c", 40),
	)
	slices.Sort(wantCosign)
	if got := callSubjects(t, cosignLog); !slices.Equal(got, wantCosign) {
		t.Fatalf("Cosign subjects mismatch\ngot:  %q\nwant: %q", got, wantCosign)
	}

	wantGH := []string{
		"attestation verify oci://" + imageRef + " predicate=" + slsaPredicate,
		"attestation verify oci://" + imageRef + " predicate=" + spdxPredicate,
		"attestation verify " + filepath.Join(assets, releaseOutputBundle) + " predicate=" + spdxPredicate,
	}
	for _, asset := range releaseOutputAssets() {
		// The two SPDX documents are release data but are not themselves
		// attested subjects; the image and source bundle carry those SBOMs.
		if strings.HasSuffix(asset, ".spdx.json") {
			continue
		}
		wantGH = append(wantGH, "attestation verify "+filepath.Join(assets, asset)+" predicate="+slsaPredicate)
	}
	slices.Sort(wantGH)
	if got := callSubjects(t, ghLog); !slices.Equal(got, wantGH) {
		t.Fatalf("gh attestation subjects mismatch\ngot:  %q\nwant: %q", got, wantGH)
	}

	for _, line := range logLines(t, ghLog) {
		args := callArgs(line)
		// Exactly one occurrence, in the bare form: gh also accepts
		// --deny-self-hosted-runners=false and uses the last occurrence, so
		// neither an appended equals form nor a repeat may be tolerated.
		if flagOccurrences(args, "--deny-self-hosted-runners") != 1 ||
			!slices.Contains(args, "--deny-self-hosted-runners") {
			t.Fatalf("gh attestation call lacks an unconditional self-hosted runner denial: %s", line)
		}
		if got := flagValue(args, "--cert-identity"); got != releaseOutputIdentity {
			t.Fatalf("gh attestation cert identity = %q, want %q: %s", got, releaseOutputIdentity, line)
		}
		if got := flagValue(args, "--cert-oidc-issuer"); got != releaseOutputIssuer {
			t.Fatalf("gh attestation OIDC issuer = %q, want %q: %s", got, releaseOutputIssuer, line)
		}
		if got := flagValue(args, "--source-digest"); got != strings.Repeat("c", 40) {
			t.Fatalf("gh attestation call lacks source digest binding: %s", line)
		}
		if got := flagValue(args, "--signer-digest"); got != strings.Repeat("c", 40) {
			t.Fatalf("gh attestation call lacks signer digest binding: %s", line)
		}
		if got := flagValue(args, "--source-ref"); got != "refs/heads/main" {
			t.Fatalf("gh attestation call lacks trusted main source ref: %s", line)
		}
		// gh also accepts -R as an alias, and the last occurrence wins: a
		// later -R would silently redirect the attestation lookup, so any
		// second spelling must fail the single-occurrence rule.
		if got := flagValue(args, "--repo", "-R"); got != "omkhar/workcell" {
			t.Fatalf("gh attestation call lacks the release repository: %s", line)
		}
	}
	for _, line := range logLines(t, cosignLog) {
		args := callArgs(line)
		if got := flagValue(args, "--certificate-github-workflow-sha"); got != strings.Repeat("c", 40) {
			t.Fatalf("Cosign call lacks source digest binding: %s", line)
		}
		if got := flagValue(args, "--certificate-identity"); got != releaseOutputIdentity {
			t.Fatalf("Cosign certificate identity = %q, want %q: %s", got, releaseOutputIdentity, line)
		}
		if got := flagValue(args, "--certificate-oidc-issuer"); got != releaseOutputIssuer {
			t.Fatalf("Cosign OIDC issuer = %q, want %q: %s", got, releaseOutputIssuer, line)
		}
	}
}

func TestVerifyReleaseOutputsRejectsMissingSignature(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	if err := os.Remove(filepath.Join(assets, "workcell.rb.sigstore.json")); err != nil {
		t.Fatal(err)
	}
	bin, _, _ := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "required release file is missing") {
		t.Fatalf("expected missing signature rejection, got %d\n%s", code, out)
	}
}

// TestVerifyReleaseOutputsRejectsCosignFailure fails one Cosign call at a time
// so each rejection is attributable to the call under test rather than to an
// earlier check that would reject first.
func TestVerifyReleaseOutputsRejectsCosignFailure(t *testing.T) {
	t.Parallel()
	imageRef := releaseOutputImage + "@sha256:" + strings.Repeat("a", 64)
	commitTag := releaseOutputImage + ":sha-" + strings.Repeat("c", 40)
	for _, tc := range []struct {
		name     string
		failGlob string
		want     string
	}{
		{
			name:     "every call",
			failGlob: "*",
			want:     "Cosign verification failed for " + releaseOutputBundle,
		},
		{
			name:     "one blob signature",
			failGlob: "verify-blob *workcell.rb --bundle *",
			want:     "Cosign verification failed for workcell.rb",
		},
		{
			name:     "image digest",
			failGlob: "verify " + imageRef + " *",
			want:     "Cosign image verification failed for " + imageRef,
		},
		{
			name:     "release tag",
			failGlob: "verify " + releaseOutputImage + ":" + releaseOutputTag + " *",
			want:     "Cosign image verification failed for " + releaseOutputImage + ":" + releaseOutputTag,
		},
		{
			name:     "commit tag",
			failGlob: "verify " + commitTag + " *",
			want:     "Cosign image verification failed for " + commitTag,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assets := releaseOutputFixture(t)
			bin, _, _ := releaseOutputStubBin(t, tc.failGlob, "")
			code, out := runVerifyReleaseOutputs(t, bin, assets, false)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("expected Cosign rejection %q, got %d\n%s", tc.want, code, out)
			}
		})
	}
}

func TestVerifyReleaseOutputsRejectsMovedNamedImageTag(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubDriver(t, verifyReleaseOutputsScript(t), "", "", strings.Repeat("b", 64), strings.Repeat("b", 64))
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "named image tag") || !strings.Contains(out, "does not bind") {
		t.Fatalf("expected moved named-image tag rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsMovedCommitImageTag(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubDriver(t, verifyReleaseOutputsScript(t), "", "", strings.Repeat("a", 64), strings.Repeat("b", 64))
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "ghcr.io/omkhar/workcell:sha-"+strings.Repeat("c", 40)) || !strings.Contains(out, "does not bind") {
		t.Fatalf("expected moved commit-image tag rejection, got %d\n%s", code, out)
	}
}

// TestVerifyReleaseOutputsRejectsAttestationFailure fails one gh attestation
// call at a time. The verifier makes ten, and only the first is reachable by a
// blanket failure, so the later OCI SBOM, per-asset provenance and source-bundle
// SBOM calls each need their own case to prove their result is not swallowed.
// TestVerifyReleaseOutputsRejectsEmptyImageVerification covers a Cosign call
// that succeeds but returns no verification results. A digest predicate built
// only from `all` is vacuously true on an empty array, so the verifier must
// also require at least one result before trusting a named tag.
func TestVerifyReleaseOutputsRejectsEmptyImageVerification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                           string
		releaseTagDigest, commitDigest string
		want                           string
	}{
		{
			name:         "release tag",
			commitDigest: strings.Repeat("a", 64),
			want:         "named image tag " + releaseOutputImage + ":" + releaseOutputTag + " does not bind",
		},
		{
			name:             "commit tag",
			releaseTagDigest: strings.Repeat("a", 64),
			want:             "named image tag " + releaseOutputImage + ":sha-" + strings.Repeat("c", 40) + " does not bind",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assets := releaseOutputFixture(t)
			bin, _, _ := releaseOutputStubDriver(t, verifyReleaseOutputsScript(t), "", "", tc.releaseTagDigest, tc.commitDigest)
			code, out := runVerifyReleaseOutputs(t, bin, assets, false)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("expected empty verification rejection %q, got %d\n%s", tc.want, code, out)
			}
		})
	}
}

func TestVerifyReleaseOutputsRejectsAttestationFailure(t *testing.T) {
	t.Parallel()
	imageRef := releaseOutputImage + "@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name     string
		failGlob string
		want     string
	}{
		{
			name:     "every call",
			failGlob: "*",
			want:     "GitHub attestation verification failed for oci://" + imageRef,
		},
		{
			name:     "image SBOM",
			failGlob: "attestation verify oci://* --predicate-type " + spdxPredicate + " *",
			want:     "GitHub attestation verification failed for oci://" + imageRef,
		},
		{
			name:     "one asset provenance",
			failGlob: "attestation verify *SHA256SUMS --repo *",
			want:     "GitHub attestation verification failed for ",
		},
		{
			name:     "source bundle SBOM",
			failGlob: "attestation verify *" + releaseOutputBundle + " --repo * --predicate-type " + spdxPredicate + " *",
			want:     "GitHub attestation verification failed for ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assets := releaseOutputFixture(t)
			bin, _, ghLog := releaseOutputStubBin(t, "", tc.failGlob)
			code, out := runVerifyReleaseOutputs(t, bin, assets, true)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("expected GitHub attestation rejection %q, got %d\n%s", tc.want, code, out)
			}
			// Exactly one call may fail, so the run must stop on the call the
			// glob selected rather than on an earlier one.
			calls := logLines(t, ghLog)
			last := calls[len(calls)-1]
			if tc.failGlob != "*" && len(calls) < 2 {
				t.Fatalf("verifier stopped before reaching the selected call: %q", calls)
			}
			if !strings.Contains(out, "failed for "+callArgs(last)[2]) {
				t.Fatalf("rejection does not name the failing subject %q\n%s", last, out)
			}
		})
	}
}

func TestVerifyReleaseOutputsRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	if err := os.WriteFile(filepath.Join(assets, "workcell.rb"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, _, _ := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "SHA256SUMS digest mismatch") {
		t.Fatalf("expected checksum mismatch rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsUnexpectedChecksumEntry(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	sumsPath := filepath.Join(assets, "SHA256SUMS")
	file, err := os.OpenFile(sumsPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(file, "%s  unexpected.bin\n", strings.Repeat("c", 64)); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	driver, _, _ := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputs(t, driver, assets, false)
	if code == 0 || !strings.Contains(out, "unexpected asset") {
		t.Fatalf("expected checksum inventory rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsSymlinkBundle(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bundle := filepath.Join(assets, "workcell-v1.2.3.tar.gz")
	if err := os.Remove(bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", bundle); err != nil {
		t.Fatal(err)
	}
	bin, _, _ := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "unsafe") {
		t.Fatalf("expected symlink rejection, got %d\n%s", code, out)
	}
}

// rewriteAsset replaces one release asset and its SHA256SUMS entry so the
// checksum inventory still matches the mutated content. That keeps a negative
// control aimed at the check under test instead of tripping the checksum gate.
func rewriteAsset(t *testing.T, assets, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(assets, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	sumsPath := filepath.Join(assets, "SHA256SUMS")
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatal(err)
	}
	sumLines := strings.Split(string(sums), "\n")
	replaced := false
	for i, line := range sumLines {
		if strings.HasSuffix(line, "  "+name) {
			sumLines[i] = hex.EncodeToString(digest[:]) + "  " + name
			replaced = true
		}
	}
	if !replaced {
		t.Fatalf("SHA256SUMS has no entry for %s", name)
	}
	if err := os.WriteFile(sumsPath, []byte(strings.Join(sumLines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyReleaseOutputsRejectsUnboundBuildInputs points the build input
// manifest at another commit while keeping SHA256SUMS valid, so only the
// verifier's .build.ref binding can reject it.
func TestVerifyReleaseOutputsRejectsUnboundBuildInputs(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	rewriteAsset(t, assets, "workcell-build-inputs.json", []byte(`{"build":{"ref":"`+strings.Repeat("d", 40)+`"}}`))
	bin, _, _ := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "build input manifest does not bind to release commit") {
		t.Fatalf("expected build input binding rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsMalformedImageDigestFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "multiple lines",
			content: "ghcr.io/omkhar/workcell@sha256:" + strings.Repeat("A", 64) + "\nextra\n",
			want:    "exactly one line",
		},
		{
			name:    "uppercase digest",
			content: "ghcr.io/omkhar/workcell@sha256:" + strings.Repeat("A", 64) + "\n",
			want:    "lowercase SHA-256 digest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assets := releaseOutputFixture(t)
			rewriteAsset(t, assets, "workcell-image.digest", []byte(tc.content))
			bin, _, _ := releaseOutputStubBin(t, "", "")
			code, out := runVerifyReleaseOutputs(t, bin, assets, false)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("expected image digest rejection %q, got %d\n%s", tc.want, code, out)
			}
		})
	}
}

func TestFlagValueRejectsShortAliasOverride(t *testing.T) {
	args := []string{"sha256:aa", "--repo", "omkhar/workcell", "-R", "attacker/repo"}
	if got := flagValue(args, "--repo", "-R"); got != "" {
		t.Fatalf("flagValue tolerated a short-alias repository override, got %q", got)
	}
	if got := flagValue([]string{"-R", "omkhar/workcell"}, "--repo", "-R"); got != "omkhar/workcell" {
		t.Fatalf("flagValue missed the short alias alone, got %q", got)
	}
}
