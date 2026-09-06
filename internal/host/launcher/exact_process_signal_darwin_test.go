// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package launcher

import (
	"testing"
	"unsafe"
)

func TestDarwinAuditTokenSignalVersionGate(t *testing.T) {
	tests := map[string]bool{
		"22.9.0": false,
		"23.1.0": false,
		"23.2.0": true,
		"24.0.0": true,
		"23":     false,
		"x.2.0":  false,
		"23.-1":  false,
	}
	for release, want := range tests {
		if got := darwinReleaseSupportsAuditTokenSignal(release); got != want {
			t.Errorf("darwinReleaseSupportsAuditTokenSignal(%q) = %t, want %t", release, got, want)
		}
	}
}

func TestDarwinSignalIdentityABILayout(t *testing.T) {
	var identity darwinUniqueIdentifierInfo
	if got, want := unsafe.Sizeof(identity), uintptr(56); got != want {
		t.Fatalf("darwinUniqueIdentifierInfo size = %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(identity.idVersion), uintptr(32); got != want {
		t.Fatalf("idVersion offset = %d, want %d", got, want)
	}
	handle := darwinAuditTokenSignalHandle{pid: 42}
	handle.token[5] = uint32(handle.pid)
	handle.token[7] = identity.idVersion
	if handle.token[5] != 42 || handle.token[7] != identity.idVersion {
		t.Fatalf("audit token identity slots = %#v", handle.token)
	}
}
