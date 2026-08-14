// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package injectionpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPolicyACLReaderRejectsUnprovableFilesystem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.toml")
	if err := os.WriteFile(path, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	old := policyFstatfs
	policyFstatfs = func(int, *unix.Statfs_t) error { return errors.New("no filesystem proof") }
	t.Cleanup(func() { policyFstatfs = old })
	if err := rejectPolicyExtendedACL(int(file.Fd())); err == nil || !strings.Contains(err.Error(), "ACL proof") {
		t.Fatalf("rejectPolicyExtendedACL() = %v, want proof rejection", err)
	}
}

func TestPolicyACLReaderRejectsSMBFilesystem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.toml")
	if err := os.WriteFile(path, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, filesystemType := range []int64{cifsSuperMagic, int64(unix.SMB_SUPER_MAGIC), int64(unix.SMB2_SUPER_MAGIC)} {
		old := policyFstatfs
		policyFstatfs = func(_ int, stat *unix.Statfs_t) error {
			stat.Type = filesystemType
			return nil
		}
		err := rejectPolicyExtendedACL(int(file.Fd()))
		policyFstatfs = old
		if err == nil || !strings.Contains(err.Error(), "SMB ACL") {
			t.Fatalf("rejectPolicyExtendedACL() = %v, want SMB ACL rejection", err)
		}
	}
}

func TestPolicyACLReaderRejectsDescriptorXattrs(t *testing.T) {
	path := writePolicyFile(t, t.TempDir(), "policy.toml", []byte("version = 1\n"))
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	oldStat, oldList := policyFstatfs, policyFlistxattr
	policyFstatfs = func(int, *unix.Statfs_t) error { return nil }
	t.Cleanup(func() { policyFstatfs, policyFlistxattr = oldStat, oldList })
	for _, name := range []string{"system.cifs_acl", "system.cifs_ntsd", "system.cifs_ntsd_full", "system.smb3_acl", "security.NTACL"} {
		policyFlistxattr = func(_ int, buffer []byte) (int, error) {
			if buffer == nil {
				return len(name) + 1, nil
			}
			copy(buffer, name+"\x00")
			return len(name) + 1, nil
		}
		if err := rejectPolicyExtendedACL(int(file.Fd())); err == nil || !strings.Contains(err.Error(), "extended ACL") {
			t.Fatalf("rejectPolicyExtendedACL(%q) = %v, want descriptor rejection", name, err)
		}
	}
}

func TestPolicyACLReaderAllowsNonACLXattr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.toml")
	if err := os.WriteFile(path, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := unix.Fsetxattr(int(file.Fd()), "user.workcell", []byte("test"), 0); err != nil {
		t.Skipf("user xattrs are unavailable: %v", err)
	}
	if err := rejectPolicyExtendedACL(int(file.Fd())); err != nil {
		t.Fatalf("rejectPolicyExtendedACL() = %v, want nil", err)
	}
}
