// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import "testing"

// An extensionless script is shell when its interpreter is, whatever options
// the shebang passes after it.
func TestIsShellSourceReadsTheInterpreter(t *testing.T) {
	t.Parallel()
	for first, want := range map[string]bool{
		"#!/bin/sh -e":            true,
		"#!/usr/bin/env bash -eu": true,
		"#!/bin/bash -p":          true,
		"#!/usr/bin/env -S -i PATH=/usr/bin:/bin BASH_ENV= ENV= /bin/bash": true,
		"#!/usr/bin/env python3": false,
		"#!/usr/bin/fish":        false,
		"no shebang":             false,
	} {
		if got := isShellSource("scripts/tool", []byte(first+"\ngit fetch || true\n")); got != want {
			t.Errorf("isShellSource(%q) = %v, want %v", first, got, want)
		}
	}
}
