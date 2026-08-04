// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package validatorbind

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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

func TestRequireCancellationCleansChallenge(t *testing.T) {
	t.Parallel()
	workspace := createWorkspace(t, "workspace")
	docker := executableFixture(t, "docker")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan error, 1)

	go func() {
		result <- require(ctx, Options{
			DockerBinary: docker,
			Image:        "validator:fixture",
			Workspace:    workspace,
		}, func(ctx context.Context, _ string, _ string, _ []string) error {
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
