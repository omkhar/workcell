// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The cache lock across users and processes (issue 812): runs as root that
// keep the user's HOME, what an older root run left behind, paths this user
// cannot resolve, and a lock that lives as long as the descriptors that hold
// it.

// cacheLockOtherUID is the user that probes run as when a test needs one
// other than root, such as the user whose HOME a sudo run keeps.
const cacheLockOtherUID = 65534

// otherUserLibrary returns a directory that every user can search and a
// prelude that sources a copy of the lock library kept in it, so a probe can
// run as cacheLockOtherUID even when that user cannot reach the checkout. A
// root test skips when no temporary directory is open to other users.
func otherUserLibrary(t *testing.T) (string, string) {
	t.Helper()
	var base string
	for _, parent := range []string{os.TempDir(), "/tmp", "/var/tmp", "/dev/shm"} {
		if os.Geteuid() == 0 && !searchableByOthers(parent) {
			continue
		}
		if dir, err := os.MkdirTemp(parent, "cache-lock-public-"); err == nil {
			base = dir
			break
		}
	}
	if base == "" {
		t.Skipf("uid %d can search none of the temporary directories, so this probe cannot run as that user", cacheLockOtherUID)
	}
	t.Cleanup(func() { removeCacheLockTree(base) })
	lib := filepath.Join(base, "repo", "scripts", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(lib), filepath.Dir(filepath.Dir(lib)), base} {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"go-run-env.sh", "workcell-cache-lock.pl"} {
		data, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "lib", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lib, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return base, "set -euo pipefail\nsource " + ShellQuote(filepath.Join(lib, "go-run-env.sh")) + "\n"
}

// searchableByOthers reports whether every directory from dir up to / lets
// other users search it.
func searchableByOthers(dir string) bool {
	for {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm()&0o001 == 0 {
			return false
		}
		if parent := filepath.Dir(dir); parent != dir {
			dir = parent
			continue
		}
		return true
	}
}

// runUnprivileged runs a bash probe as cacheLockOtherUID when the test runs
// as root, and as the test's own user otherwise.
func runUnprivileged(t *testing.T, script string, env map[string]string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cacheProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "-p", "-c", script)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "BASH_ENV=", "ENV="}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Dir = "/"
	if os.Geteuid() == 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: cacheLockOtherUID, Gid: cacheLockOtherUID}}
	}
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("probe did not finish within %v:\n%s", cacheProbeTimeout, output)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(output)
	}
	if err != nil {
		t.Fatalf("probe failed: %v\n%s", err, output)
	}
	return 0, string(output)
}

// A root run that keeps the user's HOME, such as sudo workcell, gives the
// cache home and lock file that it creates to the user, so the user's gc
// meets the root run's lock and the user's own runs lock after it. It does so
// on a file system without link(2) too, such as some FUSE file systems.
func TestCacheLockRootRunSharesUserLock(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	for name, shim := range map[string]string{
		"with link(2)":    "",
		"without link(2)": cacheLockShim("workcell_cache_lock_perl", `*CORE::GLOBAL::link = sub { $! = Errno::EPERM(); 0 };`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rootRunSharesUserLock(t, shim)
		})
	}
}

