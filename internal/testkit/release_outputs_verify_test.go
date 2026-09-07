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
// by an earlier check rejecting first.
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
cosign() {
  [[ -z "${GITHUB_TOKEN+x}" && -z "${GH_TOKEN+x}" && -z "${ATTESTATION_TOKEN+x}" ]] || return 96
  printf '%%s\n' "$*" >>%q
  if [[ "${1:-}" == "verify" ]]; then
    tag_digest=%q
    if [[ "$*" == *":sha-%s"* ]]; then
      tag_digest=%q
    fi
    printf '[{"critical":{"image":{"docker-manifest-digest":"sha256:%%s"}}}]\n' "${tag_digest}"
  fi
  [[ -n "${cosign_fail_glob}" && "$*" == ${cosign_fail_glob} ]] && return 1
  return 0
}
gh() {
  [[ -z "${GITHUB_TOKEN+x}" && -z "${ATTESTATION_TOKEN+x}" ]] || return 98
  [[ "${GH_TOKEN:-}" == "test-token" ]] || return 97
  [[ "${GH_HOST:-}" == "github.com" ]] || return 95
  [[ -z "${GH_ENTERPRISE_TOKEN:-}" && -z "${GITHUB_ENTERPRISE_TOKEN:-}" ]] || return 94
  printf '%%s\n' "$*" >>%q
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
	cmd := exec.Command(driver, args...)
	cmd.Env = append([]string{"BASH_ENV=", "ENV="}, env...)
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
// directly rather than through the sourcing driver every other test uses. With
// no arguments it must run main and fail closed; a script whose entry-point
// guard no longer calls main would exit 0 having verified nothing, which is how
// the release workflow invokes it.
func TestVerifyReleaseOutputsRunsFromItsEntryPoint(t *testing.T) {
	t.Parallel()
	code, out := runVerifyDriver(t, verifyReleaseOutputsScript(t), nil, nil)
	if code == 0 || !strings.Contains(out, "assets directory is required") {
		t.Fatalf("release verifier did not run from its entry point, got %d\n%s", code, out)
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

// flagValue returns the argument that follows flag in a logged stub call.
func flagValue(line, flag string) string {
	fields := strings.Fields(line)
	for i, field := range fields {
		if field == flag && i+1 < len(fields) {
			return fields[i+1]
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
		subject := ""
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "--") {
				break
			}
			subject += field + " "
		}
		if bundle := flagValue(line, "--bundle"); bundle != "" {
			subject += "bundle=" + bundle + " "
		}
		if predicate := flagValue(line, "--predicate-type"); predicate != "" {
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
		// Must be the exact standalone flag: gh also accepts
		// --deny-self-hosted-runners=false, which a substring match would pass.
		if !slices.Contains(strings.Fields(line), "--deny-self-hosted-runners") {
			t.Fatalf("gh attestation call lacks self-hosted runner denial: %s", line)
		}
		if got := flagValue(line, "--cert-identity"); got != releaseOutputIdentity {
			t.Fatalf("gh attestation cert identity = %q, want %q: %s", got, releaseOutputIdentity, line)
		}
		if got := flagValue(line, "--cert-oidc-issuer"); got != releaseOutputIssuer {
			t.Fatalf("gh attestation OIDC issuer = %q, want %q: %s", got, releaseOutputIssuer, line)
		}
		if got := flagValue(line, "--source-digest"); got != strings.Repeat("c", 40) {
			t.Fatalf("gh attestation call lacks source digest binding: %s", line)
		}
		if got := flagValue(line, "--signer-digest"); got != strings.Repeat("c", 40) {
			t.Fatalf("gh attestation call lacks signer digest binding: %s", line)
		}
		if got := flagValue(line, "--source-ref"); got != "refs/heads/main" {
			t.Fatalf("gh attestation call lacks trusted main source ref: %s", line)
		}
		if got := flagValue(line, "--repo"); got != "omkhar/workcell" {
			t.Fatalf("gh attestation call lacks the release repository: %s", line)
		}
	}
	for _, line := range logLines(t, cosignLog) {
		if got := flagValue(line, "--certificate-github-workflow-sha"); got != strings.Repeat("c", 40) {
			t.Fatalf("Cosign call lacks source digest binding: %s", line)
		}
		if got := flagValue(line, "--certificate-identity"); got != releaseOutputIdentity {
			t.Fatalf("Cosign certificate identity = %q, want %q: %s", got, releaseOutputIdentity, line)
		}
		if got := flagValue(line, "--certificate-oidc-issuer"); got != releaseOutputIssuer {
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
			if !strings.Contains(out, "failed for "+strings.Fields(last)[2]) {
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
