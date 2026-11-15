// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// The callers of the cache lock outside go-run-env.sh: the launcher's
// long-lived helpers, the Go tool wrappers, validate-repo.sh and the
// uninstaller.

// A helper meant to outlive the run, such as a Colima VM, gets no lock
// descriptor, so it never keeps a root from gc.
func TestCacheLockLongLivedHelperHoldsNoLock(t *testing.T) {
	t.Parallel()
	home, root := cacheLockFixture(t)
	prelude := cacheLockPrelude(t)
	launcher, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "workcell"))
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "daemon.pid")
	colima := filepath.Join(t.TempDir(), "colima")
	if err := os.WriteFile(colima, []byte("#!/bin/sh\n( exec sleep 300 ) </dev/null >/dev/null 2>&1 &\necho $! >"+ShellQuote(pidFile)+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, output := runCacheProbe(t, prelude+extractShellFunction(t, string(launcher), "run_host_colima")+`
ensure_go_run_env
HOST_COLIMA_BIN=`+ShellQuote(colima)+` REAL_HOME=`+ShellQuote(filepath.Dir(home))+` COLIMA_STATE_ROOT=/nonexistent
run_host_colima start
`, cacheWriterEnv(root))
	if code != 0 {
		t.Fatalf("probe exited %d: %s", code, output)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	daemon, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(daemon, syscall.SIGKILL) })
	waitForGCRemoval(t, prelude, root, "a Colima daemon kept the cache lock")
}

// Uninstall removes a cache root and its lock file only under the exclusive
// lock, and keeps a root that a writer holds.
func TestCacheLockUninstall(t *testing.T) {
	t.Parallel()
	home, root := cacheLockFixture(t)
	prelude := cacheLockPrelude(t)
	writer := startCacheWriter(t, prelude, root)
	env := map[string]string{
		"HOME":                         filepath.Dir(home),
		"XDG_CACHE_HOME":               "",
		"WORKCELL_UNINSTALL_TEMP_ROOT": t.TempDir(),
	}
	// run runs the uninstaller with args, prints what it said, and then
	// runs checks, bash that tests that output as "${out}".
	run := func(args, checks string) (int, string) {
		return runCacheProbe(t, `
out="$(`+ShellQuote(filepath.Join(repoRoot(t), "scripts", "uninstall.sh"))+` `+args+` 2>&1)"
printf '%s\n' "${out}"
says() { grep -qxF -- "$1" <<<"${out}"; }
`+checks, env)
	}
	lock := root + ".lock"
	q := ShellQuote

	// A dry run says that it would keep the held root.
	code, output := run("--dry-run", `
says `+q("Preserved cache root in use "+root)+`
! says `+q("Would remove "+root)+`
`)
	if code != 0 {
		t.Fatalf("dry run did not report the held root as kept (status %d):\n%s", code, output)
	}

	code, output = run("", "")
	if code != 0 {
		t.Fatalf("uninstall exited %d:\n%s", code, output)
	}
	if _, err := os.Stat(filepath.Join(root, "go", "build-cache")); err != nil {
		t.Fatalf("uninstall removed a root that a writer holds: %v\n%s", err, output)
	}
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("uninstall removed the lock of a held root: %v\n%s", err, output)
	}

	if err := writer.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Wait()
	code, output = run("--dry-run", `
says `+q("Would remove "+root)+`
says `+q("Would remove "+lock)+`
`)
	if code != 0 {
		t.Fatalf("dry run did not report the free root and its lock (status %d):\n%s", code, output)
	}
	// The root and its lock file are two paths.
	code, output = run("", `says "Removed 2 Workcell path(s)."`+"\n")
	if code != 0 {
		t.Fatalf("uninstall exited %d or miscounted:\n%s", code, output)
	}
	for _, path := range []string{root, lock} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstall left %s: %v\n%s", path, err, output)
		}
	}
}

// A dry run of the uninstaller tries the lock that the real run takes, beside
// the directory that a symlinked cache root points to, and never creates it.
func TestCacheLockUninstallDryRunFollowsRoot(t *testing.T) {
	t.Parallel()
	home, root := cacheLockFixture(t)
	real := filepath.Join(filepath.Dir(home), "Library", "Caches", "workcell")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real+"/", root); err != nil {
		t.Fatal(err)
	}
	writer := startCacheWriter(t, cacheLockPrelude(t), root)
	dryRun := func(checks string) {
		t.Helper()
		code, output := runCacheProbe(t, `
out="$(`+ShellQuote(filepath.Join(repoRoot(t), "scripts", "uninstall.sh"))+` --dry-run 2>&1)"
printf '%s\n' "${out}"
says() { grep -qxF -- "$1" <<<"${out}"; }
`+checks, map[string]string{"HOME": filepath.Dir(home), "XDG_CACHE_HOME": "", "WORKCELL_UNINSTALL_TEMP_ROOT": t.TempDir()})
		if code != 0 {
			t.Fatalf("dry run (status %d):\n%s", code, output)
		}
	}
	dryRun(`says ` + ShellQuote("Preserved cache root in use "+root) + "\n")

	if err := writer.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Wait()
	if err := os.Remove(real + ".lock"); err != nil {
		t.Fatal(err)
	}
	dryRun(`says ` + ShellQuote("Would remove "+root) + "\n")
	for _, lock := range []string{real + ".lock", root + ".lock"} {
		if _, err := os.Lstat(lock); !os.IsNotExist(err) {
			t.Fatalf("dry run created %s: %v", lock, err)
		}
	}
}

