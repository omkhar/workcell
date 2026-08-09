// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestColimaProfileStatusMissingProfileReturnsNoMatch(t *testing.T) {
	t.Parallel()
	input := []byte(strings.Join([]string{
		`{"name":"default","status":"Running"}`,
		`{"name":"workcell-test","status":"Stopped"}`,
		"",
	}, "\n"))

	_, err := ColimaProfileStatus(input, "does-not-exist")
	if !IsNoMatch(err) {
		t.Fatalf("ColimaProfileStatus() err = %v, want IsNoMatch", err)
	}
	for _, malformed := range []string{`{}`, `{"name":1,"status":"Running"}`, `{"name":"","status":"Running"}`} {
		_, err := ColimaProfileStatus([]byte(malformed), "does-not-exist")
		if err == nil || IsNoMatch(err) {
			t.Fatalf("ColimaProfileStatus(%s) err = %v, want malformed-record error", malformed, err)
		}
	}
	for _, ordered := range []string{"{}\n{\"name\":\"target\",\"status\":\"Running\"}", "{\"name\":\"target\",\"status\":\"Running\"}\n{}"} {
		if _, err = ColimaProfileStatus([]byte(ordered), "target"); err == nil {
			t.Fatal("ColimaProfileStatus accepted malformed ordered inventory")
		}
	}
	_, err = ColimaProfileStatus([]byte("{\"name\":\"target\",\"status\":\"Running\"}\n{\"name\":\"target\",\"status\":\"Stopped\"}"), "target")
	if err == nil {
		t.Fatal("ColimaProfileStatus accepted duplicate profile names")
	}
}

func TestColimaProfileProcessPIDsPinsApprovedCommands(t *testing.T) {
	t.Parallel()
	output := []byte(strings.Join([]string{
		"11 /opt/homebrew/bin/limactl hostagent --pidfile /tmp/colima-target/ha.pid colima-target",
		"12 ssh: /tmp/colima-target/ssh.sock [mux]",
		"13 tail -f /tmp/colima-target/ha.stderr.log",
		"14 /opt/homebrew/bin/limactl hostagent /tmp/colima-target-extra/ha.pid colima-target-extra",
		"15 /bin/sh -c echo /limactl hostagent /tmp/colima-target/ha.pid",
		"16 ssh: /tmp/colima-target/ssh.sock [mux] extra",
	}, "\n"))
	pids, err := ColimaProfileProcessPIDs(output, "target")
	if err != nil || !slices.Equal(pids, []int{11, 12}) {
		t.Fatalf("ColimaProfileProcessPIDs() = %v, %v", pids, err)
	}
	for _, invalid := range []string{
		"bad ssh: /tmp/colima-target/ssh.sock [mux]",
		"0 ssh: /tmp/colima-target/ssh.sock [mux]",
		"-1 ssh: /tmp/colima-target/ssh.sock [mux]",
		"01 ssh: /tmp/colima-target/ssh.sock [mux]",
		"11 ssh: /tmp/colima-target/ssh.sock [mux]\n11 ssh: /tmp/colima-target/ssh.sock [mux]",
	} {
		if _, err := ColimaProfileProcessPIDs([]byte(invalid), "target"); err == nil {
			t.Fatalf("ColimaProfileProcessPIDs accepted %q", invalid)
		}
	}
	if _, err := ColimaProfileProcessPIDs(nil, ""); err == nil {
		t.Fatal("ColimaProfileProcessPIDs accepted an empty profile")
	}
}

func TestProfileLockIsStaleReportsMalformedOwnerMetadata(t *testing.T) {
	t.Parallel()
	lockDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := ProfileLockIsStale(lockDir)
	if err == nil {
		t.Fatal("ProfileLockIsStale() error = nil, want parse error")
	}
	if stale {
		t.Fatal("ProfileLockIsStale() stale = true, want false on malformed owner metadata")
	}
}

