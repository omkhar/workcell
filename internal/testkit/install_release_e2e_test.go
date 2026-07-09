// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These tests drive the whole verified consumer install path end to end —
// scripts/install-release.sh, which downloads a tagged release, verifies it
// fail-closed via scripts/verify-release-artifact.sh, and only then extracts it
// and hands off to the bundle's own scripts/install.sh. release_verify_test.go
// already proves the verifier in isolation; this exercises the ORCHESTRATION
// install-release.sh wraps around it (download -> verify -> extract -> handoff)
// so a regression that, say, extracted before verifying, or ran the bundle's
// installer even when verification failed, is caught.
//
// It needs neither the network nor a published release: a fake `curl` on the
// script's trusted PATH serves a locally built fixture "release" from disk, and
// a fake `cosign` stands in for the keyless signature check (which needs GitHub
// OIDC and cannot run in a unit test). The sha256 digest binding between the
// bundle and its signed SHA256SUMS is exercised for real. This is the
// CI-automatable half of ci-threat-model known gap 1's "exercise
// install-release.sh end to end"; the network fetch of a genuinely published,
// genuinely cosign-signed release remains the local-operator/release remainder
// (see docs/install-lifecycle.md).
//
// The fixture bundle's scripts/install.sh is a stub that only touches a marker
// file, so "the handoff ran" is observable as "the marker exists". A real
// bundle install is covered by the macOS install-verification CI lane.

const installReleaseFixtureVersion = "v9.9.9"

func installReleaseScript(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "scripts", "install-release.sh")
}

