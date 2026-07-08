// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These tests drive scripts/verify-release-artifact.sh — the installer-side,
// fail-closed release verification gate — by executing it against fixture
// assets with a stubbed cosign on PATH. Real keyless cosign signing needs
// GitHub OIDC and cannot run in a unit test, so cosign's signature check is
// stubbed while the sha256 binding is exercised for real. The stub lets us
// prove both halves of fail-closed independently: a failing cosign is rejected,
// and a tampered artifact whose sha256 no longer matches the (stub-"verified")
// SHA256SUMS is also rejected.

func verifyReleaseScript(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "scripts", "verify-release-artifact.sh")
}

// stubBinDir builds a directory containing a fake `cosign` with the given exit
// code plus symlinks to the real tools the script needs, and returns it for use
// as the sole PATH entry so the test fully controls tool resolution.
func stubBinDir(t *testing.T, cosignExit int, extraTools ...string) string {
	t.Helper()
	dir := t.TempDir()

	if cosignExit >= 0 {
		cosign := filepath.Join(dir, "cosign")
		script := "#!/bin/bash\nexit " + strconv.Itoa(cosignExit) + "\n"
		if err := os.WriteFile(cosign, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	tools := append([]string{"env", "bash", "awk", "wc", "sha256sum", "shasum"}, extraTools...)
	for _, tool := range tools {
		resolved, err := exec.LookPath(tool)
		if err != nil {
			continue // sha256sum/shasum: only one is required, symlink whichever exists.
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

// writeAssets creates an assets dir with the artifact, a SHA256SUMS listing its
// real digest, and a placeholder sigstore bundle. It returns the assets dir.
func writeAssets(t *testing.T, artifact string, content []byte, withBundle bool) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, artifact), content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	line := hex.EncodeToString(sum[:]) + "  " + artifact + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if withBundle {
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS.sigstore.json"), []byte(`{"stub":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runVerify(t *testing.T, binDir, assetsDir string, args ...string) (int, string) {
	t.Helper()
	full := append([]string{"--assets-dir", assetsDir}, args...)
	cmd := exec.Command(verifyReleaseScript(t), full...)
	// The script hardens PATH to a fixed trusted allowlist (so ambient PATH
	// pollution cannot shadow cosign/sha256sum); WORKCELL_INSTALL_TRUSTED_PATH is
	// its documented override, which the test uses to inject the stub bin dir.
	// PATH is still set so the #!/usr/bin/env shebang can resolve bash from the
	// same dir before the script re-hardens PATH.
	cmd.Env = []string{
		"PATH=" + binDir,
		"WORKCELL_INSTALL_TRUSTED_PATH=" + binDir,
		"BASH_ENV=", "ENV=", "HOME=" + t.TempDir(),
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("running verify-release-artifact.sh: %v (out=%s)", err, out)
		}
	}
	return code, string(out)
}

func TestVerifyReleaseArtifactAcceptsCorrectlySignedArtifact(t *testing.T) {
	t.Parallel()
	artifact := "workcell-v1.2.3.tar.gz"
	content := []byte("hello workcell\n")
	bin := stubBinDir(t, 0)
	assets := writeAssets(t, artifact, content, true)

	code, out := runVerify(t, bin, assets, "--artifact", artifact)
	if code != 0 {
		t.Fatalf("expected accept (exit 0), got %d\n%s", code, out)
	}
	if !strings.Contains(out, "OK: "+artifact+" verified") {
		t.Fatalf("expected success message, got:\n%s", out)
	}
}

func TestVerifyReleaseArtifactRejectsTamperedArtifact(t *testing.T) {
	t.Parallel()
	artifact := "workcell-v1.2.3.tar.gz"
	bin := stubBinDir(t, 0) // cosign "succeeds": isolates the sha256 binding.
	assets := writeAssets(t, artifact, []byte("hello workcell\n"), true)
	// Tamper the artifact after SHA256SUMS was written over the original bytes.
	if err := os.WriteFile(filepath.Join(assets, artifact), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runVerify(t, bin, assets, "--artifact", artifact)
	if code == 0 {
		t.Fatalf("expected rejection of tampered artifact, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "digest mismatch") {
		t.Fatalf("expected digest mismatch error, got:\n%s", out)
	}
}

func TestVerifyReleaseArtifactRejectsBadSignature(t *testing.T) {
	t.Parallel()
	artifact := "workcell-v1.2.3.tar.gz"
	bin := stubBinDir(t, 1) // cosign fails signature verification.
	assets := writeAssets(t, artifact, []byte("hello workcell\n"), true)

	code, out := runVerify(t, bin, assets, "--artifact", artifact)
	if code == 0 {
		t.Fatalf("expected rejection on bad signature, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "cosign could not verify") {
		t.Fatalf("expected cosign verify error, got:\n%s", out)
	}
}

func TestVerifyReleaseArtifactRejectsMissingSigstoreBundle(t *testing.T) {
	t.Parallel()
	artifact := "workcell-v1.2.3.tar.gz"
	bin := stubBinDir(t, 0)
	assets := writeAssets(t, artifact, []byte("hello workcell\n"), false) // no sigstore bundle

	code, out := runVerify(t, bin, assets, "--artifact", artifact)
	if code == 0 {
		t.Fatalf("expected rejection on missing verification material, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "verification material missing") {
		t.Fatalf("expected missing-material error, got:\n%s", out)
	}
}

func TestVerifyReleaseArtifactRejectsWhenCosignAbsent(t *testing.T) {
	t.Parallel()
	artifact := "workcell-v1.2.3.tar.gz"
	bin := stubBinDir(t, -1) // no cosign in the stub bin dir.
	assets := writeAssets(t, artifact, []byte("hello workcell\n"), true)

	code, out := runVerify(t, bin, assets, "--artifact", artifact)
	if code == 0 {
		t.Fatalf("expected fail-closed when cosign is absent, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "cosign is required") {
		t.Fatalf("expected cosign-required error, got:\n%s", out)
	}
}

func TestVerifyReleaseArtifactSkipVerifyRequiresAcknowledgement(t *testing.T) {
	t.Parallel()
	artifact := "workcell-v1.2.3.tar.gz"
	bin := stubBinDir(t, 0)
	assets := writeAssets(t, artifact, []byte("hello workcell\n"), true)

	code, out := runVerify(t, bin, assets, "--artifact", artifact, "--skip-verify")
	if code == 0 {
		t.Fatalf("expected --skip-verify without ack to refuse, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "requires --i-understand-unverified-install") {
		t.Fatalf("expected acknowledgement-required error, got:\n%s", out)
	}
}

// exitSkippedUnverified mirrors EXIT_SKIPPED_UNVERIFIED in
// verify-release-artifact.sh: the distinct code an acknowledged skip returns so
// it can never be confused with a genuine (exit 0) verification.
const exitSkippedUnverified = 10

// Neutralization control: bypassing the gate with an acknowledged --skip-verify
// makes the SAME tampered artifact that TestVerifyReleaseArtifactRejectsTamperedArtifact
// rejects proceed instead. This proves the verification is the load-bearing
// gate, not incidental behavior. The skip must NOT report success (exit 0): it
// returns the distinct exit 10 and an unverified-install banner, never the
// "verified" message, so a caller cannot mistake a skip for a verified install.
func TestVerifyReleaseArtifactSkipVerifyNeutralizationLetsTamperedArtifactPass(t *testing.T) {
	t.Parallel()
	artifact := "workcell-v1.2.3.tar.gz"
	bin := stubBinDir(t, 0)
	assets := writeAssets(t, artifact, []byte("hello workcell\n"), true)
	if err := os.WriteFile(filepath.Join(assets, artifact), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := runVerify(t, bin, assets, "--artifact", artifact,
		"--skip-verify", "--i-understand-unverified-install")
	if code != exitSkippedUnverified {
		t.Fatalf("expected acknowledged --skip-verify to exit %d (skipped, not verified), got %d\n%s",
			exitSkippedUnverified, code, out)
	}
	if !strings.Contains(out, "WARNING") || !strings.Contains(out, "UNVERIFIED") {
		t.Fatalf("expected a loud unverified-install warning when skipping verification, got:\n%s", out)
	}
	// A skip must never be reported as a verified install.
	if strings.Contains(out, "OK: "+artifact+" verified") {
		t.Fatalf("skip path must NOT report the artifact as verified, got:\n%s", out)
	}
}
