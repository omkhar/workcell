// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package cliexit carries an explicit os.Exit code through Go's error
// chain so a wrapper main() can preserve a specific exit code instead
// of always returning 1.  Originally introduced for the bash→Go CLI
// translation work (each translated main returns ExitCodeError so the
// shell-level contract survives), now the canonical exit-code type
// for every workcell Go CLI — including binaries (cmd/workcell-citools)
// that have no bash predecessor.
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
