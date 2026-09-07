// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"io"
	"sync/atomic"
	"time"
)

type helperConfig struct {
	path    string
	timeout time.Duration
	report  func(error)
}

func fixedEnvironment(environment map[string]string) []string {
	result := []string{"HOME=/root", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so"}
	for _, name := range sortedEnvironment(environment) {
		result = append(result, name+"="+environment[name])
	}
	return result
}

type limitedBuffer struct {
	data     []byte
	overflow atomic.Bool
}

func newLimitedBuffer() *limitedBuffer {
	return &limitedBuffer{data: make([]byte, 0, MaxOutputBytes)}
}
func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := MaxOutputBytes - len(b.data)
	if len(data) > remaining {
		b.data = append(b.data, data[:remaining]...)
		b.overflow.Store(true)
		return len(data), io.ErrShortBuffer
	}
	b.data = append(b.data, data...)
	return len(data), nil
}
func (b *limitedBuffer) bytes() []byte {
	return append([]byte(nil), b.data...)
}
