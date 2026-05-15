// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package cliexit is the canonical exit-code error type used by all
// workcell Go CLI translations. Each translated bash main returns
// ExitCodeError when the wrapper main() should propagate a specific
// exit code; cmd/workcell-hostutil/main.go (and the sibling umbrella
// binaries) match it with errors.As and propagate the Code.
package cliexit

import "errors"

// ExitCodeError carries the bash exit-code contract through to the
// process wrapper.  Code is the os.Exit code to emit; Message, when
// non-empty, is written to stderr.
type ExitCodeError struct {
	Code    int
	Message string
}

func (e *ExitCodeError) Error() string { return e.Message }

// IsExitCodeError reports whether err is or wraps an *ExitCodeError.
// If so, the returned non-nil pointer is the unwrapped `*ExitCodeError`.
func IsExitCodeError(err error) (*ExitCodeError, bool) {
	var ec *ExitCodeError
	if errors.As(err, &ec) {
		return ec, true
	}
	return nil, false
}
