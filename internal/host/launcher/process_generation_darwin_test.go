// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package launcher

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

var compositeDarwinGenerationPattern = regexp.MustCompile(`^darwin:[1-9][0-9]*\.[0-9]{6}:[1-9][0-9]*$`)

func TestProcessGenerationDistinguishesProcessIncarnations(t *testing.T) {
	first := startGenerationTestProcess(t)
	second := startGenerationTestProcess(t)
	firstGeneration, err := processGeneration(first.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	secondGeneration, err := processGeneration(second.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if firstGeneration == secondGeneration {
		t.Fatalf("distinct processes share kernel generation %q", firstGeneration)
	}
	assertCompositeDarwinGeneration(t, firstGeneration)
	assertCompositeDarwinGeneration(t, secondGeneration)
}

func TestProcessGenerationIsStableForOneProcess(t *testing.T) {
	cmd := startGenerationTestProcess(t)
	first, err := processGeneration(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ObserveProcessGeneration(cmd.Process.Pid, first)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("process generation changed from %q to %q", first, second)
	}
}

func TestObserveProcessGenerationAcceptsLegacyDarwinRecord(t *testing.T) {
	cmd := startGenerationTestProcess(t)
	recorded, err := legacyDarwinProcessGeneration(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := ObserveProcessGeneration(cmd.Process.Pid, recorded)
	if err != nil || observed != recorded {
		t.Fatalf("legacy observation = %q, %v, want %q", observed, err, recorded)
	}
}

func TestFormatDarwinProcessGenerationFormatsStableSnapshot(t *testing.T) {
	generation, err := formatDarwinProcessGeneration(42, "darwin:123.456789", 7, 7)
	if err != nil || generation != "darwin:123.456789:7" {
		t.Fatalf("stable snapshot = %q, %v, want darwin:123.456789:7, nil", generation, err)
	}
}

func TestFormatDarwinProcessGenerationRejectsChangedSnapshot(t *testing.T) {
	generation, err := formatDarwinProcessGeneration(42, "darwin:123.456789", 7, 8)
	if err == nil || generation != "" {
		t.Fatalf("changed snapshot = %q, %v, want empty generation and error", generation, err)
	}
	if IsProcessGone(err) {
		t.Fatalf("changed snapshot error = %v, want hard non-process-gone error", err)
	}
	want := "read process 42 Darwin identity: process generation changed"
	if err.Error() != want {
		t.Fatalf("changed snapshot error = %q, want %q", err, want)
	}
}

func TestWriteProfileOwnerPersistsCompositeDarwinGeneration(t *testing.T) {
	ownerPath := filepath.Join(t.TempDir(), "owner.json")
	if err := WriteProfileOwner(ownerPath, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	var owner profileLockOwner
	if err := json.Unmarshal(content, &owner); err != nil {
		t.Fatal(err)
	}
	if owner.PID != os.Getpid() {
		t.Fatalf("owner PID = %d, want %d", owner.PID, os.Getpid())
	}
	assertCompositeDarwinGeneration(t, owner.Started)
}

func TestProfileLockPreservesLiveLegacyDarwinOwner(t *testing.T) {
	lockDir := t.TempDir()
	started, err := legacyDarwinProcessGeneration(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(profileLockOwner{PID: os.Getpid(), Started: started})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, err := ProfileLockIsStale(lockDir)
	if err != nil || stale {
		t.Fatalf("ProfileLockIsStale() = %t, %v, want false, nil", stale, err)
	}
	if err := ReleaseProfileLock(lockDir, os.Getpid()); err != nil {
		t.Fatalf("ReleaseProfileLock() legacy owner: %v", err)
	}
}

func TestProcessGenerationReportsGoneProcess(t *testing.T) {
	if _, err := processGeneration(1 << 30); !IsProcessGone(err) {
		t.Fatalf("missing process generation error = %v, want IsProcessGone", err)
	}
}

func TestObserveProcessGenerationRejectsMalformedDarwinRecords(t *testing.T) {
	for _, recorded := range []string{
		"darwin:1.000000:0",
		"darwin:1.000000:01",
		"darwin:1.000000:1:2",
		"darwin:1.00000:1",
	} {
		if _, err := ObserveProcessGeneration(1, recorded); err == nil {
			t.Errorf("ObserveProcessGeneration(%q) accepted malformed record", recorded)
		}
	}
}

func startGenerationTestProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func assertCompositeDarwinGeneration(t *testing.T, generation string) {
	t.Helper()
	if !compositeDarwinGenerationPattern.MatchString(generation) {
		t.Fatalf("process generation = %q, want darwin:<seconds>.<six-microseconds>:<positive-canonical-id>", generation)
	}
}
