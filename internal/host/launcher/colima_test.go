// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
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
	dir := t.TempDir()
	fake := writeFakeColima(t, dir, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	relative := filepath.Base(fake)

	_, err := RunHostColima(HostColimaInvocation{ColimaBin: relative, Args: []string{"list"}})
	if err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("RunHostColima() err = %v, want absolute-path rejection", err)
	}
	_, err = RunHostColimaWithTimeout(1, HostColimaInvocation{ColimaBin: relative, Args: []string{"start"}})
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
	for _, timeout := range []int{0, -1} {
		code, err := RunHostColimaWithTimeout(timeout, HostColimaInvocation{
			ColimaBin: fake,
			RealHome:  dir,
			Args:      []string{"list"},
		})
		if err != nil {
			t.Fatalf("RunHostColimaWithTimeout(%d) err = %v", timeout, err)
		}
		if code != 11 {
			t.Fatalf("RunHostColimaWithTimeout(%d) code = %d, want 11", timeout, code)
		}
	}
}

func TestRunHostColimaWithTimeoutCapsPositiveTimeout(t *testing.T) {
	t.Parallel()
	code, err := RunHostColimaWithTimeout(86400, HostColimaInvocation{
		ColimaBin: "/usr/bin/true",
		Args:      []string{"list"},
	})
	if err != nil || code != 0 {
		t.Fatalf("RunHostColimaWithTimeout(86400) = %d, %v", code, err)
	}
	_, err = RunHostColimaWithTimeout(86401, HostColimaInvocation{
		ColimaBin: "/usr/bin/true",
		Args:      []string{"list"},
	})
	if err == nil || !strings.Contains(err.Error(), "must not exceed 86400 seconds") {
		t.Fatalf("RunHostColimaWithTimeout(86401) err = %v", err)
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
			groupID := registerProcessGroupCleanup(t, marker)
			fake := writeFakeColima(t, dir, "#!/bin/sh\n"+tc.setup+`
sh -c "`+tc.setup+`; while :; do sleep 1; done" &
child=$!
pgid=$(ps -o pgid= -p $$ | tr -d ' ')
printf '%s %s\n' "$pgid" "$child" >"$2"
wait "$child"
`)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			go cancelWhenProcessMarkerReady(ctx, func() { cancel(context.DeadlineExceeded) }, marker)
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
			parsedGroupID, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatal(err)
			}
			*groupID = parsedGroupID
			if err := syscall.Kill(-*groupID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("process group %d still exists: %v", *groupID, err)
			}
			*groupID = -1
		})
	}
}

func TestRunHostColimaWithTimeoutHandlesDirectHelperSignals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX process-group and signal semantics")
	}
	if os.Getenv("WORKCELL_COLIMA_SIGNAL_HELPER") == "1" {
		code, err := RunHostColimaWithTimeout(30, HostColimaInvocation{
			ColimaBin: os.Getenv("WORKCELL_COLIMA_SIGNAL_BIN"),
			RealHome:  os.Getenv("WORKCELL_COLIMA_SIGNAL_HOME"),
			Args: []string{
				"start",
				os.Getenv("WORKCELL_COLIMA_SIGNAL_PROCESS_MARKER"),
				os.Getenv("WORKCELL_COLIMA_SIGNAL_TERM_MARKER"),
			},
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(125)
		}
		os.Exit(code)
	}

	for _, tc := range []struct {
		name     string
		signal   os.Signal
		wantCode int
	}{
		{"SIGINT", os.Interrupt, 128 + int(syscall.SIGINT)},
		{"SIGTERM", syscall.SIGTERM, 128 + int(syscall.SIGTERM)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			processMarker := filepath.Join(dir, "processes")
			termMarker := filepath.Join(dir, "term")
			fake := writeFakeColima(t, dir, `#!/bin/sh
trap 'printf "TERM\n" >"$3"; while :; do sleep 1; done' TERM
sh -c 'trap "" TERM; while :; do sleep 1; done' &
child=$!
pgid=$(ps -o pgid= -p $$ | tr -d ' ')
printf '%s %s\n' "$pgid" "$child" >"$2"
while kill -0 "$child" 2>/dev/null; do
  wait "$child" || true
done
`)
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunHostColimaWithTimeoutHandlesDirectHelperSignals$")
			cmd.Env = append(os.Environ(),
				"WORKCELL_COLIMA_SIGNAL_HELPER=1",
				"WORKCELL_COLIMA_SIGNAL_BIN="+fake,
				"WORKCELL_COLIMA_SIGNAL_HOME="+dir,
				"WORKCELL_COLIMA_SIGNAL_PROCESS_MARKER="+processMarker,
				"WORKCELL_COLIMA_SIGNAL_TERM_MARKER="+termMarker,
			)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			groupID := registerProcessGroupCleanup(t, processMarker)
			t.Cleanup(func() {
				if !waited {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			})

			fields := pollProcessMarker(processMarker, 5*time.Second)
			if len(fields) != 2 {
				t.Fatalf("process marker %s was not ready", processMarker)
			}
			var err error
			*groupID, err = strconv.Atoi(fields[0])
			if err != nil {
				t.Fatal(err)
			}
			startedCleanup := time.Now()
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.wantCode {
				t.Fatalf("signal helper wait = %v, want status %d", err, tc.wantCode)
			}
			if elapsed := time.Since(startedCleanup); elapsed < 4750*time.Millisecond || elapsed > 10*time.Second {
				t.Fatalf("signal cleanup completed in %s", elapsed)
			}
			if got := strings.TrimSpace(string(mustReadFile(t, termMarker))); got != "TERM" {
				t.Fatalf("TERM marker = %q, want TERM", got)
			}
			if err := syscall.Kill(-*groupID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("process group %d still exists: %v", *groupID, err)
			}
			*groupID = -1
		})
	}
}

