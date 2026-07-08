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
	ev, err := auditEvent(line, encodingBashQuote, sessions.SessionRecord{SessionID: "s"}, supportbundle.NewRedactor("").String, 0)
	if err != nil {
		t.Fatalf("auditEvent: %v", err)
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
	// ...and the unexpected key is bucketed, redacted.
	v, ok := audit.Unmapped["audit.unexpected_fields"]
	if !ok {
		t.Fatalf("unexpected key must be bucketed under audit.unexpected_fields, got %+v", audit.Unmapped)
	}
	if !strings.Contains(v, "[REDACTED-TOKEN]") {
		t.Fatalf("bucketed unexpected field must be redacted, got %q", v)
	}
}
