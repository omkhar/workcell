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

// TestCheckRustVendorRejectsTamper proves the check binds the committed vendor
// tree to a reference built from crates.io. A fake cargo on HOME/.cargo/bin
// serves a pristine copy, so the script has no test-only override to abuse.
func TestCheckRustVendorRejectsTamper(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	realVendor := filepath.Join(root, "runtime", "container", "rust", "vendor")
	reference := filepath.Join(t.TempDir(), "reference")
	fixture := t.TempDir()
	vendor := filepath.Join(fixture, "runtime", "container", "rust", "vendor")
	home := filepath.Join(fixture, "home")
	for _, dir := range []string{filepath.Dir(vendor), filepath.Join(fixture, "scripts", "lib"), filepath.Join(home, ".cargo", "bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, dst := range []string{reference, vendor} {
		if out, err := exec.Command("cp", "-R", realVendor, dst).CombinedOutput(); err != nil {
			t.Fatalf("copy vendor: %v: %s", err, out)
		}
	}
	for _, rel := range []string{"check-rust-vendor.sh", filepath.Join("lib", "trusted-entrypoint.sh")} {
		data, err := os.ReadFile(filepath.Join(root, "scripts", rel))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture, "scripts", rel), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeCargo := "#!/bin/sh\ncp -R '" + reference + "' \"$3\"\n"
	if err := os.WriteFile(filepath.Join(home, ".cargo", "bin", "cargo"), []byte(fakeCargo), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func() (string, error) {
		cmd := exec.Command(filepath.Join(fixture, "scripts", "check-rust-vendor.sh"))
		cmd.Env = canonicalBuildEnv(map[string]string{"HOME": home})
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run(); err != nil {
		t.Fatalf("clean tree rejected: %v: %s", err, out)
	}

	// A checksum file below a crate root is not the crate-root file.
	nested := filepath.Join(vendor, "libc", "src", ".cargo-checksum.json")
	if err := os.WriteFile(nested, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(); err == nil {
		t.Fatalf("nested checksum file accepted: %s", out)
	}
	if err := os.Remove(nested); err != nil {
		t.Fatal(err)
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
