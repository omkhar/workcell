// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// How a root run that keeps the user's HOME makes the cache home and lock
// file for the user (issue 812): the temporary names that it uses, what it
// does when a step fails or another run gets there first, and what a killed
// root run leaves for the next run.

// A run of the user's that creates the cache home while a root run makes it
// keeps its own; the root run removes its temporary directory and goes on.
func TestCacheLockRootRunLosesHomeRace(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(home, cacheLockOtherUID, cacheLockOtherUID); err != nil {
		t.Fatal(err)
	}
	// The user's run creates the cache home just before the root run renames
	// its own into place.
	race := `*CORE::GLOBAL::rename = sub { mkdir($_[1], 0700); open(my $f, ">", "$_[1]/user"); chown(` +
		strconv.Itoa(cacheLockOtherUID) + `, ` + strconv.Itoa(cacheLockOtherUID) + `, $_[1]); CORE::rename($_[0], $_[1]) };`
	code, output := runCacheProbe(t, cacheLockLibrary(t)+cacheLockShim("workcell_cache_lock_perl", race)+`
workcell_cache_lock_perl prepare `+ShellQuote(filepath.Join(home, ".cache", "workcell.lock"))+`
`, nil)
	if code != 0 {
		t.Fatalf("root run exited %d:\n%s", code, output)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 1 {
		t.Fatalf("HOME after the race: %v, %v; want only .cache", entries, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cache", "user")); err != nil {
		t.Fatalf("the root run replaced the user's cache home: %v", err)
	}
}

// A root run killed between making the cache home and renaming it into
// place leaves an empty temporary directory. The next root run removes one
// that root owns once it is over a minute old, but never a younger one, which
// another root run may be making, one that anyone else owns, or a directory
// that is only named like one.
func TestCacheLockRootRunRemovesStaleTemp(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	home := filepath.Join(t.TempDir(), "home")
	for _, temp := range []struct {
		name  string
		uid   int
		fresh bool
	}{{"1.0000000a", 0, false}, {"2.0000000b", 0, true}, {"3.0000000c", cacheLockOtherUID, false}, {"tmp", 0, false}} {
		dir := filepath.Join(home, ".workcell-cache-home."+temp.name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, temp.uid, temp.uid); err != nil {
			t.Fatal(err)
		}
		if !temp.fresh {
			ageCacheLockEntry(t, dir)
		}
	}
	if err := os.Chown(home, cacheLockOtherUID, cacheLockOtherUID); err != nil {
		t.Fatal(err)
	}
	code, output := runCacheProbe(t, cacheLockLibrary(t)+`
workcell_cache_lock_perl prepare `+ShellQuote(filepath.Join(home, ".cache", "workcell.lock"))+`
`, nil)
	if code != 0 {
		t.Fatalf("root run exited %d:\n%s", code, output)
	}
	if got := cacheLockEntries(t, home); got != ".cache .workcell-cache-home.2.0000000b .workcell-cache-home.3.0000000c .workcell-cache-home.tmp" {
		t.Fatalf("HOME after the root run: %s; want .cache, the fresh and user temps and the look-alike", got)
	}
}

// ageCacheLockEntry dates path, not what a symlink points to, five minutes
// back.
func ageCacheLockEntry(t *testing.T, path string) {
	t.Helper()
	ts := unix.NsecToTimespec(time.Now().Add(-5 * time.Minute).UnixNano())
	if err := unix.UtimesNanoAt(unix.AT_FDCWD, path, []unix.Timespec{ts, ts}, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Fatal(err)
	}
}

// cacheLockEntries lists the names in dir, sorted, on one line.
func cacheLockEntries(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return strings.Join(names, " ")
}

// The user can swap the root run's temporary cache home for a directory of
// their own, or the cache home for another once it is renamed into place.
// The root run gives away only the empty directory that it made, and asks
// for a retry (75) when the name holds anything else.
func TestCacheLockRootRunChecksItsTemp(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	other := strconv.Itoa(cacheLockOtherUID)
	made := `*CORE::GLOBAL::mkdir = sub { CORE::mkdir($_[0], $_[1]) or return 0; return 1 unless $_[0] =~ /cache-home/; `
	for name, swap := range map[string]string{
		"temp swapped for the user's directory":  made + `rmdir($_[0]); CORE::mkdir($_[0], 0700); chown(` + other + `, ` + other + `, $_[0]) };`,
		"temp swapped for a full root directory": made + `open(my $f, ">", "$_[0]/entry") };`,
		"temp listed through another directory":  `*CORE::GLOBAL::opendir = sub (*$) { my $p = $_[1]; if ($p =~ /cache-home/) { CORE::mkdir("$p.other", 0700); $p = "$p.other" } CORE::opendir($_[0], $p) };`,
		"cache home swapped after the rename":    `*CORE::GLOBAL::rename = sub { CORE::rename($_[0], $_[1]) or return 0; CORE::rename($_[1], "$_[1].moved"); CORE::mkdir($_[1], 0700) };`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := filepath.Join(t.TempDir(), "home")
			if err := os.Mkdir(home, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(home, cacheLockOtherUID, cacheLockOtherUID); err != nil {
				t.Fatal(err)
			}
			code, output := runCacheProbe(t, cacheLockLibrary(t)+cacheLockShim("workcell_cache_lock_perl", swap)+`
workcell_cache_lock_perl prepare `+ShellQuote(filepath.Join(home, ".cache", "workcell.lock"))+`
`, nil)
			if code != 75 {
				t.Fatalf("root run exited %d, want 75:\n%s", code, output)
			}
		})
	}
}

// Any other failure to link a root run's lock file into place stops the run
// (73), so it never goes on unlocked while gc can remove its root.
func TestCacheLockRootRunStopsOnLinkFailure(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	for _, errno := range []string{"EACCES", "EIO"} {
		t.Run(errno, func(t *testing.T) {
			t.Parallel()
			home := filepath.Join(t.TempDir(), "home")
			if err := os.Mkdir(home, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(home, cacheLockOtherUID, cacheLockOtherUID); err != nil {
				t.Fatal(err)
			}
			code, output := runCacheProbe(t, cacheLockLibrary(t)+cacheLockShim("workcell_cache_lock_perl", `*CORE::GLOBAL::link = sub { $! = Errno::`+errno+`(); 0 };`)+`
workcell_cache_lock_perl prepare `+ShellQuote(filepath.Join(home, ".cache", "workcell.lock"))+`
`, nil)
			if code != 73 {
				t.Fatalf("root run exited %d, want 73:\n%s", code, output)
			}
		})
	}
}

// A root run killed before it removes its temporary lock file leaves it
// beside the lock. The next root run that creates a lock file there removes
// a regular one over a minute old, but never a younger one, a symlink, or a
// file only named like one, such as the lock of a root named .workcell-lock.r.
func TestCacheLockRootRunRemovesStaleLockTemp(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	cacheHome := filepath.Join(t.TempDir(), "home", ".cache")
	if err := os.MkdirAll(cacheHome, 0o700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(cacheHome, ".workcell-lock.r.lock")
	for _, name := range []string{".workcell-lock.r.lock", ".workcell-lock.1.0000000a", ".workcell-lock.2.0000000b", ".workcell-lock.3.0000000c",
		"xworkcell-lock.4.0000000d", ".workcell-lock-5.0000000e", ".workcell-lock.6-0000000f", ".workcell-lock.7.0000000ax", ".workcell-lock.8.0000000a0", ".workcell-lock.9.000000a", ".workcell-lock.r.0000000b"} {
		path := filepath.Join(cacheHome, name)
		var err error
		if name == ".workcell-lock.3.0000000c" {
			err = os.Symlink("/nonexistent", path)
		} else {
			err = os.WriteFile(path, nil, 0o600)
		}
		if err == nil {
			err = os.Lchown(path, cacheLockOtherUID, cacheLockOtherUID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if name != ".workcell-lock.2.0000000b" {
			ageCacheLockEntry(t, path)
		}
	}
	if err := os.Chown(cacheHome, cacheLockOtherUID, cacheLockOtherUID); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(lock)
	if err != nil {
		t.Fatal(err)
	}
	code, output := runCacheProbe(t, cacheLockLibrary(t)+`
workcell_cache_lock_perl prepare `+ShellQuote(lock)+`
`, nil)
	if after, err := os.Lstat(lock); code != 0 || err != nil || !os.SameFile(before, after) {
		t.Fatalf("root run exited %d or replaced the lock file (%v):\n%s", code, err, output)
	}
	if got := cacheLockEntries(t, cacheHome); got != ".workcell-lock-5.0000000e .workcell-lock.2.0000000b .workcell-lock.3.0000000c .workcell-lock.6-0000000f .workcell-lock.7.0000000ax .workcell-lock.8.0000000a0 .workcell-lock.9.000000a .workcell-lock.r.0000000b .workcell-lock.r.lock xworkcell-lock.4.0000000d" {
		t.Fatalf("cache home after the root run: %s; want all but the stale temp", got)
	}
}

// otherUserCacheHome makes HOME/.cache in base for cacheLockOtherUID and
// returns the lock file path in it.
func otherUserCacheHome(t *testing.T, base string) string {
	t.Helper()
	home := filepath.Join(base, "home")
	for _, dir := range []string{home, filepath.Join(home, ".cache")} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, cacheLockOtherUID, cacheLockOtherUID); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(home, ".cache", "workcell.lock")
}

// startRootRun starts a root run of prepare on lock, in a process group of its
// own, with the shim slowing it, and waits until ready holds for the lock
// file. The test may kill the group; cleanup kills it in any case.
func startRootRun(t *testing.T, lib, shim, lock string, ready func(*syscall.Stat_t) bool) *exec.Cmd {
	t.Helper()
	rootRun := exec.Command("/bin/bash", "-p", "-c", lib+shim+`workcell_cache_lock_perl prepare `+ShellQuote(lock)+"\n")
	rootRun.Env = []string{"PATH=/usr/bin:/bin", "BASH_ENV=", "ENV="}
	rootRun.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := rootRun.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-rootRun.Process.Pid, syscall.SIGKILL)
		_ = rootRun.Wait()
	})
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if info, err := os.Lstat(lock); err == nil && ready(info.Sys().(*syscall.Stat_t)) {
			return rootRun
		}
		if time.Now().After(deadline) {
			t.Fatal("the root run never reached the step that the test waits for")
		}
	}
}

