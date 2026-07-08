// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessions

import (
	"bytes"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
)

// Property-based coverage for the session-record lifecycle. The example-based
// tests in sessions_test.go pin individual transitions; these tests generate
// randomized lifecycles (start -> active churn -> terminal -> post-terminal
// retries, including tampered inputs) and assert the invariants the C1
// hardening established hold for every generated interleaving:
//
//   - Idempotency / byte-identity: re-encoding an already-encoded record with no
//     updates reproduces the exact same bytes (Inv B).
//   - No unusable record: any bytes EncodeSessionRecordFrom returns decode
//     cleanly through DecodeSessionRecord (Inv A).
//   - Terminal-status monotonicity: once a record is terminal, an update that
//     drives status back to a non-terminal value is refused (Inv C).
//   - Round-trip fidelity: decode then re-encode is a fixed point (Inv D).
//   - Newline rejection: injecting CR/LF into any field is always refused, so a
//     record can never smuggle a forged audit line (Inv E).
//   - Normalization idempotency: normalize(normalize(x)) == normalize(x) (Inv F).
//
// All properties run against a fixed PRNG seed so the explored inputs are
// identical on every run (reproducible, non-flaky by construction). The seed is
// deliberately not derived from the clock.
const lifecyclePropertySeed = 0x5715ec70

// propertyChecks bounds each quick.Check run. 800 lifecycles per property is
// enough to interleave every start/active/terminal/retry combination the
// generator produces while keeping the lane fast.
const propertyChecks = 800

var (
	activeStatuses   = []string{"starting", "running", "stopping"}
	terminalStatuses = []string{"exited", "failed", "aborted"}
)

func quickConfig() *quick.Config {
	return &quick.Config{
		MaxCount: propertyChecks,
		Rand:     rand.New(rand.NewSource(lifecyclePropertySeed)),
	}
}

// randToken returns a non-empty identifier-like string with no CR/LF, matching
// the shape of real session-record values (ids, profiles, paths).
func randToken(rnd *rand.Rand) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./"
	n := 1 + rnd.Intn(12)
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[rnd.Intn(len(alphabet))]
	}
	return string(b)
}

func pick(rnd *rand.Rand, options []string) string {
	return options[rnd.Intn(len(options))]
}

// startUpdates builds a valid initial (active) session record update map with
// the required fields plus a random selection of safe optional fields.
func startUpdates(rnd *rand.Rand) map[string]string {
	updates := map[string]string{
		"session_id":     randToken(rnd),
		"profile":        randToken(rnd),
		"agent":          randToken(rnd),
		"mode":           randToken(rnd),
		"status":         pick(rnd, activeStatuses),
		"workspace":      "/" + randToken(rnd),
		"started_at":     "2026-04-08T12:00:00Z",
		"ui":             randToken(rnd),
		"execution_path": randToken(rnd),
	}
	if rnd.Intn(2) == 0 {
		updates["live_status"] = randToken(rnd)
	}
	if rnd.Intn(2) == 0 {
		updates["current_assurance"] = randToken(rnd)
	}
	if rnd.Intn(3) == 0 {
		// A valid detached marker: monitor_pid requires session_audit_dir.
		updates["monitor_pid"] = "12345"
		updates["session_audit_dir"] = "/" + randToken(rnd)
	}
	return updates
}

// activeUpdates builds a mid-lifecycle update that keeps the session active.
func activeUpdates(rnd *rand.Rand) map[string]string {
	updates := map[string]string{
		"status":      pick(rnd, activeStatuses),
		"observed_at": "2026-04-08T12:00:30Z",
	}
	if rnd.Intn(2) == 0 {
		updates["live_status"] = randToken(rnd)
	}
	return updates
}

// regressionUpdates drives a record to an active status while clearing the
// completion fields. Against an active record it is a valid update; against a
// terminal record only the monotonicity guard rejects it.
func regressionUpdates(rnd *rand.Rand) map[string]string {
	return map[string]string{
		"status":          pick(rnd, activeStatuses),
		"observed_at":     "2026-04-08T12:00:45Z",
		"finished_at":     "",
		"exit_status":     "",
		"final_assurance": "",
	}
}

// terminalUpdates builds a valid completion update: a terminal status must set
// finished_at, exit_status, and final_assurance.
func terminalUpdates(rnd *rand.Rand) map[string]string {
	return map[string]string{
		"status":          pick(rnd, terminalStatuses),
		"finished_at":     "2026-04-08T12:05:00Z",
		"exit_status":     randToken(rnd),
		"final_assurance": randToken(rnd),
		"live_status":     randToken(rnd),
	}
}

