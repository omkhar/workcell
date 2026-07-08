// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package ocsf maps a Workcell session export (the session record plus its
// per-session audit lifecycle records) onto the Open Cybersecurity Schema
// Framework (OCSF) and emits it as JSONL — one OCSF event object per line.
//
// Class choice: a Workcell session is a sandboxed agent application instance
// that is started and stopped, so its lifecycle maps to the OCSF
// "Application Lifecycle" class (class_uid 6002) in the "Application Activity"
// category (category_uid 6). One summary event is emitted for the session
// record itself, followed by one event per audit lifecycle record. Event names
// map to OCSF activity_ids (Start/Stop/Other); the long tail of session and
// audit fields is preserved under the OCSF `unmapped` object so no evidence is
// lost while the typed OCSF attributes stay well-defined.
//
// Redaction is SHARED with the G2 support-bundle redactor
// (supportbundle.Redactor): every free-form string that enters an OCSF event is
// passed through the same rule-set, so no credential, token, or operator home
// path leaks into the export. Redaction is applied unconditionally — there is no
// un-redacted OCSF output path.
package ocsf

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/supportbundle"
)

// OCSFSchemaVersion is the OCSF schema release the mapping targets. It is
// emitted as metadata.version so consumers can select the right schema.
const OCSFSchemaVersion = "1.3.0"

// MappingVersion versions the Workcell field→OCSF-attribute mapping itself,
// independent of the OCSF schema version. Bump it whenever the mapping in this
// package changes (a field is remapped, an activity_id reassigned, an attribute
// added/removed) so consumers can gate on the exact mapping that produced an
// event. It is emitted as metadata.mapping_version.
const MappingVersion = "1"

// OCSF Application Lifecycle classification constants.
const (
	categoryUID  = 6
	categoryName = "Application Activity"
	classUID     = 6002
	className    = "Application Lifecycle"
)

// productName / vendorName identify Workcell as the OCSF metadata.product.
const (
	productName = "workcell"
	vendorName  = "Workcell"
)

// Application Lifecycle activity_ids (OCSF 1.x).
const (
	activityUnknown = 0
	activityStart   = 3
	activityStop    = 4
	activityOther   = 99
)

// OCSF base-event severity_ids.
const (
	severityInformational = 1
	severityMedium        = 3
)

// OCSF base-event status_ids.
const (
	statusUnknown = 0
	statusSuccess = 1
	statusFailure = 2
)

// OCSF device type_id 6 = "Virtual"; a Workcell target is a VM/container.
const (
	deviceTypeVirtualID = 6
	deviceTypeVirtual   = "Virtual"
)

// Product is the OCSF metadata.product object identifying the emitter.
type Product struct {
	Name       string `json:"name"`
	VendorName string `json:"vendor_name"`
}

// Metadata is the OCSF base-event metadata object. Version carries the OCSF
// schema version; MappingVersion is a Workcell extension carrying the mapping
// version so the two version axes are independently discoverable.
type Metadata struct {
	Version        string  `json:"version"`
	Product        Product `json:"product"`
	LoggedTime     int64   `json:"logged_time"`
	MappingVersion string  `json:"mapping_version"`
}

// App is the OCSF Application object describing the agent session instance.
type App struct {
	Name string `json:"name,omitempty"`
	UID  string `json:"uid,omitempty"`
}

// Device is the OCSF Device object describing the sandbox target.
type Device struct {
	TypeID   int    `json:"type_id"`
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	UID      string `json:"uid,omitempty"`
}

// Event is a single OCSF Application Lifecycle event. Field order and json tags
// follow OCSF base-event + Application Lifecycle attribute names. Unmapped holds
// the redacted long tail of session/audit fields that have no typed OCSF home.
type Event struct {
	CategoryUID  int               `json:"category_uid"`
	CategoryName string            `json:"category_name"`
	ClassUID     int               `json:"class_uid"`
	ClassName    string            `json:"class_name"`
	ActivityID   int               `json:"activity_id"`
	ActivityName string            `json:"activity_name"`
	TypeUID      int               `json:"type_uid"`
	TypeName     string            `json:"type_name"`
	Time         int64             `json:"time"`
	TimeDt       string            `json:"time_dt,omitempty"`
	SeverityID   int               `json:"severity_id"`
	Severity     string            `json:"severity"`
	StatusID     int               `json:"status_id"`
	Status       string            `json:"status"`
	Message      string            `json:"message"`
	Metadata     Metadata          `json:"metadata"`
	App          *App              `json:"app,omitempty"`
	Device       *Device           `json:"device,omitempty"`
	Unmapped     map[string]string `json:"unmapped,omitempty"`
}