// buildFixtureBundle writes a workcell-<version>.tar.gz whose sole content is an
// executable scripts/install.sh stub that touches $WORKCELL_TEST_INSTALL_MARKER,
// and returns the tarball bytes. The stub deliberately does NOT re-exec under a
// scrubbed environment the way the real installer does, so the marker env var
// survives into it.
func buildFixtureBundle(t *testing.T) []byte {
	t.Helper()
	stub := "#!/bin/bash\nset -euo pipefail\n: >\"${WORKCELL_TEST_INSTALL_MARKER:?marker path required}\"\necho \"fixture bundle install.sh ran\"\n"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	root := "workcell-" + installReleaseFixtureVersion
	entries := []struct {
		name string
		mode int64
		body string
		dir  bool
	}{
		{name: root + "/", mode: 0o755, dir: true},
		{name: root + "/scripts/", mode: 0o755, dir: true},
		{name: root + "/scripts/install.sh", mode: 0o755, body: stub},
	}
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body))}
		if e.dir {
			hdr.Typeflag = tar.TypeDir
		} else {
			hdr.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeFixtureRelease lays out the assets a real release publishes — the bundle,
// its signed SHA256SUMS, and the sigstore bundle — in a directory the fake curl
// serves from. When tamper is true the bundle bytes no longer match the digest
// recorded in SHA256SUMS, so the real digest binding must reject them even
// though the (stubbed) signature "verifies".
func writeFixtureRelease(t *testing.T, tamper bool) string {
	t.Helper()
	dir := t.TempDir()
	bundle := buildFixtureBundle(t)
	bundleName := "workcell-" + installReleaseFixtureVersion + ".tar.gz"

	sum := sha256.Sum256(bundle)
	sums := hex.EncodeToString(sum[:]) + "  " + bundleName + "\n"

	if tamper {
		bundle = append(bundle, []byte("tampered")...)
	}
	if err := os.WriteFile(filepath.Join(dir, bundleName), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.sigstore.json"), []byte(`{"stub":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// installReleaseStubBin builds the sole PATH entry install-release.sh (and the
// verifier it calls) resolves tools from: a fake cosign with the given exit
// code, a fake curl that copies the requested asset out of fixtureDir instead of
// fetching it, and symlinks to the real utilities the scripts need.
func installReleaseStubBin(t *testing.T, cosignExit int, fixtureDir string) string {
	t.Helper()
	dir := t.TempDir()

	cosign := "#!/bin/bash\nexit " + strconv.Itoa(cosignExit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "cosign"), []byte(cosign), 0o755); err != nil {
		t.Fatal(err)
	}

	// Fake curl: ignore every real download flag, find the -o output path and the
	// trailing URL, and copy fixtureDir/<basename-of-URL> to the output path. A
	// missing fixture asset exits non-zero, mimicking a failed download.
	curl := "#!/bin/bash\nset -euo pipefail\nout=\"\"\nurl=\"\"\nwhile [[ $# -gt 0 ]]; do\n  case \"$1\" in\n    -o) out=\"$2\"; shift 2;;\n    http://*|https://*) url=\"$1\"; shift;;\n    *) shift;;\n  esac\ndone\n[[ -n \"${out}\" && -n \"${url}\" ]] || exit 2\nsrc=\"" + fixtureDir + "/${url##*/}\"\n[[ -f \"${src}\" ]] || exit 22\ncp \"${src}\" \"${out}\"\n"
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(curl), 0o755); err != nil {
		t.Fatal(err)
	}

	// tar wrapper: install-release.sh extracts with `tar -xzf` only AFTER
	// verification passes. Wrap tar so any real extraction appends to a durable
	// $WORKCELL_TEST_TAR_LOG (kept outside the installer's WORK_DIR, so the
	// installer's own EXIT-trap cleanup cannot erase it) and then execs the real
	// tar. This makes "the untrusted bundle was extracted" observable and durable:
	// on a fail-closed path tar is never invoked, so the log stays absent even
	// though WORK_DIR is torn down. `command -v tar` (a resolve, not an exec) does
	// not trigger the wrapper, so only a genuine extraction logs.
	realTar, err := exec.LookPath("tar")
	if err != nil {
		t.Fatal(err)
	}
	tarWrapper := "#!/bin/bash\nprintf 'extract-invoked\\n' >>\"${WORKCELL_TEST_TAR_LOG:-/dev/null}\"\nexec " + realTar + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "tar"), []byte(tarWrapper), 0o755); err != nil {
		t.Fatal(err)
	}

	// gzip: the wrapped real tar (GNU tar on the Ubuntu validate lane) forks a
	// separate `gzip` for `-z`, so gzip must be reachable on the stub-only PATH
	// too. macOS bsdtar decompresses in-process and would pass without it — hence
	// this is the CI-lane-load-bearing symlink. curl/cosign/tar are stubbed above;
	// every other tool resolves to the real system binary.
	tools := []string{
		"env", "bash", "gzip", "find", "mktemp", "rm", "mkdir", "cp", "cat",
		"head", "touch", "awk", "wc", "sha256sum", "shasum", "dirname", "sed",
	}
	for _, tool := range tools {
		resolved, err := exec.LookPath(tool)
		if err != nil {
			continue // sha256sum/shasum: only one is required.
		}
		link := filepath.Join(dir, tool)
		if _, statErr := os.Lstat(link); statErr == nil {
			continue
		}
		if err := os.Symlink(resolved, link); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// installReleaseResult captures the durable, post-run observables: the process
// exit code and output, whether the fixture bundle's install.sh handoff ran
// (installMarker), and whether the untrusted bundle was ever extracted
// (tarLog). Both markers live outside the installer's WORK_DIR so its cleanup
// cannot affect them.
type installReleaseResult struct {
	code          int
	out           string
	installMarker string
	tarLog        string
}

func (r installReleaseResult) installRan() bool { return fileExists(r.installMarker) }
func (r installReleaseResult) extracted() bool  { return fileExists(r.tarLog) }

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runInstallRelease(t *testing.T, cosignExit int, tamper bool) installReleaseResult {
	t.Helper()
	fixtureDir := writeFixtureRelease(t, tamper)
	binDir := installReleaseStubBin(t, cosignExit, fixtureDir)
	home := t.TempDir()
	marker := filepath.Join(home, "install-ran.marker")
	tarLog := filepath.Join(home, "tar-invoked.log")

	cmd := exec.Command(installReleaseScript(t), "--version", installReleaseFixtureVersion, "--repo", "omkhar/workcell")
	cmd.Env = []string{
		"PATH=" + binDir,
		"WORKCELL_INSTALL_TRUSTED_PATH=" + binDir,
		"BASH_ENV=", "ENV=",
		"HOME=" + home,
		"TMPDIR=" + t.TempDir(),
		"WORKCELL_TEST_INSTALL_MARKER=" + marker,
		"WORKCELL_TEST_TAR_LOG=" + tarLog,
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running install-release.sh: %v (out=%s)", err, out)
		}
		code = exitErr.ExitCode()
	}
	return installReleaseResult{code: code, out: string(out), installMarker: marker, tarLog: tarLog}
}

