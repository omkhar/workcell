// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"reflect"
	"testing"
)

func TestParseSudoCompatArgumentsAcceptsPackageHelper(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		input        []string
		wantCommand  []string
		wantPreserve []string
	}{
		{
			name:        "helper-only",
			input:       []string{sudoCompatHelperPath},
			wantCommand: []string{},
		},
		{
			name:         "separate-preserve",
			input:        []string{"-n", "--preserve-env", "DEBIAN_FRONTEND,APT_LISTCHANGES_FRONTEND", "--", sudoCompatHelperPath, "apt-get", "-qq", "update"},
			wantCommand:  []string{"apt-get", "-qq", "update"},
			wantPreserve: []string{"DEBIAN_FRONTEND", "APT_LISTCHANGES_FRONTEND"},
		},
		{
			name:         "equals-preserve",
			input:        []string{"--preserve-env=DEBCONF_NONINTERACTIVE_SEEN", sudoCompatHelperPath, "apt", "install", "example"},
			wantCommand:  []string{"apt", "install", "example"},
			wantPreserve: []string{"DEBCONF_NONINTERACTIVE_SEEN"},
		},
		{
			name:        "repeated-noninteractive",
			input:       []string{"-n", "-n", sudoCompatHelperPath, "apt-get", "update"},
			wantCommand: []string{"apt-get", "update"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command, preserve, status := parseSudoCompatArguments(testCase.input)
			if status != 0 || !reflect.DeepEqual(command, testCase.wantCommand) || !reflect.DeepEqual(preserve, testCase.wantPreserve) {
				t.Fatalf("parseSudoCompatArguments(%q) = command %q preserve %q status %d, want %q %q 0", testCase.input, command, preserve, status, testCase.wantCommand, testCase.wantPreserve)
			}
		})
	}
}

func TestParseSudoCompatArgumentsRejectsUnsupportedInvocation(t *testing.T) {
	for _, input := range [][]string{
		{"-u", "root", sudoCompatHelperPath},
		{"--socket", "/tmp/attacker", sudoCompatHelperPath},
		{"/bin/sh", "-c", "true"},
		{"--", "/usr/local/libexec/workcell/other-helper.sh"},
		{"--", "apt-get", "update"},
	} {
		_, _, status := parseSudoCompatArguments(input)
		if status != 1 {
			t.Errorf("parseSudoCompatArguments(%q) status = %d, want 1", input, status)
		}
	}
}

func TestParseSudoCompatArgumentsRejectsMalformedSyntax(t *testing.T) {
	for _, input := range [][]string{
		nil,
		{},
		{"-n"},
		{"--"},
		{"--preserve-env"},
		{"--preserve-env", ""},
		{"--preserve-env", "--", sudoCompatHelperPath},
		{"--preserve-env=", sudoCompatHelperPath},
		{"--preserve-env=A", "--preserve-env=B", sudoCompatHelperPath},
	} {
		_, _, status := parseSudoCompatArguments(input)
		if status != 2 {
			t.Errorf("parseSudoCompatArguments(%q) status = %d, want 2", input, status)
		}
	}
}
