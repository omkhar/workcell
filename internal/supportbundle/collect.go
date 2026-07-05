// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/providerid"
)

// maxSessionSummaries caps how many session summaries a bundle carries so a
// long-lived host cannot balloon the document; Truncated records the overflow.
const maxSessionSummaries = 50

// providerOrder is the stable provider iteration order: the supported adapters
// followed by the planned antigravity scaffold.
var providerOrder = []string{
	providerid.Claude,
	providerid.Codex,
	providerid.Copilot,
	providerid.Gemini,
	providerid.Antigravity,
}

// changelogVersionRe matches a released `## vX.Y.Z ...` CHANGELOG heading.
var changelogVersionRe = regexp.MustCompile(`^##\s+(v[0-9]+\.[0-9]+\.[0-9]+)`)

// InstallSection captures whether Workcell is installed and on what host.
type InstallSection struct {
	Section
	LauncherPath    string `json:"launcher_path"`
	LauncherPresent bool   `json:"launcher_present"`
	RepoRoot        string `json:"repo_root"`
	RepoRootPresent bool   `json:"repo_root_present"`
	Version         string `json:"version"`
	HostOS          string `json:"host_os"`
	HostArch        string `json:"host_arch"`
}

func collectInstall(cfg Config, r Redactor, hostOS, hostArch string) InstallSection {
	s := InstallSection{
		LauncherPath:    r.String(cfg.LauncherPath),
		LauncherPresent: fileExists(cfg.LauncherPath),
		RepoRoot:        r.String(cfg.RepoRoot),
		RepoRootPresent: dirExists(cfg.RepoRoot),
		Version:         changelogVersion(cfg.RepoRoot),
		HostOS:          hostOS,
		HostArch:        hostArch,
	}
	s.Available = s.LauncherPresent
	if !s.LauncherPresent {
		s.note("launcher not found at reported path; install may be incomplete")
	}
	if !s.RepoRootPresent {
		s.note("repo root not found; version could not be resolved")
	}
	return s
}

func changelogVersion(repoRoot string) string {
	if repoRoot == "" {
		return "unknown"
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, "CHANGELOG.md"))
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if m := changelogVersionRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return "unknown"
}

// PolicySection inventories repo policy artifacts and the user injection policy.
// It records presence only; policy files are listed by name and the user
// injection policy body is never read (it may reference credential sources).
type PolicySection struct {
	Section
	RepoPolicyDir              string   `json:"repo_policy_dir"`
	RepoPolicyFiles            []string `json:"repo_policy_files"`
	UserInjectionPolicyPath    string   `json:"user_injection_policy_path"`
	UserInjectionPolicyPresent bool     `json:"user_injection_policy_present"`
	HostedControlsPresent      bool     `json:"hosted_controls_present"`
}

func collectPolicy(cfg Config, r Redactor) PolicySection {
	policyDir := ""
	if cfg.RepoRoot != "" {
		policyDir = filepath.Join(cfg.RepoRoot, "policy")
	}
	userPolicy := ""
	if cfg.RealHome != "" {
		userPolicy = filepath.Join(cfg.RealHome, ".config", "workcell", "injection-policy.toml")
	}

	s := PolicySection{
		RepoPolicyDir:              r.String(policyDir),
		RepoPolicyFiles:            listPolicyFiles(policyDir),
		UserInjectionPolicyPath:    r.String(userPolicy),
		UserInjectionPolicyPresent: userPolicy != "" && fileExists(userPolicy),
		HostedControlsPresent:      policyDir != "" && fileExists(filepath.Join(policyDir, "github-hosted-controls.toml")),
	}
	s.Available = len(s.RepoPolicyFiles) > 0
	if !s.Available {
		s.note("repo policy directory missing or empty")
	}
	return s
}