func TestProfileLockIsStaleReportsIncompleteOwnerMetadata(t *testing.T) {
	t.Parallel()
	lockDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(`{"pid":`+strconv.Itoa(os.Getpid())+`}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := ProfileLockIsStale(lockDir)
	if err == nil {
		t.Fatal("ProfileLockIsStale() error = nil, want incomplete metadata error")
	}
	if stale {
		t.Fatal("ProfileLockIsStale() stale = true, want false on incomplete owner metadata")
	}
}

func TestProfileLockIsStaleRecognizesLiveOwner(t *testing.T) {
	t.Parallel()
	lockDir := t.TempDir()
	started, err := processGeneration(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(`{"pid":`+strconv.Itoa(os.Getpid())+`,"started":"`+started+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := ProfileLockIsStale(lockDir)
	if err != nil {
		t.Fatalf("ProfileLockIsStale() error = %v", err)
	}
	if stale {
		t.Fatal("ProfileLockIsStale() stale = true, want false for live owner")
	}
}

func TestProfileLockIsStalePreservesLiveLegacyOwner(t *testing.T) {
	t.Parallel()
	lockDir := t.TempDir()
	started, err := ProcessStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"pid":` + strconv.Itoa(os.Getpid()) + `,"started":"` + started + `"}` + "\n")
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := ProfileLockIsStale(lockDir)
	if err != nil {
		t.Fatalf("ProfileLockIsStale() error = %v", err)
	}
	if stale {
		t.Fatal("ProfileLockIsStale() stale = true, want false for live legacy owner")
	}
}

func TestProfileLockIsStaleRecognizesReusedPIDGeneration(t *testing.T) {
	t.Parallel()
	lockDir := t.TempDir()
	payload := []byte(`{"pid":` + strconv.Itoa(os.Getpid()) + `,"started":"darwin:different"}` + "\n")
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	stale, err := ProfileLockIsStale(lockDir)
	if err != nil {
		t.Fatalf("ProfileLockIsStale() error = %v", err)
	}
	if !stale {
		t.Fatal("ProfileLockIsStale() stale = false, want true for reused PID generation")
	}
}

func TestAcquireProfileLockCreatesOwnerAtomically(t *testing.T) {
	t.Parallel()
	lockDir := filepath.Join(t.TempDir(), "profile.lock")

	acquired, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil {
		t.Fatalf("AcquireProfileLock() error = %v", err)
	}
	if !acquired {
		t.Fatal("AcquireProfileLock() = false, want true")
	}

	content, err := os.ReadFile(filepath.Join(lockDir, "owner.json"))
	if err != nil {
		t.Fatalf("read owner.json: %v", err)
	}
	var owner struct {
		PID     int    `json:"pid"`
		Started string `json:"started"`
	}
	if err := json.Unmarshal(content, &owner); err != nil {
		t.Fatalf("unmarshal owner.json: %v", err)
	}
	if owner.PID != os.Getpid() {
		t.Fatalf("owner PID = %d, want %d", owner.PID, os.Getpid())
	}
	if owner.Started == "" {
		t.Fatal("owner.Started = empty, want process generation")
	}
	wantGeneration, err := processGeneration(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if owner.Started != wantGeneration {
		t.Fatalf("owner.Started = %q, want %q", owner.Started, wantGeneration)
	}
	if err := ReleaseProfileLock(lockDir, os.Getpid()); err != nil {
		t.Fatalf("ReleaseProfileLock() error = %v", err)
	}
}

func TestAcquireProfileLockReturnsFalseWhenLockExists(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	lockDir := filepath.Join(parent, "profile.lock")
	first, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil || !first {
		t.Fatalf("first AcquireProfileLock() = %v, %v; want true, nil", first, err)
	}

	acquired, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil {
		t.Fatalf("AcquireProfileLock() error = %v", err)
	}
	if acquired {
		t.Fatal("AcquireProfileLock() = true, want false for existing lock")
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".pending.") {
			t.Fatalf("temporary lock dir leaked: %s", entry.Name())
		}
	}
	if err := ReleaseProfileLock(lockDir, os.Getpid()); err != nil {
		t.Fatalf("ReleaseProfileLock() error = %v", err)
	}
}

func TestAcquireProfileLockSerializesConcurrentStaleReclaim(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "audit.log.lock")
	holder := exec.Command("/bin/sleep", "60")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfileOwner(filepath.Join(lockDir, "owner.json"), holder.Process.Pid); err != nil {
		_ = holder.Process.Kill()
		_ = holder.Wait()
		t.Fatal(err)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err == nil {
		t.Fatal("killed stale lock holder exited successfully")
	}

	type result struct {
		acquired bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			acquired, err := AcquireProfileLock(lockDir, os.Getpid())
			results <- result{acquired: acquired, err: err}
		}()
	}
	close(start)

	acquiredCount := 0
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatalf("AcquireProfileLock() error = %v", got.err)
		}
		if got.acquired {
			acquiredCount++
		}
	}
	if acquiredCount != 1 {
		t.Fatalf("concurrent stale reclaim acquired count = %d, want 1", acquiredCount)
	}
	stale, err := ProfileLockIsStale(lockDir)
	if err != nil {
		t.Fatalf("ProfileLockIsStale() error = %v", err)
	}
	if stale {
		t.Fatal("concurrent stale reclaim removed the live successor lock")
	}
	if err := ReleaseProfileLock(lockDir, os.Getpid()); err != nil {
		t.Fatalf("ReleaseProfileLock() error = %v", err)
	}
}

func TestReleaseProfileLockRejectsDifferentOwner(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "profile.lock")
	acquired, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil || !acquired {
		t.Fatalf("AcquireProfileLock() = %v, %v; want true, nil", acquired, err)
	}
	if err := ReleaseProfileLock(lockDir, os.Getpid()+1); err == nil {
		t.Fatal("ReleaseProfileLock() accepted a different owner")
	}
	if _, err := os.Stat(lockDir); err != nil {
		t.Fatalf("lock disappeared after rejected release: %v", err)
	}
	if err := ReleaseProfileLock(lockDir, os.Getpid()); err != nil {
		t.Fatalf("ReleaseProfileLock() error = %v", err)
	}
	info, err := os.Stat(lockDir + ".guard")
	if err != nil {
		t.Fatalf("stat persistent transition guard: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("transition guard mode = %o, want 600", info.Mode().Perm())
	}
}

func TestReleaseProfileLockRejectsChangedProcessGeneration(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "profile.lock")
	acquired, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil || !acquired {
		t.Fatalf("AcquireProfileLock() = %v, %v; want true, nil", acquired, err)
	}
	payload := []byte(`{"pid":` + strconv.Itoa(os.Getpid()) + `,"started":"darwin:different"}` + "\n")
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseProfileLock(lockDir, os.Getpid()); err == nil {
		t.Fatal("ReleaseProfileLock() accepted a changed process generation")
	}
	if _, err := os.Stat(lockDir); err != nil {
		t.Fatalf("lock disappeared after rejected generation: %v", err)
	}
}

func TestProfileLockTransitionGuardFailsFastWhenHeld(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "profile.lock")
	holder := exec.Command("/bin/sleep", "60")
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfileOwner(filepath.Join(lockDir, "owner.json"), holder.Process.Pid); err != nil {
		_ = holder.Process.Kill()
		_ = holder.Wait()
		t.Fatal(err)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err == nil {
		t.Fatal("killed stale lock holder exited successfully")
	}

	guardFD, err := unix.Open(lockDir+".guard", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(guardFD) //nolint:errcheck // test cleanup
	if err := unix.Flock(guardFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	second, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil {
		t.Fatalf("AcquireProfileLock() behind held guard error = %v", err)
	}
	if second {
		t.Fatal("AcquireProfileLock() acquired behind a held transition guard")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("AcquireProfileLock() blocked behind guard for %s", elapsed)
	}

	start = time.Now()
	if err := ReleaseProfileLock(lockDir, holder.Process.Pid); !errors.Is(err, ErrProfileLockTransitionBusy) {
		t.Fatalf("ReleaseProfileLock() behind held guard error = %v, want busy", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ReleaseProfileLock() blocked behind guard for %s", elapsed)
	}

	if err := unix.Flock(guardFD, unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	acquired, err := AcquireProfileLock(lockDir, os.Getpid())
	if err != nil || !acquired {
		t.Fatalf("AcquireProfileLock() after guard release = %v, %v; want true, nil", acquired, err)
	}
	if err := ReleaseProfileLock(lockDir, os.Getpid()); err != nil {
		t.Fatalf("ReleaseProfileLock() after guard release error = %v", err)
	}
}
