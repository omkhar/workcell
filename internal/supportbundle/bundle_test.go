// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/host/sessions"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata golden files")

// fixedNow is the injected bundle timestamp; pinning it keeps golden output
// stable across runs.
var fixedNow = time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)

// fixedAuditMTime is the audit-log modification time set on fixture files so
// the collected modified_at field is deterministic.
var fixedAuditMTime = time.Date(2026, 7, 4, 9, 30, 0, 0, time.UTC)

// buildFixture materializes a complete host layout under a temp home so every
// absolute path redacts to a stable ~-relative string. It returns a Config
// pointing at the fixture. Options let scenario tests perturb one class.
type fixtureOptions struct {
	omitLauncher      bool
	omitPolicyDir     bool
	omitColimaConfig  bool     // create the profile dir without colima.yaml
	omitAdapters      []string // adapter dirs to skip creating
	userInjection     bool
	sessionStatus     string // when "", no session record is written
	sessionLiveStatus string
	sessionWorkspace  string // overrides the session workspace field
	sessionProfile    string // overrides the session profile field
	sessionID         string // overrides the session ID field
	writeAuditLog     bool
}

func firstNonEmpty(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

func buildFixture(t *testing.T, opts fixtureOptions) Config {
	t.Helper()
	home := t.TempDir()
	repoRoot := filepath.Join(home, "src", "workcell")
	mustMkdir(t, repoRoot)

	// Install artifacts.
	launcher := filepath.Join(repoRoot, "scripts", "workcell")
	if !opts.omitLauncher {
		mustWrite(t, launcher, "#!/bin/bash\n")
	}
	mustWrite(t, filepath.Join(repoRoot, "CHANGELOG.md"),
		"# Changelog\n\n## Unreleased\n\n## v0.11.2 - 2026-06-15\n\n## v0.11.1 - 2026-06-15\n")

	// Policy inventory.
	if !opts.omitPolicyDir {
		policyDir := filepath.Join(repoRoot, "policy")
		mustWrite(t, filepath.Join(policyDir, "allowed-actions.toml"), "x = 1\n")
		mustWrite(t, filepath.Join(policyDir, "github-hosted-controls.toml"), "x = 1\n")
		mustWrite(t, filepath.Join(policyDir, "requirements.toml"), "version = 1\n")
		mustWrite(t, filepath.Join(policyDir, "host-support-matrix.tsv"), "os\tarch\n")
		mustWrite(t, filepath.Join(policyDir, "notes.md"), "ignored\n") // non-policy ext, skipped
	}

	// Adapter directories.
	skip := map[string]bool{}
	for _, name := range opts.omitAdapters {
		skip[name] = true
	}
	for _, name := range []string{"claude", "codex", "copilot", "gemini", "antigravity"} {
		if skip[name] {
			continue
		}
		mustMkdir(t, filepath.Join(repoRoot, "adapters", name))
	}

	// User injection policy (presence only).
	if opts.userInjection {
		mustWrite(t, filepath.Join(home, ".config", "workcell", "injection-policy.toml"),
			"version = 1\n# token=ghp_"+"0123456789abcdefghijklmnopqrstuvwx should never leak\n")
	}

	// State roots.
	workcellStateRoot := filepath.Join(home, ".local", "state", "workcell")
	mustMkdir(t, workcellStateRoot)
	colimaStateRoot := filepath.Join(home, ".colima")
	profileDir := filepath.Join(colimaStateRoot, "wcl-strict")
	mustMkdir(t, profileDir)
	if !opts.omitColimaConfig {
		mustWrite(t, filepath.Join(profileDir, "colima.yaml"), "cpu: 4\n")
	}
	mustMkdir(t, filepath.Join(colimaStateRoot, "_lima", "colima-wcl-strict"))
	mustMkdir(t, filepath.Join(colimaStateRoot, "_disks")) // underscore-prefixed, skipped

	cfg := Config{
		RepoRoot:          repoRoot,
		LauncherPath:      launcher,
		RealHome:          home,
		WorkcellStateRoot: workcellStateRoot,
		ColimaStateRoot:   colimaStateRoot,
		HostOS:            "darwin",
		HostArch:          "arm64",
		Now:               fixedNow,
	}

	if opts.sessionStatus != "" {
		auditLog := filepath.Join(profileDir, "workcell.audit.log")
		workspace := repoRoot
		if opts.sessionWorkspace != "" {
			workspace = opts.sessionWorkspace
		}
		rec := sessions.SessionRecord{
			Version:              1,
			SessionID:            firstNonEmpty(opts.sessionID, "sess-abc123"),
			Profile:              firstNonEmpty(opts.sessionProfile, "wcl-strict"),
			TargetKind:           "vm",
			TargetProvider:       "colima",
			TargetID:             "default",
			TargetAssuranceClass: "strict",
			RuntimeAPI:           "docker",
			WorkspaceTransport:   "direct",
			Agent:                "codex",
			Mode:                 "strict",
			Status:               opts.sessionStatus,
			LiveStatus:           opts.sessionLiveStatus,
			Workspace:            workspace,
			AuditLogPath:         auditLog,
			StartedAt:            "2026-07-04T09:00:00Z",
		}
		writeSessionRecord(t, profileDir, "sess-abc123.json", rec)
		if opts.writeAuditLog {
			mustWrite(t, auditLog, "AUDIT RECORD BODY token=ghp_"+"0123456789abcdefghijklmnopqrstuvwx\n")
			if err := os.Chtimes(auditLog, fixedAuditMTime, fixedAuditMTime); err != nil {
				t.Fatalf("chtimes audit log: %v", err)
			}
		}
	}

	return cfg
}

func writeSessionRecord(t *testing.T, profileDir, name string, rec sessions.SessionRecord) {
	t.Helper()
	sessionsDir := filepath.Join(profileDir, "sessions")
	mustMkdir(t, sessionsDir)
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal session record: %v", err)
	}
	mustWrite(t, filepath.Join(sessionsDir, name), string(data))
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestGoldenBundleShape pins the full deterministic bundle shape. Because the
// fixture lives under a temp home and every path is redacted to ~-relative,
// the output is byte-stable across machines. Regenerate with:
//
//	go test ./internal/supportbundle -run TestGoldenBundleShape -update-golden
func TestGoldenBundleShape(t *testing.T) {
	cfg := buildFixture(t, fixtureOptions{
		userInjection:     true,
		sessionStatus:     "running",
		sessionLiveStatus: "running",
		writeAuditLog:     true,
	})

	got, err := Collect(cfg).JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden-bundle.json")
	if *updateGolden {
		mustWrite(t, goldenPath, string(got))
		t.Logf("updated %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update-golden to create)", err)
	}
	if string(got) != string(want) {
		t.Fatalf("bundle shape drift.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBundleDeterministic proves repeated collection is byte-identical.
func TestBundleDeterministic(t *testing.T) {
	cfg := buildFixture(t, fixtureOptions{sessionStatus: "exited", writeAuditLog: true})
	first, err := Collect(cfg).JSON()
	if err != nil {
		t.Fatalf("first JSON: %v", err)
	}
	second, err := Collect(cfg).JSON()
	if err != nil {
		t.Fatalf("second JSON: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("collection not deterministic")
	}
}

// TestBundleSchemaAndRedactionMetadata asserts the self-describing header.
func TestBundleSchemaAndRedactionMetadata(t *testing.T) {
	b := Collect(buildFixture(t, fixtureOptions{}))
	if b.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %q", b.SchemaVersion)
	}
	if b.Tool != toolName {
		t.Fatalf("tool = %q", b.Tool)
	}
	if b.GeneratedAt != "2026-07-05T12:00:00Z" {
		t.Fatalf("generated_at = %q", b.GeneratedAt)
	}
	if b.Redaction.PolicyVersion != RedactionPolicyVersion {
		t.Fatalf("redaction policy version = %q", b.Redaction.PolicyVersion)
	}
	if len(b.Redaction.Rules) != len(redactionRules) {
		t.Fatalf("redaction rules count = %d", len(b.Redaction.Rules))
	}
}

// TestBundleZeroNow omits generated_at when Now is unset.
func TestBundleZeroNow(t *testing.T) {
	cfg := buildFixture(t, fixtureOptions{})
	cfg.Now = time.Time{}
	if b := Collect(cfg); b.GeneratedAt != "" {
		t.Fatalf("generated_at = %q, want empty", b.GeneratedAt)
	}
}