// The detached session monitor outlives the run, so it gets no lock
// descriptor and never keeps a root from gc.
func TestCacheLockSessionMonitorHoldsNoLock(t *testing.T) {
	t.Parallel()
	_, root := cacheLockFixture(t)
	prelude := cacheLockPrelude(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "monitor.pid")
	var b strings.Builder
	// The launcher starts the monitor as this script with "session monitor
	// --state-file FILE"; the stand-in marks itself ready and sleeps.
	b.WriteString(`if [[ "${1:-}" == session ]]; then
  : >"${4%/*}/session-monitor.ready"
  exec sleep 300
fi
`)
	b.WriteString(prelude)
	// start_detached_session_monitor has an indented brace, where
	// extractShellFunction would stop, so sed cuts at the column-0 one.
	for _, name := range []string{"session_monitor_state_path", "session_monitor_ready_path", "wait_for_session_monitor_ready", "start_detached_session_monitor"} {
		b.WriteString(`eval "$(sed -n '/^` + name + `() {$/,/^}$/p' ` + ShellQuote(filepath.Join(repoRoot(t), "scripts", "workcell")) + `)"` + "\n")
	}
	b.WriteString(`load_session_runtime_metadata() { :; }
write_session_monitor_env_file() { :; }
mark_detached_session_running() { :; }
SESSION_AUDIT_DIR=` + ShellQuote(dir) + ` SESSION_ID=s DEBUG_LOG_PATH="" TRUSTED_HOST_PATH=/usr/bin:/bin REAL_HOME=/nonexistent
ensure_go_run_env
start_detached_session_monitor
printf '%s\n' "${SESSION_MONITOR_PID}" >` + ShellQuote(pidFile) + `
`)
	script := filepath.Join(dir, "monitor-probe.sh")
	if err := os.WriteFile(script, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	code, output := runCacheProbe(t, "exec /bin/bash -p "+ShellQuote(script)+"\n", cacheWriterEnv(root))
	if code != 0 {
		t.Fatalf("probe exited %d: %s", code, output)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(monitor, syscall.SIGKILL) })
	waitForGCRemoval(t, prelude, root, "the session monitor kept the cache lock")
}

// The Go tool wrappers stop when ensure_go_run_env fails, even where the
// caller tests their status and set -e does not apply.
func TestCacheLockTransientFailureStopsGoTools(t *testing.T) {
	t.Parallel()
	library, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "lib", "launcher", "go-hostutil.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go_hostutil", "go_hostutil_publish_pr", "go_colimautil"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, root := cacheLockFixture(t)
			ran := filepath.Join(t.TempDir(), "ran")
			code, output := runCacheProbe(t, cacheLockPrelude(t)+extractShellFunction(t, string(library), name)+`
workcell_cache_lock_perl() { return 71; }
go_tool_bin() { : >`+ShellQuote(ran)+`; GO_TOOL_BIN=/bin/true; }
if `+name+` version; then echo "the wrapper went on"; exit 1; fi
`, cacheWriterEnv(root))
			if code != 0 {
				t.Fatalf("probe exited %d:\n%s", code, output)
			}
			if _, err := os.Lstat(ran); !os.IsNotExist(err) {
				t.Fatalf("%s went on after the cache lock failed:\n%s", name, output)
			}
		})
	}
}

// validate-repo.sh holds the cache lock on its validate cache, so it waits
// for a gc that holds the root before it creates anything there.
func TestCacheLockValidateRepoWaitsForGC(t *testing.T) {
	t.Parallel()
	home, _ := cacheLockFixture(t)
	ready := ShellQuote(filepath.Join(t.TempDir(), "ready"))
	code, output := runCacheProbe(t, cacheLockPrelude(t)+`
workcell_cache_lock_run_exclusive `+ShellQuote(filepath.Join(home, "workcell.lock"))+` /bin/bash -c ': >"$1"; sleep 1.5' gc `+ready+` &
gc=$!
until [[ -e `+ready+` ]]; do sleep 0.05; done
start="$(date +%s%N)"
# TMPDIR under /dev/null makes validate-repo.sh fail right after the lock.
env -i PATH=/usr/bin:/bin HOME=`+ShellQuote(filepath.Dir(home))+` XDG_CACHE_HOME=`+ShellQuote(home)+` GOENV=off GOWORK=off TMPDIR=/dev/null/x \
  /bin/bash -p `+ShellQuote(filepath.Join(repoRoot(t), "scripts", "validate-repo.sh"))+` >/dev/null 2>&1 || true
end="$(date +%s%N)"
wait "${gc}"
echo "$(((end - start) / 1000000))"
`, nil)
	if code != 0 {
		t.Fatalf("probe exited %d:\n%s", code, output)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	waited, err := strconv.Atoi(lines[len(lines)-1])
	if err != nil {
		t.Fatalf("probe output:\n%s", output)
	}
	if waited < 1000 {
		t.Fatalf("validate-repo.sh ran for %d ms while gc held its cache root for 1500 ms", waited)
	}
}
