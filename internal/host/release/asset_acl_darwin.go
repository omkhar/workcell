// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package release

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

func rejectExtendedACL(fd int) error {
	attributes := unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buffer [12]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attributes)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), unix.FSOPT_REPORT_FULLSIZE, 0)
	if errno != 0 {
		return fmt.Errorf("inspect extended ACL: %w", errno)
	}
	if binary.LittleEndian.Uint32(buffer[:4]) < uint32(len(buffer)) {
		return errors.New("inspect extended ACL: malformed attribute response")
	}
	if binary.LittleEndian.Uint32(buffer[8:]) != 0 {
		return inputErrorf("extended ACLs are not permitted")
	}
	return nil
}
