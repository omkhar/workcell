// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckRustVendorRejectsSelfConsistentTamper proves the check binds the
// committed vendor tree to a reference built from crates.io: a tampered crate
// whose .cargo-checksum.json is regenerated still fails.
func TestCheckRustVendorRejectsSelfConsistentTamper(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	realVendor := filepath.Join(root, "runtime", "container", "rust", "vendor")
	reference := filepath.Join(t.TempDir(), "reference")
	fixture := t.TempDir()
	vendor := filepath.Join(fixture, "runtime", "container", "rust", "vendor")
	if err := os.MkdirAll(filepath.Dir(vendor), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dst := range []string{reference, vendor} {
		if out, err := exec.Command("cp", "-R", realVendor, dst).CombinedOutput(); err != nil {
			t.Fatalf("copy vendor: %v: %s", err, out)
		}
	}

	run := func() (string, error) {
		cmd := exec.Command(filepath.Join(root, "scripts", "check-rust-vendor.sh"))
		cmd.Env = canonicalBuildEnv(map[string]string{
			"WORKCELL_RUST_VENDOR_ROOT":          fixture,
			"WORKCELL_RUST_VENDOR_REFERENCE_DIR": reference,
		})
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run(); err != nil {
		t.Fatalf("clean tree rejected: %v: %s", err, out)
	}

	lib := filepath.Join(vendor, "libc", "src", "lib.rs")
	f, err := os.OpenFile(lib, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\npub const TAMPER: u32 = 1;\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// Regenerate the per-file hash, as an attacker would.
	sumPath := filepath.Join(vendor, "libc", ".cargo-checksum.json")
	var sum struct {
		Files   map[string]string `json:"files"`
		Package string            `json:"package"`
	}
	raw, err := os.ReadFile(sumPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &sum); err != nil {
		t.Fatal(err)
	}
	tampered, err := exec.Command("shasum", "-a", "256", lib).Output()
	if err != nil {
		t.Fatal(err)
	}
	sum.Files["src/lib.rs"] = strings.Fields(string(tampered))[0]
	raw, err = json.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sumPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := run(); err == nil {
		t.Fatalf("tampered vendor tree accepted: %s", out)
	}
}
