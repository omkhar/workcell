// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The cache lock's defenses (issue 812): failures that stop a writer or
// that it outlasts, file systems without working flock(2), unsafe lock files
// and cache homes, every spelling of a cache path and root, and races between
// a writer and gc. Like the library tests, these drive
// workcell_cache_lock_shared and workcell_cache_lock_run_exclusive.

// cacheLockShim defines the bash function NAME, which runs the lock helper
// like workcell_cache_lock_perl after the perl BEGIN block BEGIN, such as an
// override of flock that fakes a file system without flock(2).
func cacheLockShim(name, begin string) string {
	return name + `() {
  /usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/perl -MErrno -MFcntl=:flock -e 'BEGIN { ` + begin + ` } $0 = shift; do $0; die $@ if $@;' \
    "${GO_RUN_ENV_REPO_ROOT}/scripts/lib/workcell-cache-lock.pl" "$@"
}
`
}

// symlinkCacheHome moves the cache home to HOME.real and leaves a symlink to
// it in its place, a common dotfile layout.
func symlinkCacheHome(t *testing.T, home string) {
	t.Helper()
	if err := os.Rename(home, home+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home+".real", home); err != nil {
		t.Fatal(err)
	}
}

// gcLockStatus returns the status of an exclusive lock on lock, 75 while a
// writer holds it.
func gcLockStatus(t *testing.T, lock string) int {
	t.Helper()
	code, _ := runCacheProbe(t, cacheLockLibrary(t)+`
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(lock)+` /bin/true || rc=$?
exit "${rc}"
`, nil)
	return code
}

// waitForGCLock waits up to ten seconds for gc's lock status to become want.
func waitForGCLock(t *testing.T, lock string, want int, why string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := gcLockStatus(t, lock)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s (gc lock status %d, want %d)", why, got, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Only a lasting refusal (69: no flock(2), 77: an unsafe lock file) lets a
// writer go on without the lock. Any other failure, such as a failed fork or
// EMFILE, is retried and then stops the writer, since gc could lock the root
// later and remove it in use.
func TestCacheLockTransientFailureStopsWriter(t *testing.T) {
	t.Parallel()
	failingHelper := func(status int) string {
		return fmt.Sprintf("workcell_cache_lock_perl() { return %d; }\n", status)
	}
	for _, status := range []int{64, 71, 73, 75, 1, 69, 77} {
		t.Run(fmt.Sprintf("status %d", status), func(t *testing.T) {
			t.Parallel()
			_, root := cacheLockFixture(t)
			// || turns set -e off, so the probe sees the status itself.
			code, output := runCacheProbe(t, cacheLockLibrary(t)+failingHelper(status)+
				`workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}" || exit 9`+"\n", cacheWriterEnv(root))
			lasting := status == 69 || status == 77
			if lasting && code != 0 || !lasting && code != 9 {
				t.Fatalf("writer status %d after lock status %d:\n%s", code, status, output)
			}
		})
	}

	// One transient failure is retried, and the retry holds the lock.
	t.Run("retried", func(t *testing.T) {
		t.Parallel()
		_, root := cacheLockFixture(t)
		failed := filepath.Join(t.TempDir(), "failed")
		code, output := runCacheProbe(t, cacheLockLibrary(t)+`
eval "$(declare -f workcell_cache_lock_perl | sed '1s/^workcell_cache_lock_perl/real_workcell_cache_lock_perl/')"
workcell_cache_lock_perl() {
  if [[ ! -e `+ShellQuote(failed)+` ]]; then
    : >`+ShellQuote(failed)+`
    return 71
  fi
  real_workcell_cache_lock_perl "$@"
}
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}"
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(root+".lock")+` /bin/true || rc=$?
exit "${rc}"
`, cacheWriterEnv(root))
		if code != 75 {
			t.Fatalf("writer did not retry the lock (gc lock status %d):\n%s", code, output)
		}
	})
}

// A file system where flock(2) fails, or where it locks the process instead
// of the open file, is a lasting refusal: the writer goes on unlocked and gc
// keeps the root (69).
func TestCacheLockUnsupportedFlock(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"ENOLCK":     `*CORE::GLOBAL::flock = sub { $! = Errno::ENOLCK(); return 0 };`,
		"EOPNOTSUPP": `*CORE::GLOBAL::flock = sub { $! = Errno::EOPNOTSUPP(); return 0 };`,
		"EINVAL":     `*CORE::GLOBAL::flock = sub { $! = Errno::EINVAL(); return 0 };`,
		// Every lock succeeds, as when locks belong to the process.
		"process lock": `*CORE::GLOBAL::flock = sub { return 1 };`,
	}
	for name, begin := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, root := cacheLockFixture(t)
			code, output := runCacheProbe(t, cacheLockLibrary(t)+cacheLockShim("workcell_cache_lock_perl", begin)+`
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}"
[[ -z "${WORKCELL_CACHE_LOCK_FDS}" ]] || { echo "writer kept a descriptor without a lock"; exit 1; }
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(root+".lock")+` /bin/true || rc=$?
exit "${rc}"
`, cacheWriterEnv(root))
			if code != 69 {
				t.Fatalf("gc lock status %d, want 69:\n%s", code, output)
			}
		})
	}
}

func TestCacheLockRefusesUnsafeLockFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		setup func(t *testing.T, home, lock string)
		// check runs after gc and the writer, for a case that must leave
		// something untouched.
		check func(t *testing.T, home, lock string)
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, home, lock string) {
				target := filepath.Join(home, "target")
				if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, lock); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, home, lock string) {
				if data, err := os.ReadFile(filepath.Join(home, "target")); err != nil || string(data) != "keep\n" {
					t.Fatalf("symlink target changed: %q, %v", data, err)
				}
			},
		},
		{
			name: "other owner",
			setup: func(t *testing.T, home, lock string) {
				if os.Geteuid() != 0 {
					t.Skip("making a file owned by another user needs root")
				}
				if err := os.WriteFile(lock, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Lchown(lock, 65534, 65534); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hard link",
			setup: func(t *testing.T, home, lock string) {
				other := filepath.Join(home, "other")
				if err := os.WriteFile(other, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(other, lock); err != nil {
					t.Fatal(err)
				}
				// A root run's temporary lock file of another inode does not
				// make the second link a brief one.
				if err := os.WriteFile(filepath.Join(home, ".workcell-lock.1.0000000a"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "group readable",
			setup: func(t *testing.T, home, lock string) {
				if err := os.WriteFile(lock, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(lock, 0o640); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, home, lock string) {
				if info, err := os.Lstat(lock); err != nil || info.Mode().Perm() != 0o640 {
					t.Fatalf("lock file mode changed: %v, %v", info.Mode(), err)
				}
			},
		},
		{
			name: "directory",
			setup: func(t *testing.T, home, lock string) {
				if err := os.Mkdir(lock, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "fifo",
			setup: func(t *testing.T, home, lock string) {
				if err := syscall.Mkfifo(lock, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink to shared directory",
			setup: func(t *testing.T, home, lock string) {
				symlinkCacheHome(t, home)
				if err := os.Chmod(home+".real", 0o777); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "shared directory",
			setup: func(t *testing.T, home, lock string) {
				if err := os.Chmod(home, 0o777); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home, root := cacheLockFixture(t)
			lock := root + ".lock"
			tc.setup(t, home, lock)
			warnings := ShellQuote(filepath.Join(t.TempDir(), "warnings"))
			// gc cannot lock (77), and a writer warns once, gives the
			// helper's reason, and goes on.
			code, output := runCacheProbe(t, cacheLockLibrary(t)+`
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(lock)+` /bin/true || rc=$?
[[ "${rc}" -eq 77 ]] || { echo "gc lock status ${rc}, want 77"; exit 1; }
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}" 2>>`+warnings+`
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}" 2>>`+warnings+`
[[ "$(grep -c 'in use without the cache lock' `+warnings+`)" -eq 1 ]] || { echo "the writer did not warn once"; cat `+warnings+`; exit 1; }
grep -q 'workcell-cache-lock: refusing' `+warnings+` || { echo "the helper gave no reason"; cat `+warnings+`; exit 1; }
`, cacheWriterEnv(root))
			if code != 0 {
				t.Fatalf("probe exited %d:\n%s", code, output)
			}
			if tc.check != nil {
				tc.check(t, home, lock)
			}
		})
	}
}

// A symlinked cache home is a private directory too: the lock lives in the
// directory that the link resolves to.
func TestCacheLockSymlinkedCacheHome(t *testing.T) {
	t.Parallel()
	home, root := cacheLockFixture(t)
	symlinkCacheHome(t, home)
	code, output := runCacheProbe(t, cacheLockLibrary(t)+`
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}"
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(root+".lock")+` /bin/true || rc=$?
exit "${rc}"
`, cacheWriterEnv(root))
	if code != 75 {
		t.Fatalf("writer under a symlinked cache home did not hold the lock (gc lock status %d):\n%s", code, output)
	}
	info, err := os.Lstat(filepath.Join(home+".real", "workcell.lock"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("lock file in the resolved cache home: %v, %v", info, err)
	}
	waitForGCLock(t, root+".lock", 0, "the lock outlived its writer")
}

// A writer that names its root through another spelling holds the same lock
// as gc: a symlinked alias of the cache home, an XDG_CACHE_HOME with a
// trailing slash, .., or a cache root that is itself a symlink, whose lock is
// beside the directory that it points to.
func TestCacheLockAliasedRootHoldsLock(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// setup returns the writer's environment and the lock that gc takes.
		setup func(t *testing.T, home, root string) (map[string]string, string)
	}{
		{"symlinked alias", func(t *testing.T, home, root string) (map[string]string, string) {
			symlinkCacheHome(t, home)
			return map[string]string{"HOME": filepath.Dir(home), "WORKCELL_GO_CACHE_ROOT": home + ".real/workcell/go"}, root + ".lock"
		}},
		{"XDG_CACHE_HOME with a trailing slash", func(t *testing.T, home, root string) (map[string]string, string) {
			if runtime.GOOS == "darwin" {
				t.Skip("macOS runs use ~/Library/Caches")
			}
			return map[string]string{"XDG_CACHE_HOME": home + "/"}, root + ".lock"
		}},
		{"dot-dot", func(t *testing.T, home, root string) (map[string]string, string) {
			return map[string]string{"WORKCELL_GO_CACHE_ROOT": home + "/x/../workcell/go"}, root + ".lock"
		}},
		{"cache root holding an entry named like itself", func(t *testing.T, home, root string) (map[string]string, string) {
			if err := os.MkdirAll(filepath.Join(root, "workcell"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "workcell.lock"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return cacheWriterEnv(root), root + ".lock"
		}},
	}
	// A cache root that is a symlink, however its target is spelled, shares
	// the lock of the directory that it points to, never one inside it: X
	// is that directory, and W a second link to it.
	for _, spelling := range []string{"X", "X/", "X/.", "X//", "../Library/Caches/workcell/", "X/go/..", "W", "W/", "W/.", "../wc/"} {
		cases = append(cases, struct {
			name  string
			setup func(t *testing.T, home, root string) (map[string]string, string)
		}{"symlinked cache root to " + spelling, func(t *testing.T, home, root string) (map[string]string, string) {
			real := filepath.Join(filepath.Dir(home), "Library", "Caches", "workcell")
			if err := os.MkdirAll(filepath.Join(real, "go"), 0o700); err != nil {
				t.Fatal(err)
			}
			wc := filepath.Join(filepath.Dir(home), "wc")
			if err := os.Symlink(real, wc); err != nil {
				t.Fatal(err)
			}
			target := strings.Replace(strings.Replace(spelling, "X", real, 1), "W", wc, 1)
			if err := os.Symlink(target, root); err != nil {
				t.Fatal(err)
			}
			return cacheWriterEnv(root), real + ".lock"
		}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home, root := cacheLockFixture(t)
			env, lock := tc.setup(t, home, root)
			code, output := runCacheProbe(t, cacheLockLibrary(t)+`
[[ -n "${WORKCELL_GO_CACHE_ROOT:-}" ]] || unset WORKCELL_GO_CACHE_ROOT
workcell_cache_lock_shared "$(default_go_cache_root)"
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(lock)+` /bin/true || rc=$?
exit "${rc}"
`, env)
			if code != 75 {
				t.Fatalf("writer did not hold the lock that gc takes (gc lock status %d):\n%s", code, output)
			}
		})
	}
}

// A writer under umask 002 creates its cache home owner-only, so it can lock
// the root instead of refusing a group-writable home.
func TestCacheLockCreatesOwnerOnlyHome(t *testing.T) {
	t.Parallel()
	home, root := cacheLockFixture(t)
	if err := os.Remove(home); err != nil {
		t.Fatal(err)
	}
	code, output := runCacheProbe(t, cacheLockLibrary(t)+`
umask 002
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}"
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(root+".lock")+` /bin/true || rc=$?
exit "${rc}"
`, cacheWriterEnv(root))
	if code != 75 {
		t.Fatalf("writer under umask 002 did not hold the lock (gc lock status %d):\n%s", code, output)
	}
	info, err := os.Stat(home)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("cache home: %v, %v; want mode 0700", info, err)
	}
}

// A writer that waited for gc checks that the lock file it locked is still
// the one at the path: gc may have removed it, and a writer locked on the
// removed file would not keep the next gc out. gc here removes the file only
// once the writer says that it waits.
func TestCacheLockWriterRechecksAfterWait(t *testing.T) {
	t.Parallel()
	_, root := cacheLockFixture(t)
	lock := ShellQuote(root + ".lock")
	dir := t.TempDir()
	ready, waited := ShellQuote(filepath.Join(dir, "ready")), ShellQuote(filepath.Join(dir, "stderr"))
	code, output := runCacheProbe(t, cacheLockLibrary(t)+`
workcell_cache_lock_run_exclusive `+lock+` /bin/bash -c '
: >"$2"
for _ in $(seq 200); do
  if grep -q "waiting for workcell --gc" "$3" 2>/dev/null; then
    rm -f -- "$1"
    exit 0
  fi
  sleep 0.05
done
exit 1' gc `+lock+` `+ready+` `+waited+` &
gc=$!
until [[ -e `+ready+` ]]; do sleep 0.05; done
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}" 2>`+waited+`
wait "${gc}" || { echo "the writer did not say that it waited"; exit 1; }
rc=0
workcell_cache_lock_run_exclusive `+lock+` /bin/true || rc=$?
exit "${rc}"
`, cacheWriterEnv(root))
	if code != 75 {
		t.Fatalf("writer did not hold the lock file at the path after gc removed the old one (gc lock status %d):\n%s", code, output)
	}
}

// gc checks after it locks that the file is still the one at the path, so a
// writer that locked a new file there meanwhile keeps its root.
func TestCacheLockGCRechecksAfterLock(t *testing.T) {
	t.Parallel()
	_, root := cacheLockFixture(t)
	lock := ShellQuote(root + ".lock")
	dir := t.TempDir()
	ready, proceed := filepath.Join(dir, "ready"), filepath.Join(dir, "go")
	// The shim stops gc after it opens the lock file and before its
	// exclusive lock, until the writer has locked a new file.
	code, output := runCacheProbe(t, cacheLockLibrary(t)+cacheLockShim("slow_gc_perl",
		`*CORE::GLOBAL::flock = sub { if ($_[1] & LOCK_EX) { open(my $r, ">", q{`+ready+`}) or die; close($r); for (1 .. 400) { last if -e q{`+proceed+`}; select(undef, undef, undef, 0.05) } } CORE::flock($_[0], $_[1]) };`)+`
slow_gc_perl run-exclusive `+lock+` /bin/true &
gc=$!
until [[ -e `+ShellQuote(ready)+` ]]; do sleep 0.05; done
rm -f -- `+lock+`
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}"
: >`+ShellQuote(proceed)+`
rc=0
wait "${gc}" || rc=$?
exit "${rc}"
`, cacheWriterEnv(root))
	if code != 75 {
		t.Fatalf("gc ran on a replaced lock file that a writer holds (status %d):\n%s", code, output)
	}
}

// Without the lock helper, no run can lock: the writer goes on after a
// warning and gc keeps the root (69).
func TestCacheLockMissingHelper(t *testing.T) {
	t.Parallel()
	_, root := cacheLockFixture(t)
	dir := t.TempDir()
	warning := ShellQuote(filepath.Join(dir, "warning"))
	code, output := runCacheProbe(t, cacheLockLibrary(t)+`
GO_RUN_ENV_REPO_ROOT=`+ShellQuote(filepath.Join(dir, "missing"))+`
workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}" 2>`+warning+`
grep -q 'in use without the cache lock' `+warning+` || { echo "the writer did not warn"; exit 1; }
[[ -z "${WORKCELL_CACHE_LOCK_FDS}" ]] || { echo "writer kept a descriptor without a lock"; exit 1; }
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(root+".lock")+` /bin/true || rc=$?
exit "${rc}"
`, cacheWriterEnv(root))
	if code != 69 {
		t.Fatalf("gc lock status %d, want 69:\n%s", code, output)
	}
}

// A refusal that only an alias meets, through a symlink in the cache home or
// cache root, stops the run instead of letting it go on unlocked, since gc of
// the directory that the alias points to would not meet the refusal and
// would remove the root in use. gc keeps a root that it reaches through such
// an alias, and a symlink loop is refused, not followed forever.
func TestCacheLockUnsafeAliasStopsRun(t *testing.T) {
	t.Parallel()
	physical := func(t *testing.T, home string) string {
		real := filepath.Join(filepath.Dir(home), "Library", "Caches", "workcell")
		if err := os.MkdirAll(filepath.Join(real, "go"), 0o700); err != nil {
			t.Fatal(err)
		}
		return real
	}
	symlink := func(t *testing.T, target, link string) {
		if err := os.RemoveAll(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	loop := func(t *testing.T, at string) {
		symlink(t, at+".loop", at)
		symlink(t, at, at+".loop")
	}
	for name, setup := range map[string]func(t *testing.T, home, root string){
		"root link in a group-writable cache home": func(t *testing.T, home, root string) {
			symlink(t, physical(t, home), root)
			chmodOrFail(t, home, 0o775)
		},
		"cache home link in a shared directory": func(t *testing.T, home, root string) {
			symlinkCacheHome(t, home)
			chmodOrFail(t, filepath.Dir(home), 0o777)
		},
		"cache home link owned by another user": func(t *testing.T, home, root string) {
			if os.Geteuid() != 0 {
				t.Skip("making a symlink owned by another user needs root")
			}
			symlinkCacheHome(t, home)
			if err := os.Lchown(home, 65534, 65534); err != nil {
				t.Fatal(err)
			}
		},
		"second link in a shared directory, through a trailing slash": func(t *testing.T, home, root string) {
			shared := filepath.Join(filepath.Dir(home), "shared")
			if err := os.Mkdir(shared, 0o700); err != nil {
				t.Fatal(err)
			}
			symlink(t, physical(t, home), filepath.Join(shared, "wc"))
			chmodOrFail(t, shared, 0o777)
			symlink(t, filepath.Join(shared, "wc")+"/", root)
		},
		"root linked to its own cache home": func(t *testing.T, home, root string) {
			symlink(t, home, root)
		},
		"root linked above its cache home": func(t *testing.T, home, root string) {
			symlink(t, filepath.Dir(home)+"/", root)
		},
		"cache home loop": func(t *testing.T, home, root string) { loop(t, home) },
		"cache root loop": func(t *testing.T, home, root string) { loop(t, root) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, root := cacheLockFixture(t)
			setup(t, home, root)
			code, output := runCacheProbe(t, cacheLockLibrary(t)+`
rc=0
workcell_cache_lock_run_exclusive `+ShellQuote(root+".lock")+` /bin/true 2>/dev/null || rc=$?
[[ "${rc}" -eq 73 ]] || { echo "gc lock status ${rc}, want 73"; exit 1; }
rc=0
reason="$(workcell_cache_lock_shared "${WORKCELL_GO_CACHE_ROOT}" 2>&1)" || rc=$?
printf '%s\n' "${reason}"
[[ "${rc}" -ne 0 ]] || { echo "the writer went on"; exit 1; }
grep -q 'workcell-cache-lock: refusing .*so the run stops' <<<"${reason}" || { echo "no reason given"; exit 1; }
`, cacheWriterEnv(root))
			if code != 0 {
				t.Fatalf("probe exited %d:\n%s", code, output)
			}
		})
	}
}

// A cache home spelled through a symlinked parent, as HOME often is, is
// compared with the root once both are resolved, so a root linked to the
// home or above it stops the run (73) however the home is spelled.
func TestCacheLockRootAboveLinkedHomeStopsRun(t *testing.T) {
	t.Parallel()
	for _, target := range []string{".", "..", "REAL", "REAL/.cache"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			home, _ := cacheLockFixture(t)
			real := filepath.Join(filepath.Dir(home), "real")
			if err := os.MkdirAll(filepath.Join(real, ".cache"), 0o700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(filepath.Dir(home), "link")
			root := filepath.Join(link, ".cache", "workcell")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(strings.Replace(target, "REAL", real, 1), root); err != nil {
				t.Fatal(err)
			}
			code, output := runCacheProbe(t, cacheLockLibrary(t)+`
workcell_cache_lock_run_exclusive `+ShellQuote(root+".lock")+` /bin/true
`, nil)
			if code != 73 {
				t.Fatalf("gc lock status %d, want 73:\n%s", code, output)
			}
		})
	}
}

func chmodOrFail(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
