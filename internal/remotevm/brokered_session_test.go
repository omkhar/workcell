// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/auditseal"
	"github.com/omkhar/workcell/internal/host/sessions"
)

// sessionFixture runs materialization and bootstrap through the fake broker.
// The docker mode of later broker calls is *mode, and *calls counts them.
type sessionFixture struct {
	target BrokeredTarget
	broker *string
	mode   *string
	calls  *int
	log    string
	state  string
	c      ConformanceCase
}

func newSessionFixture(t *testing.T) sessionFixture {
	t.Helper()
	guestRoot := t.TempDir()
	log := filepath.Join(guestRoot, "log")
	broker, mode, calls := "guest", "ok", 0
	c := DefaultConformanceCase(t.TempDir(), filepath.Join("testdata", "source-workspace"))
	c.ImageRef = pinnedTestImage
	c.GuestFloorScript = []byte("true\n")
	c.RunArgv = sessionRunArgv()
	target := BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(string) []string {
		calls++
		return []string{os.Args[0], "-test.run=^TestFakeBrokerProcess$", "--", broker, guestRoot, mode, "ok", log}
	}}
	return sessionFixture{target: target, broker: &broker, mode: &mode, calls: &calls, log: log, state: log + ".containers", c: c}
}

func (f sessionFixture) startRequest(t *testing.T) StartSessionRequest {
	t.Helper()
	ctx := context.Background()
	materialization, err := f.target.MaterializeWorkspace(ctx, MaterializeRequest{StateRoot: f.c.StateRoot, TargetID: f.c.TargetID, MaterializationID: f.c.MaterializationID, SourceWorkspace: f.c.SourceWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := f.target.BootstrapTarget(ctx, BootstrapRequest{StateRoot: f.c.StateRoot, TargetID: f.c.TargetID, BootstrapID: f.c.BootstrapID, ImageRef: f.c.ImageRef, GuestFloorScript: f.c.GuestFloorScript})
	if err != nil {
		t.Fatal(err)
	}
	return StartSessionRequest{SessionID: f.c.SessionID, Agent: f.c.Agent, Mode: f.c.Mode, StartedAt: f.c.StartedAt, Materialization: materialization, Bootstrap: bootstrap, RunArgv: f.c.RunArgv}
}

func (f sessionFixture) containers(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(readLog(t, f.state))
}

func TestRunConformancePassesWithTheBrokeredTarget(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	result, err := RunConformance(context.Background(), f.target, DefaultGCPVMContract(), f.c)
	if err != nil {
		t.Fatalf("RunConformance() = %v", err)
	}
	docker := readLog(t, f.log)
	for _, want := range []string{
		"docker " + strings.Join(sessionRunArgv(), " ") + "\n",
		"docker container stop --time 10 " + sessionContainer + "\n",
		"docker container rm --force " + sessionContainer + "\n",
	} {
		if !strings.Contains(docker, want) {
			t.Fatalf("guest docker log lacks %q:\n%s", want, docker)
		}
	}
	if got := f.containers(t); got != "" {
		t.Fatalf("guest containers after finish = %q", got)
	}
	if result.Started.Record.ContainerName != sessionContainer || !strings.Contains(result.Exported.AuditRecords[2], "container_name="+sessionContainer) {
		t.Fatalf("started record or audit lacks the container: %+v %q", result.Started.Record, result.Exported.AuditRecords[2])
	}
	signing := filepath.Join(t.TempDir(), "signing")
	seal, err := auditseal.SignSessionHead(signing, result.Finished.AuditLogPath, GCPVMProvider, f.c.SessionID, f.c.FinishedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auditseal.VerifySessionSeal(signing, result.Finished.AuditLogPath, GCPVMProvider, f.c.SessionID, seal); err != nil {
		t.Fatalf("VerifySessionSeal() = %v", err)
	}

	// A repeated finish returns the final record and calls no broker.
	calls, auditBefore := *f.calls, readLog(t, result.Finished.AuditLogPath)
	again, err := f.target.FinishSession(context.Background(), FinishSessionRequest{Started: result.Started, FinishedAt: "2026-04-21T00:20:00Z", ExitStatus: "9"})
	if err != nil || again.Record.ExitStatus != f.c.ExitStatus || *f.calls != calls || readLog(t, result.Finished.AuditLogPath) != auditBefore {
		t.Fatalf("repeated FinishSession() = %+v, %v (broker calls %d -> %d)", again.Record, err, calls, *f.calls)
	}
}

func TestBrokeredFinishFailsClosedAndARetrySucceeds(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"stuck", "lsfail", "rmlsfail"} {
		f := newSessionFixture(t)
		started, err := f.target.StartSession(context.Background(), f.startRequest(t))
		if err != nil {
			t.Fatal(err)
		}
		*f.mode = mode
		finish := FinishSessionRequest{Started: started, FinishedAt: f.c.FinishedAt, ExitStatus: "0"}
		if _, err := f.target.FinishSession(context.Background(), finish); err == nil {
			t.Fatalf("%s: FinishSession() succeeded", mode)
		}
		record, err := sessions.ReadSessionRecord(started.RecordPath)
		if err != nil || record.Status != SessionStartStatus || strings.Contains(readLog(t, started.AuditLogPath), "session_finished") {
			t.Fatalf("%s: a failed teardown changed the record or audit: %+v %v", mode, record, err)
		}
		*f.mode = "ok"
		finished, err := f.target.FinishSession(context.Background(), finish)
		if err != nil || finished.Record.Status != SessionFinalStatus || f.containers(t) != "" {
			t.Fatalf("%s: retry = %+v, %v, containers %q", mode, finished.Record, err, f.containers(t))
		}
	}
}

func TestBrokeredFinishAcceptsAContainerThatIsAlreadyGone(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	started, err := f.target.StartSession(context.Background(), f.startRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	// An earlier teardown removed the container and stopped before the record.
	if err := os.WriteFile(f.state, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	finished, err := f.target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: f.c.FinishedAt, ExitStatus: "0"})
	if err != nil || finished.Record.Status != SessionFinalStatus {
		t.Fatalf("FinishSession() = %+v, %v", finished.Record, err)
	}
	if strings.Contains(readLog(t, f.log), "container rm") {
		t.Fatal("the guest removed a container that docker did not list")
	}
}

func TestBrokeredFinishRemovesAContainerWhoseStopFailed(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	started, err := f.target.StartSession(context.Background(), f.startRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	*f.mode = "stopfail"
	if _, err := f.target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: f.c.FinishedAt, ExitStatus: "0"}); err != nil || f.containers(t) != "" {
		t.Fatalf("FinishSession() = %v, containers %q", err, f.containers(t))
	}
}

func TestBrokeredStartFailsClosed(t *testing.T) {
	t.Parallel()

	t.Run("docker run fails", func(t *testing.T) {
		f := newSessionFixture(t)
		req := f.startRequest(t)
		*f.mode = "runfail"
		_, err := f.target.StartSession(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "failed to set up container networking") {
			t.Fatalf("StartSession() = %v, want the docker run stderr", err)
		}
		if f.containers(t) != "" {
			t.Fatalf("the guest kept the created container %q", f.containers(t))
		}
		if _, err := os.Lstat(sessionRecordPath(req.Bootstrap, f.c.SessionID)); !os.IsNotExist(err) {
			t.Fatalf("a failed start wrote a session record: %v", err)
		}
		if _, err := os.Lstat(req.Bootstrap.AuditLogPath); !os.IsNotExist(err) {
			t.Fatalf("a failed start wrote audit records: %v", err)
		}
	})
	t.Run("the ack is lost", func(t *testing.T) {
		f := newSessionFixture(t)
		req := f.startRequest(t)
		*f.broker = "guestnoack"
		if _, err := f.target.StartSession(context.Background(), req); err == nil {
			t.Fatal("StartSession() succeeded without an ack")
		}
		if !strings.Contains(readLog(t, f.log), "docker "+strings.Join(sessionRunArgv(), " ")) || f.containers(t) != "" {
			t.Fatalf("the started container %q was not removed", f.containers(t))
		}
	})
	t.Run("host audit write fails", func(t *testing.T) {
		f := newSessionFixture(t)
		req := f.startRequest(t)
		if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), req.Bootstrap.AuditLogPath); err != nil {
			t.Fatal(err)
		}
		if _, err := f.target.StartSession(context.Background(), req); err == nil {
			t.Fatal("StartSession() succeeded with an unsafe audit log")
		}
		if f.containers(t) != "" {
			t.Fatalf("a container runs without its audit: %q", f.containers(t))
		}
		if _, err := os.Lstat(sessionRecordPath(req.Bootstrap, f.c.SessionID)); !os.IsNotExist(err) {
			t.Fatalf("the record of a removed container remains: %v", err)
		}
	})
}