// Options carries the host context the mapper needs. Home is the operator home
// directory; it is forwarded to the shared support-bundle redactor so paths are
// rewritten to ~ exactly as the support bundle does.
type Options struct {
	Home string
	// Now is the export time stamped into metadata.logged_time. Zero means
	// time.Now() at call time; tests pin it for deterministic golden output.
	Now time.Time
}

// Export maps a session export to OCSF Application Lifecycle events with the
// shared support-bundle redaction applied to every free-form string. The first
// event summarizes the session record; each subsequent event is one audit
// lifecycle record. It fails closed if any audit record is tampered (carries a
// duplicate key) so a forged record is never emitted as an OCSF event.
func Export(export sessions.SessionExport, opts Options) ([]Event, error) {
	r := supportbundle.NewRedactor(opts.Home)
	return mapExport(export, r.String, opts.Now)
}

// WriteJSONL writes events as JSON Lines: one compact JSON object per line,
// terminated by a newline. It is the on-the-wire form of the OCSF export.
func WriteJSONL(w io.Writer, events []Event) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// mapExport is the redaction-injectable core. redact is applied to every
// free-form string before it enters an event; production passes the shared
// support-bundle redactor. A test can pass an identity function to prove that,
// without redaction, a secret-bearing field would leak — then that the shared
// redactor removes it.
func mapExport(export sessions.SessionExport, redact func(string) string, now time.Time) ([]Event, error) {
	if now.IsZero() {
		now = time.Now()
	}
	loggedTime := now.UnixMilli()

	// Select the audit decoder from the session's writer: apple-container
	// sessions percent-encode path fields, every launcher backend uses bash %q.
	enc := auditEncodingForProvider(export.Session.TargetProvider)

	events := make([]Event, 0, 1+len(export.AuditRecords))
	events = append(events, sessionEvent(export.Session, redact, loggedTime))
	for _, line := range export.AuditRecords {
		ev, err := auditEvent(line, enc, redact, loggedTime)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}

// meta builds the OCSF metadata object stamped with both version axes.
func meta(loggedTime int64) Metadata {
	return Metadata{
		Version:        OCSFSchemaVersion,
		Product:        Product{Name: productName, VendorName: vendorName},
		LoggedTime:     loggedTime,
		MappingVersion: MappingVersion,
	}
}

// activityName maps an activity_id to its OCSF label.
func activityName(id int) string {
	switch id {
	case activityStart:
		return "Start"
	case activityStop:
		return "Stop"
	case activityOther:
		return "Other"
	default:
		return "Unknown"
	}
}

// severityName maps a severity_id to its OCSF label.
func severityName(id int) string {
	switch id {
	case severityMedium:
		return "Medium"
	default:
		return "Informational"
	}
}

// statusName maps a status_id to its OCSF label.
func statusName(id int) string {
	switch id {
	case statusSuccess:
		return "Success"
	case statusFailure:
		return "Failure"
	default:
		return "Unknown"
	}
}

// sessionEvent builds the summary event from the session record. A session that
// has finished (FinishedAt or ExitStatus set) maps to Stop; otherwise Start.
func sessionEvent(rec sessions.SessionRecord, redact func(string) string, loggedTime int64) Event {
	finished := strings.TrimSpace(rec.FinishedAt) != "" || strings.TrimSpace(rec.ExitStatus) != ""
	activity := activityStart
	if finished {
		activity = activityStop
	}

	severityID := severityFromExit(rec.ExitStatus)
	statusID := statusUnknown
	if finished {
		statusID = statusFromExit(rec.ExitStatus)
	}

	timeStr := rec.StartedAt
	if strings.TrimSpace(rec.FinishedAt) != "" {
		timeStr = rec.FinishedAt
	}
	epoch, dt := parseTime(timeStr, redact)

	unmapped := newUnmapped()
	unmapped.put("session.version", fmt.Sprintf("%d", rec.Version))
	unmapped.putStr("session.profile", rec.Profile, redact)
	unmapped.putStr("session.status", rec.Status, redact)
	unmapped.putStr("session.live_status", rec.LiveStatus, redact)
	unmapped.putStr("session.ui", rec.UI, redact)
	unmapped.putStr("session.execution_path", rec.ExecutionPath, redact)
	unmapped.putStr("session.workspace", rec.Workspace, redact)
	unmapped.putStr("session.workspace_origin", rec.WorkspaceOrigin, redact)
	unmapped.putStr("session.workspace_root", rec.WorkspaceRoot, redact)
	unmapped.putStr("session.worktree_path", rec.WorktreePath, redact)
	unmapped.putStr("session.workspace_transport", rec.WorkspaceTransport, redact)
	unmapped.putStr("session.workspace_control_plane", rec.WorkspaceControlPlane, redact)
	unmapped.putStr("session.git_branch", rec.GitBranch, redact)
	unmapped.putStr("session.git_head", rec.GitHead, redact)
	unmapped.putStr("session.git_base", rec.GitBase, redact)
	unmapped.putStr("session.runtime_api", rec.RuntimeAPI, redact)
	unmapped.putStr("session.target_kind", rec.TargetKind, redact)
	unmapped.putStr("session.target_provider", rec.TargetProvider, redact)
	unmapped.putStr("session.target_id", rec.TargetID, redact)
	unmapped.putStr("session.target_assurance_class", rec.TargetAssuranceClass, redact)
	unmapped.putStr("session.initial_assurance", rec.InitialAssurance, redact)
	unmapped.putStr("session.current_assurance", rec.CurrentAssurance, redact)
	unmapped.putStr("session.final_assurance", rec.FinalAssurance, redact)
	unmapped.putStr("session.bootstrap_id", rec.BootstrapID, redact)
	unmapped.putStr("session.image_ref", rec.ImageRef, redact)
	unmapped.putStr("session.container_name", rec.ContainerName, redact)
	unmapped.putStr("session.monitor_pid", rec.MonitorPID, redact)
	unmapped.putStr("session.session_audit_dir", rec.SessionAuditDir, redact)
	unmapped.putStr("session.audit_log_path", rec.AuditLogPath, redact)
	unmapped.putStr("session.debug_log_path", rec.DebugLogPath, redact)
	unmapped.putStr("session.file_trace_log_path", rec.FileTraceLogPath, redact)
	unmapped.putStr("session.transcript_log_path", rec.TranscriptLogPath, redact)
	unmapped.putStr("session.started_at", rec.StartedAt, redact)
	unmapped.putStr("session.observed_at", rec.ObservedAt, redact)
	unmapped.putStr("session.finished_at", rec.FinishedAt, redact)
	unmapped.putStr("session.exit_status", rec.ExitStatus, redact)

	return Event{
		CategoryUID:  categoryUID,
		CategoryName: categoryName,
		ClassUID:     classUID,
		ClassName:    className,
		ActivityID:   activity,
		ActivityName: activityName(activity),
		TypeUID:      typeUID(activity),
		TypeName:     className + ": " + activityName(activity),
		Time:         epoch,
		TimeDt:       dt,
		SeverityID:   severityID,
		Severity:     severityName(severityID),
		StatusID:     statusID,
		Status:       statusName(statusID),
		Message:      "workcell session " + redact(rec.SessionID) + " " + activityName(activity),
		Metadata:     meta(loggedTime),
		App:          sessionApp(rec, redact),
		Device:       sessionDevice(rec, redact),
		Unmapped:     unmapped.finish(),
	}
}

// sessionApp builds the OCSF App object for the agent session. Agent and mode
// are enumerated values but are still routed through redact for uniformity.
func sessionApp(rec sessions.SessionRecord, redact func(string) string) *App {
	name := strings.TrimSpace(rec.Agent)
	if mode := strings.TrimSpace(rec.Mode); mode != "" {
		if name != "" {
			name += "/" + mode
		} else {
			name = mode
		}
	}
	if name == "" && strings.TrimSpace(rec.SessionID) == "" {
		return nil
	}
	return &App{Name: redact(name), UID: redact(rec.SessionID)}
}

// sessionDevice builds the OCSF Device object for the sandbox target.
func sessionDevice(rec sessions.SessionRecord, redact func(string) string) *Device {
	name := strings.TrimSpace(rec.TargetID)
	host := strings.TrimSpace(rec.ContainerName)
	if name == "" && host == "" {
		return nil
	}
	return &Device{
		TypeID:   deviceTypeVirtualID,
		Type:     deviceTypeVirtual,
		Name:     redact(name),
		Hostname: redact(host),
		UID:      redact(name),
	}
}

// auditEvent builds one OCSF event from a single audit lifecycle record. The
// line is decoded per the session's writer encoding (bash `%q` so spaced values
// like the endpoints allowlist survive intact, or the AppleContainer percent
// form so backslash-bearing paths survive intact) and rejected fail-closed if it
// carries a duplicate key. The event, session_id, and timestamp keys drive
// classification and occurrence; the remaining decoded fields become the
// (redacted) unmapped object. Decode happens BEFORE redaction so the redactor
// masks the real value.
func auditEvent(line string, enc auditEncoding, redact func(string) string, loggedTime int64) (Event, error) {
	fields, err := decodeAuditLine(line, enc)
	if err != nil {
		return Event{}, err
	}
	lookup := make(map[string]string, len(fields))
	for _, f := range fields {
		lookup[f.key] = f.value
	}

	eventName := lookup["event"]
	activity := activityForEvent(eventName)

	severityID := severityFromExit(lookup["exit_status"])
	statusID := statusUnknown
	if activity == activityStop {
		statusID = statusFromExit(lookup["exit_status"])
	}

	epoch, dt := parseTime(firstNonEmpty(lookup["timestamp"], lookup["ts"]), redact)

	unmapped := newUnmapped()
	for _, f := range fields {
		switch f.key {
		case "event", "session_id", "timestamp", "ts":
			// consumed by classification / occurrence
			continue
		}
		unmapped.putStr("audit."+f.key, f.value, redact)
	}

	msgEvent := eventName
	if msgEvent == "" {
		msgEvent = "unknown"
	}

	return Event{
		CategoryUID:  categoryUID,
		CategoryName: categoryName,
		ClassUID:     classUID,
		ClassName:    className,
		ActivityID:   activity,
		ActivityName: activityName(activity),
		TypeUID:      typeUID(activity),
		TypeName:     className + ": " + activityName(activity),
		Time:         epoch,
		TimeDt:       dt,
		SeverityID:   severityID,
		Severity:     severityName(severityID),
		StatusID:     statusID,
		Status:       statusName(statusID),
		Message:      "workcell audit " + redact(msgEvent),
		Metadata:     meta(loggedTime),
		App:          auditApp(lookup, redact),
		Device:       auditDevice(lookup, redact),
		Unmapped:     unmapped.finish(),
	}, nil
}

// auditApp builds the App object from an audit record's session/agent fields.
func auditApp(fields map[string]string, redact func(string) string) *App {
	name := strings.TrimSpace(fields["agent"])
	if mode := strings.TrimSpace(fields["mode"]); mode != "" {
		if name != "" {
			name += "/" + mode
		} else {
			name = mode
		}
	}
	uid := strings.TrimSpace(fields["session_id"])
	if name == "" && uid == "" {
		return nil
	}
	return &App{Name: redact(name), UID: redact(uid)}
}

// auditDevice builds the Device object from an audit record's target fields.
func auditDevice(fields map[string]string, redact func(string) string) *Device {
	name := strings.TrimSpace(fields["target_id"])
	if name == "" && strings.TrimSpace(fields["target_kind"]) == "" {
		return nil
	}
	return &Device{
		TypeID: deviceTypeVirtualID,
		Type:   deviceTypeVirtual,
		Name:   redact(name),
		UID:    redact(name),
	}
}

// activityForEvent maps a Workcell audit event name to an OCSF activity_id.
// Start-shaped and stop-shaped lifecycle events map to Start/Stop; every other
// recognized lifecycle event (assurance-change, bootstrap_ready,
// workspace_materialized, …) maps to Other. An empty event name is Unknown.
func activityForEvent(event string) int {
	switch event {
	case "session_started", "launch":
		return activityStart
	case "session_finished", "exit":
		return activityStop
	case "":
		return activityUnknown
	default:
		return activityOther
	}
}

// typeUID follows the OCSF convention type_uid = class_uid*100 + activity_id.
func typeUID(activity int) int {
	return classUID*100 + activity
}

// severityFromExit returns Medium when a non-empty exit status is non-zero,
// otherwise Informational.
func severityFromExit(exitStatus string) int {
	s := strings.TrimSpace(exitStatus)
	if s != "" && s != "0" {
		return severityMedium
	}
	return severityInformational
}

// statusFromExit maps a terminal exit status to Success/Failure. An unset exit
// status on a terminal event is Success (a clean stop without a recorded code).
func statusFromExit(exitStatus string) int {
	s := strings.TrimSpace(exitStatus)
	if s == "" || s == "0" {
		return statusSuccess
	}
	return statusFailure
}

// parseTime parses an RFC3339 timestamp to epoch milliseconds and returns the
// redacted original string as time_dt. An unparseable value yields time 0 and
// preserves the (redacted) original for the consumer.
func parseTime(value string, redact func(string) string) (int64, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, ""
	}
	dt := redact(value)
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UnixMilli(), dt
	}
	return 0, dt
}

// firstNonEmpty returns the first trimmed-non-empty argument.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// unmapped accumulates the OCSF `unmapped` object, skipping empty values so the
// object only carries fields the record actually set.
type unmapped struct {
	m map[string]string
}

func newUnmapped() *unmapped { return &unmapped{m: make(map[string]string)} }

// put records a raw (already-safe) key/value, skipping empties.
func (u *unmapped) put(key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	u.m[key] = value
}

// putStr redacts value through the shared redactor before recording it.
func (u *unmapped) putStr(key, value string, redact func(string) string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	u.m[key] = redact(value)
}

// finish returns the accumulated map, or nil when empty so the omitempty tag
// drops the object entirely.
func (u *unmapped) finish() map[string]string {
	if len(u.m) == 0 {
		return nil
	}
	return u.m
}
