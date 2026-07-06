// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"fmt"
	"testing"
)

func TestRequireMacOS26(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		goos    string
		version string
		verErr  error
		wantErr bool
	}{
		{name: "macos 26 ok", goos: "darwin", version: "26.5.1", wantErr: false},
		{name: "macos 27 ok", goos: "darwin", version: "27.0", wantErr: false},
		{name: "macos 25 fails", goos: "darwin", version: "25.5.0", wantErr: true},
		{name: "macos 15 fails", goos: "darwin", version: "15.4", wantErr: true},
		{name: "linux fails", goos: "linux", version: "26.0", wantErr: true},
		{name: "windows fails", goos: "windows", version: "26.0", wantErr: true},
		{name: "unparseable version fails", goos: "darwin", version: "sequoia", wantErr: true},
		{name: "sw_vers error fails", goos: "darwin", verErr: fmt.Errorf("boom"), wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := requireMacOS26(tc.goos, func() (string, error) {
				return tc.version, tc.verErr
			})
			if tc.wantErr && err == nil {
				t.Fatalf("requireMacOS26(%q, %q) = nil, want error", tc.goos, tc.version)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("requireMacOS26(%q, %q) = %v, want nil", tc.goos, tc.version, err)
			}
		})
	}
}
