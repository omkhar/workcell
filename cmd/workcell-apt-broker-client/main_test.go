// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"reflect"
	"testing"
)

func TestCommandArgumentsRemovesOnlySeparator(t *testing.T) {
	for _, testCase := range []struct {
		args []string
		want []string
	}{
		{args: []string{"--", "apt-get", "update"}, want: []string{"apt-get", "update"}},
		{args: []string{"apt-get", "update"}, want: []string{"apt-get", "update"}},
		{args: nil, want: nil},
	} {
		if got := commandArguments(testCase.args); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("commandArguments(%q) = %q, want %q", testCase.args, got, testCase.want)
		}
	}
}

func TestPreservedNamesDropsEmptyFields(t *testing.T) {
	if got, want := preservedNames("DEBIAN_FRONTEND,,APT_LISTCHANGES_FRONTEND"), []string{"DEBIAN_FRONTEND", "APT_LISTCHANGES_FRONTEND"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("preservedNames() = %q, want %q", got, want)
	}
	if got := preservedNames(""); got != nil {
		t.Fatalf("preservedNames(\"\") = %q, want nil", got)
	}
}
