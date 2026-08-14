// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injectionpolicy

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestBundleReaderRejectsFileACL(t *testing.T) {
	path := writePolicyFile(t, t.TempDir(), "policy.toml", []byte("version = 1\n"))
	old := rejectPolicyACL
	rejectPolicyACL = func(fd int) error {
		var stat unix.Stat_t
		if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
			return nil
		}
		return errors.New("extended ACLs are not permitted")
	}
	t.Cleanup(func() { rejectPolicyACL = old })
	if _, err := NewBundleReader().Read(path); err == nil || !strings.Contains(err.Error(), "extended ACL") {
		t.Fatalf("Read error = %v, want ACL rejection", err)
	}
}

func TestBundleReaderRejectsMutation(t *testing.T) {
	for _, test := range []struct {
		name              string
		afterConfirmation bool
	}{
		{name: "after confirmation", afterConfirmation: true},
		{name: "after accepted read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writePolicyFile(t, t.TempDir(), "policy.toml", []byte("version = 1\n"))
			reader := NewBundleReader()
			mutate := func() {
				if err := os.WriteFile(path, []byte("version = 2\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.afterConfirmation {
				reader.afterConfirmRead = mutate
			} else {
				reader.beforeFinalStat = func() {
					if err := os.Chmod(path, 0o400); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := reader.Read(path); err == nil || !strings.Contains(err.Error(), "changed while it was read") {
				t.Fatalf("Read error = %v, want mutation rejection", err)
			}
		})
	}
}

func TestBundleReaderConfirmsSecondRead(t *testing.T) {
	path := writePolicyFile(t, t.TempDir(), "policy.toml", []byte("version = 1\n"))
	reader := NewBundleReader()
	reader.beforeConfirmRead = func(file *os.File) { _, _ = file.Seek(1, io.SeekStart) }
	if _, err := reader.Read(path); err == nil || !strings.Contains(err.Error(), "changed while it was read") {
		t.Fatalf("Read error = %v, want confirmation rejection", err)
	}
}

func writePolicyFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBundleReaderRejectsInvalidUTF8(t *testing.T) {
	path := writePolicyFile(t, t.TempDir(), "policy.toml", []byte{'\xff'})
	if _, err := NewBundleReader().Read(path); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("Read error = %v, want UTF-8 rejection", err)
	}
}

func TestBundleReaderReportsCurrentDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "policy")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := writePolicyFile(t, dir, "policy.toml", []byte("version = 1\n"))
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBundleReader().Read(path); err == nil || !strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), path) {
		t.Fatalf("Read error = %v, want current directory %s", err, dir)
	}
}
