// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package release

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

func rejectExtendedACL(fd int) error {
	for attempt := 0; attempt < 3; attempt++ {
		size, err := unix.Flistxattr(fd, nil)
		if err != nil {
			return fmt.Errorf("list extended attributes for ACL validation: %w", err)
		}
		if size == 0 {
			return nil
		}
		if size > 64*1024 {
			return errors.New("list extended attributes for ACL validation: oversized response")
		}
		buffer := make([]byte, size)
		read, err := unix.Flistxattr(fd, buffer)
		if err == unix.ERANGE {
			continue
		}
		if err != nil {
			return fmt.Errorf("read extended attributes for ACL validation: %w", err)
		}
		for _, name := range strings.Split(string(buffer[:read]), "\x00") {
			if isExtendedACLName(name) {
				return inputErrorf("extended ACLs are not permitted")
			}
		}
		return nil
	}
	return errors.New("read extended attributes for ACL validation: attribute list changed repeatedly")
}