// A user run that meets the lock file while a root run still has it linked
// under its temporary name asks for a retry (75), not the lasting refusal of
// a lock file with two links (77) that would let it run unlocked. One that a
// killed root run left linked is the user's own file in the user's own
// directory, so a later user run removes the second name and locks. Freshness
// goes by the ctime, not the mtime, and a ctime in the future is stale.
func TestCacheLockUserRunWaitsOutRootLink(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	for _, tc := range []struct{ name, after string }{
		{"finished", ""},
		{"killed", cacheLockLater()},
		{"killed, clock behind the link", cacheLockEarlier()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, lib := otherUserLibrary(t)
			lock := otherUserCacheHome(t, base)
			slow := cacheLockShim("workcell_cache_lock_perl", `*CORE::GLOBAL::unlink = sub { sleep 5 if $_[0] =~ /workcell-lock/; CORE::unlink(@_) };`)
			rootRun := startRootRun(t, lib, slow, lock, func(st *syscall.Stat_t) bool { return st.Nlink == 2 })
			ageCacheLockEntry(t, lock)
			prepare := lib + `workcell_cache_lock_perl prepare ` + ShellQuote(lock) + "\n"
			if code, output := runUnprivileged(t, prepare, nil); code != 75 {
				t.Fatalf("user run in the root run's link window exited %d, want 75:\n%s", code, output)
			}
			if tc.after != "" {
				if err := syscall.Kill(-rootRun.Process.Pid, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
				_ = rootRun.Wait()
				prepare = lib + tc.after + `workcell_cache_lock_perl prepare ` + ShellQuote(lock) + "\n"
			} else if err := rootRun.Wait(); err != nil {
				t.Fatalf("root run: %v", err)
			}
			if code, output := runUnprivileged(t, prepare, nil); code != 0 {
				t.Fatalf("user run after the root run exited %d:\n%s", code, output)
			}
			if got := cacheLockEntries(t, filepath.Dir(lock)); got != "workcell.lock" {
				t.Fatalf("cache home after the user run: %s; want only the lock file", got)
			}
			if info, err := os.Lstat(lock); err != nil || info.Sys().(*syscall.Stat_t).Nlink != 1 {
				t.Fatalf("lock file after the user run: %v; want one link", err)
			}
			gc := lib + `workcell_cache_lock_perl run-exclusive ` + ShellQuote(lock) + " /bin/true\n"
			if code, output := runUnprivileged(t, gc, nil); code != 0 {
				t.Fatalf("the user's gc exited %d:\n%s", code, output)
			}
		})
	}
}