func TestBrokeredStartRefusesBeforeTheBrokerStarts(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	base := f.startRequest(t)
	withArgv := func(argv ...string) func(*StartSessionRequest) {
		return func(req *StartSessionRequest) { req.RunArgv = argv }
	}
	for name, change := range map[string]func(*StartSessionRequest){
		"no argv":           withArgv(),
		"attached run":      withArgv("run", "-i", "--name", sessionContainer, pinnedTestImage),
		"not run":           withArgv("exec", "-d", "--name", sessionContainer, pinnedTestImage),
		"other image":       withArgv("run", "-d", "--name", sessionContainer, "ghcr.io/omkhar/workcell:latest"),
		"no name":           withArgv("run", "-d", pinnedTestImage, "--name", sessionContainer),
		"option name":       withArgv("run", "-d", "--name", "-x", pinnedTestImage),
		"spaced name":       withArgv("run", "-d", "--name", "a b", pinnedTestImage),
		"nul word":          withArgv("run", "-d", "--name", sessionContainer, "-e", "A=\x00", pinnedTestImage),
		"spaced session id": func(req *StartSessionRequest) { req.SessionID = "a b" },
		"no egress floor":   func(req *StartSessionRequest) { req.Bootstrap.Manifest.EgressFloorSHA256 = "" },
		"option target id":  func(req *StartSessionRequest) { req.Bootstrap.Manifest.TargetID = "-x" },
		"unpinned image":    func(req *StartSessionRequest) { req.Bootstrap.Manifest.ImageRef = "ghcr.io/omkhar/workcell:latest" },
	} {
		req := base
		change(&req)
		calls := *f.calls
		_, err := f.target.StartSession(context.Background(), req)
		if err == nil || *f.calls != calls {
			t.Fatalf("%s: StartSession() = %v, broker calls %d -> %d", name, err, calls, *f.calls)
		}
		if name == "no argv" && !errors.Is(err, errRunArgvPending) {
			t.Fatalf("no argv: StartSession() = %v, want the renderer gap", err)
		}
		if name == "other image" && !strings.Contains(err.Error(), "bootstrapped image") {
			t.Fatalf("other image: StartSession() = %v, want the image refusal", err)
		}
	}
	if _, err := f.target.StartSession(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	calls := *f.calls
	if _, err := f.target.StartSession(context.Background(), base); err == nil || *f.calls != calls {
		t.Fatalf("a second start of one session = %v", err)
	}
}

func TestBrokeredFinishRefusesATamperedRecordBeforeTheBrokerStarts(t *testing.T) {
	t.Parallel()

	for field, value := range map[string]string{"target_id": "-x", "container_name": "a b", "session_id": "a b"} {
		f := newSessionFixture(t)
		started, err := f.target.StartSession(context.Background(), f.startRequest(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := sessions.WriteSessionRecord(started.RecordPath, map[string]string{field: value}); err != nil {
			t.Fatal(err)
		}
		calls := *f.calls
		if _, err := f.target.FinishSession(context.Background(), FinishSessionRequest{Started: started, FinishedAt: f.c.FinishedAt, ExitStatus: "0"}); err == nil || *f.calls != calls {
			t.Fatalf("%s=%q: FinishSession() = %v, broker calls %d -> %d", field, value, err, calls, *f.calls)
		}
	}
}