func TestInstallReleaseVerifiesThenInstalls(t *testing.T) {
	t.Parallel()
	r := runInstallRelease(t, 0, false)
	if r.code != 0 {
		t.Fatalf("expected verified install to succeed (exit 0), got %d\n%s", r.code, r.out)
	}
	if !strings.Contains(r.out, "Verified") || !strings.Contains(r.out, "installing") {
		t.Fatalf("expected verified-then-install log, got:\n%s", r.out)
	}
	// After genuine verification the bundle IS extracted and its install.sh runs.
	// Asserting both markers PRESENT here is what makes their absence on the
	// fail-closed paths meaningful (present when extraction/handoff happen).
	if !r.extracted() {
		t.Fatalf("expected the verified bundle to be extracted (tar sentinel missing)\n%s", r.out)
	}
	if !r.installRan() {
		t.Fatalf("bundle install.sh handoff did not run (marker missing)\n%s", r.out)
	}
}

// TestInstallReleaseFailsClosedOnBadSignature is the load-bearing control: the
// ONLY change from the passing case is that cosign fails, and that alone must
// stop the install BEFORE the untrusted bundle is EXTRACTED (verify-before-
// extract) — not merely before install.sh runs. The absent tar sentinel proves
// extraction never happened; the absent install marker proves no handoff.
func TestInstallReleaseFailsClosedOnBadSignature(t *testing.T) {
	t.Parallel()
	r := runInstallRelease(t, 1, false)
	if r.code == 0 {
		t.Fatalf("expected fail-closed on bad signature, got exit 0\n%s", r.out)
	}
	if !strings.Contains(r.out, "cosign could not verify") {
		t.Fatalf("expected cosign verify failure, got:\n%s", r.out)
	}
	if r.extracted() {
		t.Fatalf("untrusted bundle was extracted despite failed verification (tar sentinel present)\n%s", r.out)
	}
	if r.installRan() {
		t.Fatalf("bundle install.sh ran despite failed verification (marker present)\n%s", r.out)
	}
}

// TestInstallReleaseFailsClosedOnDigestMismatch proves the digest binding gates
// the end-to-end path too: even with a "valid" (stubbed) signature, a bundle
// whose bytes do not match the signed SHA256SUMS is refused before it is
// extracted or handed off.
func TestInstallReleaseFailsClosedOnDigestMismatch(t *testing.T) {
	t.Parallel()
	r := runInstallRelease(t, 0, true)
	if r.code == 0 {
		t.Fatalf("expected fail-closed on digest mismatch, got exit 0\n%s", r.out)
	}
	if !strings.Contains(r.out, "digest mismatch") {
		t.Fatalf("expected digest mismatch error, got:\n%s", r.out)
	}
	if r.extracted() {
		t.Fatalf("untrusted bundle was extracted despite digest mismatch (tar sentinel present)\n%s", r.out)
	}
	if r.installRan() {
		t.Fatalf("bundle install.sh ran despite digest mismatch (marker present)\n%s", r.out)
	}
}
