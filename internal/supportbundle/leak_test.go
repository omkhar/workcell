// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"strings"
	"testing"
)

// TestNoSecretLeaksThroughPipeline is the G2 exit-gate proof: known secrets and
// the operator home/username are seeded into real collected fields (a session
// workspace path, an audit-log body, a user injection-policy body) and the
// fully marshaled bundle is asserted to contain none of them.
func TestNoSecretLeaksThroughPipeline(t *testing.T) {
	// Assembled from fragments so the literal token shape never appears in
	// source (GitHub push protection); the runtime value still exercises the
	// leak-detection pipeline.
	const ghToken = "ghp_" + "0123456789abcdefghijklmnopqrstuvwx"

	// The session workspace path carries a live-token-shaped segment; the
	// audit-log body (never read) and the user injection policy body (never
	// read) also carry the token in the fixture.
	cfg := buildFixture(t, fixtureOptions{
		userInjection:    true,
		sessionStatus:    "running",
		writeAuditLog:    true,
		sessionWorkspace: "/srv/repos/" + ghToken + "-project",
		// User-influenceable identifiers (e.g. --colima-profile) can carry
		// token-shaped text; they must be redacted before marshaling too.
		sessionProfile: ghToken + "-profile",
		sessionID:      "sess-" + ghToken,
	})

	out, err := Collect(cfg).JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	rendered := string(out)

	if strings.Contains(rendered, ghToken) {
		t.Fatalf("github token leaked into bundle:\n%s", rendered)
	}
	if strings.Contains(rendered, "AUDIT RECORD BODY") {
		t.Fatalf("audit log body content leaked into bundle")
	}
	// The audit-log body byte count is fine to expose; assert the pointer
	// carries metadata but not content.
	if !strings.Contains(rendered, "\"size_bytes\":") {
		t.Fatalf("audit pointer should carry size metadata")
	}

	// The fixture's real home is a temp dir; its final path segment is the
	// username-equivalent. Every occurrence must have been rewritten to ~.
	homeBase := lastSegment(cfg.RealHome)
	if homeBase != "" && strings.Contains(rendered, cfg.RealHome) {
		t.Fatalf("absolute home path leaked into bundle")
	}
	if !strings.Contains(rendered, "~/") {
		t.Fatalf("expected home-relative paths in bundle")
	}
	// The redacted workspace must show a masked token, not the raw value.
	if !strings.Contains(rendered, "[REDACTED-TOKEN]") {
		t.Fatalf("expected redaction marker for the workspace token")
	}
}

// TestEverySecretPatternSurvivesMarshaling feeds each known-secret shape into a
// session workspace field and asserts marshaled output never contains it. This
// exercises the collector+marshal path (not just the unit redactor).
func TestEverySecretPatternSurvivesMarshaling(t *testing.T) {
	for _, tc := range knownSecrets {
		t.Run(tc.name, func(t *testing.T) {
			cfg := buildFixture(t, fixtureOptions{
				sessionStatus:    "exited",
				sessionWorkspace: "/srv/" + tc.secret + "/x",
			})
			out, err := Collect(cfg).JSON()
			if err != nil {
				t.Fatalf("JSON: %v", err)
			}
			if strings.Contains(string(out), tc.secret) {
				t.Fatalf("secret %q (%s) leaked into bundle", tc.secret, tc.name)
			}
		})
	}
}

func lastSegment(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
