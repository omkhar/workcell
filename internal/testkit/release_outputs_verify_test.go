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
// replaces cosign and gh with logging stubs. cosignFailGlob selects which
// cosign invocations fail: "" fails none, "*" fails every one, and a narrower
// glob fails a single call so a negative control cannot be satisfied by an
// earlier check rejecting first.
func releaseOutputStubBin(t *testing.T, cosignFailGlob string, ghCode int) (string, string, string) {
	return releaseOutputStubBinWithNamedTagDigests(t, cosignFailGlob, ghCode, strings.Repeat("a", 64), strings.Repeat("a", 64))
}

func releaseOutputStubBinWithNamedTagDigests(t *testing.T, cosignFailGlob string, ghCode int, releaseTagDigest, commitTagDigest string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	cosignLog := filepath.Join(dir, "cosign.log")
	ghLog := filepath.Join(dir, "gh.log")
	driver := filepath.Join(dir, "verify-release-outputs-test-driver.sh")
	script := fmt.Sprintf(`#!/bin/bash
source %q
cosign_fail_glob=%q
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
  printf '%%s\n' "$*" >>%q
  return %d
}
main "$@"
`, verifyReleaseOutputsScript(t), cosignFailGlob, cosignLog, releaseTagDigest, strings.Repeat("c", 40), commitTagDigest, ghLog, ghCode)
	if err := os.WriteFile(driver, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return driver, cosignLog, ghLog
}

func runVerifyReleaseOutputs(t *testing.T, driver, assets string, attestations bool) (int, string) {
	return runVerifyReleaseOutputsWithDigests(t, driver, assets, strings.Repeat("c", 40), strings.Repeat("c", 40), attestations)
}

func runVerifyReleaseOutputsWithDigests(t *testing.T, driver, assets, sourceDigest, workflowDigest string, attestations bool) (int, string) {
	t.Helper()
	args := []string{
		"--assets-dir", assets,
		"--repo", "omkhar/workcell",
		"--tag", "v1.2.3",
		"--image-repository", "ghcr.io/omkhar/workcell",
		"--source-digest", sourceDigest,
		"--workflow-digest", workflowDigest,
	}
	if attestations {
		args = append(args, "--attestations")
	}
	cmd := exec.Command(driver, args...)
	cmd.Env = []string{
		"GITHUB_TOKEN=test-token",
		"BASH_ENV=",
		"ENV=",
	}
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

func TestVerifyReleaseOutputsRejectsDifferentSourceAndWorkflowCommits(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBin(t, "", 0)
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
	bin, _, _ := releaseOutputStubBin(t, "", 0)
	args := []string{
		"--assets-dir", assets,
		"--repo", "omkhar/workcell",
		"--tag", "v1.2.3",
		"--image-repository", "ghcr.io/attacker/workcell",
		"--source-digest", strings.Repeat("c", 40),
		"--workflow-digest", strings.Repeat("c", 40),
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"BASH_ENV=", "ENV="}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "image repository must match release repository") {
		t.Fatalf("expected unrelated image repository rejection, got %v\n%s", err, out)
	}
}

func TestVerifyReleaseOutputsRejectsUnreviewedTagClass(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBin(t, "", 0)
	args := []string{
		"--assets-dir", assets,
		"--repo", "omkhar/workcell",
		"--tag", "v1.2.3-beta.1",
		"--image-repository", "ghcr.io/omkhar/workcell",
		"--source-digest", strings.Repeat("c", 40),
		"--workflow-digest", strings.Repeat("c", 40),
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"BASH_ENV=", "ENV="}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "invalid release tag") {
		t.Fatalf("expected unreviewed tag class rejection, got %v\n%s", err, out)
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

func TestVerifyReleaseOutputsPinsToolPath(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(verifyReleaseOutputsScript(t))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, "WORKCELL_RELEASE_VERIFY_TRUSTED_PATH") {
		t.Fatal("release verifier accepts a caller-selected tool path")
	}
	if !strings.Contains(text, `export PATH="${TRUSTED_PATH}"`) {
		t.Fatal("release verifier does not export its fixed trusted tool path")
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
	bin, cosignLog, ghLog := releaseOutputStubBin(t, "", 0)
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
		if !strings.Contains(line, "--deny-self-hosted-runners") {
			t.Fatalf("gh attestation call lacks self-hosted runner denial: %s", line)
		}
		if !strings.Contains(line, "--source-digest "+strings.Repeat("c", 40)) {
			t.Fatalf("gh attestation call lacks source digest binding: %s", line)
		}
		if !strings.Contains(line, "--signer-digest "+strings.Repeat("c", 40)) {
			t.Fatalf("gh attestation call lacks signer digest binding: %s", line)
		}
		if !strings.Contains(line, "--source-ref refs/heads/main") {
			t.Fatalf("gh attestation call lacks trusted main source ref: %s", line)
		}
	}
	for _, line := range logLines(t, cosignLog) {
		if !strings.Contains(line, "--certificate-github-workflow-sha "+strings.Repeat("c", 40)) {
			t.Fatalf("Cosign call lacks source digest binding: %s", line)
		}
		if !strings.Contains(line, "release.yml@refs/heads/main") {
			t.Fatalf("Cosign call lacks trusted main workflow identity: %s", line)
		}
	}
}

func TestVerifyReleaseOutputsRejectsMissingSignature(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	if err := os.Remove(filepath.Join(assets, "workcell.rb.sigstore.json")); err != nil {
		t.Fatal(err)
	}
	bin, _, _ := releaseOutputStubBin(t, "", 0)
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
			bin, _, _ := releaseOutputStubBin(t, tc.failGlob, 0)
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
	bin, _, _ := releaseOutputStubBinWithNamedTagDigests(t, "", 0, strings.Repeat("b", 64), strings.Repeat("b", 64))
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "named image tag") || !strings.Contains(out, "does not bind") {
		t.Fatalf("expected moved named-image tag rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsMovedCommitImageTag(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBinWithNamedTagDigests(t, "", 0, strings.Repeat("a", 64), strings.Repeat("b", 64))
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "ghcr.io/omkhar/workcell:sha-"+strings.Repeat("c", 40)) || !strings.Contains(out, "does not bind") {
		t.Fatalf("expected moved commit-image tag rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsAttestationFailure(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBin(t, "", 1)
	code, out := runVerifyReleaseOutputs(t, bin, assets, true)
	if code == 0 || !strings.Contains(out, "GitHub attestation verification failed") {
		t.Fatalf("expected GitHub attestation rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	if err := os.WriteFile(filepath.Join(assets, "workcell.rb"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, _, _ := releaseOutputStubBin(t, "", 0)
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
	driver, _, _ := releaseOutputStubBin(t, "", 0)
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
	bin, _, _ := releaseOutputStubBin(t, "", 0)
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "unsafe") {
		t.Fatalf("expected symlink rejection, got %d\n%s", code, out)
	}
}

// rewriteImageDigestAsset replaces the image digest file and its SHA256SUMS
// entry so the checksum inventory still matches the mutated content.
func rewriteImageDigestAsset(t *testing.T, assets string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(assets, "workcell-image.digest"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	sumsPath := filepath.Join(assets, "SHA256SUMS")
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		t.Fatal(err)
	}
	sumLines := strings.Split(string(sums), "\n")
	for i, line := range sumLines {
		if strings.HasSuffix(line, "  workcell-image.digest") {
			sumLines[i] = hex.EncodeToString(digest[:]) + "  workcell-image.digest"
		}
	}
	if err := os.WriteFile(sumsPath, []byte(strings.Join(sumLines, "\n")), 0o644); err != nil {
		t.Fatal(err)
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
			rewriteImageDigestAsset(t, assets, []byte(tc.content))
			bin, _, _ := releaseOutputStubBin(t, "", 0)
			code, out := runVerifyReleaseOutputs(t, bin, assets, false)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("expected image digest rejection %q, got %d\n%s", tc.want, code, out)
			}
		})
	}
}
