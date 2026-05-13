// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateColimaStatusOutputAcceptsExpectedStatus(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{
		"INFO[0000] colima profile workcell-test is running",
		"runtime: docker",
		"vmType: vz",
		"mountType: virtiofs",
		"arch: aarch64",
		"Using Virtualization.Framework",
	}, "\n")
	if err := ValidateColimaStatusOutput(status, "workcell-test"); err != nil {
		t.Fatalf("ValidateColimaStatusOutput() err = %v, want nil", err)
	}
}

func TestValidateColimaStatusOutputDetectsMissingVZ(t *testing.T) {
	t.Parallel()
	status := "runtime: docker\nmountType: virtiofs\n"
	err := ValidateColimaStatusOutput(status, "workcell-test")
	if err == nil {
		t.Fatal("ValidateColimaStatusOutput() err = nil, want missing Virtualization.Framework error")
	}
	if !strings.Contains(err.Error(), "Virtualization.Framework") {
		t.Fatalf("error %q does not mention Virtualization.Framework", err.Error())
	}
	if !strings.Contains(err.Error(), "workcell-test") {
		t.Fatalf("error %q does not mention the profile name", err.Error())
	}
}

func TestValidateColimaStatusOutputDetectsMissingVirtiofs(t *testing.T) {
	t.Parallel()
	status := "Virtualization.Framework\nruntime: docker\n"
	err := ValidateColimaStatusOutput(status, "workcell-test")
	if err == nil {
		t.Fatal("ValidateColimaStatusOutput() err = nil, want missing virtiofs error")
	}
	if !strings.Contains(err.Error(), "virtiofs") {
		t.Fatalf("error %q does not mention virtiofs", err.Error())
	}
}

func TestValidateColimaStatusOutputDetectsMissingDockerRuntime(t *testing.T) {
	t.Parallel()
	status := "Virtualization.Framework\nmountType: virtiofs\nruntime: containerd\n"
	err := ValidateColimaStatusOutput(status, "workcell-test")
	if err == nil {
		t.Fatal("ValidateColimaStatusOutput() err = nil, want missing docker runtime error")
	}
	if !strings.Contains(err.Error(), "Docker runtime") {
		t.Fatalf("error %q does not mention Docker runtime", err.Error())
	}
}

func TestValidateColimaStatusOutputRequiresProfileName(t *testing.T) {
	t.Parallel()
	if err := ValidateColimaStatusOutput("anything", ""); err == nil {
		t.Fatal("ValidateColimaStatusOutput() err = nil, want profile-required error")
	}
}

func TestRunHostColimaReturnsZeroForEmptyArgs(t *testing.T) {
	t.Parallel()
	code, err := RunHostColima(HostColimaInvocation{})
	if err != nil {
		t.Fatalf("RunHostColima() err = %v", err)
	}
	if code != 0 {
		t.Fatalf("RunHostColima() code = %d, want 0", code)
	}
}

func TestRunHostColimaRequiresColimaBin(t *testing.T) {
	t.Parallel()
	_, err := RunHostColima(HostColimaInvocation{Args: []string{"list"}})
	if err == nil {
		t.Fatal("RunHostColima() err = nil, want colima-bin-required error")
	}
}

func TestRunHostColimaForwardsArgsAndPropagatesExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX /bin/sh helpers")
	}
	// Intentionally serial: writes-then-execs a script on disk. Running
	// these in parallel races against the Linux kernel's ETXTBSY check
	// when concurrent goroutines exec freshly-written executables.
	dir := t.TempDir()
	fake := writeFakeColima(t, dir, `#!/bin/sh
echo "argc=$#"
printf '%s\n' "$@"
echo "HOME=$HOME"
echo "COLIMA_HOME=$COLIMA_HOME"
exit 7
`)

	code, err := RunHostColima(HostColimaInvocation{
		ColimaBin:  fake,
		RealHome:   dir,
		ColimaHome: filepath.Join(dir, "state"),
		CWD:        dir,
		Args:       []string{"list", "--json"},
	})
	if err != nil {
		t.Fatalf("RunHostColima() err = %v", err)
	}
	if code != 7 {
		t.Fatalf("RunHostColima() code = %d, want 7", code)
	}
}

func TestRunHostColimaFallsBackToRootWhenCWDMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX /bin/sh helpers")
	}
	// Serial — see ETXTBSY note on TestRunHostColimaForwardsArgsAndPropagatesExitCode.
	dir := t.TempDir()
	fake := writeFakeColima(t, dir, `#!/bin/sh
pwd
exit 0
`)
	code, err := RunHostColima(HostColimaInvocation{
		ColimaBin:  fake,
		RealHome:   filepath.Join(dir, "does-not-exist"),
		ColimaHome: dir,
		CWD:        filepath.Join(dir, "also-missing"),
		Args:       []string{"version"},
	})
	if err != nil {
		t.Fatalf("RunHostColima() err = %v", err)
	}
	if code != 0 {
		t.Fatalf("RunHostColima() code = %d, want 0", code)
	}
}

func TestRunHostColimaWithTimeoutNoTimeoutDelegates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX /bin/sh helpers")
	}
	// Serial — see ETXTBSY note on TestRunHostColimaForwardsArgsAndPropagatesExitCode.
	dir := t.TempDir()
	fake := writeFakeColima(t, dir, `#!/bin/sh
exit 11
`)
	code, err := RunHostColimaWithTimeout(0, HostColimaInvocation{
		ColimaBin: fake,
		RealHome:  dir,
		Args:      []string{"list"},
	})
	if err != nil {
		t.Fatalf("RunHostColimaWithTimeout() err = %v", err)
	}
	if code != 11 {
		t.Fatalf("RunHostColimaWithTimeout() code = %d, want 11", code)
	}
}

func TestRunHostColimaWithTimeoutKillsRunawayChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX /bin/sh helpers")
	}
	// Serial — see ETXTBSY note on TestRunHostColimaForwardsArgsAndPropagatesExitCode.
	dir := t.TempDir()
	fake := writeFakeColima(t, dir, `#!/bin/sh
# Sleep well past the test deadline to force the timeout path.
sleep 30
exit 0
`)
	start := time.Now()
	code, err := RunHostColimaWithTimeout(1, HostColimaInvocation{
		ColimaBin: fake,
		RealHome:  dir,
		Args:      []string{"start"},
	})
	if err != nil {
		t.Fatalf("RunHostColimaWithTimeout() err = %v", err)
	}
	if code != ColimaTimeoutExitCode {
		t.Fatalf("RunHostColimaWithTimeout() code = %d, want %d", code, ColimaTimeoutExitCode)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("RunHostColimaWithTimeout() took %s, expected to honour 1s deadline", elapsed)
	}
}

func TestRunHostColimaWithTimeoutReturnsExitCodeWhenFastEnough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX /bin/sh helpers")
	}
	// Serial — see ETXTBSY note on TestRunHostColimaForwardsArgsAndPropagatesExitCode.
	dir := t.TempDir()
	fake := writeFakeColima(t, dir, `#!/bin/sh
exit 0
`)
	code, err := RunHostColimaWithTimeout(30, HostColimaInvocation{
		ColimaBin: fake,
		RealHome:  dir,
		Args:      []string{"list"},
	})
	if err != nil {
		t.Fatalf("RunHostColimaWithTimeout() err = %v", err)
	}
	if code != 0 {
		t.Fatalf("RunHostColimaWithTimeout() code = %d, want 0", code)
	}
}

func writeFakeColima(t *testing.T, dir, script string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-colima")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake colima: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake colima: %v", err)
	}
	return path
}
