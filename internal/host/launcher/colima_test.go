// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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

func TestRunHostColimaRequiresAbsoluteColimaBin(t *testing.T) {
	t.Parallel()
	_, err := RunHostColima(HostColimaInvocation{ColimaBin: "colima", Args: []string{"list"}})
	if err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("RunHostColima() err = %v, want absolute-path rejection", err)
	}
	_, err = RunHostColimaWithTimeout(1, HostColimaInvocation{ColimaBin: "colima", Args: []string{"start"}})
	if err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("RunHostColimaWithTimeout() err = %v, want absolute-path rejection", err)
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

func TestRunHostColimaPreservesSignalExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX signal semantics")
	}
	dir := t.TempDir()
	fake := writeFakeColima(t, dir, `#!/bin/sh
kill -TERM $$
`)
	code, err := RunHostColima(HostColimaInvocation{
		ColimaBin: fake,
		RealHome:  dir,
		CWD:       dir,
		Args:      []string{"start"},
	})
	if err != nil {
		t.Fatalf("RunHostColima() err = %v", err)
	}
	want := 128 + int(syscall.SIGTERM)
	if code != want {
		t.Fatalf("RunHostColima() code = %d, want %d", code, want)
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

func TestRunHostColimaWithTimeoutRejectsMoreThanOneDay(t *testing.T) {
	t.Parallel()
	_, err := RunHostColimaWithTimeout(24*60*60+1, HostColimaInvocation{
		ColimaBin: "/bin/true",
		Args:      []string{"list"},
	})
	if err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("RunHostColimaWithTimeout() err = %v, want timeout-cap rejection", err)
	}
}

func TestRunHostColimaWithTimeoutKillsRunawayChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX /bin/sh helpers")
	}
	// Serial — see ETXTBSY note on TestRunHostColimaForwardsArgsAndPropagatesExitCode.
	for _, tc := range []struct {
		name   string
		setup  string
		minRun time.Duration
		maxRun time.Duration
	}{
		{"TERM-responsive", ":", 0, 4 * time.Second},
		{"TERM-resistant", "trap '' TERM", 4750 * time.Millisecond, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "processes")
			fake := writeFakeColima(t, dir, "#!/bin/sh\n"+tc.setup+`
sh -c "`+tc.setup+`; while :; do sleep 1; done" &
child=$!
pgid=$(ps -o pgid= -p $$ | tr -d ' ')
printf '%s %s\n' "$pgid" "$child" >"$2"
wait "$child"
`)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			go cancelWhenFileExists(ctx, cancel, marker)
			start := time.Now()
			code, err := runHostColimaWithContext(ctx, HostColimaInvocation{
				ColimaBin: fake,
				RealHome:  dir,
				Args:      []string{"start", marker},
			})
			if err != nil || code != ColimaTimeoutExitCode {
				t.Fatalf("runHostColimaWithContext() = %d, %v", code, err)
			}
			if elapsed := time.Since(start); elapsed < tc.minRun || elapsed > tc.maxRun {
				t.Fatalf("process-group cleanup completed in %s", elapsed)
			}
			fields := strings.Fields(string(mustReadFile(t, marker)))
			if len(fields) != 2 {
				t.Fatalf("process marker = %q", fields)
			}
			groupID, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(-groupID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("process group %d still exists: %v", groupID, err)
			}
		})
	}
}

func TestColimaCancellationResultPreservesOutcomes(t *testing.T) {
	termErr := processSignalError(t, syscall.SIGTERM)
	killErr := processSignalError(t, syscall.SIGKILL)
	interruptErr := processSignalError(t, syscall.SIGINT)
	exitErr := exec.Command("/bin/sh", "-c", "exit 7").Run()
	plainErr := errors.New("write failed")
	cleanupErr := errors.New("group remains")
	for _, tc := range []struct {
		name       string
		runErr     error
		cleanupErr error
		wantCode   int
		wantErrors []error
	}{
		{"nil", nil, nil, 124, nil},
		{"deadline", context.DeadlineExceeded, nil, 124, nil},
		{"TERM", termErr, nil, 124, nil},
		{"KILL", killErr, nil, 124, nil},
		{"exit 7", exitErr, nil, 7, nil},
		{"SIGINT", interruptErr, nil, 128 + int(syscall.SIGINT), nil},
		{"I/O error", plainErr, nil, 0, []error{plainErr}},
		{"cleanup", termErr, cleanupErr, 0, []error{termErr, cleanupErr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, err := colimaCancellationResult(tc.runErr, tc.cleanupErr)
			if code != tc.wantCode || (len(tc.wantErrors) == 0 && err != nil) {
				t.Fatalf("colimaCancellationResult() = %d, %v", code, err)
			}
			for _, wantErr := range tc.wantErrors {
				if !errors.Is(err, wantErr) {
					t.Fatalf("colimaCancellationResult() err = %v, want %v", err, wantErr)
				}
			}
		})
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
	runaway := writeFakeColima(t, dir, "#!/bin/sh\nsleep 30\n")
	start := time.Now()
	code, err = RunHostColimaWithTimeout(1, HostColimaInvocation{
		ColimaBin: runaway,
		RealHome:  dir,
		Args:      []string{"start"},
	})
	if err != nil || code != ColimaTimeoutExitCode {
		t.Fatalf("RunHostColimaWithTimeout(runaway) = %d, %v", code, err)
	}
	if elapsed := time.Since(start); elapsed < 750*time.Millisecond || elapsed > 10*time.Second {
		t.Fatalf("one-second timeout completed in %s", elapsed)
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

func processSignalError(t *testing.T, signal syscall.Signal) error {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	if err == nil {
		t.Fatalf("signal %s produced no error", signal)
	}
	return err
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func cancelWhenFileExists(ctx context.Context, cancel context.CancelFunc, path string) {
	for ctx.Err() == nil {
		if data, err := os.ReadFile(path); err == nil && len(strings.Fields(string(data))) == 2 {
			cancel()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
