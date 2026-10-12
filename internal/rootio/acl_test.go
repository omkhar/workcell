// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux || darwin

package rootio

import (
	"errors"
	"os"
	"testing"
)

func TestExtendedACLNamePolicy(t *testing.T) {
	for _, name := range []string{"system.nfs4_acl", "system.posix_acl_access", "system.posix_acl_default", "system.richacl"} {
		if !isExtendedACLName(name) {
			t.Fatalf("isExtendedACLName(%q) = false", name)
		}
	}
	if isExtendedACLName("user.workcell") {
		t.Fatal("isExtendedACLName(user.workcell) = true")
	}
}

// assertRejectExtendedACL checks a plain directory passes and the same
// directory fails once addACL gives it an extended ACL.
func assertRejectExtendedACL(t *testing.T, addACL func(path string) error) {
	t.Helper()
	directory := t.TempDir()
	file, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := RejectExtendedACL(int(file.Fd())); err != nil {
		t.Fatalf("RejectExtendedACL(plain) = %v", err)
	}
	if err := addACL(directory); err != nil {
		t.Fatal(err)
	}
	if err := RejectExtendedACL(int(file.Fd())); !errors.Is(err, ErrExtendedACL) {
		t.Fatalf("RejectExtendedACL(acl) = %v, want ErrExtendedACL", err)
	}
}
