// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injectionpolicy

import "strings"

var rejectPolicyACL = rejectPolicyExtendedACL

func isPolicyACLName(name string, allowPOSIX bool) bool {
	switch name {
	case "system.posix_acl_access", "system.posix_acl_default":
		return !allowPOSIX
	case "system.nfs4_acl", "system.richacl", "system.cifs_acl", "system.cifs_ntsd", "system.cifs_ntsd_full", "security.NTACL":
		return true
	default:
		return strings.HasPrefix(name, "system.smb3_")
	}
}
