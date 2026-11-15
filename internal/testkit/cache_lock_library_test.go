// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The cache lock library (issue 812): scripts/lib/go-run-env.sh maps a Go
// cache path to the lock beside its Workcell cache root, and
// scripts/lib/workcell-cache-lock.pl takes flock(2) locks on it. A writer
// holds a shared lock for as long as its shell runs; gc takes an exclusive
// lock without waiting. These tests call workcell_cache_lock_shared and
// workcell_cache_lock_run_exclusive directly, against a fixture cache home.

// cacheLockFixture returns a private cache home, named .cache like the one
// gc reads, and its Workcell cache root.
func cacheLockFixture(t *testing.T) (string, string) {
	t.Helper()
	base, err := os.MkdirTemp(ExecFixtureDir(t), "cache-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeCacheLockTree(base) })
	home := filepath.Join(base, ".cache")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(home, "workcell")
}

// removeCacheLockTree removes a fixture whose tests made some directories
// unsearchable.
func removeCacheLockTree(base string) {
	_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(base)
}

// cacheLockLibrary is a probe prelude that sources the lock library.
func cacheLockLibrary(t *testing.T) string {
	t.Helper()
	return "set -euo pipefail\nunset GOPATH GOMODCACHE GOCACHE\nsource " +
		ShellQuote(filepath.Join(repoRoot(t), "scripts", "lib", "go-run-env.sh")) + "\n"
}

// cacheWriterEnv names a writer's Go cache under root; a probe takes its
// lock with workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}".
func cacheWriterEnv(root string) map[string]string {
	return map[string]string{"WORKCELL_GO_CACHE_ROOT": filepath.Join(root, "go")}
}

// cacheProbeTimeout bounds every probe, so a lock wait that blocks, such as
// a gc that waits for a writer instead of keeping its root, fails the test
// within seconds instead of hanging it until the package deadline.
const cacheProbeTimeout = 30 * time.Second

// runCacheProbe runs a bash probe in its own process group and kills the
// group when the probe outlives cacheProbeTimeout.
func runCacheProbe(t *testing.T, script string, env map[string]string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cacheProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-p", "-c", script)
	cmd.Env = append(os.Environ(), "BASH_ENV=", "ENV=")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("probe did not finish within %v; a cache lock wait blocked:\n%s", cacheProbeTimeout, output)
	}
	if err == nil {
		return 0, string(output)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(output)
	}
	t.Fatalf("probe failed: %v\n%s", err, output)
	return 0, ""
}

func TestCacheLockPath(t *testing.T) {
	t.Parallel()
	// A missing HOME and /h keep these rows lexical; a relative path is taken
	// from the working directory, /.
	code, output := runCacheProbe(t, cacheLockLibrary(t)+`
cd /
for dir in /h/.cache/workcell/go /h/.cache/workcell/go/build-cache/ /h/.cache/workcell /h/.cache/workcell/ \
  /h/Library/Caches/workcell/go /x/workcell/validate/go-build /h/.cache/go-build relative/.cache/workcell/go \
  /h/src/workcell/.cache/workcell/validate /h/src/workcell/go /h/.cache/workcell/go/mod/github.com/omkhar/workcell@v1 \
  "${XDG_CACHE_HOME}/workcell/go" /h/.cache//workcell/go /h/.cache/./workcell/go /h/.cache/x/../workcell/go \
  /h/.cache/workcell/.. /x/workcell/../workcell/validate; do
  printf '%s=%s\n' "${dir}" "$(workcell_cache_lock_path "${dir}")"
done
printf 'XDG_CACHE_HOME=/y//./z/. /y/z/workcell/go=%s\n' "$(XDG_CACHE_HOME=/y//./z/. workcell_cache_lock_path /y/z/workcell/go)"
`, map[string]string{"XDG_CACHE_HOME": "/x/", "HOME": "/nonexistent-cache-lock-home"})
	if code != 0 {
		t.Fatalf("probe exited %d: %s", code, output)
	}
	want := strings.Join([]string{
		"/h/.cache/workcell/go=/h/.cache/workcell.lock",
		"/h/.cache/workcell/go/build-cache/=/h/.cache/workcell.lock",
		"/h/.cache/workcell=/h/.cache/workcell.lock",
		"/h/.cache/workcell/=/h/.cache/workcell.lock",
		"/h/Library/Caches/workcell/go=/h/Library/Caches/workcell.lock",
		"/x/workcell/validate/go-build=/x/workcell.lock",
		"/h/.cache/go-build=",
		"relative/.cache/workcell/go=/relative/.cache/workcell.lock",
		"/h/src/workcell/.cache/workcell/validate=/h/src/workcell/.cache/workcell.lock",
		"/h/src/workcell/go=",
		"/h/.cache/workcell/go/mod/github.com/omkhar/workcell@v1=/h/.cache/workcell.lock",
		"/x//workcell/go=/x/workcell.lock",
		"/h/.cache//workcell/go=/h/.cache/workcell.lock",
		"/h/.cache/./workcell/go=/h/.cache/workcell.lock",
		"/h/.cache/x/../workcell/go=/h/.cache/workcell.lock",
		"/h/.cache/workcell/..=",
		"/x/workcell/../workcell/validate=/x/workcell.lock",
		"XDG_CACHE_HOME=/y//./z/. /y/z/workcell/go=/y/z/workcell.lock",
	}, "\n") + "\n"
	if output != want {
		t.Fatalf("lock paths:\n%s\nwant:\n%s", output, want)
	}
}
