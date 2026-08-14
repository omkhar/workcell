// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package injectionpolicy

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

const cifsSuperMagic = 0xff534d42

var (
	policyFstatfs    = unix.Fstatfs
	policyFlistxattr = unix.Flistxattr
)

func rejectPolicyExtendedACL(fd int) error {
	var filesystem unix.Statfs_t
	if err := policyFstatfs(fd, &filesystem); err != nil {
		return fmt.Errorf("identify filesystem for ACL proof: %w", err)
	}
	if isSMBFilesystem(int64(filesystem.Type)) {
		return errors.New("cannot prove absence of SMB ACL descriptors")
	}
	for attempt := 0; attempt < 3; attempt++ {
		size, err := policyFlistxattr(fd, nil)
		if err != nil {
			return fmt.Errorf("list extended attributes: %w", err)
		}
		if size == 0 {
			return nil
		}
		if size > 64*1024 {
			return errors.New("list extended attributes: oversized response")
		}
		buffer := make([]byte, size)
		read, err := policyFlistxattr(fd, buffer)
		if err == unix.ERANGE {
			continue
		}
		if err != nil {
			return fmt.Errorf("read extended attributes: %w", err)
		}
		for _, name := range strings.Split(string(buffer[:read]), "\x00") {
			if isPolicyACLName(name) {
				return errors.New("extended ACLs are not permitted")
			}
		}
		return nil
	}
	return errors.New("read extended attributes: attribute list changed repeatedly")
}

func isSMBFilesystem(filesystemType int64) bool {
	return filesystemType == cifsSuperMagic || filesystemType == int64(unix.SMB_SUPER_MAGIC) || filesystemType == int64(unix.SMB2_SUPER_MAGIC)
}
