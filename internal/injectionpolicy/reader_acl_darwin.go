// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin && cgo

package injectionpolicy

/*
#include <errno.h>
#include <grp.h>
#include <membership.h>
#include <string.h>
#include <sys/acl.h>
static int policy_no_acl_flags(acl_entry_t entry) {
	acl_flagset_t flags;
	if (acl_get_flagset_np(entry, &flags) != 0) {
		return 0;
	}
	for (unsigned int bit = 1; bit <= (1U << 30); bit <<= 1)
		if (acl_get_flag_np(flags, (acl_flag_t)bit) != 0) {
			return 0;
		}
	return 1;
}
static int policy_standard_deny_acl(int fd) {
	struct group group, *found = NULL;
	char buffer[4096]; uuid_t everyone;
	if (getgrnam_r("everyone", &group, buffer, sizeof(buffer), &found) != 0 || !found || mbr_gid_to_uuid(group.gr_gid, everyone) != 0) {
		return 0;
	}
	acl_t acl = acl_get_fd_np(fd, ACL_TYPE_EXTENDED);
	acl_entry_t entry; acl_tag_t tag; acl_permset_mask_t perms; guid_t *qualifier;
	if (!acl || acl_valid(acl) != 0 || acl_get_entry(acl, ACL_FIRST_ENTRY, &entry) != 0 || acl_get_tag_type(entry, &tag) != 0 || acl_get_permset_mask_np(entry, &perms) != 0 || !(qualifier = acl_get_qualifier(entry))) {
		if (acl) acl_free(acl);
		return 0;
	}
	int safe = tag == ACL_EXTENDED_DENY && perms == ACL_DELETE && policy_no_acl_flags(entry) && memcmp(qualifier, everyone, sizeof(everyone)) == 0;
	acl_free(qualifier);
	if (!safe) {
		acl_free(acl);
		return 0;
	}
	errno = 0;
	safe = acl_get_entry(acl, ACL_NEXT_ENTRY, &entry) == -1 && errno == EINVAL;
	acl_free(acl);
	return safe;
}
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

func rejectPolicyExtendedACL(fd int) error {
	attributes := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buffer [12]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), unix.FSOPT_REPORT_FULLSIZE, 0)
	if errno != 0 {
		return fmt.Errorf("inspect extended ACL: %w", errno)
	}
	if binary.LittleEndian.Uint32(buffer[:4]) < uint32(len(buffer)) {
		return errors.New("inspect extended ACL: malformed attribute response")
	}
	if binary.LittleEndian.Uint32(buffer[8:]) == 0 {
		return nil
	}
	if C.policy_standard_deny_acl(C.int(fd)) != 1 {
		return errors.New("extended ACL is not the permitted deny-only form")
	}
	return nil
}
