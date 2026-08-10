// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package validatorbind

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var errInvalidCleanupContext = errors.New("cleanup context is canceled or unbounded")

func TestRequireProvesExactWorkspaceAndCleansChallenge(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace,with-comma")
	canonical, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	docker := executableFixture(t, "docker")
	var captured []string

	err = require(context.Background(), Options{
		DockerBinary:    docker,
		Image:           "validator:fixture",
		Workspace:       workspace,
		Context:         "fixture-context",
		ContextExplicit: true,
	}, func(_ context.Context, dir, binary string, args []string) error {
		if dir != canonical {
			t.Fatalf("command dir = %q, want %q", dir, canonical)
		}
		if binary != docker {
			t.Fatalf("Docker binary = %q, want %q", binary, docker)
		}
		captured = append([]string(nil), args...)
		name := argumentValue(t, args, "WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME=")
		value := argumentValue(t, args, "WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE=")
		if len(value) != 64 {
			t.Fatalf("challenge value length = %d, want 64", len(value))
		}
		challenge := filepath.Join(canonical, name)
		info, err := os.Stat(challenge)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("challenge mode = %o, want 644", info.Mode().Perm())
		}
		data, err := os.ReadFile(challenge)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != value+"\n" {
			t.Fatalf("challenge content = %q, want %q", data, value+"\\n")
		}
		return runProbe(context.Background(), args, canonical)
	})
	if err != nil {
		t.Fatal(err)
	}
	mount, err := MountSpec(canonical, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--context", "fixture-context",
		"--mount", mount,
		"validator:fixture",
	} {
		if !slices.Contains(captured, want) {
			t.Fatalf("Docker args missing %q: %q", want, captured)
		}
	}
	if slices.Contains(captured, "--volume") {
		t.Fatalf("Docker args use source-creating --volume syntax: %q", captured)
	}
	runIndex := slices.Index(captured, "run")
	if runIndex < 0 || runIndex+1 == len(captured) || captured[runIndex+1] != "--rm" {
		t.Fatalf("Docker args omit run --rm: %q", captured)
	}
	nameIndex := slices.Index(captured, "--name")
	if nameIndex < 0 || nameIndex+1 == len(captured) || !validProbeName(captured[nameIndex+1]) {
		t.Fatalf("Docker args omit a valid probe name: %q", captured)
	}
	assertNoChallenges(t, canonical)
}

