// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// MinimumMajorVersion is the lowest macOS major version the Apple `container`
// backend targets. C1 fails closed below it.
const MinimumMajorVersion = 27

// RequireMacOS27 fails closed unless the host is Apple Silicon (arm64) running
// macOS 27 or newer — the exact apple-container support constraint (see
// docs/apple-container-evaluation.md and policy/host-support-matrix.tsv). On any
// other OS, on Intel (amd64), or on macOS below 27, it returns an error rather
// than proceeding.
func RequireMacOS27() error {
	return requireMacOS27(runtime.GOOS, runtime.GOARCH, swVersProductVersion)
}

// requireMacOS27 is the testable core of RequireMacOS27: goos/goarch are the
// runtime OS/architecture and productVersion reports the macOS product version
// string (e.g. "27.0.1"). It checks the architecture BEFORE the version so an
// unsupported host is rejected without shelling out to sw_vers.
func requireMacOS27(goos, goarch string, productVersion func() (string, error)) error {
	if goos != "darwin" {
		return fmt.Errorf("apple container requires macOS %d+, host OS is %q", MinimumMajorVersion, goos)
	}
	if goarch != "arm64" {
		return fmt.Errorf("apple container requires Apple Silicon (arm64), host arch is %q", goarch)
	}
	version, err := productVersion()
	if err != nil {
		return fmt.Errorf("apple container requires macOS %d+: %w", MinimumMajorVersion, err)
	}
	major, err := majorVersion(version)
	if err != nil {
		return fmt.Errorf("apple container requires macOS %d+: %w", MinimumMajorVersion, err)
	}
	if major < MinimumMajorVersion {
		return fmt.Errorf("apple container requires macOS %d+, host is macOS %s", MinimumMajorVersion, version)
	}
	return nil
}

// majorVersion parses the leading integer of a dotted version string.
func majorVersion(version string) (int, error) {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return 0, fmt.Errorf("empty macOS product version")
	}
	head, _, _ := strings.Cut(trimmed, ".")
	major, err := strconv.Atoi(strings.TrimSpace(head))
	if err != nil {
		return 0, fmt.Errorf("unparseable macOS product version %q", version)
	}
	return major, nil
}

// swVersProductVersion shells out to `sw_vers -productVersion`.
func swVersProductVersion() (string, error) {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return "", fmt.Errorf("sw_vers -productVersion: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