func rootRunSharesUserLock(t *testing.T, shim string) {
	base, lib := otherUserLibrary(t)
	home := filepath.Join(base, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(home, cacheLockOtherUID, cacheLockOtherUID); err != nil {
		t.Fatal(err)
	}
	cacheHome := filepath.Join(home, ".cache")
	lock := filepath.Join(cacheHome, "workcell.lock")
	userOwned := func(path string) {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != cacheLockOtherUID || info.Mode().IsRegular() && st.Nlink != 1 || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("root run left %s that the user does not own privately: %v %v", path, info.Mode(), info.Sys())
		}
	}
	dir := ShellQuote(filepath.Join(cacheHome, "workcell", "go"))
	ready := filepath.Join(t.TempDir(), "ready")
	writer := exec.Command("/bin/bash", "-p", "-c", lib+shim+`workcell_cache_lock_shared `+dir+`
: >`+ShellQuote(ready)+`
exec sleep 300
`)
	writer.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "BASH_ENV=", "ENV="}
	writer.Stdout, writer.Stderr = io.Discard, io.Discard
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = writer.Process.Kill()
		_ = writer.Wait()
	})
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("root writer did not take the lock")
		}
	}
	userOwned(cacheHome)
	userOwned(lock)
	// The root run made the cache home under a temporary name and renamed
	// it into place; nothing else is left in HOME.
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 1 {
		t.Fatalf("HOME after the root run: %v, %v; want only .cache", entries, err)
	}

	code, output := runUnprivileged(t, lib+`
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(lock)+` /bin/true || rc=$?
exit "${rc}"
`, nil)
	if code != 75 {
		t.Fatalf("the user's gc did not meet the root run's lock (status %d):\n%s", code, output)
	}
	_ = writer.Process.Kill()
	_ = writer.Wait()
	code, output = runUnprivileged(t, lib+`
workcell_cache_lock_shared `+dir+`
[[ -n "${WORKCELL_CACHE_LOCK_FDS}" ]] || { echo "the user's run did not lock"; exit 1; }
`, nil)
	if code != 0 {
		t.Fatalf("the user's run after the root run exited %d:\n%s", code, output)
	}
	// A later root run opens the user's lock file as it is, never a new one.
	before, err := os.Lstat(lock)
	if err != nil {
		t.Fatal(err)
	}
	code, output = runCacheProbe(t, lib+shim+`
workcell_cache_lock_perl prepare `+ShellQuote(lock)+`
`, nil)
	if after, err := os.Lstat(lock); code != 0 || err != nil || !os.SameFile(before, after) {
		t.Fatalf("a later root run replaced the user's lock file (status %d, %v):\n%s", code, err, output)
	}
	// A root gc that meets no lock file creates one for the user too.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if code, output := runCacheProbe(t, lib+shim+`workcell_cache_lock_run_exclusive `+ShellQuote(lock)+` /bin/true
`, nil); code != 0 {
		t.Fatalf("root gc exited %d:\n%s", code, output)
	}
	userOwned(lock)
}

// What an older root run left behind, a cache home or lock file that this
// user cannot use, is a lasting refusal (77) that names the reason, not a
// failed open (73) that would stop every run. The open's own error decides:
// the checks that follow it must not hide it.
func TestCacheLockUnusableLeftovers(t *testing.T) {
	t.Parallel()
	root := os.Geteuid() == 0
	cases := []struct {
		name     string
		rootOnly bool
		// setup makes what the run left in cacheHome; the probe's user owns
		// cacheHome unless it changes the owner.
		setup  func(t *testing.T, cacheHome string)
		reason string
		// under is where the lock's own cache home sits in cacheHome, such
		// as an XDG_CACHE_HOME of ~/.cache/xdg.
		under string
	}{
		{"unwritable cache home", false, func(t *testing.T, cacheHome string) {
			chmodOrFatal(t, cacheHome, 0o555)
		}, "cannot open", ""},
		{"unsearchable cache home", false, func(t *testing.T, cacheHome string) {
			chmodOrFatal(t, cacheHome, 0o600)
		}, "cannot reach", ""},
		{"unsearchable cache home above XDG_CACHE_HOME", false, func(t *testing.T, cacheHome string) {
			chmodOrFatal(t, cacheHome, 0o600)
		}, "cannot reach", "xdg"},
		{"root lock file", true, func(t *testing.T, cacheHome string) {
			if err := os.WriteFile(filepath.Join(cacheHome, "workcell.lock"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "cannot open", ""},
		{"root cache home", true, func(t *testing.T, cacheHome string) {
			if err := os.Chown(cacheHome, 0, 0); err != nil {
				t.Fatal(err)
			}
		}, "cannot reach", ""},
		{"root cache home above XDG_CACHE_HOME", true, func(t *testing.T, cacheHome string) {
			if err := os.Chown(cacheHome, 0, 0); err != nil {
				t.Fatal(err)
			}
		}, "cannot reach", "xdg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.rootOnly && !root {
				t.Skip("leaving files that another user owns needs root")
			}
			base, lib := otherUserLibrary(t)
			home := filepath.Join(base, "home")
			cacheHome := filepath.Join(home, ".cache")
			if err := os.MkdirAll(cacheHome, 0o700); err != nil {
				t.Fatal(err)
			}
			chmodOrFatal(t, home, 0o755)
			if root {
				for _, dir := range []string{home, cacheHome} {
					if err := os.Chown(dir, cacheLockOtherUID, cacheLockOtherUID); err != nil {
						t.Fatal(err)
					}
				}
			}
			tc.setup(t, cacheHome)
			// The run looks an hour later, past the seconds in which a root
			// run may still be giving a lock file that it made to the user.
			later := cacheLockLater()
			code, output := runUnprivileged(t, lib+later+`
rc=0
reason="$(workcell_cache_lock_perl prepare `+ShellQuote(filepath.Join(cacheHome, tc.under, "workcell.lock"))+` 2>&1)" || rc=$?
printf '%s\n' "${reason}"
[[ "${rc}" -eq 77 ]] || { echo "helper status ${rc}, want 77"; exit 1; }
grep -q -- `+ShellQuote("workcell-cache-lock: refusing .*"+tc.reason)+` <<<"${reason}" || { echo "no reason given"; exit 1; }
`, nil)
			if code != 0 {
				t.Fatalf("probe exited %d:\n%s", code, output)
			}
		})
	}
}

// cacheLockLater is a shim under which the helper looks an hour later, so
// what a root run left in the last few seconds reads as long left.
func cacheLockLater() string {
	return cacheLockShim("workcell_cache_lock_perl", "*CORE::GLOBAL::time = sub { CORE::time() + 3600 };")
}

func chmodOrFatal(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// A path that cannot be resolved to tell whether it is in a cache root gets
// a warning, never a silent skip, and the writer goes on as for 69 and 77.
func TestCacheLockUnresolvedPathWarns(t *testing.T) {
	t.Parallel()
	base, lib := otherUserLibrary(t)
	private := filepath.Join(base, "private")
	if err := os.MkdirAll(filepath.Join(private, ".cache", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The probe's user cannot search private, so .. under it cannot be
	// resolved; filepath.Join would clean the .. away.
	if err := os.Chmod(private, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := ShellQuote(filepath.Join(private, ".cache", "x") + "/../workcell/go")
	code, output := runUnprivileged(t, lib+`
rc=0
workcell_cache_lock_path `+dir+` >/dev/null || rc=$?
[[ "${rc}" -eq 2 ]] || { echo "path status ${rc}, want 2"; exit 1; }
warning="$(workcell_cache_lock_shared `+dir+` 2>&1)"
grep -q 'Workcell warning: cannot resolve .* in use without the cache lock' <<<"${warning}" || { echo "no warning: ${warning}"; exit 1; }
`, nil)
	if code != 0 {
		t.Fatalf("probe exited %d:\n%s", code, output)
	}
}

// A candidate cache home that cannot be resolved cannot hold the path, so
// the lookup skips it and still finds the root, instead of giving up.
func TestCacheLockUnresolvableHomeSkipped(t *testing.T) {
	t.Parallel()
	base, lib := otherUserLibrary(t)
	public := filepath.Join(base, "public", ".cache")
	if err := os.MkdirAll(filepath.Join(public, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(public), 0o755); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(base, "private-xdg")
	if err := os.Mkdir(private, 0o600); err != nil {
		t.Fatal(err)
	}
	code, output := runUnprivileged(t, lib+`
workcell_cache_lock_path `+ShellQuote(filepath.Join(public, "x")+"/../workcell/go")+`
`, map[string]string{"XDG_CACHE_HOME": private})
	if want := filepath.Join(public, "workcell.lock") + "\n"; code != 0 || output != want {
		t.Fatalf("lock path status %d:\n%s\nwant:\n%s", code, output, want)
	}
}

// A child that the writer's shell started keeps the root after that shell
// exits: the lock lives on the descriptor that the child inherited, not on
// the shell's process ID.
func TestCacheLockChildWriterOutlivesShell(t *testing.T) {
	t.Parallel()
	_, root := cacheLockFixture(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	code, output := runCacheProbe(t, cacheLockLibrary(t)+`
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}"
( exec sleep 300 ) </dev/null >/dev/null 2>&1 &
printf '%s\n' "$!" >`+ShellQuote(pidFile)+`
`, cacheWriterEnv(root))
	if code != 0 {
		t.Fatalf("writer exited %d: %s", code, output)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	// Give a lock that follows the shell's process ID time to lapse.
	time.Sleep(time.Second)
	if code := gcLockStatus(t, root+".lock"); code != 75 {
		t.Fatalf("gc could lock a root that a child writer holds after its shell exited (status %d)", code)
	}
	if err := syscall.Kill(child, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitForGCLock(t, root+".lock", 0, "the lock outlived the last writer")
}

// release_all drops the lock even when the caller ignores SIGTERM, and a
// lock first taken in a command substitution ends with it, so release_all in
// the main shell leaves nothing behind.
func TestCacheLockReleaseAll(t *testing.T) {
	t.Parallel()
	for name, take := range map[string]string{
		"SIGTERM ignored": "trap '' TERM\nworkcell_cache_lock_shared \"${WORKCELL_GO_CACHE_ROOT}\"\n",
		"subshell first":  "sub=\"$(workcell_cache_lock_shared \"${WORKCELL_GO_CACHE_ROOT}\"; echo sub)\"\n[[ \"${sub}\" == sub ]]\nworkcell_cache_lock_shared \"${WORKCELL_GO_CACHE_ROOT}\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, root := cacheLockFixture(t)
			code, output := runCacheProbe(t, cacheLockLibrary(t)+take+`
[[ -n "${WORKCELL_CACHE_LOCK_FDS}" ]] || { echo "the writer did not lock"; exit 1; }
workcell_cache_lock_release_all
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(root+".lock")+` /bin/true || rc=$?
exit "${rc}"
`, cacheWriterEnv(root))
			if code != 0 {
				t.Fatalf("gc could not lock after release_all (status %d):\n%s", code, output)
			}
		})
	}
}
