// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package auditlog provides bounded streaming reads for the shared host audit
// log. The limits are intentionally generous for normal session history while
// preventing an attacker-controlled log from forcing an unbounded allocation.
package auditlog

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// MaxLineBytes is the maximum physical audit-log line size, including its
// optional newline terminator. Session audit records are normally far smaller.
const MaxLineBytes = 1 << 20

// MaxLogBytes is the maximum audit-log size processed by one operation. This
// preserves the existing complete-log semantics while bounding total work.
const MaxLogBytes = 64 << 20

// ForEachLine streams a file to fn without reading the whole file into memory.
// It returns an error when the file exceeds either documented audit-log limit.
func ForEachLine(path string, fn func(line string, lineNumber int) error) error {
	file, err := openRegularSingleLink(path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64<<10)
	var line []byte
	var total int64
	lineNumber := 0
	for {
		fragment, readErr := reader.ReadSlice('\n')
		total += int64(len(fragment))
		if total > MaxLogBytes {
			return fmt.Errorf("auditlog: %s exceeds maximum size of %d bytes", path, MaxLogBytes)
		}
		if len(line)+len(fragment) > MaxLineBytes {
			return fmt.Errorf("auditlog: %s line %d exceeds maximum size of %d bytes", path, lineNumber+1, MaxLineBytes)
		}
		line = append(line, fragment...)
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if len(line) > 0 {
			lineNumber++
			if err := fn(string(line), lineNumber); err != nil {
				return err
			}
			line = nil
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
