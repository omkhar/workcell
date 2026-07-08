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

	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/supportbundle"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata golden files")

// fixedNow pins metadata.logged_time so golden output is stable across runs.
var fixedNow = time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

// mustExport runs Export and fails the test on error.
func mustExport(t *testing.T, exp sessions.SessionExport, opts Options) []Event {
	t.Helper()
	events, err := Export(exp, opts)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	return events
}

// sampleExport is a session-only fixture (no audit records); the per-audit
// records and their tests are added by the companion audit change.
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
	}
}

func TestSessionEventClassification(t *testing.T) {
	events := mustExport(t, sampleExport(), Options{Home: "/Users/op", Now: fixedNow})
	if len(events) != 1 {
		t.Fatalf("want 1 session event, got %d", len(events))
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
