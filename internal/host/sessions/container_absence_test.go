// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessions

import (
	"strings"
	"testing"
)

func TestProveContainerAbsentForDelete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		containerName string
		inventory     string
		wantErr       bool
	}{
		{
			name:          "empty inventory",
			containerName: "workcell-session-fixture",
		},
		{
			name:          "unrelated and near names",
			containerName: "workcell-session-fixture",
			inventory:     "unrelated-container\nworkcell-session-fixture-neighbor",
		},
		{
			name:          "slash-prefixed target",
			containerName: "/workcell-session-fixture",
			inventory:     "unrelated-container",
		},
		{
			name:          "CRLF inventory",
			containerName: "workcell-session-fixture",
			inventory:     "unrelated-container\r\nneighbor-container\r\n",
		},
		{
			name:          "exact name",
			containerName: "workcell-session-fixture",
			inventory:     "workcell-session-fixture",
			wantErr:       true,
		},
		{
			name:          "exact name among near names",
			containerName: "workcell-session-fixture",
			inventory:     "workcell-session-fixture-neighbor\nworkcell-session-fixture",
			wantErr:       true,
		},
		{
			name:          "whitespace line",
			containerName: "workcell-session-fixture",
			inventory:     " ",
			wantErr:       true,
		},
		{
			name:          "one-character line",
			containerName: "workcell-session-fixture",
			inventory:     "a",
			wantErr:       true,
		},
		{
			name:          "slash in observed name",
			containerName: "workcell-session-fixture",
			inventory:     "valid-name\ninvalid/name",
			wantErr:       true,
		},
		{
			name:          "blank interior line",
			containerName: "workcell-session-fixture",
			inventory:     "unrelated-container\n\nneighbor-container",
			wantErr:       true,
		},
		{
			name:          "container ID target",
			containerName: strings.Repeat("a", 64),
			wantErr:       true,
		},
		{
			name:          "non-Workcell target",
			containerName: "unrelated-container",
			wantErr:       true,
		},
		{
			name:          "double slash target",
			containerName: "//workcell-session-fixture",
			wantErr:       true,
		},
		{
			name:          "oversized inventory line",
			containerName: "workcell-session-fixture",
			inventory:     strings.Repeat("a", 64*1024),
			wantErr:       true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ProveContainerAbsentForDelete(test.containerName, test.inventory)
			if test.wantErr && err == nil {
				t.Fatal("ProveContainerAbsentForDelete unexpectedly proved absence")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ProveContainerAbsentForDelete() error = %v", err)
			}
		})
	}
}
