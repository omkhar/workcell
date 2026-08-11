// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package release

func isExtendedACLName(name string) bool {
	switch name {
	case "system.nfs4_acl", "system.posix_acl_access", "system.posix_acl_default", "system.richacl":
		return true
	default:
		return false
	}
}
