// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestReleaseUsageOmitsLegacyHelperCommands(t *testing.T) {
	err := runRelease([]string{"create-payload"})
	var exitErr *cliexit.ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("legacy release command error = %v, want usage exit code 2", err)
	}
	for _, command := range []string{
		"create-payload", "publish-payload", "validate-repository",
		"validate-draft-release", "validate-published-state",
		"validate-latest-response", "list-page-size",
		"select-listed-release", "metadata", "encode-name",
	} {
		if strings.Contains(exitErr.Message, command) {
			t.Errorf("release usage still advertises legacy command %q", command)
		}
	}
}
