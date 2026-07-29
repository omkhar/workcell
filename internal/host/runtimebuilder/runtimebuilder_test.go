// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package runtimebuilder

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
)

const (
	testContainerID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherContainerID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type fakeDocker struct {
	containers                                    map[string]string
	volumes                                       map[string]volumeInspect
	containerRemoveFailures, volumeRemoveFailures int
	calls                                         []string
}

type runnerFunc func(args ...string) (string, error)

func (run runnerFunc) Run(args ...string) (string, error) { return run(args...) }

func newFakeDocker() *fakeDocker {
	return &fakeDocker{containers: make(map[string]string), volumes: make(map[string]volumeInspect)}
}

func (f *fakeDocker) Run(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case equal(args, "ps", "-a", "--format", "{{.Names}}"):
		return names(f.containers), nil
	case equal(args, "volume", "ls", "--format", "{{.Name}}"):
		return names(f.volumes), nil
	case len(args) == 6 && equal(args[:5], "inspect", "--type", "container", "--format", "{{.Id}}"):
		id, ok := f.containers[args[5]]
		if !ok {
			return "", errors.New("container absent")
		}
		return id + "\n", nil
	case len(args) == 5 && equal(args[:4], "volume", "inspect", "--format", "{{json .}}"):
		volume, ok := f.volumes[args[4]]
		if !ok {
			return "", errors.New("volume absent")
		}
		raw, _ := json.Marshal(volume)
		return string(raw) + "\n", nil
	case len(args) == 3 && equal(args[:2], "rm", "-f"):
		if f.containerRemoveFailures > 0 {
			f.containerRemoveFailures--
			return "", errors.New("container busy")
		}
		for name, id := range f.containers {
			if id == args[2] {
				delete(f.containers, name)
				return "", nil
			}
		}
		return "", errors.New("container identity absent")
	case len(args) == 3 && equal(args[:2], "volume", "rm"):
		if f.volumeRemoveFailures > 0 {
			f.volumeRemoveFailures--
			return "", errors.New("volume busy")
		}
		if _, ok := f.volumes[args[2]]; !ok {
			return "", errors.New("volume absent")
		}
		delete(f.volumes, args[2])
		return "", nil
	default:
		return "", errors.New("unexpected fake Docker command: " + strings.Join(args, " "))
	}
}

func fixture(t *testing.T) (Config, target) {
	t.Helper()
	root := t.TempDir()
	cfg := Config{
		Profile:         "wcl-test",
		Backend:         "colima",
		TargetKind:      "local_vm",
		TargetStateRoot: filepath.Join(root, "targets"),
		ColimaStateRoot: filepath.Join(root, "colima"),
		Workspace:       filepath.Join(root, "workspace"),
		DockerBin:       filepath.Join(root, "docker"),
		DockerConfig:    filepath.Join(root, "docker-config"),
		DockerHome:      filepath.Join(root, "docker-home"),
		DockerCWD:       root,
		BuildxBin:       filepath.Join(root, "docker-buildx"),
		ToolPath:        "/usr/bin:/bin",
		RealHome:        root,
	}
	cfg.DockerHost = "unix://" + filepath.Join(cfg.ColimaStateRoot, cfg.Profile, "docker.sock")
	cfg.DockerEndpoint = cfg.DockerHost
	stateDir := filepath.Join(cfg.TargetStateRoot, cfg.TargetKind, cfg.Backend, cfg.Profile)
	must(t, os.MkdirAll(stateDir, 0o700))
	must(t, os.WriteFile(filepath.Join(stateDir, "workcell.managed"), []byte(cfg.Workspace+"\n"), 0o600))
	target, err := prepare(cfg)
	must(t, err)
	return cfg, target
}

func TestLifecycleUsesRandomNameAndImmutableIdentity(t *testing.T) {
	_, target := fixture(t)
	docker := newFakeDocker()
	builder, err := claim(target, docker, bytes.NewReader(make([]byte, 16)), noSleep)
	must(t, err)
	if want := "workcell-runtime-wcl-test-" + strings.Repeat("0", 32); builder != want {
		t.Fatalf("builder = %q, want %q", builder, want)
	}
	created := false
	buildx := runnerFunc(func(args ...string) (string, error) {
		if len(args) > 0 && args[0] == "create" {
			created = true
		} else if len(args) > 0 && args[0] == "inspect" && created {
			addBuilder(docker, builder, testContainerID)
		}
		return "", nil
	})
	must(t, create(target, docker, buildx))
	must(t, cleanup(target, docker, noSleep))
	if len(docker.containers) != 0 || len(docker.volumes) != 0 {
		t.Fatalf("cleanup left containers=%v volumes=%v", docker.containers, docker.volumes)
	}
	if _, err := os.Lstat(target.recordPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ownership record remains: %v", err)
	}
	if !slices.Contains(docker.calls, "rm -f "+testContainerID) {
		t.Fatalf("cleanup did not remove by immutable container ID: %v", docker.calls)
	}
}

func TestCleanupRejectsChangedAuthorityOrIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*target, *fakeDocker, [4]string)
	}{
		{"container", func(_ *target, docker *fakeDocker, names [4]string) { docker.containers[names[1]] = otherContainerID }},
		{"volume", func(_ *target, docker *fakeDocker, names [4]string) {
			volume := docker.volumes[names[3]]
			volume.CreatedAt += ".replacement"
			docker.volumes[names[3]] = volume
		}},
		{"endpoint", func(target *target, _ *fakeDocker, _ [4]string) { target.endpoint += ".retargeted" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, docker, builder := ownedFixture(t)
			test.mutate(&target, docker, resourceNames(builder))
			if err := cleanup(target, docker, noSleep); err == nil {
				t.Fatal("cleanup unexpectedly succeeded")
			}
			if len(docker.containers) != 1 || len(docker.volumes) != 1 {
				t.Fatal("cleanup removed resources after authority or identity changed")
			}
			if _, err := os.Lstat(target.recordPath); err != nil {
				t.Fatalf("cleanup removed ownership record: %v", err)
			}
		})
	}
}

func TestCleanupVerifiesAfterFifthRemoval(t *testing.T) {
	target, docker, _ := ownedFixture(t)
	docker.containerRemoveFailures = 4
	docker.volumeRemoveFailures = 4
	must(t, cleanup(target, docker, noSleep))
}

func ownedFixture(t *testing.T) (target, *fakeDocker, string) {
	t.Helper()
	_, target := fixture(t)
	docker := newFakeDocker()
	builder, err := claim(target, docker, bytes.NewReader(make([]byte, 16)), noSleep)
	must(t, err)
	addBuilder(docker, builder, testContainerID)
	must(t, adopt(target, docker))
	return target, docker, builder
}

func TestClaimRecoversPriorPartialBuilder(t *testing.T) {
	_, target := fixture(t)
	docker := newFakeDocker()
	builder, err := claim(target, docker, bytes.NewReader(make([]byte, 16)), noSleep)
	must(t, err)
	addBuilder(docker, builder, testContainerID)
	random := append(bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)...)
	next, err := claim(target, docker, bytes.NewReader(random), noSleep)
	must(t, err)
	if next == builder || len(docker.containers) != 0 || len(docker.volumes) != 0 {
		t.Fatalf("recovery next=%q containers=%v volumes=%v", next, docker.containers, docker.volumes)
	}
}

func TestAuthorityAndSymlinksFailClosed(t *testing.T) {
	cfg, _ := fixture(t)
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"wrong socket", func(c *Config) { c.DockerHost += ".other" }},
		{"unexpected context", func(c *Config) { c.DockerContext = "desktop-linux" }},
		{"wrong kind", func(c *Config) { c.TargetKind = "local_compat" }},
		{"bad profile", func(c *Config) { c.Profile = "../escape" }},
		{"relative Docker", func(c *Config) { c.DockerBin = "docker" }},
		{"relative real home", func(c *Config) { c.RealHome = "home" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copy := cfg
			tc.mutate(&copy)
			if _, err := prepare(copy); err == nil {
				t.Fatal("prepare unexpectedly succeeded")
			}
		})
	}
	desktop := Config{Backend: "docker-desktop", TargetKind: "local_compat", DockerContext: "desktop-linux", DockerHome: "/sandbox/home", RealHome: "/Users/operator", DockerEndpoint: "unix:///Users/operator/.docker/run/docker.sock"}
	if _, err := validateAuthority(desktop); err != nil {
		t.Fatalf("distinct Docker command and endpoint homes failed authority validation: %v", err)
	}
	marker := filepath.Join(cfg.TargetStateRoot, cfg.TargetKind, cfg.Backend, cfg.Profile, "workcell.managed")
	must(t, os.Remove(marker))
	must(t, os.Symlink(cfg.DockerConfig, marker))
	if _, err := prepare(cfg); err == nil {
		t.Fatal("prepare accepted symlink marker")
	}
}

func TestMainUsageUsesExitTwo(t *testing.T) {
	var stdout bytes.Buffer
	err := Main([]string{"unknown"}, &stdout)
	exitErr, ok := cliexit.IsExitCodeError(err)
	if !ok || exitErr.Code != 2 {
		t.Fatalf("unknown action error = %v, want exit code 2", err)
	}
}

func TestCommandRunnerKeepsWarningsOutOfProtocol(t *testing.T) {
	runner := commandRunner{cfg: Config{DockerCWD: t.TempDir(), ToolPath: "/usr/bin:/bin"}, bin: "/bin/sh"}
	if output, err := runner.Run("-c", "printf protocol; printf warning >&2"); err != nil || output != "protocol" {
		t.Fatalf("output = %q, error = %v", output, err)
	}
}

func addBuilder(docker *fakeDocker, builder, containerID string) {
	names := resourceNames(builder)
	docker.containers[names[1]] = containerID
	docker.volumes[names[3]] = volumeInspect{
		Name:       names[3],
		CreatedAt:  "2026-07-29T00:00:00Z",
		Driver:     "local",
		Mountpoint: "/var/lib/docker/volumes/" + names[3] + "/_data",
		Scope:      "local",
	}
}

func names[T any](values map[string]T) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return strings.Join(keys, "\n") + strings.Repeat("\n", min(len(keys), 1))
}

func equal(got []string, want ...string) bool {
	return strings.Join(got, "\x00") == strings.Join(want, "\x00")
}

func noSleep(time.Duration) {}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
