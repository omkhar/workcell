// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package launcher

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processGeneration returns the kernel start-tick generation from procfs.
// Field 22 is stable for a process lifetime and distinguishes PID reuse.
func processGeneration(pid int) (string, error) {
	path := fmt.Sprintf("/proc/%d/stat", pid)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", processGoneErr{pid: pid}
	}
	if err != nil {
		return "", fmt.Errorf("read process %d kernel identity: %w", pid, err)
	}
	// The comm field is parenthesized and may contain spaces or ')' bytes, so
	// split after its final closing parenthesis before indexing fields 3..N.
	commEnd := strings.LastIndex(string(raw), ") ")
	if commEnd < 0 {
		return "", fmt.Errorf("read process %d kernel identity: malformed stat", pid)
	}
	fields := strings.Fields(string(raw[commEnd+2:]))
	const startTimeIndex = 19 // field 22 after fields 1 (pid) and 2 (comm)
	if len(fields) <= startTimeIndex {
		return "", fmt.Errorf("read process %d kernel identity: incomplete stat", pid)
	}
	started, err := strconv.ParseUint(fields[startTimeIndex], 10, 64)
	if err != nil || started == 0 || strconv.FormatUint(started, 10) != fields[startTimeIndex] {
		return "", fmt.Errorf("read process %d kernel identity: invalid start ticks", pid)
	}
	return fmt.Sprintf("linux:%d", started), nil
}