// Without link(2), a root run creates the lock file in place as root and then
// gives it to the user. A user run that opens it in between asks for a retry
// (75); it does not take the lasting refusal (77), run unlocked, and let gc
// take the exclusive lock under it.
func TestCacheLockUserRunWaitsOutRootGive(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	base, lib := otherUserLibrary(t)
	lock := otherUserCacheHome(t, base)
	slow := cacheLockShim("workcell_cache_lock_perl", `*CORE::GLOBAL::link = sub { $! = Errno::EPERM(); 0 }; my $calls = 0; *CORE::GLOBAL::chown = sub { sleep 5 if $calls++; CORE::chown(@_) };`)
	rootRun := startRootRun(t, lib, slow, lock, func(st *syscall.Stat_t) bool { return st.Uid == 0 })
	prepare := lib + `workcell_cache_lock_perl prepare ` + ShellQuote(lock) + "\n"
	if code, output := runUnprivileged(t, prepare, nil); code != 75 {
		t.Fatalf("user run before the root run gave it the lock file exited %d, want 75:\n%s", code, output)
	}
	if err := rootRun.Wait(); err != nil {
		t.Fatalf("root run: %v", err)
	}
	if code, output := runUnprivileged(t, prepare, nil); code != 0 {
		t.Fatalf("user run after the root run exited %d:\n%s", code, output)
	}
}

