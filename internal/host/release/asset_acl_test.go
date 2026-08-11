// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package release

import "testing"

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
