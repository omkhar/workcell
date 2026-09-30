// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/cliexit"
)

func TestRunFailsClosedOnBadConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared")
	if err := os.WriteFile(shared, []byte("a.example:5432\nb.example:5432\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		args []string
		want string
	}{
		"no allowlist":      {nil, "-allowlist is required"},
		"missing file":      {[]string{"-allowlist", filepath.Join(dir, "missing")}, "no such file"},
		"shared plain port": {[]string{"-allowlist", shared}, "more than one host"},
		"unknown flag":      {[]string{"-bogus"}, "flag provided but not defined"},
	}
	for name, tc := range cases {
		err := run(tc.args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: run error = %v, want %q", name, err, tc.want)
		}
		if ec, ok := cliexit.IsExitCodeError(err); !ok || ec.Code != 2 {
			t.Errorf("%s: run error = %v, want exit code 2", name, err)
		}
	}
}

func TestRunReportsHelpAsErrHelp(t *testing.T) {
	t.Parallel()
	if err := run([]string{"-h"}, io.Discard, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run(-h) error = %v, want flag.ErrHelp", err)
	}
}
