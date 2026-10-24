// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package hoststate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnsureOwnerOnlyDirectoryRejectsExtendedACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acl")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/chmod", "+a", "everyone allow list,search", dir).CombinedOutput(); err != nil {
		t.Fatalf("chmod +a: %v: %s", err, output)
	}
	if err := EnsureOwnerOnlyDirectory(dir); err == nil {
		t.Fatal("a 0700 directory with an extended ACL was accepted")
	}
}