// opOutcome classifies whether an update is expected to be accepted or rejected
// by the encoder, so the property can assert the exact direction for each step
// instead of tolerating any error.
type opOutcome int

const (
	// mustSucceed marks an update that is a valid transition from the current
	// record; the encoder MUST accept it. An error here is a property failure.
	mustSucceed opOutcome = iota
	// mustFailMonotonic marks a terminal->non-terminal regression; the
	// terminal-status monotonicity guard MUST reject it.
	mustFailMonotonic
)

// lifecycleOp is one update plus its expected outcome and a label for
// diagnostics.
type lifecycleOp struct {
	updates map[string]string
	outcome opOutcome
	label   string
}

// sessionLifecycle is a generated sequence of ops applied in order to a single
// record, modelling a realistic run plus adversarial retries. Each op carries
// its expected outcome so the property can require success on the valid steps
// and rejection on the regression steps.
type sessionLifecycle struct {
	ops []lifecycleOp
}

// Generate implements quick.Generator. Every lifecycle starts with a valid
// start op, applies zero or more active churn ops, may complete with a terminal
// op, and may append post-terminal retry ops. The valid transitions (start,
// churn, terminal, benign terminal retry) are tagged mustSucceed; the terminal
// ->active regressions are tagged mustFailMonotonic. Because a regression op is
// only ever emitted after a successful terminal op, the record is always
// terminal when one runs, so the monotonicity guard is the sole rejecter.
func (sessionLifecycle) Generate(rnd *rand.Rand, _ int) reflect.Value {
	ops := []lifecycleOp{{updates: startUpdates(rnd), outcome: mustSucceed, label: "start"}}

	for churn := rnd.Intn(4); churn > 0; churn-- {
		ops = append(ops, lifecycleOp{updates: activeUpdates(rnd), outcome: mustSucceed, label: "active-churn"})
	}

	if rnd.Intn(4) != 0 {
		ops = append(ops, lifecycleOp{updates: terminalUpdates(rnd), outcome: mustSucceed, label: "terminal"})
		for retry := rnd.Intn(3); retry > 0; retry-- {
			if rnd.Intn(2) == 0 {
				// Adversarial: regress a terminal record to active. The
				// completion fields are cleared so the record would otherwise
				// pass validation, leaving the terminal-status monotonicity
				// guard as the only thing that rejects the transition.
				ops = append(ops, lifecycleOp{updates: regressionUpdates(rnd), outcome: mustFailMonotonic, label: "terminal->active regression"})
			} else {
				// Benign: a second terminal update.
				ops = append(ops, lifecycleOp{updates: terminalUpdates(rnd), outcome: mustSucceed, label: "terminal retry"})
			}
		}
	}

	return reflect.ValueOf(sessionLifecycle{ops: ops})
}

// TestLifecyclePropertyInvariants applies each generated lifecycle step by step
// through the pure encode/decode path and asserts Inv A/B/C/D. current holds the
// last successfully encoded bytes; a rejected step must not advance it. Every
// step has a known expected direction: mustSucceed steps REQUIRE the encoder to
// accept (an error there fails the property rather than being skipped), and
// mustFailMonotonic steps REQUIRE rejection.
func TestLifecyclePropertyInvariants(t *testing.T) {
	t.Parallel()

	property := func(lc sessionLifecycle) bool {
		var current []byte
		for i, op := range lc.ops {
			next, err := EncodeSessionRecordFrom(current, op.updates)

			if op.outcome == mustFailMonotonic {
				// Inv C: a terminal record must reject regression to a
				// non-terminal status. current is unchanged on rejection.
				if err == nil {
					t.Errorf("step %d (%s): terminal-status regression was accepted", i, op.label)
					return false
				}
				continue
			}

			// mustSucceed: a valid transition MUST be accepted. An unexpected
			// error here is a property failure, not a silent skip.
			if err != nil {
				t.Errorf("step %d (%s): valid update was rejected: %v", i, op.label, err)
				return false
			}

			// Inv A: encoded bytes always decode cleanly.
			decoded, decErr := DecodeSessionRecord(next, "next")
			if decErr != nil {
				t.Errorf("step %d (%s): encoded record did not decode: %v", i, op.label, decErr)
				return false
			}

			// Inv B: re-encoding with no updates reproduces identical bytes.
			reencoded, reErr := EncodeSessionRecordFrom(next, map[string]string{})
			if reErr != nil {
				t.Errorf("step %d (%s): re-encoding a valid record errored: %v", i, op.label, reErr)
				return false
			}
			if !bytes.Equal(next, reencoded) {
				t.Errorf("step %d (%s): re-encode not byte-identical:\n%s\n---\n%s", i, op.label, next, reencoded)
				return false
			}

			// Inv D: a valid record MUST re-encode from its own decoded fields,
			// byte-identically. A re-encode error means the record's own fields
			// do not satisfy the encoder, which is a real bug, not a skip.
			roundTrip, rtErr := EncodeSessionRecordFrom(nil, recordToUpdates(decoded))
			if rtErr != nil {
				t.Errorf("step %d (%s): decoded record failed to re-encode: %v", i, op.label, rtErr)
				return false
			}
			if !bytes.Equal(next, roundTrip) {
				t.Errorf("step %d (%s): decode->encode not a fixed point:\n%s\n---\n%s", i, op.label, next, roundTrip)
				return false
			}

			current = next
		}
		return true
	}

	if err := quick.Check(property, quickConfig()); err != nil {
		t.Fatalf("lifecycle invariants property failed: %v", err)
	}
}