func TestMountSpecCSVEncodesWorkspaceAndReadonlyMode(t *testing.T) {
	t.Parallel()
	workspace := `/tmp/workspace,with"quote`
	readOnly, err := MountSpec(workspace, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := `type=bind,"src=/tmp/workspace,with""quote",dst=/workspace,readonly`; readOnly != want {
		t.Fatalf("readonly mount = %q, want %q", readOnly, want)
	}
	readWrite, err := MountSpec(workspace, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := `type=bind,"src=/tmp/workspace,with""quote",dst=/workspace`; readWrite != want {
		t.Fatalf("read-write mount = %q, want %q", readWrite, want)
	}
	if _, err := MountSpec("relative", false); err == nil {
		t.Fatal("relative workspace mount accepted")
	}
}

func TestRequireRejectsStaleWorkspaceAndCleansChallenge(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	staleWorkspace := createWorkspace(t, "stale-workspace")
	docker := executableFixture(t, "docker")

	err := require(context.Background(), Options{
		DockerBinary: docker,
		Image:        "validator:fixture",
		Workspace:    workspace,
		Context:      "fixture-context",
	}, func(ctx context.Context, _ string, _ string, args []string) error {
		return runProbe(ctx, args, staleWorkspace)
	})
	if err == nil || !strings.Contains(err.Error(), "Validator workspace is not visible") {
		t.Fatalf("stale workspace error = %v", err)
	}
	assertNoChallenges(t, workspace)
}

func TestRequireCancellationCleansChallengeAndContainer(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	docker := executableFixture(t, "docker")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)
	runArgs := make(chan []string, 1)
	cleanupArgs := make(chan []string, 1)

	go func() {
		result <- require(ctx, Options{
			DockerBinary: docker,
			Image:        "validator:fixture",
			Workspace:    workspace,
		}, func(ctx context.Context, _ string, _ string, args []string) error {
			if isProbeCleanup(args) {
				if err := cleanupContextError(ctx); err != nil {
					return err
				}
				cleanupArgs <- append([]string(nil), args...)
				return nil
			}
			runArgs <- append([]string(nil), args...)
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled workspace proof error = %v, want context.Canceled", err)
	}
	assertProbeCleanupArgs(t, receiveArgs(t, cleanupArgs), "", probeContainerName(t, receiveArgs(t, runArgs)))
	assertNoChallenges(t, workspace)
}

func TestRequireLocalDeadlineFailsClosedAndCleansChallengeAndContainer(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	docker := executableFixture(t, "docker")

	var cleanupArgs []string
	var probeName string
	err := requireWithProbeTimeout(context.Background(), Options{
		DockerBinary: docker,
		Image:        "validator:fixture",
		Workspace:    workspace,
		Context:      "fixture-context",
	}, func(ctx context.Context, _ string, _ string, args []string) error {
		if isProbeCleanup(args) {
			if err := cleanupContextError(ctx); err != nil {
				return err
			}
			cleanupArgs = append([]string(nil), args...)
			return nil
		}
		probeName = probeContainerName(t, args)
		<-ctx.Done()
		return nil
	}, 0)
	if !errors.Is(err, errValidatorBindProbeTimeout) {
		t.Fatalf("local deadline error = %v, want %v", err, errValidatorBindProbeTimeout)
	}
	assertProbeCleanupArgs(t, cleanupArgs, "fixture-context", probeName)
	assertNoChallenges(t, workspace)
}

func TestRequireParentCancellationTakesPrecedenceAndCleansChallengeAndContainer(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	docker := executableFixture(t, "docker")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	parentCause := errors.New("parent canceled validator proof")
	probeExpired := make(chan struct{})
	returnCommand := make(chan struct{})
	result := make(chan error, 1)
	cleanup := make(chan []string, 1)
	runArgs := make(chan []string, 1)

	go func() {
		result <- requireWithProbeTimeout(ctx, Options{
			DockerBinary: docker,
			Image:        "validator:fixture",
			Workspace:    workspace,
		}, func(probeCtx context.Context, _ string, _ string, args []string) error {
			if isProbeCleanup(args) {
				if err := cleanupContextError(probeCtx); err != nil {
					return err
				}
				cleanup <- append([]string(nil), args...)
				return nil
			}
			runArgs <- append([]string(nil), args...)
			<-probeCtx.Done()
			close(probeExpired)
			<-returnCommand
			return nil
		}, 0)
	}()
	<-probeExpired
	cancel(parentCause)
	close(returnCommand)
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancellation error = %v, want context.Canceled", err)
	}
	if errors.Is(err, parentCause) {
		t.Fatalf("parent cancellation error = %v, unexpectedly matches %v", err, parentCause)
	}
	if errors.Is(err, errValidatorBindProbeTimeout) {
		t.Fatalf("parent cancellation error = %v, unexpectedly matches %v", err, errValidatorBindProbeTimeout)
	}
	assertProbeCleanupArgs(t, receiveArgs(t, cleanup), "", probeContainerName(t, receiveArgs(t, runArgs)))
	assertNoChallenges(t, workspace)
}

func TestRequireLocalDeadlineJoinsCleanupFailure(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	docker := executableFixture(t, "docker")
	cleanupErr := errors.New("cleanup daemon rejected removal")
	var cleanupArgs []string
	var probeName string

	err := requireWithProbeTimeout(context.Background(), Options{
		DockerBinary: docker,
		Image:        "validator:fixture",
		Workspace:    workspace,
	}, func(ctx context.Context, _ string, _ string, args []string) error {
		if isProbeCleanup(args) {
			if err := cleanupContextError(ctx); err != nil {
				return err
			}
			cleanupArgs = append([]string(nil), args...)
			return cleanupErr
		}
		probeName = probeContainerName(t, args)
		<-ctx.Done()
		return nil
	}, 0)
	if !errors.Is(err, errValidatorBindProbeTimeout) {
		t.Fatalf("local deadline error = %v, want %v", err, errValidatorBindProbeTimeout)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("local deadline error = %v, want cleanup error %v", err, cleanupErr)
	}
	assertProbeCleanupArgs(t, cleanupArgs, "", probeName)
	assertNoChallenges(t, workspace)
}

func TestRequireLocalDeadlineStopsBlockingExecutable(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	docker, commandLog := blockingDockerFixture(t)
	result := make(chan error, 1)

	go func() {
		result <- requireWithProbeTimeout(context.Background(), Options{
			DockerBinary: docker,
			Image:        "validator:fixture",
			Workspace:    workspace,
			Context:      "fixture-context",
		}, runCommand, time.Second)
	}()
	waitForLoggedProbeStart(t, commandLog)
	select {
	case err := <-result:
		if !errors.Is(err, errValidatorBindProbeTimeout) {
			t.Fatalf("blocking executable error = %v, want %v", err, errValidatorBindProbeTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocking executable did not stop after the local deadline")
	}
	assertLoggedProbeCleanup(t, commandLog, "fixture-context")
	assertNoChallenges(t, workspace)
}

func TestRequireFailsClosedAndCleansChallenge(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	docker := executableFixture(t, "docker")
	for _, tc := range []struct {
		name     string
		explicit bool
		want     string
	}{
		{
			name:     "explicit context",
			explicit: true,
			want:     "Configured WORKCELL_DOCKER_CONTEXT=fixture-context cannot bind this checkout",
		},
		{
			name: "selected context",
			want: "Select a Docker context whose daemon can bind this checkout",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := require(context.Background(), Options{
				DockerBinary:    docker,
				Image:           "validator:fixture",
				Workspace:       workspace,
				Context:         "fixture-context",
				ContextExplicit: tc.explicit,
			}, func(context.Context, string, string, []string) error {
				return errors.New("daemon rejected bind")
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			assertNoChallenges(t, workspace)
		})
	}
}

func TestRequireRejectsInvalidInputsBeforeDocker(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	docker := executableFixture(t, "docker")
	for _, tc := range []struct {
		name    string
		options Options
		want    string
	}{
		{
			name:    "relative Docker binary",
			options: Options{DockerBinary: "docker", Image: "validator:fixture", Workspace: workspace},
			want:    "Docker binary must be an absolute path",
		},
		{
			name:    "missing image",
			options: Options{DockerBinary: docker, Workspace: workspace},
			want:    "validator image is required",
		},
		{
			name:    "missing workspace",
			options: Options{DockerBinary: docker, Image: "validator:fixture", Workspace: filepath.Join(t.TempDir(), "missing")},
			want:    "Validator workspace does not exist",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			err := require(context.Background(), tc.options, func(context.Context, string, string, []string) error {
				called = true
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if called {
				t.Fatal("Docker command ran after invalid input")
			}
		})
	}
}

func createWorkspace(t *testing.T, name string) string {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(workspace, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/workcell\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, "scripts", "validate-repo.sh"),
		[]byte("#!/bin/bash\nexit 0\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func executableFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func blockingDockerFixture(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	commandLog := path + ".log"
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" >> %q
if [ "$1" = "--context" ]; then
	shift 2
fi
if [ "$1" = "rm" ]; then
	exit 0
fi
exec /bin/sleep 60
`, commandLog)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, commandLog
}

func argumentValue(t *testing.T, args []string, prefix string) string {
	t.Helper()
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	t.Fatalf("Docker args missing %q: %q", prefix, args)
	return ""
}

func runProbe(ctx context.Context, args []string, mountedWorkspace string) error {
	var script string
	for i := range args {
		if args[i] == "-c" && i+1 < len(args) {
			script = args[i+1]
			break
		}
	}
	if script == "" {
		return errors.New("Docker args omit probe script")
	}
	script = strings.ReplaceAll(script, "/workspace", "${WORKCELL_TEST_MOUNT}")
	command := exec.CommandContext(ctx, "/bin/bash", "-c", script)
	command.Env = append(
		os.Environ(),
		"WORKCELL_TEST_MOUNT="+mountedWorkspace,
		"WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME="+argumentValueFromArgs(args, "WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME="),
		"WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE="+argumentValueFromArgs(args, "WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE="),
	)
	return command.Run()
}

func argumentValueFromArgs(args []string, prefix string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func probeContainerName(t *testing.T, args []string) string {
	t.Helper()
	index := slices.Index(args, "--name")
	if index < 0 || index+1 == len(args) {
		t.Fatalf("Docker args omit probe name: %q", args)
	}
	name := args[index+1]
	if !validProbeName(name) {
		t.Fatalf("Docker probe name = %q, want workcell-validator-bind-<hex>", name)
	}
	return name
}

func validProbeName(name string) bool {
	const prefix = "workcell-validator-bind-"
	value, ok := strings.CutPrefix(name, prefix)
	if !ok || len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isProbeCleanup(args []string) bool {
	if len(args) >= 1 && args[0] == "rm" {
		return true
	}
	return len(args) >= 3 && args[0] == "--context" && args[2] == "rm"
}

func cleanupContextError(ctx context.Context) error {
	if ctx.Err() != nil {
		return errInvalidCleanupContext
	}
	if _, ok := ctx.Deadline(); !ok {
		return errInvalidCleanupContext
	}
	return nil
}

func assertLoggedProbeCleanup(t *testing.T, commandLog, dockerContext string) {
	t.Helper()
	data, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(args) < 5 {
		t.Fatalf("Docker command log is too short: %q", args)
	}
	nameIndex := slices.Index(args, "--name")
	if nameIndex < 0 || nameIndex+1 == len(args) || !validProbeName(args[nameIndex+1]) {
		t.Fatalf("Docker command log omits a valid probe name: %q", args)
	}
	cleanupArgs := args[len(args)-5:]
	want := []string{"--context", dockerContext, "rm", "--force", args[nameIndex+1]}
	if !slices.Equal(cleanupArgs, want) {
		t.Fatalf("logged cleanup args = %q, want %q", cleanupArgs, want)
	}
}

func waitForLoggedProbeStart(t *testing.T, commandLog string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if data, err := os.ReadFile(commandLog); err == nil && slices.Contains(strings.Split(string(data), "\n"), "run") {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("blocking Docker executable did not record the probe command")
		case <-ticker.C:
		}
	}
}

func assertProbeCleanupArgs(t *testing.T, args []string, dockerContext, wantName string) {
	t.Helper()
	want := make([]string, 0, 5)
	if dockerContext != "" {
		want = append(want, "--context", dockerContext)
	}
	want = append(want, "rm", "--force", wantName)
	if !slices.Equal(args, want) {
		t.Fatalf("cleanup args = %q, want %q", args, want)
	}
}

func receiveArgs(t *testing.T, result <-chan []string) []string {
	t.Helper()
	select {
	case args := <-result:
		return args
	case <-time.After(2 * time.Second):
		t.Fatal("Docker command was not recorded")
		return nil
	}
}

func assertNoChallenges(t *testing.T, workspace string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(workspace, ".workcell-validator-bind.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("validator bind challenge residue remains: %v", matches)
	}
}
