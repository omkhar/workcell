// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"fmt"
	"testing"
)

func TestRequireMacOS27(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		goos    string
		goarch  string
		version string
		verErr  error
		wantErr bool
	}{
		{name: "arm64 macos 28 ok", goos: "darwin", goarch: "arm64", version: "28.1", wantErr: false},
		{name: "arm64 macos 27 ok", goos: "darwin", goarch: "arm64", version: "27.0", wantErr: false},
		{name: "arm64 macos 26 fails", goos: "darwin", goarch: "arm64", version: "26.5.1", wantErr: true},
		{name: "intel macos 26 fails", goos: "darwin", goarch: "amd64", version: "26.5.1", wantErr: true},
		{name: "intel macos 27 fails", goos: "darwin", goarch: "amd64", version: "27.0", wantErr: true},
		{name: "arm64 macos 25 fails", goos: "darwin", goarch: "arm64", version: "25.5.0", wantErr: true},
		{name: "arm64 macos 15 fails", goos: "darwin", goarch: "arm64", version: "15.4", wantErr: true},
		{name: "linux arm64 fails", goos: "linux", goarch: "arm64", version: "27.0", wantErr: true},
		{name: "linux amd64 fails", goos: "linux", goarch: "amd64", version: "27.0", wantErr: true},
		{name: "windows fails", goos: "windows", goarch: "amd64", version: "27.0", wantErr: true},
		{name: "arm64 unparseable version fails", goos: "darwin", goarch: "arm64", version: "sequoia", wantErr: true},
		{name: "arm64 sw_vers error fails", goos: "darwin", goarch: "arm64", verErr: fmt.Errorf("boom"), wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := requireMacOS27(tc.goos, tc.goarch, func() (string, error) {
				return tc.version, tc.verErr
			})
			if tc.wantErr && err == nil {
				t.Fatalf("requireMacOS27(%q, %q, %q) = nil, want error", tc.goos, tc.goarch, tc.version)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("requireMacOS27(%q, %q, %q) = %v, want nil", tc.goos, tc.goarch, tc.version, err)
			}
		})
	}
}

func TestCheckHostMapsLauncherHostFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		hostOS, hostArch, version string
		wantErr                   bool
	}{
		{"macos", "arm64", "27.0.1", false},
		{"macos", "arm64", "26.5.1", true},
		{"macos", "amd64", "27.0.1", true},
		{"macos", "arm64", "", true},
		{"linux", "arm64", "27.0", true},
	}
	for _, tc := range cases {
		err := CheckHost(tc.hostOS, tc.hostArch, tc.version)
		if (err != nil) != tc.wantErr {
			t.Errorf("CheckHost(%q, %q, %q) = %v, wantErr %v", tc.hostOS, tc.hostArch, tc.version, err, tc.wantErr)
		}
	}
}
