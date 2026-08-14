// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package injectionpolicy

import (
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPolicyACLReaderACLProof(t *testing.T) {
	path := writePolicyFile(t, t.TempDir(), "policy.toml", []byte("version = 1\n"))
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	oldUser, oldStat, oldList := policyCurrentEUID, policyFstatfs, policyFlistxattr
	policyCurrentEUID = func() uint32 { return 1000 }
	policyFstatfs = func(_ int, stat *unix.Statfs_t) error { stat.Type = unix.OVERLAYFS_SUPER_MAGIC; return nil }
	t.Cleanup(func() { policyCurrentEUID, policyFstatfs, policyFlistxattr = oldUser, oldStat, oldList })
	fixture := func(name string) {
		policyFlistxattr = func(_ int, buffer []byte) (int, error) {
			if buffer != nil {
				copy(buffer, name+"\x00")
			}
			return len(name) + 1, nil
		}
	}
	for _, test := range []struct {
		name, attribute string
		stat            unix.Stat_t
		reject          bool
	}{
		{"home access ACL", "system.posix_acl_access", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, false},
		{"home default ACL", "system.posix_acl_default", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, false},
		{"normal xattr", "user.workcell", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, false},
		{"effective write", "system.posix_acl_access", unix.Stat_t{Mode: unix.S_IFDIR | 0o775, Uid: 0, Ino: 1}, true},
		{"NFS descriptor", "system.nfs4_acl", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, true},
		{"Rich ACL", "system.richacl", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, true},
		{"CIFS descriptor", "system.cifs_acl", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, true},
		{"CIFS NTSD", "system.cifs_ntsd", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, true},
		{"CIFS security descriptor", "system.cifs_ntsd_full", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, true},
		{"SMB descriptor", "security.NTACL", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, true},
		{"SMB3 descriptor", "system.smb3_acl", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, true},
		{"current-user ancestor", "system.posix_acl_access", unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: 1000, Ino: 1}, true},
		{"sticky transit", "system.posix_acl_access", unix.Stat_t{Mode: unix.S_IFDIR | unix.S_ISVTX | 0o777, Uid: 0, Ino: 1}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture(test.attribute)
			_, err := validatePolicyDirectory(int(file.Fd()), "/home", test.stat, false)
			if (err != nil) != test.reject {
				t.Fatalf("validatePolicyDirectory() = %v, want rejection=%t", err, test.reject)
			}
		})
	}
	policyFstatfs = func(_ int, stat *unix.Statfs_t) error { stat.Type = unix.FUSE_SUPER_MAGIC; return nil }
	fixture("system.posix_acl_access")
	if _, err := validatePolicyDirectory(int(file.Fd()), "/home", unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}, false); err == nil {
		t.Fatal("validatePolicyDirectory accepted a POSIX ACL on FUSE")
	}
	for _, filesystemType := range []int64{int64(unix.EXT2_SUPER_MAGIC), int64(unix.XFS_SUPER_MAGIC), int64(unix.BTRFS_SUPER_MAGIC), int64(unix.TMPFS_MAGIC), int64(unix.OVERLAYFS_SUPER_MAGIC)} {
		if !isLocalPOSIXACLFilesystem(filesystemType) {
			t.Fatal("local POSIX ACL filesystem was rejected")
		}
	}
	policyFstatfs = func(int, *unix.Statfs_t) error { return errors.New("no filesystem proof") }
	if err := rejectPolicyExtendedACL(int(file.Fd()), false); err == nil || !strings.Contains(err.Error(), "ACL proof") {
		t.Fatalf("rejectPolicyExtendedACL() = %v, want proof rejection", err)
	}
	for _, filesystemType := range []int64{int64(unix.NFS_SUPER_MAGIC), cifsSuperMagic, int64(unix.SMB_SUPER_MAGIC), int64(unix.SMB2_SUPER_MAGIC)} {
		policyFstatfs = func(_ int, stat *unix.Statfs_t) error { stat.Type = filesystemType; return nil }
		if err := rejectPolicyExtendedACL(int(file.Fd()), false); err == nil || !strings.Contains(err.Error(), "ACL descriptors") {
			t.Fatalf("rejectPolicyExtendedACL() = %v, want filesystem ACL rejection", err)
		}
	}
}
