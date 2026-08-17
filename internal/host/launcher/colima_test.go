// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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

func TestRunHostColimaWithTimeoutKillsRunawayChild(t *testing.T) {
	testRunHostColimaWithTimeout(t, false)
}

func TestRunHostColimaWithTimeoutReturnsExitCodeWhenFastEnough(t *testing.T) {
	testRunHostColimaWithTimeout(t, true)
}

func testRunHostColimaWithTimeout(t *testing.T, public bool) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on POSIX /bin/sh helpers")
	}
	if os.Getenv("WORKCELL_COLIMA_TIMEOUT_HELPER") == "1" {
		waitForOwnedFixtureCommandReady()
		inv := HostColimaInvocation{
			ColimaBin: os.Getenv("WORKCELL_COLIMA_TIMEOUT_BIN"),
			RealHome:  os.Getenv("WORKCELL_COLIMA_TIMEOUT_HOME"),
			Args:      []string{"start", os.Getenv("WORKCELL_COLIMA_TIMEOUT_MARKER")},
		}
		if os.Getenv("WORKCELL_COLIMA_TIMEOUT_PUBLIC") == "true" {
			code, err := RunHostColimaWithTimeout(1, inv)
			exitColimaFixture(code, err)
		}
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(context.Canceled)
		go func() {
			for ctx.Err() == nil {
				if _, err := os.Stat(os.Getenv("WORKCELL_COLIMA_TIMEOUT_ACK")); err == nil {
					cancel(context.DeadlineExceeded)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		code, err := runHostColimaWithContext(ctx, inv)
		exitColimaFixture(code, err)
	}
	// Serial — see ETXTBSY note on TestRunHostColimaForwardsArgsAndPropagatesExitCode.
	for _, tc := range []struct {
		name     string
		setup    string
		public   bool
		childACK string
		wantCode int
		minRun   time.Duration
		maxRun   time.Duration
	}{
		{"TERM-responsive", ":", false, "ready\n", ColimaTimeoutExitCode, 0, 4 * time.Second},
		{"TERM-resistant descendant", `trap "" TERM`, false, "ready\n", ColimaTimeoutExitCode, 4750 * time.Millisecond, 11 * time.Second},
		{"public one-second deadline", "#!/bin/sh\ncount=0\nwhile [ \"$count\" -lt 500 ]; do sleep .01; count=$((count + 1)); done\nexit 125\n", true, "", ColimaTimeoutExitCode, 750 * time.Millisecond, 4 * time.Second},
		{"public fast exit 7", "#!/bin/sh\nsleep .1\nexit 7\n", true, "", 7, 100 * time.Millisecond, 900 * time.Millisecond},
		{"missing child ACK", ":", false, "", 125, 4 * time.Second, 12 * time.Second},
		{"malformed child ACK", ":", false, "bad\n", 125, 0, 4 * time.Second},
	} {
		if tc.public != public {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "processes")
			ack := filepath.Join(dir, "ack")
			fakeSource := `#!/bin/sh
pgid=$(ps -o pgid= -p $$ | tr -d ' '); printf '%s %s\n' "$pgid" "$$"
count=0
while [ ! -f "$2.owned" ] && [ "$count" -lt 500 ]; do sleep .01; count=$((count + 1)); done
[ "$(cat "$2.owned" 2>/dev/null)" = ready ] || exit 125
sh -c 'count=0
while [ ! -f "$1.child-owned" ] && [ "$count" -lt 500 ]; do sleep .01; count=$((count + 1)); done
[ "$(cat "$1.child-owned" 2>/dev/null)" = ready ] || exit 125
` + tc.setup + `; printf 'ready\n' >"$1.child-ready"; while :; do sleep 1; done' fixture-child "$2" &
child=$!
printf '%s %s %s\n' "$pgid" "$$" "$child" >"$2"
wait "$child"
`
			if tc.public {
				fakeSource = tc.setup
			}
			fake := writeFakeColima(t, dir, fakeSource)
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunHostColimaWithTimeoutKillsRunawayChild$")
			ownerPipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(os.Environ(),
				"WORKCELL_COLIMA_TIMEOUT_HELPER=1",
				"WORKCELL_COLIMA_TIMEOUT_BIN="+fake,
				"WORKCELL_COLIMA_TIMEOUT_HOME="+dir,
				"WORKCELL_COLIMA_TIMEOUT_MARKER="+marker,
				"WORKCELL_COLIMA_TIMEOUT_ACK="+ack,
				"WORKCELL_COLIMA_TIMEOUT_PUBLIC="+strconv.FormatBool(tc.public),
			)
			start := time.Now()
			fixtureCommand := startOwnedFixtureCommand(t, cmd)
			var fixtureGroup *ownedFixtureProcessGroup
			if !tc.public {
				fixtureGroup, err = readOwnedFixtureProcessGroupPipe(ownerPipe, cmd.Process.Pid, fake)
				if err != nil {
					t.Fatal(err)
				}
				registerOwnedFixtureGroupCleanup(t, fixtureGroup)
				if err := os.WriteFile(marker+".owned", []byte("ready\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := validateOwnedFixtureProcessGroupMarker(fixtureGroup, marker); err != nil {
					t.Fatal(err)
				}
				if tc.childACK != "" {
					if err := os.WriteFile(marker+".child-owned", []byte(tc.childACK), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if tc.childACK == "ready\n" && len(pollProcessMarker(marker+".child-ready", 5*time.Second, 1)) != 1 {
					t.Fatal("fixture child was not ready")
				}
				if tc.wantCode == ColimaTimeoutExitCode && !tc.public {
					if err := os.WriteFile(ack, []byte("ready\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			err = fixtureCommand.wait()
			if fixtureGroup != nil {
				fixtureGroup.proveAbsent(t)
			} else if !waitForFixtureCommandPathAbsent(fake, 5*time.Second) {
				t.Fatalf("fixture command remains: %s", fake)
			}
			fixtureCommand.group.proveAbsent(t)
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.wantCode {
				t.Fatalf("timeout helper wait = %v, want status %d", err, tc.wantCode)
			}
			if elapsed := fixtureCommand.finishedAt.Sub(start); elapsed < tc.minRun || elapsed > tc.maxRun {
				t.Fatalf("process-group cleanup completed in %s", elapsed)
			}
		})
	}
}

func TestRunHostColimaWithContextPreservesPreStartDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	code, err := runHostColimaWithContext(ctx, HostColimaInvocation{
		ColimaBin: "/usr/bin/true",
		Args:      []string{"start"},
	})
	if err != nil || code != ColimaTimeoutExitCode {
		t.Fatalf("pre-start deadline = %d, %v, want %d", code, err, ColimaTimeoutExitCode)
	}

	code, err = runHostColimaWithContext(context.Background(), HostColimaInvocation{
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
	for _, tc := range []struct {
		name       string
		runErr     error
		cleanupErr error
		cause      error
		wantCode   int
		wantErrors []error
	}{
		{"nil after deadline", nil, nil, context.DeadlineExceeded, 124, nil},
		{"deadline error", context.DeadlineExceeded, nil, context.DeadlineExceeded, 124, nil},
		{"TERM after deadline", termErr, nil, context.DeadlineExceeded, 124, nil},
		{"KILL after deadline", killErr, nil, context.DeadlineExceeded, 124, nil},
		{"ordinary exit 7", exitErr, nil, context.DeadlineExceeded, 7, nil},
		{"ordinary exit 124", exit124Err, nil, context.DeadlineExceeded, 124, nil},
		{"unrelated SIGINT", interruptErr, nil, context.DeadlineExceeded, 128 + int(syscall.SIGINT), nil},
		{"I/O error", ioErr, nil, context.DeadlineExceeded, 0, []error{ioErr}},
		{"cleanup after deadline", termErr, cleanupErr, context.DeadlineExceeded, 0, []error{termErr, cleanupErr, context.DeadlineExceeded}},
		{"unexpected cause", termErr, nil, context.Canceled, 0, []error{context.Canceled}},
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
		})
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

func exitColimaFixture(code int, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	os.Exit(code)
}

func pollProcessMarker(path string, limit time.Duration, fieldCount int) []string {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if fields := strings.Fields(string(data)); len(fields) == fieldCount {
				return fields
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func parseFixtureProcessMarker(path string) ([]int, error) {
	fields := pollProcessMarker(path, 5*time.Second, 3)
	values := make([]int, len(fields))
	for i, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("process marker %s field %q: %w", path, field, err)
		}
		values[i] = value
	}
	if len(values) != 3 {
		return nil, fmt.Errorf("process marker %s was not ready", path)
	}
	return values, nil
}

func fixtureProcessIdentity(pid int) (int, string, error) {
	groupID, err := syscall.Getpgid(pid)
	if err != nil {
		return 0, "", err
	}
	output, err := exec.Command("/bin/ps", "-o", "lstart=", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	return groupID, string(output), err
}

func readOwnedFixtureProcessGroupPipe(reader io.Reader, parentPID int, commandPath string) (*ownedFixtureProcessGroup, error) {
	result := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(reader).ReadString('\n')
		result <- line
	}()
	var line string
	select {
	case line = <-result:
	case <-time.After(5 * time.Second):
		return nil, errors.New("fixture owner handshake timed out")
	}
	fields := strings.Fields(line)
	if len(fields) != 2 {
		return nil, fmt.Errorf("fixture owner handshake has %d fields", len(fields))
	}
	groupID, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("fixture owner group %q: %w", fields[0], err)
	}
	leaderPID, err := strconv.Atoi(fields[1])
	if err != nil || groupID != leaderPID || validateSafeProcessGroupID(groupID) != nil {
		return nil, fmt.Errorf("fixture owner has unsafe group=%d leader=%d", groupID, leaderPID)
	}
	actual, identity, err := fixtureProcessIdentity(leaderPID)
	parent, parentErr := fixtureProcessParent(leaderPID)
	if err != nil || parentErr != nil || actual != groupID || parent != parentPID || !strings.Contains(identity, commandPath) {
		return nil, fmt.Errorf("fixture owner identity is not trusted: identity=%v parent=%v", err, parentErr)
	}
	return &ownedFixtureProcessGroup{id: groupID, members: map[int]string{leaderPID: identity}, active: true}, nil
}

func fixtureProcessParent(pid int) (int, error) {
	output, err := exec.Command("/bin/ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(output)))
}

func validateOwnedFixtureProcessGroupMarker(group *ownedFixtureProcessGroup, marker string) error {
	values, err := parseFixtureProcessMarker(marker)
	if err != nil {
		return err
	}
	groupID, leaderPID, childPID := values[0], values[1], values[2]
	if group == nil || groupID != group.id || leaderPID != group.id || !group.hasLiveMember() {
		return fmt.Errorf("process marker %s does not identify the owned group", marker)
	}
	actual, identity, err := fixtureProcessIdentity(childPID)
	parent, parentErr := fixtureProcessParent(childPID)
	if err != nil || parentErr != nil || actual != group.id || parent != leaderPID || !strings.Contains(identity, marker) {
		return fmt.Errorf("fixture child %d identity is not owned: identity=%v parent=%v", childPID, err, parentErr)
	}
	group.members[childPID] = identity
	return nil
}

type ownedFixtureProcessGroup struct {
	id      int
	members map[int]string
	active  bool
}

type ownedFixtureCommand struct {
	cmd        *exec.Cmd
	group      *ownedFixtureProcessGroup
	waitDone   chan struct{}
	waitErr    error
	finishedAt time.Time
}

func startOwnedFixtureCommand(t *testing.T, cmd *exec.Cmd) *ownedFixtureCommand {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "outer-group-ready")
	cmd.Env = append(cmd.Env, "WORKCELL_TEST_OUTER_GROUP_READY="+ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fixture := &ownedFixtureCommand{cmd: cmd, waitDone: make(chan struct{})}
	go func() {
		fixture.waitErr = cmd.Wait()
		fixture.finishedAt = time.Now()
		close(fixture.waitDone)
	}()
	t.Cleanup(func() {
		if fixture.group != nil {
			if err := fixture.group.cleanup(); err != nil {
				t.Error(err)
			}
		}
		select {
		case <-fixture.waitDone:
			return
		default:
			_ = cmd.Process.Kill()
		}
		select {
		case <-fixture.waitDone:
		case <-time.After(5 * time.Second):
			t.Error("fixture process did not exit during cleanup")
		}
	})
	actual, identity, err := fixtureProcessIdentity(cmd.Process.Pid)
	if err != nil || actual != cmd.Process.Pid || validateSafeProcessGroupID(actual) != nil {
		t.Fatalf("fixture leader %d has unsafe process group %d: %v", cmd.Process.Pid, actual, err)
	}
	group := &ownedFixtureProcessGroup{id: actual, members: map[int]string{cmd.Process.Pid: identity}}
	group.active = true
	fixture.group = group
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *ownedFixtureCommand) wait() error {
	select {
	case <-f.waitDone:
		return f.waitErr
	case <-time.After(15 * time.Second):
		_ = f.group.cleanup()
		_ = f.cmd.Process.Kill()
		select {
		case <-f.waitDone:
			return fmt.Errorf("fixture command exceeded its wait limit: %w", f.waitErr)
		case <-time.After(5 * time.Second):
			return errors.New("fixture command did not exit after its wait limit")
		}
	}
}

func registerOwnedFixtureGroupCleanup(t *testing.T, group *ownedFixtureProcessGroup) {
	t.Helper()
	t.Cleanup(func() {
		if err := group.cleanup(); err != nil {
			t.Error(err)
		}
	})
}

func (g *ownedFixtureProcessGroup) cleanup() error {
	if !g.active {
		return nil
	}
	absent, err := waitForFixtureGroupExit(g.id, 0)
	if err == nil && absent {
		g.active = false
		return nil
	}
	if !g.hasLiveMember() {
		return fmt.Errorf("refuse cleanup of process group %d without a live owned member", g.id)
	}
	if err := syscall.Kill(-g.id, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("cleanup process group %d: %w", g.id, err)
	}
	absent, err = waitForFixtureGroupExit(g.id, 5*time.Second)
	if err != nil || !absent {
		return fmt.Errorf("cleanup did not prove process group %d absent: %v", g.id, err)
	}
	g.active = false
	return nil
}

func (g *ownedFixtureProcessGroup) hasLiveMember() bool {
	return g.hasLiveMemberWith(fixtureProcessIdentity)
}

func (g *ownedFixtureProcessGroup) hasLiveMemberWith(inspect func(int) (int, string, error)) bool {
	for pid, identity := range g.members {
		if actual, current, err := inspect(pid); err == nil && actual == g.id && current == identity {
			return true
		}
	}
	return false
}

func (g *ownedFixtureProcessGroup) proveAbsent(t *testing.T) {
	t.Helper()
	absent, err := waitForFixtureGroupExit(g.id, 5*time.Second)
	if err != nil || !absent {
		t.Fatalf("process group %d remains: absent=%v err=%v", g.id, absent, err)
	}
	g.active = false
}

func waitForFixtureGroupExit(groupID int, limit time.Duration) (bool, error) {
	if groupID <= 1 || groupID == syscall.Getpgrp() {
		return false, fmt.Errorf("unsafe fixture group %d", groupID)
	}
	deadline := time.Now().Add(limit)
	for {
		err := syscall.Kill(-groupID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true, nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return false, err
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForOwnedFixtureCommandReady() {
	ready := os.Getenv("WORKCELL_TEST_OUTER_GROUP_READY")
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			return
		}
	}
	os.Exit(125)
}
