// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package rootio

import "errors"

// ErrExtendedACL reports that a file or directory carries an extended ACL,
// which can grant rights that its mode bits do not show. RejectExtendedACL
// returns it, wrapped, so callers decide how to classify the refusal.
var ErrExtendedACL = errors.New("extended ACLs are not permitted")

func isExtendedACLName(name string) bool {
	switch name {
	case "system.nfs4_acl", "system.posix_acl_access", "system.posix_acl_default", "system.richacl":
		return true
	default:
		return false
	}
}