func TestRunHostColimaWithContextPreservesPreStartCancellationCause(t *testing.T) {
	for _, tc := range []struct {
		name       string
		newContext func() (context.Context, context.CancelFunc)
		wantCode   int
	}{
		{
			"deadline",
			func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Unix(1, 0))
			},
			ColimaTimeoutExitCode,
		},
		{
			"SIGINT",
			func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancelCause(context.Background())
				cancel(&colimaSignalCancellation{signal: os.Interrupt})
				return ctx, func() { cancel(context.Canceled) }
			},
			128 + int(syscall.SIGINT),
		},
		{
			"SIGTERM",
			func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancelCause(context.Background())
				cancel(&colimaSignalCancellation{signal: syscall.SIGTERM})
				return ctx, func() { cancel(context.Canceled) }
			},
			128 + int(syscall.SIGTERM),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := tc.newContext()
			defer cancel()
			code, err := runHostColimaWithContext(ctx, HostColimaInvocation{
				ColimaBin: "/usr/bin/true",
				Args:      []string{"start"},
			})
			if err != nil || code != tc.wantCode {
				t.Fatalf("runHostColimaWithContext() = %d, %v, want %d", code, err, tc.wantCode)
			}
		})
	}

	code, err := runHostColimaWithContext(context.Background(), HostColimaInvocation{
		ColimaBin: "/workcell-missing-colima-binary",
		Args:      []string{"start"},
	})
	if code != 0 || err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unrelated start error = %d, %v", code, err)
	}
}

func TestColimaCancellationResultPreservesOutcomes(t *testing.T) {
	termErr := processSignalError(t, syscall.SIGTERM)
	killErr := processSignalError(t, syscall.SIGKILL)
	interruptErr := processSignalError(t, syscall.SIGINT)
	exitErr := exec.Command("/bin/sh", "-c", "exit 7").Run()
	exit124Err := exec.Command("/bin/sh", "-c", "exit 124").Run()
	ioErr := errors.New("write failed")
	cleanupErr := errors.New("group remains")
	interruptCause := &colimaSignalCancellation{signal: os.Interrupt}
	for _, tc := range []struct {
		name       string
		runErr     error
		cleanupErr error
		cause      error
		wantCode   int
		wantErrors []error
		wantSignal bool
	}{
		{"nil after deadline", nil, nil, context.DeadlineExceeded, 124, nil, false},
		{"deadline error", context.DeadlineExceeded, nil, context.DeadlineExceeded, 124, nil, false},
		{"TERM after deadline", termErr, nil, context.DeadlineExceeded, 124, nil, false},
		{"context canceled after SIGINT", context.Canceled, nil, interruptCause, 130, nil, false},
		{"KILL after SIGINT", killErr, nil, interruptCause, 130, nil, false},
		{"TERM after SIGTERM", termErr, nil, &colimaSignalCancellation{signal: syscall.SIGTERM}, 143, nil, false},
		{"ordinary exit 7", exitErr, nil, interruptCause, 7, nil, false},
		{"ordinary exit 124", exit124Err, nil, interruptCause, 124, nil, false},
		{"unrelated SIGINT", interruptErr, nil, context.DeadlineExceeded, 128 + int(syscall.SIGINT), nil, false},
		{"I/O error", ioErr, nil, context.DeadlineExceeded, 0, []error{ioErr}, false},
		{"cleanup after SIGINT", termErr, cleanupErr, interruptCause, 0, []error{termErr, cleanupErr, interruptCause}, true},
		{"cleanup after deadline", termErr, cleanupErr, context.DeadlineExceeded, 0, []error{termErr, cleanupErr, context.DeadlineExceeded}, false},
		{"unknown cause", termErr, nil, context.Canceled, 0, []error{context.Canceled}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, err := colimaCancellationResult(tc.runErr, tc.cleanupErr, tc.cause)
			if code != tc.wantCode || (len(tc.wantErrors) == 0 && err != nil) {
				t.Fatalf("colimaCancellationResult() = %d, %v", code, err)
			}
			for _, wantErr := range tc.wantErrors {
				if !errors.Is(err, wantErr) {
					t.Fatalf("colimaCancellationResult() err = %v, want %v", err, wantErr)
				}
			}
			if tc.wantSignal {
				var signalCause *colimaSignalCancellation
				if !errors.As(err, &signalCause) || signalCause.signal != os.Interrupt {
					t.Fatalf("colimaCancellationResult() err = %v, want SIGINT cause", err)
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

func pollProcessMarker(path string, limit time.Duration) []string {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if fields := strings.Fields(string(data)); len(fields) == 2 {
				return fields
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func registerProcessGroupCleanup(t *testing.T, marker string) *int {
	t.Helper()
	groupID := new(int)
	t.Cleanup(func() {
		if *groupID < 0 {
			return
		}
		if *groupID == 0 {
			if fields := pollProcessMarker(marker, 5*time.Second); len(fields) == 2 {
				*groupID, _ = strconv.Atoi(fields[0])
			}
		}
		if *groupID <= 0 {
			return
		}
		if err := syscall.Kill(-*groupID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("cleanup process group %d: %v", *groupID, err)
		}
		absent, err := waitForProcessGroupExit(*groupID, 5*time.Second)
		if err != nil || !absent {
			t.Errorf("cleanup did not prove process group %d absent: %v", *groupID, err)
		}
		*groupID = -1
	})
	return groupID
}

func cancelWhenProcessMarkerReady(ctx context.Context, cancel func(), path string) {
	for ctx.Err() == nil {
		if fields := pollProcessMarker(path, 25*time.Millisecond); len(fields) == 2 {
			cancel()
			return
		}
	}
}