func listPolicyFiles(policyDir string) []string {
	if policyDir == "" {
		return nil
	}
	entries, err := os.ReadDir(policyDir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".toml" || ext == ".json" || ext == ".tsv" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// TargetProfile is one colima profile's filesystem-observable state.
type TargetProfile struct {
	Name                string `json:"name"`
	Dir                 string `json:"dir"`
	ColimaConfigPresent bool   `json:"colima_config_present"`
	LimaDirPresent      bool   `json:"lima_dir_present"`
}

// TargetSection captures runtime-target state that is observable on the host
// filesystem. It deliberately does NOT invoke colima/lima so the bundle stays
// deterministic and side-effect free; live VM status is a documented gap.
type TargetSection struct {
	Section
	WorkcellStateRoot        string          `json:"workcell_state_root"`
	WorkcellStateRootPresent bool            `json:"workcell_state_root_present"`
	ColimaStateRoot          string          `json:"colima_state_root"`
	ColimaStateRootPresent   bool            `json:"colima_state_root_present"`
	ColimaProfiles           []TargetProfile `json:"colima_profiles"`
}

func collectTarget(cfg Config, r Redactor) TargetSection {
	s := TargetSection{
		WorkcellStateRoot:        r.String(cfg.WorkcellStateRoot),
		WorkcellStateRootPresent: dirExists(cfg.WorkcellStateRoot),
		ColimaStateRoot:          r.String(cfg.ColimaStateRoot),
		ColimaStateRootPresent:   dirExists(cfg.ColimaStateRoot),
	}
	s.ColimaProfiles = collectColimaProfiles(cfg, r, &s.Section)
	s.Available = s.WorkcellStateRootPresent || s.ColimaStateRootPresent
	if !s.Available {
		s.note("no state roots present on host; target has not been provisioned")
	}
	s.note("live colima/VM status is not collected; run `workcell --doctor` for live target state")
	return s
}

func collectColimaProfiles(cfg Config, r Redactor, sec *Section) []TargetProfile {
	root := cfg.ColimaStateRoot
	if !dirExists(root) {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		sec.note(fmt.Sprintf("colima state root unreadable: %v", r.String(err.Error())))
		return nil
	}
	var profiles []TargetProfile
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		profile := e.Name()
		configPath, cfgErr := hoststate.ProfileColimaConfigPath(root, profile)
		limaDir, limaErr := hoststate.ProfileLimaDir(root, profile)
		if cfgErr != nil || limaErr != nil {
			// A directory whose name fails profile validation is not a
			// managed profile; record it as a bare entry without probing.
			profiles = append(profiles, TargetProfile{Name: r.String(profile), Dir: r.String(filepath.Join(root, profile))})
			continue
		}
		profiles = append(profiles, TargetProfile{
			Name:                r.String(profile),
			Dir:                 r.String(filepath.Join(root, profile)),
			ColimaConfigPresent: fileExists(configPath),
			LimaDirPresent:      dirExists(limaDir),
		})
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles
}

// ProviderSummary is one provider's compiled-in facts plus adapter presence.
type ProviderSummary struct {
	ID                string   `json:"id"`
	Supported         bool     `json:"supported"`
	AdapterDirPresent bool     `json:"adapter_dir_present"`
	CredentialKeys    []string `json:"credential_keys"`
}

// ProvidersSection summarizes each provider's bootstrap surface. Credential
// KEY NAMES are non-secret and useful for diagnosis; credential VALUES are
// never touched.
type ProvidersSection struct {
	Section
	Providers []ProviderSummary `json:"providers"`
}

func collectProviders(cfg Config, _ Redactor) ProvidersSection {
	scoped := adapters.AgentScopedCredentialKeys()
	s := ProvidersSection{}
	for _, id := range providerOrder {
		adapterDir := ""
		if cfg.RepoRoot != "" {
			adapterDir = filepath.Join(cfg.RepoRoot, "adapters", id)
		}
		var keys []string
		for key := range scoped[id] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		s.Providers = append(s.Providers, ProviderSummary{
			ID:                id,
			Supported:         providerid.IsValid(id),
			AdapterDirPresent: adapterDir != "" && dirExists(adapterDir),
			CredentialKeys:    keys,
		})
	}
	s.Available = len(s.Providers) > 0
	return s
}

// SessionSummary is the redacted, body-free view of one durable session record.
type SessionSummary struct {
	SessionID       string `json:"session_id"`
	Status          string `json:"status"`
	LiveStatus      string `json:"live_status"`
	Profile         string `json:"profile"`
	TargetKind      string `json:"target_kind"`
	TargetProvider  string `json:"target_provider"`
	Agent           string `json:"agent"`
	Mode            string `json:"mode"`
	StartedAt       string `json:"started_at"`
	Workspace       string `json:"workspace"`
	AuditLogPresent bool   `json:"audit_log_present"`
}

// StatusCount is a session count for one status value.
type StatusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// SessionsSection summarizes durable session records. It never emits workspace
// bodies, transcripts, or audit content — only enumerated metadata fields.
type SessionsSection struct {
	Section
	Total        int              `json:"total"`
	StatusCounts []StatusCount    `json:"status_counts"`
	Sessions     []SessionSummary `json:"sessions"`
	Truncated    bool             `json:"truncated"`
}

func collectSessions(cfg Config, r Redactor) SessionsSection {
	s := SessionsSection{}
	roots := stateRoots(cfg)
	if len(roots) == 0 {
		s.note("no state roots configured; session metadata unavailable")
		return s
	}
	records, err := sessions.ListSessionRecordsInRoots(roots, sessions.SessionListOptions{})
	if err != nil {
		s.note(fmt.Sprintf("session enumeration failed: %v", r.String(err.Error())))
		return s
	}
	s.Available = true
	s.Total = len(records)

	counts := map[string]int{}
	for _, rec := range records {
		status := rec.Status
		if status == "" {
			status = "unknown"
		}
		counts[status]++
	}
	for status, count := range counts {
		s.StatusCounts = append(s.StatusCounts, StatusCount{Status: r.String(status), Count: count})
	}
	sort.Slice(s.StatusCounts, func(i, j int) bool { return s.StatusCounts[i].Status < s.StatusCounts[j].Status })

	// Newest-first: session IDs are timestamp-prefixed, so descending order keeps
	// the most recent sessions (the ones most likely under investigation) when
	// truncating, and is deterministic for the golden shape.
	sort.Slice(records, func(i, j int) bool { return records[i].SessionID > records[j].SessionID })
	limit := len(records)
	if limit > maxSessionSummaries {
		limit = maxSessionSummaries
		s.Truncated = true
		s.note(fmt.Sprintf("session summaries truncated to %d of %d records", maxSessionSummaries, len(records)))
	}
	for _, rec := range records[:limit] {
		// Every string field goes through the redactor: validateSessionRecord
		// only rejects newlines, so a hand-edited/malformed durable record could
		// carry token-shaped text in any of these fields.
		s.Sessions = append(s.Sessions, SessionSummary{
			SessionID:       r.String(rec.SessionID),
			Status:          r.String(rec.Status),
			LiveStatus:      r.String(rec.LiveStatus),
			Profile:         r.String(rec.Profile),
			TargetKind:      r.String(rec.TargetKind),
			TargetProvider:  r.String(rec.TargetProvider),
			Agent:           r.String(rec.Agent),
			Mode:            r.String(rec.Mode),
			StartedAt:       r.String(rec.StartedAt),
			Workspace:       r.String(rec.Workspace),
			AuditLogPresent: rec.AuditLogPath != "" && fileExists(rec.AuditLogPath),
		})
	}
	return s
}

// AuditPointer references an audit log by path + metadata; never its content.
type AuditPointer struct {
	SessionID  string `json:"session_id"`
	Path       string `json:"path"`
	Present    bool   `json:"present"`
	SizeBytes  int64  `json:"size_bytes"`
	ModifiedAt string `json:"modified_at"`
}

// AuditSection lists audit-log pointers for durable sessions. Bodies are never
// read; only path, presence, size, and mtime are recorded.
type AuditSection struct {
	Section
	Pointers []AuditPointer `json:"pointers"`
}

func collectAuditPointers(cfg Config, r Redactor) AuditSection {
	s := AuditSection{}
	roots := stateRoots(cfg)
	if len(roots) == 0 {
		s.note("no state roots configured; audit pointers unavailable")
		return s
	}
	records, err := sessions.ListSessionRecordsInRoots(roots, sessions.SessionListOptions{})
	if err != nil {
		s.note(fmt.Sprintf("session enumeration failed: %v", r.String(err.Error())))
		return s
	}
	s.Available = true
	for _, rec := range records {
		if rec.AuditLogPath == "" {
			continue
		}
		ptr := AuditPointer{
			SessionID: r.String(rec.SessionID),
			Path:      r.String(rec.AuditLogPath),
		}
		if info, statErr := os.Stat(rec.AuditLogPath); statErr == nil && !info.IsDir() {
			ptr.Present = true
			ptr.SizeBytes = info.Size()
			ptr.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
		s.Pointers = append(s.Pointers, ptr)
	}
	sort.Slice(s.Pointers, func(i, j int) bool { return s.Pointers[i].SessionID < s.Pointers[j].SessionID })
	return s
}

func stateRoots(cfg Config) []string {
	var roots []string
	if cfg.WorkcellStateRoot != "" {
		roots = append(roots, cfg.WorkcellStateRoot)
	}
	if cfg.ColimaStateRoot != "" {
		roots = append(roots, cfg.ColimaStateRoot)
	}
	return roots
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
