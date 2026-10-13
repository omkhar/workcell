// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package rootio

import (
	"errors"
	"os/exec"
	"testing"
)

func TestRejectExtendedACLDarwin(t *testing.T) {
	assertRejectExtendedACL(t, func(path string) error {
		output, err := exec.Command("/bin/chmod", "+a", "everyone deny write", path).CombinedOutput()
		if err != nil {
			return errors.Join(err, errors.New(string(output)))
		}
		return nil
	})
}
