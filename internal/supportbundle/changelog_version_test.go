// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"testing"
)

func TestChangelogVersionCapturesPreReleaseSuffix(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite(t, root+"/CHANGELOG.md",
		"# Changelog\n\n## Unreleased\n\n## v1.0.0-rc.1 - 2026-07-09\n\n## v0.11.2 - 2026-06-15\n")

	if got := changelogVersion(root); got != "v1.0.0-rc.1" {
		t.Fatalf("changelogVersion = %q, want %q", got, "v1.0.0-rc.1")
	}
}

func TestChangelogVersionFirstReleaseHeadingWins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite(t, root+"/CHANGELOG.md",
		"# Changelog\n\n## Unreleased\n\n## v0.11.2 - 2026-06-15\n\n## v0.11.1 - 2026-06-15\n")

	if got := changelogVersion(root); got != "v0.11.2" {
		t.Fatalf("changelogVersion = %q, want %q", got, "v0.11.2")
	}
}
