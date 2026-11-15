// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The cache lock in use (issue 812): ensure_go_run_env takes the shared lock
// for every Go cache writer, and workcell --gc removes a root only under an
// exclusive lock that it takes without waiting. These tests drive
// ensure_go_run_env, the Go runners and the gc functions of scripts/workcell.

// cacheLockPrelude sources the lock library and the gc functions, with
// make_stale ROOT to age every entry under ROOT past the gc threshold.
func cacheLockPrelude(t *testing.T) string {
	t.Helper()
	launcher, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "workcell"))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(cacheLockLibrary(t))
	for _, name := range []string{"workcell_nonnegative_integer_or_default", "make_workcell_tree_user_writable", "cleanup_workcell_cache_root", "cleanup_workcell_cache_roots"} {
		b.WriteString(extractShellFunction(t, string(launcher), name) + "\n")
	}
	b.WriteString("make_stale() { find \"$1\" -exec touch -t 202001010000 {} +; }\n")
	return b.String()
}

// startCacheWriter runs a writer that takes the cache lock through
// ensure_go_run_env and then execs a long sleep, the way exec_go_run_in_repo
// execs go: the descriptor that holds the lock lives on in the new program.
func startCacheWriter(t *testing.T, prelude, root string) *exec.Cmd {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("bash", "-p", "-c", prelude+`
ensure_go_run_env
printf '%s\n' "${GOCACHE}" >`+ShellQuote(ready)+`
exec sleep 300
`)
	cmd.Env = append(os.Environ(), "BASH_ENV=", "ENV=", "WORKCELL_GO_CACHE_ROOT="+filepath.Join(root, "go"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		if data, err := os.ReadFile(ready); err == nil && len(data) > 1 {
			return cmd
		}
		if time.Now().After(deadline) {
			t.Fatal("cache writer did not take the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// runCacheGC ages root and runs gc on it. A non-empty want is a line that
// gc must report.
func runCacheGC(t *testing.T, prelude, root, want string) string {
	t.Helper()
	code, output := runCacheProbe(t, prelude+`
make_stale `+ShellQuote(root)+`
report="$(cleanup_workcell_cache_root `+ShellQuote(root)+` 60 2>&1)"
printf '%s\n' "${report}"
want=`+ShellQuote(want)+`
[[ -z "${want}" ]] || grep -qxF -- "${want}" <<<"${report}" || { echo "gc did not report: ${want}"; exit 3; }
`, nil)
	if code != 0 {
		t.Fatalf("gc exited %d: %s", code, output)
	}
	return output
}

// waitForGCRemoval runs gc until it removes root, failing after ten seconds.
func waitForGCRemoval(t *testing.T, prelude, root, why string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		output := runCacheGC(t, prelude, root, "")
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s:\n%s", why, output)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestCacheLockGCKeepsRootHeldByWriter(t *testing.T) {
	t.Parallel()
	_, root := cacheLockFixture(t)
	prelude := cacheLockPrelude(t)
	startCacheWriter(t, prelude, root)

	// Every entry is past the threshold, so only the lock keeps the root.
	output := runCacheGC(t, prelude, root, "Kept Workcell cache root in use: "+root)
	if _, err := os.Stat(filepath.Join(root, "go", "build-cache")); err != nil {
		t.Fatalf("gc removed a root that a writer holds: %v\n%s", err, output)
	}
}

// gc keeps a root whose lock file it cannot trust, and says why.
func TestCacheLockGCKeepsRootWithoutSafeLock(t *testing.T) {
	t.Parallel()
	home, root := cacheLockFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "go"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "elsewhere"), root+".lock"); err != nil {
		t.Fatal(err)
	}
	output := runCacheGC(t, cacheLockPrelude(t), root, "Kept Workcell cache root without a safe cache lock: "+root)
	if _, err := os.Stat(filepath.Join(root, "go")); err != nil {
		t.Fatalf("gc removed a root without a safe lock: %v\n%s", err, output)
	}
}

func TestCacheLockGCRemovesStaleUnlockedRoot(t *testing.T) {
	t.Parallel()
	home, root := cacheLockFixture(t)
	prelude := cacheLockPrelude(t)

	// A writer that ran and exited leaves the root unlocked.
	code, output := runCacheProbe(t, prelude+"ensure_go_run_env\n", cacheWriterEnv(root))
	if code != 0 {
		t.Fatalf("writer exited %d: %s", code, output)
	}
	waitForGCRemoval(t, prelude, root, "gc kept a stale root that no writer holds")

	// The lock file sits beside the root, outlives it, and is owner-only.
	info, err := os.Lstat(filepath.Join(home, "workcell.lock"))
	if err != nil {
		t.Fatalf("lock file missing after gc: %v", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("lock file mode = %v, want an owner-only regular file", info.Mode())
	}
}

func TestCacheLockKilledWriterFreesLock(t *testing.T) {
	t.Parallel()
	_, root := cacheLockFixture(t)
	prelude := cacheLockPrelude(t)
	writer := startCacheWriter(t, prelude, root)

	output := runCacheGC(t, prelude, root, "")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("gc removed a root that a live writer holds: %v\n%s", err, output)
	}

	// SIGKILL runs no trap; the kernel closes the writer's descriptors, and
	// the lock goes with the last one.
	if err := writer.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = writer.Wait()
	waitForGCRemoval(t, prelude, root, "a killed writer still holds the cache lock")
}

// ensure_go_run_env stops before it creates the root when the lock fails,
// even where the caller tests its status and set -e does not apply, and goes
// on for a lasting status (69, 77).
func TestCacheLockEnsureGoRunEnvStopsOnFailure(t *testing.T) {
	t.Parallel()
	for _, status := range []int{64, 69, 71, 73, 75, 77, 1} {
		t.Run(fmt.Sprintf("status %d", status), func(t *testing.T) {
			t.Parallel()
			_, root := cacheLockFixture(t)
			code, output := runCacheProbe(t, cacheLockPrelude(t)+fmt.Sprintf("workcell_cache_lock_perl() { return %d; }\n", status)+"ensure_go_run_env || exit 9\n", cacheWriterEnv(root))
			_, err := os.Stat(filepath.Join(root, "go", "build-cache"))
			if status == 69 || status == 77 {
				if code != 0 || err != nil {
					t.Fatalf("writer stopped after lasting lock status %d (exit %d, %v):\n%s", status, code, err, output)
				}
				return
			}
			if code != 9 {
				t.Fatalf("writer went on after lock status %d (exit %d):\n%s", status, code, output)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatalf("writer created the root without the lock: %v\n%s", err, output)
			}
		})
	}
}

// The Go runners stop when ensure_go_run_env fails, even where the caller
// tests their status and set -e does not apply.
func TestCacheLockTransientFailureStopsGoRunners(t *testing.T) {
	t.Parallel()
	for _, call := range []string{
		`run_go_in_repo "${PWD}" version`,
		`build_go_tool_in_repo "${PWD}" "${PWD}/tool" ./cmd/x`,
		`( exec_go_run_in_repo "${PWD}" ./cmd/x )`,
	} {
		t.Run(strings.Fields(strings.TrimPrefix(call, "( "))[0], func(t *testing.T) {
			t.Parallel()
			_, root := cacheLockFixture(t)
			ran := filepath.Join(t.TempDir(), "ran")
			code, output := runCacheProbe(t, cacheLockPrelude(t)+`
workcell_cache_lock_perl() { return 71; }
resolve_go_bin() { : >`+ShellQuote(ran)+`; echo /bin/true; }
if `+call+`; then echo "the runner went on"; exit 1; fi
`, cacheWriterEnv(root))
			if code != 0 {
				t.Fatalf("probe exited %d:\n%s", code, output)
			}
			if _, err := os.Lstat(ran); !os.IsNotExist(err) {
				t.Fatalf("Go ran after the cache lock failed:\n%s", output)
			}
		})
	}
}

// workcell --gc builds its hostutil first, so its own shell holds the lock:
// cleanup_workcell_cache_roots must drop it before it locks the roots.
func TestCacheLockGCReleasesItsOwnLock(t *testing.T) {
	t.Parallel()
	home, root := cacheLockFixture(t)
	code, output := runCacheProbe(t, cacheLockPrelude(t)+`
unset XDG_CACHE_HOME
REAL_HOME=`+ShellQuote(filepath.Dir(home))+`
ensure_go_run_env
make_stale `+ShellQuote(root)+`
cleanup_workcell_cache_roots ""
[[ ! -e `+ShellQuote(root)+` ]] || { echo "gc kept its own root"; exit 1; }
`, cacheWriterEnv(root))
	if code != 0 {
		t.Fatalf("probe exited %d:\n%s", code, output)
	}
}