// newlineBaseUpdates is a fully populated, valid terminal session record: every
// string field carries a distinct newline-free value, so the record encodes
// cleanly and any single tampered field is the sole cause of a rejection. Keys
// are the JSON field names EncodeSessionRecordFrom accepts. The set is kept in
// lockstep with SessionRecord's string fields by the reflection check in
// TestNewlineRejectionEveryField, so a newly added field forces a matching entry
// here (or the test fails loudly).
var newlineBaseUpdates = map[string]string{
	"session_id":              "sess-1",
	"profile":                 "prof-1",
	"target_kind":             "local_vm",
	"target_provider":         "colima",
	"target_id":               "prof-1",
	"target_assurance_class":  "strict",
	"runtime_api":             "docker",
	"workspace_transport":     "workspace-mount",
	"agent":                   "codex",
	"mode":                    "strict",
	"status":                  "exited",
	"ui":                      "cli",
	"execution_path":          "managed-tier1",
	"workspace":               "/ws",
	"workspace_origin":        "/ws",
	"workspace_root":          "/",
	"worktree_path":           "/ws",
	"git_branch":              "main",
	"git_head":                "abc123",
	"git_base":                "def456",
	"container_name":          "wcl-1",
	"monitor_pid":             "4242",
	"live_status":             "stopped",
	"session_audit_dir":       "/audit",
	"audit_log_path":          "/audit/a.log",
	"debug_log_path":          "/audit/d.log",
	"file_trace_log_path":     "/audit/f.log",
	"transcript_log_path":     "/audit/t.log",
	"started_at":              "2026-04-08T12:00:00Z",
	"observed_at":             "2026-04-08T12:00:30Z",
	"finished_at":             "2026-04-08T12:05:00Z",
	"exit_status":             "0",
	"initial_assurance":       "managed-mutable",
	"current_assurance":       "managed-mutable",
	"final_assurance":         "managed-mutable",
	"workspace_control_plane": "masked",
	"workspace_repo_mcp":      "denied",
	"bootstrap_id":            "boot-1",
	"image_ref":               "img@sha256:aaa",
}

// sessionRecordStringFieldTags returns the JSON name of every string field on
// SessionRecord, derived by reflection so the newline test's field list can be
// pinned to the actual struct rather than a hand-maintained copy that could
// drift.
func sessionRecordStringFieldTags() []string {
	typ := reflect.TypeOf(SessionRecord{})
	tags := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Type.Kind() != reflect.String {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		tags = append(tags, name)
	}
	return tags
}

// TestNewlineRejectionEveryField asserts Inv E exhaustively: injecting a CR, LF,
// or CRLF into ANY string field of an otherwise-valid record is refused, so no
// field can smuggle a forged audit line. Unlike a random field pick, this
// enumerates every field the validator newline-checks, and a reflection guard
// fails loudly if SessionRecord grows a string field the base map does not
// cover (or lists one that no longer exists).
func TestNewlineRejectionEveryField(t *testing.T) {
	t.Parallel()

	// Drift guard: the base map's keys must be exactly SessionRecord's string
	// fields, so every newline-checked field is exercised and none is stale.
	structTags := sessionRecordStringFieldTags()
	structSet := make(map[string]struct{}, len(structTags))
	for _, tag := range structTags {
		structSet[tag] = struct{}{}
		if _, ok := newlineBaseUpdates[tag]; !ok {
			t.Fatalf("newlineBaseUpdates is missing string field %q (SessionRecord drifted)", tag)
		}
	}
	for key := range newlineBaseUpdates {
		if _, ok := structSet[key]; !ok {
			t.Fatalf("newlineBaseUpdates has stale field %q not on SessionRecord", key)
		}
	}

	// Sanity: the untampered base must encode cleanly, so a later rejection is
	// attributable solely to the injected newline.
	if _, err := EncodeSessionRecordFrom(nil, newlineBaseUpdates); err != nil {
		t.Fatalf("valid base record was rejected: %v", err)
	}

	injections := map[string]string{
		"LF":            "\n",
		"CR":            "\r",
		"CRLF":          "\r\n",
		"forged-record": "x\nsession_id=forged",
	}

	for _, field := range structTags {
		for label, injection := range injections {
			t.Run(field+"/"+label, func(t *testing.T) {
				t.Parallel()
				tampered := make(map[string]string, len(newlineBaseUpdates))
				for k, v := range newlineBaseUpdates {
					tampered[k] = v
				}
				tampered[field] = tampered[field] + injection
				if _, err := EncodeSessionRecordFrom(nil, tampered); err == nil {
					t.Errorf("newline injection (%s) into field %q was accepted", label, field)
				}
			})
		}
	}
}

