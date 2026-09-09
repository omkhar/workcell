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

// releaseIdentity holds the per-release values the verifier derives its subjects, image
// tags and certificate identity from, so a verifier that hard-codes one fails the alternate.
type releaseIdentity struct {
	repo        string
	tag         string
	imageDigest string
	commit      string
	token       string
}

func defaultRelease() releaseIdentity {
	return releaseIdentity{
		repo:        "omkhar/workcell",
		tag:         releaseOutputTag,
		imageDigest: strings.Repeat("a", 64),
		commit:      strings.Repeat("c", 40),
		token:       "test-token",
	}
}

func (r releaseIdentity) image() string    { return "ghcr.io/" + r.repo }
func (r releaseIdentity) bundle() string   { return "workcell-" + r.tag + ".tar.gz" }
func (r releaseIdentity) imageRef() string { return r.image() + "@sha256:" + r.imageDigest }

func (r releaseIdentity) identity() string {
	return "https://github.com/" + r.repo + "/.github/workflows/release.yml@refs/heads/main"
}

// assets mirrors DATA_ASSETS in the verifier, in the order it walks them.
func (r releaseIdentity) assets() []string {
	return []string{
		r.bundle(),
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

// checksumAssets mirrors CHECKSUM_ASSETS: every data asset except SHA256SUMS itself.
func (r releaseIdentity) checksumAssets() []string {
	assets := make([]string, 0, len(r.assets())-1)
	for _, asset := range r.assets() {
		if asset != "SHA256SUMS" {
			assets = append(assets, asset)
		}
	}
	return assets
}

// requireSHA256Sum skips when the verifier's trusted path holds no sha256sum, so a host
// that ships only shasum fails on the missing tool rather than on a verifier defect. The
// trusted path is read from the script so this gate cannot drift from the one it uses.
func requireSHA256Sum(t *testing.T) {
	t.Helper()
	content, err := os.ReadFile(verifyReleaseOutputsScript(t))
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(content), `readonly TRUSTED_PATH="`)
	trusted, _, closed := strings.Cut(rest, `"`)
	if !found || !closed {
		t.Fatal("release verifier no longer declares a trusted path")
	}
	for _, dir := range strings.Split(trusted, ":") {
		if info, err := os.Stat(filepath.Join(dir, "sha256sum")); err == nil && info.Mode().IsRegular() {
			return
		}
	}
	t.Skip("sha256sum is not on the release verifier's trusted path")
}

func releaseOutputFixture(t *testing.T) string {
	t.Helper()
	return releaseOutputFixtureFor(t, defaultRelease())
}

func releaseOutputFixtureFor(t *testing.T, release releaseIdentity) string {
	t.Helper()
	requireSHA256Sum(t)
	dir := t.TempDir()
	assets := release.assets()
	if err := os.WriteFile(filepath.Join(dir, "workcell-image.digest"), []byte(release.imageRef()+"\n"), 0o644); err != nil {
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
				content = []byte(`{"build":{"ref":"` + release.commit + `"}}`)
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

// stubOptions selects what the driver's cosign and gh stubs accept and return. Each fail
// glob selects which invocations fail: "" none, "*" every one, a narrower glob a single
// call, so a negative control cannot be satisfied by an earlier check rejecting first. An
// empty tag digest returns an empty result array (the case a vacuously-true `all` predicate
// accepts); the object fields return a well-shaped but out-of-contract response instead.
type stubOptions struct {
	scriptPath       string
	cosignFailGlob   string
	ghFailGlob       string
	release          releaseIdentity
	releaseTagDigest string
	commitTagDigest  string
	releaseTagObject bool
	commitTagObject  bool
}

func defaultStubOptions(t *testing.T) stubOptions {
	t.Helper()
	release := defaultRelease()
	return stubOptions{
		scriptPath:       verifyReleaseOutputsScript(t),
		release:          release,
		releaseTagDigest: release.imageDigest,
		commitTagDigest:  release.imageDigest,
	}
}

// releaseOutputStubBin builds a driver that sources the real verifier and replaces cosign and gh with logging stubs.
func releaseOutputStubBin(t *testing.T, cosignFailGlob, ghFailGlob string) (string, string, string) {
	t.Helper()
	opts := defaultStubOptions(t)
	opts.cosignFailGlob = cosignFailGlob
	opts.ghFailGlob = ghFailGlob
	return releaseOutputStubDriver(t, opts)
}

// shQuote renders s as a single-quoted Bash literal; Go's %q leaves a $ or backtick in a
// path to substitute under Bash double-quote rules while the driver starts.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func releaseOutputStubDriver(t *testing.T, opts stubOptions) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	cosignLog := filepath.Join(dir, "cosign.log")
	ghLog := filepath.Join(dir, "gh.log")
	script := fmt.Sprintf(`#!/bin/bash
source %[1]s
cosign_fail_glob=%[2]s
gh_fail_glob=%[3]s
log_call() {
  local log="$1"
  shift
  { printf '%%s\x1f' "$@"; printf '\n'; } >>"${log}"
}
# Both stubs shadow the real executable before Bash consults PATH, so a PATH widened around
# one call site would never surface as a failed lookup. Assert the effective PATH on every
# call instead, and reject the help mode that makes both real tools exit 0 proving nothing.
reject_unsafe_call() {
  [[ "${PATH}" == "${TRUSTED_PATH}" ]] || return 91
  local arg=""
  for arg in "$@"; do
    [[ "${arg}" != "--help" && "${arg}" != "-h" ]] || return 90
  done
  return 0
}
cosign() {
  [[ -z "${GITHUB_TOKEN+x}" && -z "${GH_TOKEN+x}" && -z "${ATTESTATION_TOKEN+x}" ]] || return 96
  reject_unsafe_call "$@" || return $?
  for arg in "$@"; do
    # Cosign 3.1.3 weakens verification through these three options: --insecure-ignore-tlog
    # drops the Rekor check, --insecure-ignore-sct the certificate timestamp check, and
    # --allow-insecure-registry registry TLS. Match each as a whole token in both accepted
    # forms; a substring test would also reject a subject whose path contains the word.
    case "${arg}" in
      --insecure-ignore-tlog | --insecure-ignore-tlog=* | \
        --insecure-ignore-sct | --insecure-ignore-sct=* | \
        --allow-insecure-registry | --allow-insecure-registry=*)
        return 89
        ;;
    esac
  done
  log_call %[4]s "$@"
  if [[ "${1:-}" == "verify" ]]; then
    tag_digest=%[5]s
    tag_object=%[6]t
    if [[ "$*" == *":sha-%[7]s"* ]]; then
      tag_digest=%[8]s
      tag_object=%[9]t
    fi
    if [[ "${tag_object}" == true ]]; then
      printf '{"only":{"critical":{"image":{"docker-manifest-digest":"sha256:%%s"}}}}\n' "${tag_digest}"
    elif [[ -z "${tag_digest}" ]]; then
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
  [[ "${GH_TOKEN:-}" == %[10]s ]] || return 97
  [[ "${GH_HOST:-}" == "github.com" ]] || return 95
  [[ -z "${GH_ENTERPRISE_TOKEN:-}" && -z "${GITHUB_ENTERPRISE_TOKEN:-}" ]] || return 94
  reject_unsafe_call "$@" || return $?
  for arg in "$@"; do
    [[ "${arg}" != "--hostname" && "${arg}" != --hostname=* ]] || return 93
    # --custom-trusted-root replaces the Sigstore trust root, so a shipped call must not carry it.
    [[ "${arg}" != "--custom-trusted-root" && "${arg}" != --custom-trusted-root=* ]] || return 92
  done
  log_call %[11]s "$@"
  [[ -n "${gh_fail_glob}" && "$*" == ${gh_fail_glob} ]] && return 1
  return 0
}
main "$@"
`,
		shQuote(opts.scriptPath),
		shQuote(opts.cosignFailGlob),
		shQuote(opts.ghFailGlob),
		shQuote(cosignLog),
		shQuote(opts.releaseTagDigest),
		opts.releaseTagObject,
		opts.release.commit,
		shQuote(opts.commitTagDigest),
		opts.commitTagObject,
		shQuote(opts.release.token),
		shQuote(ghLog),
	)
	driver := writeExecutable(t, dir, "verify-release-outputs-test-driver.sh", script)
	return driver, cosignLog, ghLog
}

func releaseOutputArgs(assets, repository, tag, imageRepository, sourceDigest, workflowDigest string, attestations bool) []string {
	args := []string{
		"--assets-dir", assets,
		"--repo", repository,
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
	// Bash startup files are cleared unless the caller sets them deliberately, so a test can hand the verifier hostile startup state.
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
	args := releaseOutputArgs(assets, "omkhar/workcell", releaseOutputTag, releaseOutputImage, sourceDigest, workflowDigest, attestations)
	// The hostile host and enterprise credentials must never reach gh: the verifier pins
	// GH_HOST and clears both aliases per call, and the stub rejects any call that lacks it.
	return runVerifyDriver(t, driver, args, []string{
		"GITHUB_TOKEN=test-token",
		"GH_HOST=attacker.example.com",
		"GH_ENTERPRISE_TOKEN=attacker-enterprise-token",
		"GITHUB_ENTERPRISE_TOKEN=attacker-enterprise-token",
	})
}

// TestVerifyReleaseOutputsRunsFromItsEntryPoint executes the shipped script directly. A guard
// that no longer calls main would exit 0 having verified nothing, and one calling main without
// "$@" would drop the workflow's verify_args, so both fail-closed and forwarding are checked.
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

// TestVerifyReleaseOutputsIgnoresHostileBashStartup hands the script a BASH_ENV pointing at
// attacker startup code. The `#!/bin/bash -p` shebang must make Bash ignore it; unprivileged,
// that file runs before the verifier pins PATH and could replace cosign or gh outright.
func TestVerifyReleaseOutputsIgnoresHostileBashStartup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "startup-ran")
	startup := filepath.Join(dir, "startup.sh")
	if err := os.WriteFile(startup, []byte("printf ran >"+shQuote(marker)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Bash expands the VALUE of BASH_ENV before using it as a path, so the expansion-active
	// characters a hostile TMPDIR can contain must be escaped for the literal file to run.
	escaped := strings.NewReplacer(`\`, `\\`, "$", `\$`, "`", "\\`").Replace(startup)
	hostile := []string{"BASH_ENV=" + escaped, "ENV=" + escaped}

	code, out := runVerifyDriver(t, verifyReleaseOutputsScript(t), nil, hostile)
	if code == 0 || !strings.Contains(out, "assets directory is required") {
		t.Fatalf("release verifier did not run under hostile startup state, got %d\n%s", code, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("release verifier executed caller-selected Bash startup code (%v)", err)
	}

	// Negative fixture: the same startup file does run once the privileged shebang is dropped.
	content, err := os.ReadFile(verifyReleaseOutputsScript(t))
	if err != nil {
		t.Fatal(err)
	}
	unprivileged := strings.Replace(string(content), "#!/bin/bash -p\n", "#!/bin/bash\n", 1)
	if unprivileged == string(content) {
		t.Fatal("release verifier no longer uses a privileged Bash shebang")
	}
	copyPath := writeExecutable(t, t.TempDir(), "unprivileged-verify-release-outputs.sh", unprivileged)
	runVerifyDriver(t, copyPath, nil, hostile)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("dropping the privileged shebang did not run the startup file, so the control is vacuous (%v)", err)
	}
}

// TestVerifyReleaseOutputsIsolatesTokensFromCosign supplies GH_TOKEN, which the attestation
// runs never exercise. The stub cosign refuses any invocation that can still see a token, so
// the run only succeeds if run_cosign cleared this alias too.
func TestVerifyReleaseOutputsIsolatesTokensFromCosign(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, cosignLog, _ := releaseOutputStubBin(t, "", "")
	args := releaseOutputArgs(assets, "omkhar/workcell", releaseOutputTag, releaseOutputImage, strings.Repeat("c", 40), strings.Repeat("c", 40), false)
	code, out := runVerifyDriver(t, bin, args, []string{"GH_TOKEN=test-token"})
	if code != 0 {
		t.Fatalf("Cosign saw a caller-supplied GH_TOKEN, got %d\n%s", code, out)
	}
	if got := len(logLines(t, cosignLog)); got != 12 {
		t.Fatalf("Cosign calls = %d, want the full signature set to have run", got)
	}
}

// TestVerifyReleaseOutputsAcceptsGHTokenForAttestations covers the documented GITHUB_TOKEN-or-
// GH_TOKEN fallback with attestations on, which every other attestation run misses.
func TestVerifyReleaseOutputsAcceptsGHTokenForAttestations(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, ghLog := releaseOutputStubBin(t, "", "")
	args := releaseOutputArgs(assets, "omkhar/workcell", releaseOutputTag, releaseOutputImage, strings.Repeat("c", 40), strings.Repeat("c", 40), true)
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

// TestVerifyReleaseOutputsRejectsUnexpectedReleaseFile leaves SHA256SUMS intact so only the directory inventory walk can reject the extra file.
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
	code, out := runVerifyReleaseOutputsWithDigests(t, bin, assets, strings.Repeat("b", 40), strings.Repeat("c", 40), false)
	if code == 0 || !strings.Contains(out, "source and trusted workflow digests must match") {
		t.Fatalf("expected release/workflow digest mismatch rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsUnrelatedImageRepository(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBin(t, "", "")
	args := releaseOutputArgs(assets, "omkhar/workcell", releaseOutputTag, "ghcr.io/attacker/workcell", strings.Repeat("c", 40), strings.Repeat("c", 40), false)
	code, out := runVerifyDriver(t, bin, args, nil)
	if code == 0 || !strings.Contains(out, "image repository must match release repository") {
		t.Fatalf("expected unrelated image repository rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsUnreviewedTagClass(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	bin, _, _ := releaseOutputStubBin(t, "", "")
	args := releaseOutputArgs(assets, "omkhar/workcell", "v1.2.3-beta.1", releaseOutputImage, strings.Repeat("c", 40), strings.Repeat("c", 40), false)
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

// mutatedVerifierDriver stubs a copy of the verifier with one textual change applied, so a
// negative fixture can show the assertion really fails against the regression it claims to catch.
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
	opts := defaultStubOptions(t)
	opts.scriptPath = path
	driver, _, _ := releaseOutputStubDriver(t, opts)
	return driver
}

// poisonedToolPath returns a directory where every tool the verifier shells out to exits non-zero, so any run resolving from it cannot complete.
func poisonedToolPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"jq", "sha256sum", "awk", "find", "wc", "cat", "sort", "sed", "grep"} {
		writeExecutable(t, dir, tool, "#!/bin/bash\nexit 3\n")
	}
	return dir
}

// TestVerifyReleaseOutputsPinsToolPath aims the caller-supplied tool-path variables named below
// at a directory of failing tools. The run must still succeed, which is only possible if the
// script resolved its tools from its own fixed trusted path. The check only covers the names it
// poisons: an override read from an unlisted name would be unset here and leave this green.
// TestVerifyReleaseOutputsRejectsDisarmedVerification closes the narrower case of a PATH widened
// around one call site, which no argument assertion can see.
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
	args := releaseOutputArgs(assets, "omkhar/workcell", releaseOutputTag, releaseOutputImage, strings.Repeat("c", 40), strings.Repeat("c", 40), false)
	bin, _, _ := releaseOutputStubBin(t, "", "")
	if code, out := runVerifyDriver(t, bin, args, env); code != 0 {
		t.Fatalf("release verifier honoured a caller-selected tool path, got %d\n%s", code, out)
	}

	// Negative fixtures: both regression shapes this guards against do fail under exactly the
	// environment above, so the success is not vacuous.
	for _, tc := range []struct{ name, old, replacement string }{
		{name: "inherits the caller PATH", old: `export PATH="${TRUSTED_PATH}"`, replacement: `export PATH="${PATH}"`},
		{name: "honours a caller override variable", old: `readonly TRUSTED_PATH="`, replacement: `readonly TRUSTED_PATH="${WORKCELL_RELEASE_VERIFY_TRUSTED_PATH:+${WORKCELL_RELEASE_VERIFY_TRUSTED_PATH}:}`},
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

// TestVerifyReleaseOutputsRejectsDisarmedVerification covers the regressions that leave both
// external verifiers running and exiting 0 while proving nothing: help mode, a caller-supplied
// Sigstore trust root, and a PATH widened around a single call site. Argument assertions cannot
// see the last one, so the stubs check the effective PATH; each mutant proves a check non-vacuous.
func TestVerifyReleaseOutputsRejectsDisarmedVerification(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(verifyReleaseOutputsScript(t))
	if err != nil {
		t.Fatal(err)
	}
	source := string(content)
	poisoned := poisonedToolPath(t)

	const cosignWant, ghWant = "Cosign verification failed for ", "GitHub attestation verification failed for "
	const certSHA = "  --certificate-github-workflow-sha \"${WORKFLOW_DIGEST}\" \\\n"
	const denySelfHosted = "      --deny-self-hosted-runners \\\n"
	widened := []string{"WORKCELL_RELEASE_VERIFY_PATH=" + poisoned}
	for _, tc := range []struct {
		name, old, replacement, want string
		attestations                 bool
		extraEnv                     []string
	}{
		{name: "Cosign runs in help mode", old: certSHA, replacement: "  --certificate-github-workflow-sha \"${WORKFLOW_DIGEST}\" --help \\\n", want: cosignWant},
		{name: "Cosign skips the transparency log", old: certSHA, replacement: "  --certificate-github-workflow-sha \"${WORKFLOW_DIGEST}\" --insecure-ignore-tlog \\\n", want: cosignWant},
		{name: "gh runs in help mode", old: denySelfHosted, replacement: "      --deny-self-hosted-runners --help \\\n", want: ghWant, attestations: true},
		{name: "gh takes a caller-supplied trust root", old: denySelfHosted, replacement: "      --custom-trusted-root /tmp/attacker-root.jsonl --deny-self-hosted-runners \\\n", want: ghWant, attestations: true},
		{name: "Cosign runs with a widened path", old: "    cosign \"$@\"\n", replacement: "    PATH=\"${WORKCELL_RELEASE_VERIFY_PATH:-}:${PATH}\" cosign \"$@\"\n", want: cosignWant, extraEnv: widened},
		{name: "gh runs with a widened path", old: "      gh attestation verify \"${subject}\" \\\n", replacement: "      PATH=\"${WORKCELL_RELEASE_VERIFY_PATH:-}:${PATH}\" gh attestation verify \"${subject}\" \\\n", want: ghWant, attestations: true, extraEnv: widened},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			release := defaultRelease()
			assets := releaseOutputFixture(t)
			driver := mutatedVerifierDriver(t, source, tc.old, tc.replacement)
			args := releaseOutputArgs(assets, release.repo, release.tag, release.image(), release.commit, release.commit, tc.attestations)
			env := append([]string{
				"GITHUB_TOKEN=" + release.token,
				"GH_HOST=attacker.example.com",
				"GH_ENTERPRISE_TOKEN=attacker-enterprise-token",
				"GITHUB_ENTERPRISE_TOKEN=attacker-enterprise-token",
			}, tc.extraEnv...)
			code, out := runVerifyDriver(t, driver, args, env)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("a verifier where %s was not rejected by %q, got %d\n%s", tc.name, tc.want, code, out)
			}
		})
	}
}

// callArgs splits one logged stub call back into its argument vector. The stub separates
// arguments with a unit separator, so a path containing whitespace stays one argument.
func callArgs(line string) []string {
	return strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f")
}

// flagOccurrences counts every appearance of flag, in both the separate-argument and the
// --flag=value forms. Aliases name alternate spellings the tools treat identically (gh's -R
// for --repo), so an occurrence of any spelling counts.
func flagOccurrences(args []string, flag string, aliases ...string) int {
	seen := 0
	for _, spelling := range append([]string{flag}, aliases...) {
		for _, arg := range args {
			if arg == spelling || strings.HasPrefix(arg, spelling+"=") ||
				(shortFlag(spelling) && len(arg) > 2 && strings.HasPrefix(arg, spelling)) {
				seen++
			}
		}
	}
	return seen
}

// shortFlag reports whether spelling is a single-dash short option, which gh also accepts in the compact attached form (-Rvalue).
func shortFlag(spelling string) bool {
	return len(spelling) == 2 && spelling[0] == '-' && spelling[1] != '-'
}

// flagValue returns the argument bound to flag, and only when it appears exactly once. Both
// tools take the last occurrence of a repeated flag, so a weaker second --cert-identity or
// --predicate-type would otherwise pass an assertion made against the first.
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
			if shortFlag(spelling) && len(arg) > 2 && strings.HasPrefix(arg, spelling) {
				return strings.TrimPrefix(arg[2:], "=")
			}
		}
	}
	return ""
}

// callSubjects reduces each logged stub call to the subject it verified, plus the bundle and
// predicate type that select what is proved about it. Comparing the whole multiset means a
// verifier that checks one subject repeatedly cannot pass by call count alone.
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
	release := defaultRelease()
	assets := releaseOutputFixture(t)
	bin, cosignLog, ghLog := releaseOutputStubBin(t, "", "")
	code, out := runVerifyReleaseOutputs(t, bin, assets, true)
	if code != 0 {
		t.Fatalf("expected release output verification success, got %d\n%s", code, out)
	}
	assertVerifiedRelease(t, release, assets, cosignLog, ghLog)
}

// TestVerifyReleaseOutputsVerifiesAnAlternateRelease runs a second successful release whose
// repository, tag class, release commit, image digest and attestation credential all differ from
// the primary fixture. Each reaches an argument the verifier derives rather than copies, so a
// verifier substituting any single literal passes the rest of this suite and fails here.
func TestVerifyReleaseOutputsVerifiesAnAlternateRelease(t *testing.T) {
	t.Parallel()
	release := releaseIdentity{
		// The repository name carries the words the stubs screen for as options, so a stub
		// matching an option as a substring rather than a whole token rejects this valid release.
		repo:        "acme-fork/workcell-insecure-hostname-mirror",
		tag:         "v1.4.0-rc.2",
		imageDigest: strings.Repeat("f", 64),
		commit:      strings.Repeat("e", 40),
		token:       "alternate-release-token",
	}
	assets := releaseOutputFixtureFor(t, release)
	opts := defaultStubOptions(t)
	opts.release = release
	opts.releaseTagDigest = release.imageDigest
	opts.commitTagDigest = release.imageDigest
	bin, cosignLog, ghLog := releaseOutputStubDriver(t, opts)

	args := releaseOutputArgs(assets, release.repo, release.tag, release.image(), release.commit, release.commit, true)
	code, out := runVerifyDriver(t, bin, args, []string{
		"GITHUB_TOKEN=" + release.token,
		"GH_HOST=attacker.example.com",
		"GH_ENTERPRISE_TOKEN=attacker-enterprise-token",
		"GITHUB_ENTERPRISE_TOKEN=attacker-enterprise-token",
	})
	if code != 0 {
		t.Fatalf("expected alternate release verification success, got %d\n%s", code, out)
	}
	assertVerifiedRelease(t, release, assets, cosignLog, ghLog)
}

// assertVerifiedRelease checks that a run verified exactly the subjects the release identity
// implies, and that every logged call carries the identity, issuer, commit and repository derived
// from it. Both success runs share it, so an expectation only holds for derived values.
func assertVerifiedRelease(t *testing.T, release releaseIdentity, assets, cosignLog, ghLog string) {
	t.Helper()
	wantCosign := make([]string, 0, 12)
	for _, asset := range release.assets() {
		path := filepath.Join(assets, asset)
		wantCosign = append(wantCosign, "verify-blob "+path+" bundle="+path+".sigstore.json")
	}
	wantCosign = append(wantCosign,
		"verify "+release.imageRef(),
		"verify "+release.image()+":"+release.tag,
		"verify "+release.image()+":sha-"+release.commit,
	)
	slices.Sort(wantCosign)
	if got := callSubjects(t, cosignLog); !slices.Equal(got, wantCosign) {
		t.Fatalf("Cosign subjects mismatch\ngot:  %q\nwant: %q", got, wantCosign)
	}

	wantGH := []string{
		"attestation verify oci://" + release.imageRef() + " predicate=" + slsaPredicate,
		"attestation verify oci://" + release.imageRef() + " predicate=" + spdxPredicate,
		"attestation verify " + filepath.Join(assets, release.bundle()) + " predicate=" + spdxPredicate,
	}
	for _, asset := range release.assets() {
		// The two SPDX documents are release data but not attested subjects; the image and source bundle carry those SBOMs.
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
		// Exactly one occurrence, in the bare form: gh also accepts --deny-self-hosted-runners=false
		// and uses the last occurrence, so neither an equals form nor a repeat may be tolerated.
		if flagOccurrences(args, "--deny-self-hosted-runners") != 1 ||
			!slices.Contains(args, "--deny-self-hosted-runners") {
			t.Fatalf("gh attestation call lacks an unconditional self-hosted runner denial: %s", line)
		}
		if got := flagValue(args, "--cert-identity"); got != release.identity() {
			t.Fatalf("gh attestation cert identity = %q, want %q: %s", got, release.identity(), line)
		}
		if got := flagValue(args, "--cert-oidc-issuer"); got != releaseOutputIssuer {
			t.Fatalf("gh attestation OIDC issuer = %q, want %q: %s", got, releaseOutputIssuer, line)
		}
		if got := flagValue(args, "--source-digest"); got != release.commit {
			t.Fatalf("gh attestation call lacks source digest binding: %s", line)
		}
		if got := flagValue(args, "--signer-digest"); got != release.commit {
			t.Fatalf("gh attestation call lacks signer digest binding: %s", line)
		}
		if got := flagValue(args, "--source-ref"); got != "refs/heads/main" {
			t.Fatalf("gh attestation call lacks trusted main source ref: %s", line)
		}
		// gh also accepts -R and the last occurrence wins: a later -R would silently redirect the
		// lookup, so any second spelling must fail the single-occurrence rule.
		if got := flagValue(args, "--repo", "-R"); got != release.repo {
			t.Fatalf("gh attestation call lacks the release repository: %s", line)
		}
	}
	for _, line := range logLines(t, cosignLog) {
		args := callArgs(line)
		if got := flagValue(args, "--certificate-github-workflow-sha"); got != release.commit {
			t.Fatalf("Cosign call lacks source digest binding: %s", line)
		}
		if got := flagValue(args, "--certificate-identity"); got != release.identity() {
			t.Fatalf("Cosign certificate identity = %q, want %q: %s", got, release.identity(), line)
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
	// The inventory-completeness guard counts the deleted signature before the per-file check
	// runs, so it reports first; either message proves the verifier fails closed.
	if code == 0 || !(strings.Contains(out, "required release file is missing") ||
		strings.Contains(out, "release directory listing is incomplete")) {
		t.Fatalf("expected missing signature rejection, got %d\n%s", code, out)
	}
}

// TestVerifyReleaseOutputsRejectsCosignFailure fails one Cosign call at a time so each rejection
// is attributable to the call under test rather than to an earlier check that would reject first.
func TestVerifyReleaseOutputsRejectsCosignFailure(t *testing.T) {
	t.Parallel()
	imageRef := releaseOutputImage + "@sha256:" + strings.Repeat("a", 64)
	releaseTag := releaseOutputImage + ":" + releaseOutputTag
	commitTag := releaseOutputImage + ":sha-" + strings.Repeat("c", 40)
	for _, tc := range []struct{ name, failGlob, want string }{
		{name: "every call", failGlob: "*", want: "Cosign verification failed for " + releaseOutputBundle},
		{name: "one blob signature", failGlob: "verify-blob *workcell.rb --bundle *", want: "Cosign verification failed for workcell.rb"},
		{name: "image digest", failGlob: "verify " + imageRef + " *", want: "Cosign image verification failed for " + imageRef},
		{name: "release tag", failGlob: "verify " + releaseTag + " *", want: "Cosign image verification failed for " + releaseTag},
		{name: "commit tag", failGlob: "verify " + commitTag + " *", want: "Cosign image verification failed for " + commitTag},
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
	opts := defaultStubOptions(t)
	opts.releaseTagDigest = strings.Repeat("b", 64)
	opts.commitTagDigest = strings.Repeat("b", 64)
	bin, _, _ := releaseOutputStubDriver(t, opts)
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "named image tag") || !strings.Contains(out, "does not bind") {
		t.Fatalf("expected moved named-image tag rejection, got %d\n%s", code, out)
	}
}

func TestVerifyReleaseOutputsRejectsMovedCommitImageTag(t *testing.T) {
	t.Parallel()
	assets := releaseOutputFixture(t)
	opts := defaultStubOptions(t)
	opts.commitTagDigest = strings.Repeat("b", 64)
	bin, _, _ := releaseOutputStubDriver(t, opts)
	code, out := runVerifyReleaseOutputs(t, bin, assets, false)
	if code == 0 || !strings.Contains(out, "ghcr.io/omkhar/workcell:sha-"+strings.Repeat("c", 40)) || !strings.Contains(out, "does not bind") {
		t.Fatalf("expected moved commit-image tag rejection, got %d\n%s", code, out)
	}
}

// TestVerifyReleaseOutputsRejectsAttestationFailure fails one gh attestation call at a time. The
// verifier makes ten and only the first is reachable by a blanket failure, so the later OCI SBOM,
// per-asset provenance and source-bundle SBOM calls each need a case of their own.
// TestVerifyReleaseOutputsRejectsOutOfContractImageVerification covers Cosign calls that succeed
// but return something other than a non-empty result array. A digest predicate built only from
// `all` is vacuously true on an empty array, and one omitting the array type check accepts an
// object, because jq's `length` and `.[]` both work on objects. Each shape is applied to one
// named tag at a time so the rejection is attributable to the tag under test.
func TestVerifyReleaseOutputsRejectsOutOfContractImageVerification(t *testing.T) {
	t.Parallel()
	release := defaultRelease()
	releaseTagWant := "named image tag " + release.image() + ":" + release.tag + " does not bind"
	commitTagWant := "named image tag " + release.image() + ":sha-" + release.commit + " does not bind"
	for _, tc := range []struct {
		name string
		opts func(stubOptions) stubOptions
		want string
	}{
		{name: "empty result array for the release tag", opts: func(o stubOptions) stubOptions { o.releaseTagDigest = ""; return o }, want: releaseTagWant},
		{name: "empty result array for the commit tag", opts: func(o stubOptions) stubOptions { o.commitTagDigest = ""; return o }, want: commitTagWant},
		{name: "object response for the release tag", opts: func(o stubOptions) stubOptions { o.releaseTagObject = true; return o }, want: releaseTagWant},
		{name: "object response for the commit tag", opts: func(o stubOptions) stubOptions { o.commitTagObject = true; return o }, want: commitTagWant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assets := releaseOutputFixture(t)
			bin, _, _ := releaseOutputStubDriver(t, tc.opts(defaultStubOptions(t)))
			code, out := runVerifyReleaseOutputs(t, bin, assets, false)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("expected out-of-contract verification rejection %q, got %d\n%s", tc.want, code, out)
			}
		})
	}
}

func TestVerifyReleaseOutputsRejectsAttestationFailure(t *testing.T) {
	t.Parallel()
	imageRef := releaseOutputImage + "@sha256:" + strings.Repeat("a", 64)
	const ghWant = "GitHub attestation verification failed for "
	for _, tc := range []struct{ name, failGlob, want string }{
		{name: "every call", failGlob: "*", want: ghWant + "oci://" + imageRef},
		{name: "image SBOM", failGlob: "attestation verify oci://* --predicate-type " + spdxPredicate + " *", want: ghWant + "oci://" + imageRef},
		{name: "one asset provenance", failGlob: "attestation verify *SHA256SUMS --repo *", want: ghWant},
		{name: "source bundle SBOM", failGlob: "attestation verify *" + releaseOutputBundle + " --repo * --predicate-type " + spdxPredicate + " *", want: ghWant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assets := releaseOutputFixture(t)
			bin, _, ghLog := releaseOutputStubBin(t, "", tc.failGlob)
			code, out := runVerifyReleaseOutputs(t, bin, assets, true)
			if code == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("expected GitHub attestation rejection %q, got %d\n%s", tc.want, code, out)
			}
			// Exactly one call may fail, so the run must stop on the call the glob selected.
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

// TestVerifyReleaseOutputsRejectsChecksumMismatch alters one asset at a time across the whole
// checksum inventory. A verifier skipping any single entry could publish that file alongside a
// stale, separately signed SHA256SUMS, so every asset must be shown to reach the binding check.
func TestVerifyReleaseOutputsRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()
	for _, asset := range defaultRelease().checksumAssets() {
		t.Run(asset, func(t *testing.T) {
			t.Parallel()
			assets := releaseOutputFixture(t)
			// The build input manifest is parsed for its release-commit binding before the
			// checksum walk, so its replacement keeps that binding and changes only the bytes.
			content := []byte("changed\n")
			if asset == "workcell-build-inputs.json" {
				content = []byte(`{"build":{"ref":"` + defaultRelease().commit + `"}}` + "\n")
			}
			if err := os.WriteFile(filepath.Join(assets, asset), content, 0o644); err != nil {
				t.Fatal(err)
			}
			bin, _, _ := releaseOutputStubBin(t, "", "")
			code, out := runVerifyReleaseOutputs(t, bin, assets, false)
			if code == 0 || !strings.Contains(out, "SHA256SUMS digest mismatch for "+asset) {
				t.Fatalf("expected checksum mismatch rejection for %s, got %d\n%s", asset, code, out)
			}
		})
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

// rewriteAsset replaces one release asset and its SHA256SUMS entry so the inventory still
// matches, keeping a negative control aimed at the check under test rather than the checksum gate.
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

// TestVerifyReleaseOutputsRejectsUnboundBuildInputs points the build input manifest at another
// commit while keeping SHA256SUMS valid, so only the verifier's .build.ref binding can reject it.
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
	for _, tc := range []struct{ name, content, want string }{
		{name: "multiple lines", content: "ghcr.io/omkhar/workcell@sha256:" + strings.Repeat("A", 64) + "\nextra\n", want: "exactly one line"},
		{name: "uppercase digest", content: "ghcr.io/omkhar/workcell@sha256:" + strings.Repeat("A", 64) + "\n", want: "lowercase SHA-256 digest"},
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
	// gh also parses the compact attached form: a later -Rattacker/repo must fail the single-occurrence rule too.
	compact := []string{"sha256:aa", "--repo", "omkhar/workcell", "-Rattacker/repo"}
	if got := flagValue(compact, "--repo", "-R"); got != "" {
		t.Fatalf("flagValue tolerated a compact short-alias override, got %q", got)
	}
	if got := flagValue([]string{"-Romkhar/workcell"}, "--repo", "-R"); got != "omkhar/workcell" {
		t.Fatalf("flagValue missed the compact short alias alone, got %q", got)
	}
}

// runReleaseOutputsInventoryGuard runs the shipped release-output verifier
// against one assets directory and reports its exit status and combined output.
//
// The run goes through a driver that sources the verifier and defines cosign as
// a shell function. The verifier pins PATH to a fixed trusted list, so a stub
// cannot be placed on PATH, but a function satisfies its `command -v cosign`
// precondition on a machine without cosign installed. The stub is never called:
// both guards under test reject before any signature verification.
func runReleaseOutputsInventoryGuard(t *testing.T, dir string) (int, string) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/bash\nsource %s\ncosign() { return 0; }\nmain \"$@\"\n",
		ShellQuote(filepath.Join(repoRoot(t), "scripts", "verify-release-outputs.sh")))
	driver := writeExecutable(t, t.TempDir(), "release-outputs-inventory-driver.sh", script)

	digest := strings.Repeat("c", 40)
	cmd := exec.Command(driver,
		"--assets-dir", dir,
		"--repo", "omkhar/workcell",
		"--tag", "v1.2.3",
		"--image-repository", "ghcr.io/omkhar/workcell",
		"--source-digest", digest,
		"--workflow-digest", digest,
	)
	// Bash startup files are cleared so caller state cannot reach the verifier.
	cmd.Env = []string{"BASH_ENV=", "ENV="}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("running release-output verifier: %v\n%s", err, out)
	}
	return exitErr.ExitCode(), string(out)
}

// TestVerifyReleaseOutputsRejectsSymlinkedAssetsDir covers a fail-open path in
// scripts/verify-release-outputs.sh. `find` emits no children for a
// command-line symlink, so a symlinked --assets-dir skipped the directory
// inventory walk entirely, while every per-asset `-f && ! -L` check still
// resolved through the symlinked parent and passed. The verifier exited 0 for a
// release directory holding an unexpected file. The run must fail closed on the
// symlink itself, before any signature check.
func TestVerifyReleaseOutputsRejectsSymlinkedAssetsDir(t *testing.T) {
	t.Parallel()
	assets := t.TempDir()
	link := filepath.Join(t.TempDir(), "assets-link")
	if err := os.Symlink(assets, link); err != nil {
		t.Fatal(err)
	}

	const rejection = "assets directory must not be a symlink"
	if code, out := runReleaseOutputsInventoryGuard(t, link); code == 0 || !strings.Contains(out, rejection) {
		t.Fatalf("expected symlinked assets directory rejection, got %d\n%s", code, out)
	}

	// Negative control: the same directory reached by its real path must clear
	// the symlink gate and reach the walk, so the check above rejects the
	// symlink rather than anything else about the fixture. Reaching the walk on
	// an empty directory also exercises the count assertion, which is what
	// catches a walk that completed successfully having seen nothing -- the
	// state a symlinked directory produces once the gate above is removed.
	if code, out := runReleaseOutputsInventoryGuard(t, assets); code == 0 ||
		!strings.Contains(out, "release directory listing is incomplete: read 0 of") {
		t.Fatalf("expected the real path to reach the inventory count check, got %d\n%s", code, out)
	}
}

// TestVerifyReleaseOutputsRejectsUnlistableAssetsDir covers a second fail-open
// in the same walk. `find` runs in a process substitution, whose nonzero exit
// Bash does not propagate, so a directory that permits traversal but not
// listing left the inventory loop with nothing to read while every per-asset
// check still opened the names it already expected. The verifier exited 0 with
// an unexpected file present but unseen. The walk must now observe `find`
// running to completion rather than inferring success from what it emitted.
func TestVerifyReleaseOutputsRejectsUnlistableAssetsDir(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permission bits, so enumeration cannot be denied")
	}
	assets := t.TempDir()
	// One decoy file: enumerable, the walk rejects it by name; unenumerable, it
	// is exactly what the fail-open used to hide.
	if err := os.WriteFile(filepath.Join(assets, "attacker.bin"), []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Negative control first, while the directory is still readable: the walk
	// sees the decoy and rejects it by name, proving the fixture is enumerable
	// and the assertion below is not passing on an inert path.
	if code, out := runReleaseOutputsInventoryGuard(t, assets); code == 0 || !strings.Contains(out, "unexpected release file: attacker.bin") {
		t.Fatalf("expected the readable fixture to be rejected by name, got %d\n%s", code, out)
	}

	// Execute-only: traversal succeeds, enumeration is denied.
	if err := os.Chmod(assets, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(assets, 0o755) })

	// The walk must reject because `find` did not run to completion, not because
	// of what it managed to emit before failing.
	const rejection = "release directory listing failed"
	code, out := runReleaseOutputsInventoryGuard(t, assets)
	if code == 0 || !strings.Contains(out, rejection) {
		t.Fatalf("expected an unlistable assets directory to fail closed, got %d\n%s", code, out)
	}
}
