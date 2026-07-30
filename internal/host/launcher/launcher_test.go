// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
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
	started, err := ProcessStartTime(os.Getpid())
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
		t.Fatal("owner.Started = empty, want process start time")
	}
}

func TestAcquireProfileLockReturnsFalseWhenLockExists(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	lockDir := filepath.Join(parent, "profile.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
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
}