// TestNormalizeIdempotent asserts Inv F: normalization is a fixed point, which
// is what makes the encode path byte-stable across repeated writes.
func TestNormalizeIdempotent(t *testing.T) {
	t.Parallel()

	property := func(lc sessionLifecycle) bool {
		rnd := rand.New(rand.NewSource(int64(len(lc.ops))))
		record := SessionRecord{
			Version:    1,
			SessionID:  randToken(rnd),
			Profile:    randToken(rnd),
			Agent:      randToken(rnd),
			Mode:       randToken(rnd),
			Status:     pick(rnd, activeStatuses),
			Workspace:  "/" + randToken(rnd),
			StartedAt:  "2026-04-08T12:00:00Z",
			TargetKind: maybeToken(rnd),
			RuntimeAPI: maybeToken(rnd),
		}
		once := normalizeSessionRecord(record)
		twice := normalizeSessionRecord(once)
		if !reflect.DeepEqual(once, twice) {
			t.Errorf("normalize not idempotent:\n%+v\n---\n%+v", once, twice)
			return false
		}
		return true
	}

	if err := quick.Check(property, quickConfig()); err != nil {
		t.Fatalf("normalize-idempotency property failed: %v", err)
	}
}

// maybeToken returns a random token half the time and "" the rest, so
// normalization's defaulting branch is exercised in both directions.
func maybeToken(rnd *rand.Rand) string {
	if rnd.Intn(2) == 0 {
		return ""
	}
	return randToken(rnd)
}

// recordToUpdates flattens a decoded record back into the update map the encode
// path accepts, so Inv D can re-encode it from scratch. Every string field is
// emitted (empty ones omitted so normalization re-derives the defaulted ones),
// so the round-trip is a fixed point regardless of which fields a lifecycle
// populates.
func recordToUpdates(r SessionRecord) map[string]string {
	updates := map[string]string{}
	add := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			updates[key] = value
		}
	}
	add("session_id", r.SessionID)
	add("profile", r.Profile)
	add("target_kind", r.TargetKind)
	add("target_provider", r.TargetProvider)
	add("target_id", r.TargetID)
	add("target_assurance_class", r.TargetAssuranceClass)
	add("runtime_api", r.RuntimeAPI)
	add("workspace_transport", r.WorkspaceTransport)
	add("agent", r.Agent)
	add("mode", r.Mode)
	add("status", r.Status)
	add("ui", r.UI)
	add("execution_path", r.ExecutionPath)
	add("workspace", r.Workspace)
	add("workspace_origin", r.WorkspaceOrigin)
	add("workspace_root", r.WorkspaceRoot)
	add("worktree_path", r.WorktreePath)
	add("git_branch", r.GitBranch)
	add("git_head", r.GitHead)
	add("git_base", r.GitBase)
	add("container_name", r.ContainerName)
	add("live_status", r.LiveStatus)
	add("current_assurance", r.CurrentAssurance)
	add("monitor_pid", r.MonitorPID)
	add("session_audit_dir", r.SessionAuditDir)
	add("audit_log_path", r.AuditLogPath)
	add("debug_log_path", r.DebugLogPath)
	add("file_trace_log_path", r.FileTraceLogPath)
	add("transcript_log_path", r.TranscriptLogPath)
	add("started_at", r.StartedAt)
	add("observed_at", r.ObservedAt)
	add("finished_at", r.FinishedAt)
	add("exit_status", r.ExitStatus)
	add("initial_assurance", r.InitialAssurance)
	add("final_assurance", r.FinalAssurance)
	add("workspace_control_plane", r.WorkspaceControlPlane)
	add("workspace_repo_mcp", r.WorkspaceRepoMcp)
	add("bootstrap_id", r.BootstrapID)
	add("image_ref", r.ImageRef)
	return updates
}
