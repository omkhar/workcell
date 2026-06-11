// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"strings"
	"testing"
)

func TestRunRejectsMissingSubcommand(t *testing.T) {
	err := run(nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("run(nil) = %v, want usage error", err)
	}
}

func TestRunRejectsUnknownSubcommand(t *testing.T) {
	err := run([]string{"unknown-subcommand"})
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("run(unknown) = %v, want usage error", err)
	}
}

func TestRunRejectsWrongArity(t *testing.T) {
	for _, args := range [][]string{
		{"validate-runtime-mounts", "only-one"},
		{"validate-profile-config", "a", "b"},
	} {
		err := run(args)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("run(%v) = %v, want usage error", args, err)
		}
	}
}
