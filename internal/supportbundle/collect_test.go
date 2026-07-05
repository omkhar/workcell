// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"strings"
	"testing"
)

// The scenario tests below cover one failure class each (install, policy,
// target, provider, runtime) and assert the bundle carries the evidence a
// supporter needs to diagnose that class.

// Install failure: the launcher is absent. The bundle must flag it yet still
// carry host + repo context.
func TestScenarioInstallFailure(t *testing.T) {
	b := Collect(buildFixture(t, fixtureOptions{omitLauncher: true}))
	if b.Install.Available {
		t.Fatalf("install section should be unavailable when launcher missing")
	}
	if b.Install.LauncherPresent {
		t.Fatalf("launcher_present should be false")
	}
	if !hasGap(b.Install.Gaps, "launcher not found") {
		t.Fatalf("expected launcher gap, got %v", b.Install.Gaps)
	}
	if b.Install.HostOS != "darwin" || b.Install.HostArch != "arm64" {
		t.Fatalf("host os/arch not carried: %q/%q", b.Install.HostOS, b.Install.HostArch)
	}
	if !b.Install.RepoRootPresent {
		t.Fatalf("repo root context should still be carried")
	}
}

// Policy failure: the repo policy directory is missing. Section flags it.
func TestScenarioPolicyFailureMissingDir(t *testing.T) {
	b := Collect(buildFixture(t, fixtureOptions{omitPolicyDir: true}))
	if b.Policy.Available {
		t.Fatalf("policy section should be unavailable when policy dir missing")
	}
	if len(b.Policy.RepoPolicyFiles) != 0 {
		t.Fatalf("expected no policy files, got %v", b.Policy.RepoPolicyFiles)
	}
	if !hasGap(b.Policy.Gaps, "repo policy directory missing") {
		t.Fatalf("expected policy gap, got %v", b.Policy.Gaps)
	}
}

// Policy diagnosis: a custom user injection policy is present. The bundle
// records its presence and path (never its body).
func TestScenarioPolicyUserInjectionPresent(t *testing.T) {
	b := Collect(buildFixture(t, fixtureOptions{userInjection: true}))
	if !b.Policy.UserInjectionPolicyPresent {
		t.Fatalf("user injection policy presence not recorded")
	}
	if !strings.HasSuffix(b.Policy.UserInjectionPolicyPath, "injection-policy.toml") {
		t.Fatalf("injection policy path = %q", b.Policy.UserInjectionPolicyPath)
	}
	if !b.Policy.HostedControlsPresent {
		t.Fatalf("hosted controls presence not recorded")
	}
}

// Target failure: a colima profile directory exists but its config is missing
// (a broken/partial profile). The bundle surfaces the mismatch.
func TestScenarioTargetBrokenProfile(t *testing.T) {
	b := Collect(buildFixture(t, fixtureOptions{omitColimaConfig: true}))
	if len(b.Target.ColimaProfiles) != 1 {
		t.Fatalf("expected one profile, got %d", len(b.Target.ColimaProfiles))
	}
	p := b.Target.ColimaProfiles[0]
	if p.Name != "wcl-strict" {
		t.Fatalf("profile name = %q", p.Name)
	}
	if p.ColimaConfigPresent {
		t.Fatalf("colima_config_present should be false for a broken profile")
	}
	if !p.LimaDirPresent {
		t.Fatalf("lima_dir_present should be true")
	}
}

// Provider failure: adapter directories are missing for two providers.
func TestScenarioProviderMissingAdapters(t *testing.T) {
	b := Collect(buildFixture(t, fixtureOptions{omitAdapters: []string{"gemini", "antigravity"}}))
	present := map[string]bool{}
	for _, p := range b.Providers.Providers {
		present[p.ID] = p.AdapterDirPresent
	}
	if present["codex"] != true {
		t.Fatalf("codex adapter should be present")
	}
	if present["gemini"] != false {
		t.Fatalf("gemini adapter should be reported missing")
	}
	if present["antigravity"] != false {
		t.Fatalf("antigravity adapter should be reported missing")
	}
	// Supported tier is compiled-in and independent of adapter dir presence.
	for _, p := range b.Providers.Providers {
		if p.ID == "antigravity" && p.Supported {
			t.Fatalf("antigravity must not be a supported provider")
		}
		if p.ID == "codex" && !p.Supported {
			t.Fatalf("codex must be a supported provider")
		}
	}
}

// Runtime failure: a live session with a retained audit log. The bundle
// carries the live status and an audit pointer (path/size/mtime, no body).
func TestScenarioRuntimeLiveSession(t *testing.T) {
	b := Collect(buildFixture(t, fixtureOptions{
		sessionStatus:     "running",
		sessionLiveStatus: "running",
		writeAuditLog:     true,
	}))
	if b.Sessions.Total != 1 {
		t.Fatalf("expected one session, got %d", b.Sessions.Total)
	}
	s := b.Sessions.Sessions[0]
	if s.LiveStatus != "running" || s.Status != "running" {
		t.Fatalf("live status not carried: %+v", s)
	}
	if !s.AuditLogPresent {
		t.Fatalf("audit_log_present should be true")
	}
	if len(b.AuditPointers.Pointers) != 1 {
		t.Fatalf("expected one audit pointer, got %d", len(b.AuditPointers.Pointers))
	}
	ptr := b.AuditPointers.Pointers[0]
	if !ptr.Present || ptr.SizeBytes == 0 || ptr.ModifiedAt == "" {
		t.Fatalf("audit pointer metadata incomplete: %+v", ptr)
	}
}

// TestSectionsDegradeWithoutStateRoots proves collection never crashes when
// nothing is provisioned.
func TestSectionsDegradeWithoutStateRoots(t *testing.T) {
	b := Collect(Config{}) // entirely empty config
	if b.Sessions.Available {
		t.Fatalf("sessions should be unavailable")
	}
	if !hasGap(b.Sessions.Gaps, "no state roots") {
		t.Fatalf("expected sessions gap, got %v", b.Sessions.Gaps)
	}
	if b.AuditPointers.Available {
		t.Fatalf("audit pointers should be unavailable")
	}
	if b.Install.Version != "unknown" {
		t.Fatalf("version should be unknown, got %q", b.Install.Version)
	}
}

func hasGap(gaps []string, substr string) bool {
	for _, g := range gaps {
		if strings.Contains(g, substr) {
			return true
		}
	}
	return false
}
