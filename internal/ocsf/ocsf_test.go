// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/applecontainer"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/supportbundle"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata golden files")

// fixedNow pins metadata.logged_time so golden output is stable across runs.
var fixedNow = time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

// mustExport runs Export and fails the test on error, for the happy-path cases
// whose fixtures carry no tampered (duplicate-key) audit records.
func mustExport(t *testing.T, exp sessions.SessionExport, opts Options) []Event {
	t.Helper()
	events, err := Export(exp, opts)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	return events
}

func sampleExport() sessions.SessionExport {
	return sessions.SessionExport{
		Session: sessions.SessionRecord{
			Version:        1,
			SessionID:      "sess-123",
			Profile:        "default",
			TargetKind:     "local_vm",
			TargetProvider: "colima",
			TargetID:       "workcell-default",
			Agent:          "claude",
			Mode:           "yolo",
			Status:         "exited",
			Workspace:      "/Users/op/src/repo",
			WorktreePath:   "/Users/op/src/repo/.worktrees/x",
			GitBranch:      "feature",
			ContainerName:  "workcell-sess-123",
			AuditLogPath:   "/Users/op/.local/state/workcell/audit.log",
			StartedAt:      "2026-07-05T11:00:00Z",
			FinishedAt:     "2026-07-05T11:30:00Z",
			ExitStatus:     "0",
		},
		AuditRecords: []string{
			"timestamp=2026-07-05T11:00:00Z session_id=sess-123 event=session_started target_kind=local_vm target_id=workcell-default agent=claude mode=yolo workspace=/Users/op/src/repo",
			"timestamp=2026-07-05T11:30:00Z session_id=sess-123 event=session_finished target_kind=local_vm target_id=workcell-default status=exited exit_status=0",
		},
	}
}

func TestSessionEventClassification(t *testing.T) {
	events := mustExport(t, sampleExport(), Options{Home: "/Users/op", Now: fixedNow})
	if len(events) != 3 {
		t.Fatalf("want 3 events (1 session + 2 audit), got %d", len(events))
	}
	s := events[0]
	if s.CategoryUID != 6 || s.ClassUID != 6002 {
		t.Fatalf("wrong classification: category=%d class=%d", s.CategoryUID, s.ClassUID)
	}
	// A finished session (FinishedAt/ExitStatus set) is a Stop.
	if s.ActivityID != activityStop || s.ActivityName != "Stop" {
		t.Fatalf("finished session should map to Stop, got activity_id=%d name=%q", s.ActivityID, s.ActivityName)
	}
	if s.TypeUID != 6002*100+activityStop {
		t.Fatalf("type_uid should be class*100+activity, got %d", s.TypeUID)
	}
	if s.StatusID != statusSuccess || s.Status != "Success" {
		t.Fatalf("exit 0 should be Success, got status_id=%d name=%q", s.StatusID, s.Status)
	}
	if s.Time != time.Date(2026, 7, 5, 11, 30, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("session time should be FinishedAt epoch millis, got %d", s.Time)
	}
	if s.App == nil || s.App.Name != "claude/yolo" {
		t.Fatalf("app name should be agent/mode, got %+v", s.App)
	}
	if s.Device == nil || s.Device.Name != "workcell-default" {
		t.Fatalf("device name should be target_id, got %+v", s.Device)
	}
}

// TestFailedTerminalStatusIsFailure proves a terminal record whose durable
// status is failed/aborted maps to OCSF Failure/Medium even when exit_status is
// 0 or empty — the outcome must reflect the session status, not the exit code
// alone (a run can fail or be aborted before a clean exit code is recorded).
func TestFailedTerminalStatusIsFailure(t *testing.T) {
	redact := supportbundle.NewRedactor("").String
	for _, tc := range []struct {
		name, status, exit  string
		wantStatus, wantSev int
	}{
		{"failed exit0", "failed", "0", statusFailure, severityMedium},
		{"aborted exit empty", "aborted", "", statusFailure, severityMedium},
		{"failed exit137", "failed", "137", statusFailure, severityMedium},
		{"exited exit0 stays success", "exited", "0", statusSuccess, severityInformational},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sessions.SessionRecord{
				Version: 1, SessionID: "s", Status: tc.status,
				ExitStatus: tc.exit, FinishedAt: "2026-07-05T11:30:00Z",
			}
			ev := sessionEvent(rec, redact, 0)
			if ev.StatusID != tc.wantStatus {
				t.Errorf("status=%q exit=%q: status_id=%d, want %d", tc.status, tc.exit, ev.StatusID, tc.wantStatus)
			}
			if ev.SeverityID != tc.wantSev {
				t.Errorf("status=%q exit=%q: severity_id=%d, want %d", tc.status, tc.exit, ev.SeverityID, tc.wantSev)
			}
		})
	}
}

