// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/auditseal"
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/sessions"
)

const (
	forkFixtureCommit = "1111111111111111111111111111111111111111"
	forkFixtureTree   = "2222222222222222222222222222222222222222"
	forkFixtureHead   = "4444444444444444444444444444444444444444"
)

type forkFixture struct {
	root, signingDir, logPath, recordPath, prev string
	lines                                       []string
}

// newForkFixture writes a detached parent session with a signed launch record
// and a signed session_snapshot record (snapshot id "snap-1").
func newForkFixture(t *testing.T, executionPath string) *forkFixture {
	t.Helper()
	return newForkFixtureWithAssurance(t, executionPath, "managed-mutable")
}

func newForkFixtureWithAssurance(t *testing.T, executionPath, assurance string) *forkFixture {
	t.Helper()
	return newForkFixtureWithVM(t, executionPath, assurance, "vm_cpu=4", "vm_memory_gib=10", "vm_disk_gib=80", "container_cpu=unmanaged", "container_memory=8g")
}

func newForkFixtureWithVM(t *testing.T, executionPath, assurance string, vm ...string) *forkFixture {
	t.Helper()
	if !slices.ContainsFunc(vm, func(field string) bool { return strings.HasPrefix(field, "provider_arg_count=") }) {
		vm = append(slices.Clone(vm), "provider_arg_count=0")
	}
	if !slices.ContainsFunc(vm, func(field string) bool { return strings.HasPrefix(field, "target_provider=") }) {
		vm = append(slices.Clone(vm), "target_provider=colima")
	}
	f := &forkFixture{root: t.TempDir(), signingDir: filepath.Join(t.TempDir(), "signing")}
	profileDir := filepath.Join(f.root, "wcl-fixture")
	if err := os.MkdirAll(filepath.Join(profileDir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.logPath = filepath.Join(profileDir, "workcell.audit.log")
	f.recordPath = filepath.Join(profileDir, "sessions", "parent-1.json")
	f.appendRecord(t, append([]string{"event=launch", "profile=wcl-fixture", "workspace_origin=/tmp/origin-repo", "workspace=/tmp/clone", "workspace_head=" + forkFixtureHead, "agent=codex", "mode=strict", "agent_autonomy=yolo",
		"injection_policy_sha256=", "container_assurance=" + assurance, "execution_path=" + executionPath}, vm...)...)
	f.appendRecord(t, "event=session_snapshot", "source=host-cli", "snapshot_id=snap-1",
		"tree="+forkFixtureTree, "commit="+forkFixtureCommit)
	if err := sessions.WriteSessionRecord(f.recordPath, map[string]string{
		"session_id": "parent-1", "profile": "wcl-fixture", "target_provider": "colima", "execution_path": executionPath,
		"agent": "codex", "mode": "strict", "status": "running", "live_status": "running",
		"monitor_pid": "4242", "session_audit_dir": "/tmp/audit-fixture", "workspace": "/tmp/clone", "workspace_origin": "/tmp/origin-repo",
		"git_head": forkFixtureHead, "started_at": "2026-07-08T00:00:00Z", "audit_log_path": f.logPath,
	}); err != nil {
		t.Fatal(err)
	}
	f.sign(t)
	return f
}

func (f *forkFixture) appendRecord(t *testing.T, args ...string) {
	t.Helper()
	args = append([]string{"session_id=parent-1"}, args...)
	ts := "2026-07-08T00:00:0" + string(rune('0'+len(f.lines))) + "Z"
	digest := hoststate.AuditRecordDigest(f.prev, ts, args)
	line := "timestamp=" + ts + " " + strings.Join(args, " ")
	if f.prev != "" {
		line += " prev_digest=" + f.prev
	}
	f.lines = append(f.lines, line+" record_digest="+digest)
	f.prev = digest
	if err := os.WriteFile(f.logPath, []byte(strings.Join(f.lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *forkFixture) sign(t *testing.T) {
	t.Helper()
	if err := SignHeadMain([]string{
		"--signing-dir=" + f.signingDir, "--audit-log=" + f.logPath, "--session-id=parent-1",
		"--record-path=" + f.recordPath, "--provider=colima", "--signed-at=2026-07-08T00:00:09Z",
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *forkFixture) run(extra ...string) (string, error) {
	var out bytes.Buffer
	args := append([]string{"--root=" + f.root, "--signing-dir=" + f.signingDir}, extra...)
	err := forkMain(args, &out, io.Discard)
	return out.String(), err
}

func TestForkMainEmitsPlanFromSignedRecords(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	out, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "2")
	if err != nil {
		t.Fatalf("forkMain error = %v", err)
	}
	origin := sha256.Sum256([]byte("/tmp/origin-repo"))
	want := strings.Join([]string{
		"session_id=parent-1", "profile=wcl-fixture", "workspace_origin=/tmp/origin-repo",
		"origin_hash=" + hex.EncodeToString(origin[:]), "agent=codex", "mode=strict",
		"agent_autonomy=yolo", "container_mutability=ephemeral", "vm_cpu=4", "vm_memory_gib=10", "vm_disk_gib=80", "container_cpu=unmanaged", "container_memory=8g", "injection_policy_sha256=", "snapshot_id=snap-1",
		"commit=" + forkFixtureCommit, "tree=" + forkFixtureTree, "count=2", "ack_arbitrary_command=",
	}, "\n") + "\n"
	if out != want {
		t.Fatalf("plan =\n%s\nwant\n%s", out, want)
	}
}

func TestForkMainAsksForSnapshotWithoutSnapshotID(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	out, err := f.run("--id", "parent-1", "--count", "1")
	if err != nil || out != "session_id=parent-1\nneeds_snapshot=1\nack_arbitrary_command=\n" {
		t.Fatalf("forkMain = %q, %v", out, err)
	}
}

func TestForkMainRequiresParentVMResources(t *testing.T) {
	f := newForkFixtureWithVM(t, "managed-tier1", "managed-mutable", "vm_cpu=6", "vm_memory_gib=12", "vm_disk_gib=90", "container_cpu=2", "container_memory=4g")
	out, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1")
	if err != nil || !strings.Contains(out, "vm_cpu=6\nvm_memory_gib=12\nvm_disk_gib=90\ncontainer_cpu=2\ncontainer_memory=4g\n") {
		t.Fatalf("plan = %q, %v", out, err)
	}
	// A parent without the resources, or with a malformed one, is refused so a
	// child never forces a profile refresh.
	for _, vm := range [][]string{{}, {"vm_cpu=4", "vm_memory_gib=10"}, {"vm_cpu=0", "vm_memory_gib=10", "vm_disk_gib=80"}} {
		g := newForkFixtureWithVM(t, "managed-tier1", "managed-mutable", vm...)
		if _, err := g.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
			!strings.Contains(err.Error(), "parent VM resources") {
			t.Fatalf("vm %v error = %v", vm, err)
		}
	}
}

func TestForkMainBindsOriginAndRefusesProviderArguments(t *testing.T) {
	writeRecord := func(f *forkFixture, origin string) {
		t.Helper()
		if err := sessions.WriteSessionRecord(f.recordPath, map[string]string{
			"session_id": "parent-1", "profile": "wcl-fixture", "target_provider": "colima", "execution_path": "managed-tier1",
			"agent": "codex", "mode": "strict", "status": "running", "live_status": "running",
			"monitor_pid": "4242", "session_audit_dir": "/tmp/audit-fixture", "workspace": "/tmp/clone", "workspace_origin": origin,
			"started_at": "2026-07-08T00:00:00Z", "audit_log_path": f.logPath,
		}); err != nil {
			t.Fatal(err)
		}
	}
	f := newForkFixture(t, "managed-tier1")
	writeRecord(f, "/tmp/other-repo")
	if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
		!strings.Contains(err.Error(), "workspace origin does not match") {
		t.Fatalf("changed origin error = %v", err)
	}
	g := newForkFixtureWithVM(t, "managed-tier1", "managed-mutable", "vm_cpu=4", "vm_memory_gib=10", "vm_disk_gib=80",
		"container_cpu=unmanaged", "container_memory=8g", "provider_arg_count=2")
	if _, err := g.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
		!strings.Contains(err.Error(), "provider arguments") {
		t.Fatalf("provider argument error = %v", err)
	}
}

func TestForkMainBindsWorkspaceAndGitHeadToSignedLaunch(t *testing.T) {
	for field, value := range map[string]string{"workspace": "/tmp/other-clone", "git_head": strings.Repeat("5", 40)} {
		f := newForkFixture(t, "managed-tier1")
		record := map[string]string{
			"session_id": "parent-1", "profile": "wcl-fixture", "target_provider": "colima", "execution_path": "managed-tier1",
			"agent": "codex", "mode": "strict", "status": "running", "live_status": "running",
			"monitor_pid": "4242", "session_audit_dir": "/tmp/audit-fixture", "workspace": "/tmp/clone", "workspace_origin": "/tmp/origin-repo",
			"git_head": forkFixtureHead, "started_at": "2026-07-08T00:00:00Z", "audit_log_path": f.logPath,
		}
		record[field] = value
		if err := sessions.WriteSessionRecord(f.recordPath, record); err != nil {
			t.Fatal(err)
		}
		if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
			!strings.Contains(err.Error(), "workspace or git head does not match") {
			t.Fatalf("changed %s error = %v", field, err)
		}
	}
}

func TestForkMainBindsTargetToSignedLaunch(t *testing.T) {
	f := newForkFixtureWithVM(t, "managed-tier1", "managed-mutable", "vm_cpu=4", "vm_memory_gib=10", "vm_disk_gib=80",
		"container_cpu=unmanaged", "container_memory=8g", "target_provider=docker-desktop")
	if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
		!strings.Contains(err.Error(), "colima target") {
		t.Fatalf("docker-desktop launch error = %v", err)
	}
}

func TestForkMainBindsProfileToSignedLaunch(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	record, err := sessions.ReadSessionRecord(f.recordPath)
	if err != nil {
		t.Fatal(err)
	}
	// The durable record is unsigned, so a changed profile must not pass.
	if err := sessions.WriteSessionRecord(f.recordPath, map[string]string{
		"session_id": "parent-1", "profile": "other-profile", "target_provider": "colima", "execution_path": "managed-tier1",
		"agent": "codex", "mode": "strict", "status": "running", "live_status": "running",
		"monitor_pid": "4242", "session_audit_dir": "/tmp/audit-fixture", "workspace": "/tmp/clone", "workspace_origin": "/tmp/origin-repo",
		"started_at": record.StartedAt, "audit_log_path": f.logPath,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
		!strings.Contains(err.Error(), "profile does not match") {
		t.Fatalf("changed profile error = %v", err)
	}
}

func TestForkMainRequiresParentContainerLimits(t *testing.T) {
	vm := []string{"vm_cpu=4", "vm_memory_gib=10", "vm_disk_gib=80"}
	for _, limits := range [][]string{{}, {"container_cpu=unmanaged"}, {"container_cpu=lots", "container_memory=8g"}, {"container_cpu=2", "container_memory=big"}} {
		g := newForkFixtureWithVM(t, "managed-tier1", "managed-mutable", append(append([]string{}, vm...), limits...)...)
		if _, err := g.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
			!strings.Contains(err.Error(), "parent container limits") {
			t.Fatalf("limits %v error = %v", limits, err)
		}
	}
}

func TestForkMainReplaysContainerPosture(t *testing.T) {
	for assurance, want := range map[string]string{"managed-readonly": "container_mutability=readonly\n", "managed-mutable": "container_mutability=ephemeral\n"} {
		f := newForkFixtureWithAssurance(t, "managed-tier1", assurance)
		out, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1")
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("%s: plan = %q, %v; want %q", assurance, out, err, want)
		}
	}
	// A posture that fork cannot name is refused, not defaulted.
	f := newForkFixtureWithAssurance(t, "managed-tier1", "unknown")
	if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
		!strings.Contains(err.Error(), "container assurance") {
		t.Fatalf("unknown assurance error = %v", err)
	}
}

func TestForkMainRefusesBeforeAskingForSnapshot(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	// A parent that the plan refuses must not reach the snapshot step.
	if out, err := f.run("--id", "parent-1", "--count", "1", "--allow-arbitrary-command"); err == nil || out != "" {
		t.Fatalf("forkMain with a refused flag = %q, %v", out, err)
	}
	g := newForkFixture(t, "unsupported-path")
	if out, err := g.run("--id", "parent-1", "--count", "1"); err == nil || out != "" {
		t.Fatalf("forkMain with an unsupported path = %q, %v", out, err)
	}
}

func TestForkMainAsksForSnapshotBeforeAnySeal(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	// A running parent has no seal until its first snapshot.
	if err := os.Remove(auditseal.SealPathForRecord(f.recordPath)); err != nil {
		t.Fatal(err)
	}
	out, err := f.run("--id", "parent-1", "--count", "1")
	if err != nil || !strings.Contains(out, "needs_snapshot=1") {
		t.Fatalf("forkMain without a seal = %q, %v", out, err)
	}
}

func TestForkMainRejectsSnapshotOutsideSignedPrefix(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	// A record appended after the seal is not covered by it.
	f.appendRecord(t, "event=session_snapshot", "source=host-cli", "snapshot_id=snap-2",
		"tree="+forkFixtureTree, "commit="+forkFixtureCommit)
	if _, err := f.run("--id", "parent-1", "--snapshot", "snap-2", "--count", "1"); err == nil ||
		!strings.Contains(err.Error(), "no signed session_snapshot record snap-2") {
		t.Fatalf("unsealed snapshot error = %v", err)
	}
	// Negative control: the sealed snapshot still resolves.
	if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err != nil {
		t.Fatalf("sealed snapshot error = %v", err)
	}
}

func TestForkMainRejectsTamperedChain(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	tampered := strings.Replace(f.lines[1], "commit="+forkFixtureCommit, "commit="+strings.Repeat("3", 40), 1)
	if err := os.WriteFile(f.logPath, []byte(f.lines[0]+"\n"+tampered+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
		!strings.Contains(err.Error(), "audit verification failed") {
		t.Fatalf("tampered chain error = %v", err)
	}
}

func TestForkMainRejectsUnsignedParent(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	if err := os.Remove(auditseal.SealPathForRecord(f.recordPath)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", "1"); err == nil ||
		!strings.Contains(err.Error(), "requires a signed audit chain") {
		t.Fatalf("unsigned parent error = %v", err)
	}
}

func TestForkMainValidatesArguments(t *testing.T) {
	f := newForkFixture(t, "managed-tier1")
	for name, args := range map[string][]string{
		"count zero":         {"--id", "parent-1", "--count", "0"},
		"count nine":         {"--id", "parent-1", "--count", "9"},
		"count text":         {"--id", "parent-1", "--count", "two"},
		"count missing":      {"--id", "parent-1"},
		"id missing":         {"--count", "1"},
		"duplicate signing":  {"--id", "parent-1", "--count", "1", "--signing-dir=/tmp/other"},
		"bad snapshot id":    {"--id", "parent-1", "--count", "1", "--snapshot", "../x"},
		"unknown option":     {"--id", "parent-1", "--count", "1", "--bogus"},
		"command for tier 1": {"--id", "parent-1", "--snapshot", "snap-1", "--count", "1", "--allow-arbitrary-command", "--ack-arbitrary-command=2026-07-08", "--", "/bin/true"},
	} {
		if _, err := f.run(args...); err == nil {
			t.Errorf("%s: forkMain accepted %v", name, args)
		}
	}
	for _, count := range []string{"1", "8"} {
		if _, err := f.run("--id", "parent-1", "--snapshot", "snap-1", "--count", count); err != nil {
			t.Errorf("count %s: %v", count, err)
		}
	}
}

func TestForkMainPinsTheParentExecutionPath(t *testing.T) {
	debug := newForkFixture(t, "lower-assurance-debug-command")
	base := []string{"--id", "parent-1", "--snapshot", "snap-1", "--count", "1"}
	if _, err := debug.run(base...); err == nil {
		t.Fatal("debug-command parent forked without the arbitrary-command flags")
	}
	if _, err := debug.run(append(base, "--allow-arbitrary-command", "--ack-arbitrary-command=2026-07-08")...); err == nil {
		t.Fatal("debug-command parent forked without a command")
	}
	out, err := debug.run(append(base, "--allow-arbitrary-command", "--ack-arbitrary-command=2026-07-08", "--", "/bin/sh", "-c", "true")...)
	if err != nil || !strings.Contains(out, "ack_arbitrary_command=2026-07-08\n") {
		t.Fatalf("debug-command fork = %q, %v", out, err)
	}
	breakglass := newForkFixture(t, "lower-assurance-breakglass")
	if _, err := breakglass.run(base...); err == nil || !strings.Contains(err.Error(), "execution path") {
		t.Fatalf("breakglass parent error = %v", err)
	}
}
