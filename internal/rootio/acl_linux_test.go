// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package rootio

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func TestRejectExtendedACLLinux(t *testing.T) {
	if _, err := exec.LookPath("setfacl"); err != nil {
		if os.Getenv("WORKCELL_REQUIRE_NATIVE_ACL_TESTS") == "1" {
			t.Fatalf("setfacl is required for native Linux ACL tests: %v", err)
		}
		t.Skip("setfacl is not installed")
	}
	entry := "u:" + strconv.FormatUint(uint64(os.Geteuid())+1, 10) + ":r-x"
	assertRejectExtendedACL(t, func(path string) error {
		output, err := exec.Command("setfacl", "-n", "-m", entry, path).CombinedOutput()
		if err != nil {
			return errors.Join(err, errors.New(string(output)))
		}
		return nil
	})
}