// A root run that cannot give its temporary lock file to the user stops (73)
// and removes it, so it leaves no file of root's in the user's cache home.
func TestCacheLockRootRunRemovesUngivenTemp(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	base, lib := otherUserLibrary(t)
	lock := otherUserCacheHome(t, base)
	failing := cacheLockShim("workcell_cache_lock_perl", `*CORE::GLOBAL::chown = sub { $! = Errno::EPERM(); 0 };`)
	if code, output := runCacheProbe(t, lib+failing+`workcell_cache_lock_perl prepare `+ShellQuote(lock)+"\n", nil); code != 73 {
		t.Fatalf("root run exited %d, want 73:\n%s", code, output)
	}
	if got := cacheLockEntries(t, filepath.Dir(lock)); got != "" {
		t.Fatalf("cache home after the root run: %s; want it empty", got)
	}
}

// A root run that cannot give the lock file that it created in place to the
// user stops (73) and leaves that file, which another run may already have
// open. A user run asks for a retry (75) while the file is new, since the
// root run may yet give it. Once it is old, the user's gc refuses it as one
// that a root run left (77), so it keeps the root.
func TestCacheLockRootRunKeepsUngivenLock(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("acting as a root run and as its user needs root")
	}
	base, lib := otherUserLibrary(t)
	lock := otherUserCacheHome(t, base)
	failing := cacheLockShim("workcell_cache_lock_perl", `*CORE::GLOBAL::link = sub { $! = Errno::EPERM(); 0 }; my $given = 0; *CORE::GLOBAL::chown = sub { return CORE::chown(@_) unless $given++; $! = Errno::EPERM(); 0 };`)
	if code, output := runCacheProbe(t, lib+failing+`workcell_cache_lock_perl prepare `+ShellQuote(lock)+"\n", nil); code != 73 {
		t.Fatalf("root run exited %d, want 73:\n%s", code, output)
	}
	if info, err := os.Lstat(lock); err != nil || info.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Fatalf("root run removed or gave away the lock file that it could not give: %v", err)
	}
	// An old mtime with a new ctime is still new.
	ageCacheLockEntry(t, lock)
	gc := `workcell_cache_lock_perl run-exclusive ` + ShellQuote(lock) + " /bin/true\n"
	if code, output := runUnprivileged(t, lib+gc, nil); code != 75 {
		t.Fatalf("the user's gc on the new lock file exited %d, want 75:\n%s", code, output)
	}
	// An hour later, or with a clock behind the file's ctime, it is old.
	for _, clock := range []string{cacheLockLater(), cacheLockEarlier()} {
		code, output := runUnprivileged(t, lib+clock+`rc=0
reason="$(`+strings.TrimSuffix(gc, "\n")+` 2>&1)" || rc=$?
printf '%s\n' "${reason}"
grep -q 'a root run left' <<<"${reason}" || { echo "no reason given"; exit 1; }
exit "${rc}"
`, nil)
		if code != 77 {
			t.Fatalf("the user's gc on the old lock file exited %d, want 77 naming the root run's leftover:\n%s", code, output)
		}
	}
}

// cacheLockEarlier is a shim under which the helper looks an hour earlier, so
// a lock file that a root run just placed has a ctime in the future.
func cacheLockEarlier() string {
	return cacheLockShim("workcell_cache_lock_perl", "*CORE::GLOBAL::time = sub { CORE::time() - 3600 };")
}

// Only a root run gives a lock file that it made to the user, so a new lock
// file that another user owns is a lasting refusal (77) at once, not a retry.
func TestCacheLockUserRunRefusesOtherUsersNewLock(t *testing.T) {
	t.Parallel()
	if os.Geteuid() != 0 {
		t.Skip("leaving a lock file that another user owns needs root")
	}
	base, lib := otherUserLibrary(t)
	lock := otherUserCacheHome(t, base)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(lock, cacheLockOtherUID-1, cacheLockOtherUID-1); err != nil {
		t.Fatal(err)
	}
	if code, output := runUnprivileged(t, lib+`workcell_cache_lock_perl prepare `+ShellQuote(lock)+"\n", nil); code != 77 {
		t.Fatalf("user run on another user's new lock file exited %d, want 77:\n%s", code, output)
	}
}
