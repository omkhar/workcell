// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"encoding/json"
	"runtime"
	"time"
)

// SchemaVersion versions the bundle JSON shape. Bump it on any field
// addition/rename/removal so consumers and the golden test can gate on it.
const SchemaVersion = "1"

// toolName is the stable tool identifier embedded in the bundle.
const toolName = "workcell support-bundle"

// Config carries every host input the collector needs. Injecting these (rather
// than reading process env directly) keeps collection hermetic and golden tests
// reproducible.
type Config struct {
	// RepoRoot is the workcell checkout / install root (ROOT_DIR).
	RepoRoot string
	// LauncherPath is the absolute path to the scripts/workcell launcher.
	LauncherPath string
	// RealHome is the operator home directory used for redaction and to
	// locate the user injection policy.
	RealHome string
	// WorkcellStateRoot and ColimaStateRoot are the two durable state roots
	// (WORKCELL_STATE_ROOT, COLIMA_STATE_ROOT).
	WorkcellStateRoot string
	ColimaStateRoot   string
	// HostOS and HostArch describe the host; empty falls back to the build's
	// runtime.GOOS / runtime.GOARCH.
	HostOS   string
	HostArch string
	// Now is the bundle timestamp. A zero value is treated as unset and the
	// generated_at field is emitted as the empty string (golden tests pin it).
	Now time.Time
}

// Section is embedded by every evidence-class section so collection can
// degrade gracefully: a missing source flips Available and appends a Gap
// rather than aborting the whole bundle.
type Section struct {
	Available bool     `json:"available"`
	Gaps      []string `json:"gaps"`
}

func (s *Section) note(gap string) {
	s.Gaps = append(s.Gaps, gap)
}

// RedactionInfo self-describes the guarantees the bundle was produced under.
type RedactionInfo struct {
	PolicyVersion string   `json:"policy_version"`
	Rules         []string `json:"rules"`
}

// Bundle is the deterministic support-bundle document. Field order here is the
// on-disk JSON order; every slice inside is sorted at collection time.
type Bundle struct {
	SchemaVersion string           `json:"schema_version"`
	Tool          string           `json:"tool"`
	GeneratedAt   string           `json:"generated_at"`
	Redaction     RedactionInfo    `json:"redaction"`
	Install       InstallSection   `json:"install"`
	Policy        PolicySection    `json:"policy"`
	Target        TargetSection    `json:"target"`
	Providers     ProvidersSection `json:"providers"`
	Sessions      SessionsSection  `json:"sessions"`
	AuditPointers AuditSection     `json:"audit_pointers"`
}

// Collect assembles a redacted bundle from cfg. It never returns an error for a
// missing evidence source — those become section gaps — so an operator on a
// half-installed host still gets a shareable bundle.
func Collect(cfg Config) Bundle {
	r := NewRedactor(cfg.RealHome)

	generatedAt := ""
	if !cfg.Now.IsZero() {
		generatedAt = cfg.Now.UTC().Format(time.RFC3339)
	}

	hostOS := cfg.HostOS
	if hostOS == "" {
		hostOS = runtime.GOOS
	}
	hostArch := cfg.HostArch
	if hostArch == "" {
		hostArch = runtime.GOARCH
	}

	return Bundle{
		SchemaVersion: SchemaVersion,
		Tool:          toolName,
		GeneratedAt:   generatedAt,
		Redaction: RedactionInfo{
			PolicyVersion: RedactionPolicyVersion,
			Rules:         RedactionRules(),
		},
		Install:       collectInstall(cfg, r, hostOS, hostArch),
		Policy:        collectPolicy(cfg, r),
		Target:        collectTarget(cfg, r),
		Providers:     collectProviders(cfg, r),
		Sessions:      collectSessions(cfg, r),
		AuditPointers: collectAuditPointers(cfg, r),
	}
}

// JSON renders the bundle as indented, deterministic JSON with a trailing
// newline. Go marshals struct fields in declaration order and every embedded
// slice is pre-sorted, so identical inputs yield byte-identical output.
func (b Bundle) JSON() ([]byte, error) {
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