func TestRunningSessionMapsToStart(t *testing.T) {
	exp := sampleExport()
	exp.Session.FinishedAt = ""
	exp.Session.ExitStatus = ""
	exp.Session.Status = "running"
	events := mustExport(t, exp, Options{Now: fixedNow})
	if events[0].ActivityID != activityStart || events[0].ActivityName != "Start" {
		t.Fatalf("running session should map to Start, got %d/%q", events[0].ActivityID, events[0].ActivityName)
	}
	if events[0].StatusID != statusUnknown {
		t.Fatalf("unfinished session should have Unknown status, got %d", events[0].StatusID)
	}
}

// TestSessionEventRedactsSharedG2Rules is the leak-proof for the session event:
// a token-shaped secret in a session field flows into the OCSF unmapped object.
// Mapping WITHOUT redaction (identity) LEAKS the token; mapping through Export —
// which uses the shared support-bundle Redactor — removes it and rewrites the
// operator home to ~, proving the G2 rules are shared rather than reinvented.
func TestSessionEventRedactsSharedG2Rules(t *testing.T) {
	// Assembled from fragments so the literal token shape never appears in
	// source and cannot trip GitHub push protection.
	const ghToken = "ghp_" + "0123456789abcdefghijklmnopqrstuvwx"
	exp := sessions.SessionExport{
		Session: sessions.SessionRecord{
			SessionID: "sess-x",
			Agent:     "claude",
			Mode:      "yolo",
			Status:    "running",
			Workspace: "/Users/op/src/repo",
			GitBranch: ghToken, // a token-shaped value in a session field
			StartedAt: "2026-07-05T11:00:00Z",
		},
	}

	if masked := supportbundle.NewRedactor("").String(ghToken); strings.Contains(masked, ghToken) {
		t.Fatalf("precondition: shared redactor did not mask the token: %q", masked)
	}

	// Without redaction the token WOULD leak into the output.
	rawEvents, err := mapExport(exp, func(s string) string { return s }, fixedNow)
	if err != nil {
		t.Fatalf("mapExport raw: %v", err)
	}
	var rawBuf bytes.Buffer
	if err := WriteJSONL(&rawBuf, rawEvents); err != nil {
		t.Fatalf("WriteJSONL raw: %v", err)
	}
	if !strings.Contains(rawBuf.String(), ghToken) {
		t.Fatalf("expected the un-redacted mapping to leak the token (proving the field is mapped through)")
	}

	// Through Export (the shared redactor) the token is gone and the path is
	// rewritten to ~, exactly as the support bundle redacts.
	events := mustExport(t, exp, Options{Home: "/Users/op", Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatalf("WriteJSONL redacted: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, ghToken) {
		t.Fatalf("SECRET LEAK: token survived the OCSF export:\n%s", out)
	}
	if !strings.Contains(out, "[REDACTED-TOKEN]") {
		t.Fatalf("expected [REDACTED-TOKEN] marker in redacted output:\n%s", out)
	}
	if strings.Contains(out, "/Users/op/") {
		t.Fatalf("operator home leaked into OCSF export:\n%s", out)
	}
	if !strings.Contains(out, "~/src/repo") {
		t.Fatalf("expected home path rewritten to ~, got:\n%s", out)
	}
}

func TestAuditEventClassification(t *testing.T) {
	events := mustExport(t, sampleExport(), Options{Home: "/Users/op", Now: fixedNow})
	started, finished := events[1], events[2]
	if started.ActivityID != activityStart {
		t.Fatalf("session_started should map to Start, got %d", started.ActivityID)
	}
	if finished.ActivityID != activityStop {
		t.Fatalf("session_finished should map to Stop, got %d", finished.ActivityID)
	}
	if got := started.Unmapped["audit.workspace"]; got != "~/src/repo" {
		t.Fatalf("audit workspace should be home-redacted, got %q", got)
	}
	if _, ok := started.Unmapped["audit.event"]; ok {
		t.Fatalf("event key should be consumed by classification, not in unmapped")
	}
	if _, ok := started.Unmapped["audit.session_id"]; ok {
		t.Fatalf("session_id should be consumed, not in unmapped")
	}
}

func TestActivityForEvent(t *testing.T) {
	cases := map[string]int{
		"session_started":        activityStart,
		"launch":                 activityStart,
		"session_finished":       activityStop,
		"exit":                   activityStop,
		"assurance-change":       activityOther,
		"bootstrap_ready":        activityOther,
		"workspace_materialized": activityOther,
		"":                       activityUnknown,
	}
	for event, want := range cases {
		if got := activityForEvent(event); got != want {
			t.Errorf("activityForEvent(%q)=%d want %d", event, got, want)
		}
	}
}

func TestFailedExitRaisesSeverity(t *testing.T) {
	line := "timestamp=2026-07-05T11:30:00Z session_id=s event=exit exit_status=137"
	ev, emit, err := auditEvent(line, encodingBashQuote, sessions.SessionRecord{SessionID: "s"}, supportbundle.NewRedactor("").String, 0)
	if err != nil {
		t.Fatalf("auditEvent: %v", err)
	}
	if !emit {
		t.Fatal("record with matching session_id should be emitted")
	}
	if ev.SeverityID != severityMedium || ev.Severity != "Medium" {
		t.Fatalf("non-zero exit should raise severity to Medium, got %d/%q", ev.SeverityID, ev.Severity)
	}
	if ev.StatusID != statusFailure || ev.Status != "Failure" {
		t.Fatalf("non-zero exit should be Failure, got %d/%q", ev.StatusID, ev.Status)
	}
}

func TestVersioningPresent(t *testing.T) {
	events := mustExport(t, sampleExport(), Options{Now: fixedNow})
	for i, ev := range events {
		if ev.Metadata.Version != OCSFSchemaVersion {
			t.Errorf("event %d: metadata.version=%q want %q", i, ev.Metadata.Version, OCSFSchemaVersion)
		}
		if ev.Metadata.MappingVersion != MappingVersion {
			t.Errorf("event %d: metadata.mapping_version=%q want %q", i, ev.Metadata.MappingVersion, MappingVersion)
		}
		if ev.Metadata.Product.Name != productName {
			t.Errorf("event %d: metadata.product.name=%q want %q", i, ev.Metadata.Product.Name, productName)
		}
		if ev.Metadata.LoggedTime != fixedNow.UnixMilli() {
			t.Errorf("event %d: logged_time=%d want %d", i, ev.Metadata.LoggedTime, fixedNow.UnixMilli())
		}
	}
}

func TestWriteJSONLOneObjectPerLine(t *testing.T) {
	events := mustExport(t, sampleExport(), Options{Home: "/Users/op", Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(events) {
		t.Fatalf("want %d JSONL lines, got %d", len(events), len(lines))
	}
	for i, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d is not well-formed JSON: %v\n%s", i, err, line)
		}
		if ev.ClassUID != 6002 {
			t.Fatalf("line %d: class_uid=%d want 6002", i, ev.ClassUID)
		}
	}
}

// TestRedactionSharesG2Rules is the leak-proof. A token-shaped secret placed in
// an audit field flows into the OCSF event unmapped object. Mapping WITHOUT
// redaction (identity) LEAKS the token; mapping through Export — which uses the
// shared support-bundle Redactor — removes it, proving the G2 rules are shared
// rather than reinvented.
func TestRedactionSharesG2Rules(t *testing.T) {
	// Assembled from fragments so the literal token shape never appears in
	// source and cannot trip GitHub push protection.
	const ghToken = "ghp_" + "0123456789abcdefghijklmnopqrstuvwx"
	exp := sessions.SessionExport{
		Session: sessions.SessionRecord{
			SessionID: "sess-x",
			Agent:     "claude",
			Mode:      "yolo",
			Status:    "running",
			Workspace: "/Users/op/src/repo",
			StartedAt: "2026-07-05T11:00:00Z",
		},
		AuditRecords: []string{
			"timestamp=2026-07-05T11:00:00Z session_id=sess-x event=launch endpoints=" + ghToken,
		},
	}

	// Sanity: the same shared redactor must actually mask this token, proving
	// the fixture carries a real secret and that Export reuses THIS rule-set.
	if masked := supportbundle.NewRedactor("").String(ghToken); strings.Contains(masked, ghToken) {
		t.Fatalf("precondition: shared redactor did not mask the token: %q", masked)
	}

	// Without redaction the token WOULD leak into the output.
	rawEvents, err := mapExport(exp, func(s string) string { return s }, fixedNow)
	if err != nil {
		t.Fatalf("mapExport raw: %v", err)
	}
	var rawBuf bytes.Buffer
	if err := WriteJSONL(&rawBuf, rawEvents); err != nil {
		t.Fatalf("WriteJSONL raw: %v", err)
	}
	if !strings.Contains(rawBuf.String(), ghToken) {
		t.Fatalf("expected the un-redacted mapping to leak the token (proving the field is mapped through)")
	}

	// Through Export (the shared redactor) the token is gone and the path is
	// rewritten to ~, exactly as the support bundle redacts.
	events := mustExport(t, exp, Options{Home: "/Users/op", Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatalf("WriteJSONL redacted: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, ghToken) {
		t.Fatalf("SECRET LEAK: token survived the OCSF export:\n%s", out)
	}
	if !strings.Contains(out, "[REDACTED-TOKEN]") {
		t.Fatalf("expected [REDACTED-TOKEN] marker in redacted output:\n%s", out)
	}
	if strings.Contains(out, "/Users/op/") {
		t.Fatalf("operator home leaked into OCSF export:\n%s", out)
	}
	if !strings.Contains(out, "~/src/repo") {
		t.Fatalf("expected home path rewritten to ~, got:\n%s", out)
	}
}

func TestExportDeterministic(t *testing.T) {
	a := mustExport(t, sampleExport(), Options{Home: "/Users/op", Now: fixedNow})
	b := mustExport(t, sampleExport(), Options{Home: "/Users/op", Now: fixedNow})
	var ba, bb bytes.Buffer
	if err := WriteJSONL(&ba, a); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSONL(&bb, b); err != nil {
		t.Fatal(err)
	}
	if ba.String() != bb.String() {
		t.Fatalf("OCSF export is not deterministic:\n%s\n---\n%s", ba.String(), bb.String())
	}
}

// TestGoldenOCSFExport pins the exact OCSF JSONL shape. Regenerate with:
//
//	go test ./internal/ocsf -run TestGoldenOCSFExport -update-golden
func TestGoldenOCSFExport(t *testing.T) {
	events := mustExport(t, sampleExport(), Options{Home: "/Users/op", Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}
	got := buf.Bytes()

	goldenPath := filepath.Join("testdata", "golden-export.jsonl")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update-golden to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("OCSF export drift.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestExportRejectsTamperedAuditRecord is the load-bearing dup-key proof at the
// export boundary: a tampered audit record (duplicate session_id) fails the
// export closed and emits NO events, so a forged record can never surface as an
// OCSF event.
func TestExportRejectsTamperedAuditRecord(t *testing.T) {
	exp := sampleExport()
	exp.AuditRecords = append(exp.AuditRecords,
		"timestamp=2026-07-05T11:31:00Z session_id=sess-123 session_id=attacker event=exit exit_status=0")

	events, err := Export(exp, Options{Home: "/Users/op", Now: fixedNow})
	if err == nil {
		t.Fatalf("expected the tampered (duplicate session_id) record to fail the export, got %d events", len(events))
	}
	if events != nil {
		t.Fatalf("no events must be emitted when a record is rejected, got %d", len(events))
	}
}

// TestExportDecodesSpacedEndpoints proves the launch record's space-delimited
// endpoints allowlist survives intact through the OCSF export (it would be
// truncated at the first space by a naive strings.Fields split).
func TestExportDecodesSpacedEndpoints(t *testing.T) {
	exp := sampleExport()
	exp.AuditRecords = []string{
		`timestamp=2026-07-05T11:00:00Z session_id=sess-123 event=launch endpoints=a.example:443\ b.example:443\ c.example:443`,
	}
	events := mustExport(t, exp, Options{Home: "/Users/op", Now: fixedNow})
	launch := events[1]
	if got := launch.Unmapped["audit.endpoints"]; got != "a.example:443 b.example:443 c.example:443" {
		t.Fatalf("spaced endpoints not preserved through export: got %q", got)
	}
}

// TestOCSFCoversEverySessionRecordField is the D7-style drift guard: every JSON
// field the default `session export` emits must be represented in the OCSF
// export (either a typed OCSF attribute or the unmapped object), so the two
// formats stay at field parity and a newly added SessionRecord field cannot be
// silently dropped from OCSF.
func TestOCSFCoversEverySessionRecordField(t *testing.T) {
	rec := fullyPopulatedRecord()
	ev := sessionEvent(rec, supportbundle.NewRedactor("").String, 0)

	// session_id/agent/mode are carried by typed OCSF attributes (app.uid /
	// app.name), not the unmapped object. Prove that before excusing them.
	if ev.App == nil || ev.App.UID == "" {
		t.Fatal("session_id must appear as app.uid")
	}
	if ev.App.Name == "" {
		t.Fatal("agent/mode must appear as app.name")
	}
	mappedElsewhere := map[string]bool{"session_id": true, "agent": true, "mode": true}

	rt := reflect.TypeOf(sessions.SessionRecord{})
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" || mappedElsewhere[name] {
			continue
		}
		if _, ok := ev.Unmapped["session."+name]; !ok {
			t.Errorf("SessionRecord field %q is dropped from the OCSF export; add session.%s to sessionEvent for JSON-export parity", name, name)
		}
	}
}

// fullyPopulatedRecord sets every SessionRecord field to a distinctive
// non-empty, non-secret value so the drift guard exercises the whole surface.
func fullyPopulatedRecord() sessions.SessionRecord {
	rec := sessions.SessionRecord{Version: 1}
	rv := reflect.ValueOf(&rec).Elem()
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rv.Field(i)
		if f.Kind() == reflect.String {
			name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			f.SetString("v-" + name)
		}
	}
	return rec
}

// TestExportAppleContainerPreservesBackslashPath is the load-bearing end-to-end
// proof: an apple-container session's workspace_materialized record (percent
// writer) with a literal backslash in the workspace path exports with the
// backslash intact, and the SAME record under a launcher provider (bash %q
// writer) corrupts it — so the OCSF export decodes per the session's writer.
func TestExportAppleContainerPreservesBackslashPath(t *testing.T) {
	auditLine := `ts=2026-07-05T11:00:00Z session_id=sess-ac event=workspace_materialized target_kind=local_vm target_provider=apple-container target_id=default workspace_transport=local-materialization materialization_id=m1 workspace_origin=/tmp/src workspace=/tmp/a\b`

	acExport := sessions.SessionExport{
		Session: sessions.SessionRecord{
			SessionID:      "sess-ac",
			Agent:          "claude",
			Mode:           "yolo",
			Status:         "running",
			TargetKind:     applecontainer.TargetKind,
			TargetProvider: applecontainer.Provider,
			StartedAt:      "2026-07-05T11:00:00Z",
		},
		AuditRecords: []string{auditLine},
	}
	events, err := Export(acExport, Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("Export apple-container: %v", err)
	}
	if got := events[1].Unmapped["audit.workspace"]; got != `/tmp/a\b` {
		t.Fatalf("apple-container export corrupted the backslash path: got %q want %q", got, `/tmp/a\b`)
	}

	// Control: the identical record under a launcher provider is decoded as
	// bash %q and the backslash is (correctly for that writer) treated as an
	// escape — proving the per-writer selection, not a no-op, is what preserves
	// the AppleContainer evidence.
	bashExport := acExport
	bashExport.Session.TargetProvider = "colima"
	bashEvents, err := Export(bashExport, Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("Export colima: %v", err)
	}
	if got := bashEvents[1].Unmapped["audit.workspace"]; got != "/tmp/ab" {
		t.Fatalf("expected the bash %%q path to drop the backslash (proving selection matters), got %q", got)
	}
}

// TestExportAppleContainerRedactsAfterDecode proves redaction still runs AFTER
// percent-decode on the AppleContainer path: a percent-encoded workspace under
// the operator home is decoded, then home-rewritten to ~ by the shared redactor.
func TestExportAppleContainerRedactsAfterDecode(t *testing.T) {
	auditLine := `ts=2026-07-05T11:00:00Z session_id=sess-ac event=workspace_materialized target_provider=apple-container workspace=/Users/op/a%20b`
	exp := sessions.SessionExport{
		Session: sessions.SessionRecord{
			SessionID:      "sess-ac",
			Agent:          "claude",
			Mode:           "yolo",
			Status:         "running",
			TargetProvider: applecontainer.Provider,
			StartedAt:      "2026-07-05T11:00:00Z",
		},
		AuditRecords: []string{auditLine},
	}
	events, err := Export(exp, Options{Home: "/Users/op", Now: fixedNow})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	// %20 decoded to a space, THEN the home prefix rewritten to ~.
	if got := events[1].Unmapped["audit.workspace"]; got != "~/a b" {
		t.Fatalf("percent-decode then home-redact expected ~/a b, got %q", got)
	}
}

// TestAuditEventDeviceFromRecord is the load-bearing proof for FIX 1: a launcher
// audit line does NOT stamp target_id/target_kind (only the SessionRecord has
// them), yet each standalone OCSF lifecycle event must still carry its sandbox
// device — sourced from the authoritative record, not the sparse line.
func TestAuditEventDeviceFromRecord(t *testing.T) {
	line := "timestamp=2026-07-05T11:00:00Z session_id=s1 event=launch profile=default agent=claude mode=yolo workspace=/tmp/w"
	// Precondition: the line itself carries no target fields, so a line-derived
	// device would be nil (this is exactly the pre-fix regression).
	decoded, err := decodeAuditLine(line, encodingBashQuote)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range decoded {
		if f.key == "target_id" || f.key == "target_kind" {
			t.Fatalf("fixture invalid: line must not carry %s", f.key)
		}
	}

	exp := sessions.SessionExport{
		Session: sessions.SessionRecord{
			SessionID: "s1", Agent: "claude", Mode: "yolo", Status: "running",
			TargetKind: "local_vm", TargetProvider: "colima", TargetID: "wcl-default",
			ContainerName: "wcl-s1", StartedAt: "2026-07-05T11:00:00Z",
		},
		AuditRecords: []string{line},
	}
	events := mustExport(t, exp, Options{Now: fixedNow})
	audit := events[1]
	if audit.Device == nil {
		t.Fatal("per-audit event lost its device; it must come from the authoritative record")
	}
	if audit.Device.Name != "wcl-default" || audit.Device.Hostname != "wcl-s1" {
		t.Fatalf("device should be the record's target, got %+v", audit.Device)
	}
	if audit.App == nil || audit.App.UID != "s1" {
		t.Fatalf("per-audit app should come from the record, got %+v", audit.App)
	}
}

// TestAuditUnexpectedKeyNeverBecomesProperty is the load-bearing proof for FIX 2:
// a tampered audit line whose KEY is a secret must not turn that secret into an
// OCSF property name. The unexpected key is bucketed under one fixed, redacted
// property, so the secret never appears in a key or value.
func TestAuditUnexpectedKeyNeverBecomesProperty(t *testing.T) {
	const ghToken = "ghp_" + "0123456789abcdefghijklmnopqrstuvwx"
	exp := sessions.SessionExport{
		Session: sessions.SessionRecord{
			SessionID: "s1", Agent: "claude", Mode: "yolo", Status: "running",
			StartedAt: "2026-07-05T11:00:00Z",
		},
		AuditRecords: []string{
			// A legitimate known key (workspace) alongside a secret-shaped
			// unexpected key.
			"timestamp=2026-07-05T11:00:00Z session_id=s1 event=launch workspace=/tmp/w " + ghToken + "=x",
		},
	}
	events := mustExport(t, exp, Options{Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, ghToken) {
		t.Fatalf("SECRET LEAK: token-shaped audit key surfaced in the OCSF output:\n%s", out)
	}

	audit := events[1]
	for k := range audit.Unmapped {
		if strings.Contains(k, "ghp_") {
			t.Fatalf("unexpected secret-shaped key became an OCSF property name: %q", k)
		}
	}
	// The known key still maps to a typed property...
	if _, ok := audit.Unmapped["audit.workspace"]; !ok {
		t.Fatalf("known key must still be a typed property, got %+v", audit.Unmapped)
	}
	// ...and the unexpected field is bucketed with BOTH key and value hard-
	// redacted, so neither the token-shaped key nor its value survives.
	v, ok := audit.Unmapped["audit.unexpected_fields"]
	if !ok {
		t.Fatalf("unexpected key must be bucketed under audit.unexpected_fields, got %+v", audit.Unmapped)
	}
	if v != FreeFormPlaceholder+"="+FreeFormPlaceholder {
		t.Fatalf("bucketed unexpected field must carry no attacker text, got %q", v)
	}
	if strings.Contains(v, "ghp_") {
		t.Fatalf("token-shaped key leaked into the bucket: %q", v)
	}
}

// bashQuoteSpaces mimics the `printf %q` escaping of spaces (the only special
// char in these fixtures) so decodeAuditLine recovers the original argv value.
func bashQuoteSpaces(s string) string { return strings.ReplaceAll(s, " ", `\ `) }

// TestFreeFormArgvHardRedactedSplitCLI is the P1 leak-proof: a credential in
// split-CLI form inside the free-form argv message is NOT a token or key=value
// shape, so the regex redactor cannot catch it — argv must be hard-redacted to
// the fixed placeholder, not redact-then-emitted.
func TestFreeFormArgvHardRedactedSplitCLI(t *testing.T) {
	const secret = "hunter2"
	argvVal := "deploy --password " + secret
	line := "timestamp=2026-07-05T11:00:00Z session_id=s1 event=command source=host-cli command=session-send argv=" + bashQuoteSpaces(argvVal)

	// Sanity: the decoded argv really carries the secret...
	decoded, err := decodeAuditLine(line, encodingBashQuote)
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldMap(decoded)["argv"]; got != argvVal {
		t.Fatalf("fixture: decoded argv = %q, want %q", got, argvVal)
	}
	// ...and the regex redactor ALONE fails to mask it (proving hard-redact is
	// necessary, not the generic redactor).
	if masked := supportbundle.NewRedactor("").String(argvVal); !strings.Contains(masked, secret) {
		t.Fatalf("precondition: the regex redactor unexpectedly masked the split-CLI secret: %q", masked)
	}

	exp := sessions.SessionExport{
		Session:      sessions.SessionRecord{SessionID: "s1", Agent: "claude", Mode: "yolo", Status: "running", StartedAt: "2026-07-05T11:00:00Z"},
		AuditRecords: []string{line},
	}
	events := mustExport(t, exp, Options{Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("SECRET LEAK: split-CLI credential survived in the OCSF output:\n%s", out)
	}
	if strings.Contains(out, "--password") {
		t.Fatalf("raw argv text leaked into the OCSF output:\n%s", out)
	}
	audit := events[1]
	if got := audit.Unmapped["audit.argv"]; got != FreeFormPlaceholder {
		t.Fatalf("argv should be hard-redacted to the placeholder, got %q", got)
	}
	// The event still emits with its bounded control fields.
	if audit.Unmapped["audit.command"] != "session-send" || audit.Unmapped["audit.source"] != "host-cli" {
		t.Fatalf("bounded control fields must still emit, got %+v", audit.Unmapped)
	}
}

// TestFreeFormArgvHardRedactedProse proves a secret in ordinary prose (no
// recognizable pattern) inside argv is also withheld.
func TestFreeFormArgvHardRedactedProse(t *testing.T) {
	const secret = "s3cr3tphrase"
	argvVal := "the deploy key is " + secret + " keep it safe"
	line := "timestamp=2026-07-05T11:00:00Z session_id=s1 event=command source=host-cli command=session-send argv=" + bashQuoteSpaces(argvVal)

	exp := sessions.SessionExport{
		Session:      sessions.SessionRecord{SessionID: "s1", Agent: "claude", Mode: "yolo", Status: "running", StartedAt: "2026-07-05T11:00:00Z"},
		AuditRecords: []string{line},
	}
	events := mustExport(t, exp, Options{Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, secret) {
		t.Fatalf("SECRET LEAK: prose credential survived in the OCSF output:\n%s", out)
	}
	if got := events[1].Unmapped["audit.argv"]; got != FreeFormPlaceholder {
		t.Fatalf("argv should be the placeholder, got %q", got)
	}
}

// TestFreeFormFieldsSubsetOfKnown enforces the invariant that every free-form
// field is also an allowlisted known field (so it takes the placeholder branch
// rather than the unexpected-key bucket).
func TestFreeFormFieldsSubsetOfKnown(t *testing.T) {
	for k := range freeFormAuditFields {
		if _, ok := knownAuditFields[k]; !ok {
			t.Errorf("free-form field %q must also be in knownAuditFields", k)
		}
	}
}

// TestExportUnexpectedFieldValueHardRedacted is the FIX-1 P1 leak-proof: an
// unexpected field's VALUE is arbitrary text the regex redactor cannot sanitize
// (split-CLI/prose secrets), so it must be hard-redacted to the placeholder, not
// regex-redacted-then-emitted.
func TestExportUnexpectedFieldValueHardRedacted(t *testing.T) {
	const secret = "hunter2"
	// `foo` is not a writer-emitted key; its value carries a split-CLI secret.
	line := "timestamp=2026-07-05T11:00:00Z session_id=s1 event=launch foo=" + bashQuoteSpaces("deploy --password "+secret)
	// Precondition: the regex redactor alone does NOT mask this value.
	if masked := supportbundle.NewRedactor("").String("deploy --password " + secret); !strings.Contains(masked, secret) {
		t.Fatalf("precondition: regex redactor unexpectedly masked the value: %q", masked)
	}
	exp := sessions.SessionExport{
		Session:      sessions.SessionRecord{SessionID: "s1", Agent: "claude", Mode: "yolo", Status: "running", StartedAt: "2026-07-05T11:00:00Z"},
		AuditRecords: []string{line},
	}
	events := mustExport(t, exp, Options{Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, secret) || strings.Contains(out, "--password") {
		t.Fatalf("SECRET LEAK: unexpected-field value survived in the OCSF output:\n%s", out)
	}
	// The key "foo" is attacker-controlled too, so it must not survive either;
	// the entry is fully hard-redacted.
	v := events[1].Unmapped["audit.unexpected_fields"]
	if v != FreeFormPlaceholder+"="+FreeFormPlaceholder {
		t.Fatalf("unexpected field must be fully hard-redacted, got %q", v)
	}
	if strings.Contains(v, "foo") {
		t.Fatalf("attacker-controlled key leaked into the bucket: %q", v)
	}
}

// TestExportTamperedEventNameNotInMessage is the FIX-2 P1 leak-proof: a tampered
// event value must not be echoed into the typed OCSF message; the message uses a
// safe generic and the raw event is hard-redacted into the bucket.
func TestExportTamperedEventNameNotInMessage(t *testing.T) {
	const secret = "hunter2"
	line := "timestamp=2026-07-05T11:00:00Z session_id=s1 event=" + bashQuoteSpaces("deploy --password "+secret)
	exp := sessions.SessionExport{
		Session:      sessions.SessionRecord{SessionID: "s1", Agent: "claude", Mode: "yolo", Status: "running", StartedAt: "2026-07-05T11:00:00Z"},
		AuditRecords: []string{line},
	}
	events := mustExport(t, exp, Options{Now: fixedNow})
	audit := events[1]
	if strings.Contains(audit.Message, secret) || strings.Contains(audit.Message, "--password") {
		t.Fatalf("SECRET LEAK: tampered event leaked into the message: %q", audit.Message)
	}
	// An unrecognized event maps to Other; the message uses that safe generic.
	if audit.Message != "workcell audit Other" {
		t.Fatalf("tampered event should yield a safe generic message, got %q", audit.Message)
	}
	if v := audit.Unmapped["audit.unexpected_fields"]; !strings.Contains(v, "event="+FreeFormPlaceholder) {
		t.Fatalf("raw tampered event must be hard-redacted into the bucket, got %q", v)
	}
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("tampered event secret survived somewhere in the output")
	}
	// A recognized event is still echoed verbatim into the message.
	rec := mustExport(t, sessions.SessionExport{
		Session:      exp.Session,
		AuditRecords: []string{"timestamp=2026-07-05T11:00:00Z session_id=s1 event=launch"},
	}, Options{Now: fixedNow})
	if rec[1].Message != "workcell audit launch" {
		t.Fatalf("recognized event should be echoed, got %q", rec[1].Message)
	}
}

// TestTornStopRecordNotSuccess is the FIX-3 correctness proof: a torn stop record
// with no exit_status must map to Unknown, not a fabricated Success; a complete
// exit_status=0 stays Success.
func TestTornStopRecordNotSuccess(t *testing.T) {
	base := sessions.SessionRecord{SessionID: "s1", Agent: "claude", Mode: "yolo", Status: "running", StartedAt: "2026-07-05T11:00:00Z"}

	torn := mustExport(t, sessions.SessionExport{
		Session:      base,
		AuditRecords: []string{"timestamp=2026-07-05T11:30:00Z session_id=s1 event=session_finished"},
	}, Options{Now: fixedNow})[1]
	if torn.ActivityID != activityStop {
		t.Fatalf("session_finished should be a Stop, got %d", torn.ActivityID)
	}
	if torn.StatusID != statusUnknown || torn.Status != "Unknown" {
		t.Fatalf("torn stop (no exit_status) must be Unknown, got status_id=%d name=%q", torn.StatusID, torn.Status)
	}

	complete := mustExport(t, sessions.SessionExport{
		Session:      base,
		AuditRecords: []string{"timestamp=2026-07-05T11:30:00Z session_id=s1 event=exit exit_status=0"},
	}, Options{Now: fixedNow})[1]
	if complete.StatusID != statusSuccess || complete.Status != "Success" {
		t.Fatalf("complete exit_status=0 must stay Success, got status_id=%d name=%q", complete.StatusID, complete.Status)
	}
}

// TestMappingVersionBumpedForMultiEvent is the FIX-4 proof: the multi-event
// export carries mapping_version "2" (was "1" for the single session event).
func TestMappingVersionBumpedForMultiEvent(t *testing.T) {
	if MappingVersion != "2" {
		t.Fatalf("multi-event mapping must be version 2, got %q", MappingVersion)
	}
	events := mustExport(t, sampleExport(), Options{Now: fixedNow})
	if len(events) < 2 {
		t.Fatalf("fixture must produce a multi-event stream, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Metadata.MappingVersion != "2" {
			t.Errorf("event %d: mapping_version=%q want 2", i, ev.Metadata.MappingVersion)
		}
	}
}

// TestExportUnexpectedKeyHardRedacted is the FIX-1 (round 2) P1 leak-proof: a
// tampered line whose KEY is arbitrary prose/split-CLI text
// (`deploy --password hunter2=x` decodes to that whole prose as the key) must
// not leak the secret through the property key. Both key and value are hard-
// redacted, so no attacker text reaches any property name or value.
func TestExportUnexpectedKeyHardRedacted(t *testing.T) {
	const secret = "hunter2"
	// The decoded KEY is "deploy --password hunter2" (everything before the '=').
	line := "timestamp=2026-07-05T11:00:00Z session_id=s1 event=launch " + bashQuoteSpaces("deploy --password "+secret) + "=x"

	// Sanity: the decoded key really carries the secret prose.
	decoded, err := decodeAuditLine(line, encodingBashQuote)
	if err != nil {
		t.Fatal(err)
	}
	var sawKey bool
	for _, f := range decoded {
		if f.key == "deploy --password "+secret {
			sawKey = true
		}
	}
	if !sawKey {
		t.Fatalf("fixture: expected the secret-bearing key to decode; got %+v", decoded)
	}

	exp := sessions.SessionExport{
		Session:      sessions.SessionRecord{SessionID: "s1", Agent: "claude", Mode: "yolo", Status: "running", StartedAt: "2026-07-05T11:00:00Z"},
		AuditRecords: []string{line},
	}
	events := mustExport(t, exp, Options{Now: fixedNow})
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, secret) || strings.Contains(out, "--password") {
		t.Fatalf("SECRET LEAK: unexpected-field KEY survived in the OCSF output:\n%s", out)
	}
	if v := events[1].Unmapped["audit.unexpected_fields"]; v != FreeFormPlaceholder+"="+FreeFormPlaceholder {
		t.Fatalf("unexpected field must be fully hard-redacted, got %q", v)
	}
}

// TestExportSkipsSpoofedSessionRecord is the FIX-2 load-bearing proof: a record
// that belongs to a DIFFERENT session (real session_id=B) but slips through the
// substring prefilter because its argv contains `session_id=A` must NOT be
// emitted when exporting session A — the decoded session_id is authoritative.
func TestExportSkipsSpoofedSessionRecord(t *testing.T) {
	// Real record for session B; its argv embeds the substring session_id=A.
	spoof := "timestamp=2026-07-05T11:00:00Z session_id=B event=command source=host-cli command=session-send argv=" +
		bashQuoteSpaces("hello session_id=A world")
	genuine := "timestamp=2026-07-05T11:05:00Z session_id=A event=launch"

	// Precondition: the spoof's decoded session_id is B (not A), and its argv
	// carries the A substring the prefilter matched on.
	decoded, err := decodeAuditLine(spoof, encodingBashQuote)
	if err != nil {
		t.Fatal(err)
	}
	m := fieldMap(decoded)
	if m["session_id"] != "B" || !strings.Contains(m["argv"], "session_id=A") {
		t.Fatalf("fixture invalid: session_id=%q argv=%q", m["session_id"], m["argv"])
	}

	exp := sessions.SessionExport{
		Session:      sessions.SessionRecord{SessionID: "A", Agent: "claude", Mode: "yolo", Status: "running", StartedAt: "2026-07-05T11:00:00Z"},
		AuditRecords: []string{spoof, genuine},
	}
	events := mustExport(t, exp, Options{Now: fixedNow})
	// Session summary + exactly the ONE genuine session-A audit event; the
	// spoofed session-B record is dropped.
	if len(events) != 2 {
		t.Fatalf("spoofed record must be skipped: want 2 events (summary + genuine), got %d", len(events))
	}
	if events[1].Message != "workcell audit launch" {
		t.Fatalf("the emitted audit event should be the genuine session-A launch, got %q", events[1].Message)
	}
	// No trace of the spoof's content anywhere.
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, events); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "session-send") {
		t.Fatalf("spoofed session-B record was attributed to session A:\n%s", buf.String())
	}
}
