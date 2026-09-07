// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"bytes"
	"slices"
	"testing"
)

func TestFixedEnvironmentIsMinimalAndOrdered(t *testing.T) {
	got := fixedEnvironment(map[string]string{
		"DEBIAN_FRONTEND":          "noninteractive",
		"APT_LISTCHANGES_FRONTEND": "none",
	})
	want := []string{
		"HOME=/root",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C",
		"LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so",
		"APT_LISTCHANGES_FRONTEND=none",
		"DEBIAN_FRONTEND=noninteractive",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("fixed environment = %q, want %q", got, want)
	}
}

func TestLimitedBufferRetainsBoundedPrefix(t *testing.T) {
	buffer := newLimitedBuffer()
	input := bytes.Repeat([]byte("x"), MaxOutputBytes+1)
	written, err := buffer.Write(input)
	if err == nil || written != len(input) || !buffer.overflow.Load() {
		t.Fatalf("Write() = (%d, %v), overflow=%t", written, err, buffer.overflow.Load())
	}
	if got := len(buffer.bytes()); got != MaxOutputBytes {
		t.Fatalf("retained bytes = %d, want %d", got, MaxOutputBytes)
	}
}
