// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWorkcellVersionFlagReadsFirstChangelogVersion(t *testing.T) {
	// Intentionally serial: writes-then-execs the launcher fixture on disk.
	// Running these in parallel races against the Linux kernel's ETXTBSY
	// check when concurrent goroutines exec freshly-written executables
	// (observed as a pre-merge flake in the validator container).
	changelog := "# Changelog\n\n## Unreleased\n\n## v9.8.7 - 2099-01-01\n\n## v9.9.9 - 2099-02-01\n"
	workcellPath := writeWorkcellVersionFixture(t, &changelog)

	got := runWorkcellVersion(t, workcellPath)
	if got != "workcell v9.8.7\n" {
		t.Fatalf("workcell --version output = %q, want %q", got, "workcell v9.8.7\n")
	}
}

func TestWorkcellVersionFlagCapturesPreReleaseSuffix(t *testing.T) {
	// Serial — see ETXTBSY note on TestWorkcellVersionFlagReadsFirstChangelogVersion.
	changelog := "# Changelog\n\n## Unreleased\n\n## v1.2.3-rc.1 - 2099-01-01\n\n## v1.2.2 - 2098-12-01\n"
	workcellPath := writeWorkcellVersionFixture(t, &changelog)

	got := runWorkcellVersion(t, workcellPath)
	if got != "workcell v1.2.3-rc.1\n" {
		t.Fatalf("workcell --version output = %q, want %q", got, "workcell v1.2.3-rc.1\n")
	}

	hyphenated := "# Changelog\n\n## Unreleased\n\n## v2.0.0-rc-1 - 2099-03-01\n"
	hyphenPath := writeWorkcellVersionFixture(t, &hyphenated)

	if got := runWorkcellVersion(t, hyphenPath); got != "workcell v2.0.0-rc-1\n" {
		t.Fatalf("workcell --version output = %q, want %q", got, "workcell v2.0.0-rc-1\n")
	}
}

func TestWorkcellVersionFlagReportsUnknownWhenChangelogMissing(t *testing.T) {
	// Serial — see ETXTBSY note on TestWorkcellVersionFlagReadsFirstChangelogVersion.
	workcellPath := writeWorkcellVersionFixture(t, nil)

	got := runWorkcellVersion(t, workcellPath)
	if got != "workcell unknown\n" {
		t.Fatalf("workcell --version output = %q, want %q", got, "workcell unknown\n")
	}
}

func TestWorkcellVersionFlagReportsUnknownWithoutVersionHeading(t *testing.T) {
	// Serial — see ETXTBSY note on TestWorkcellVersionFlagReadsFirstChangelogVersion.
	changelog := "# Changelog\n\n## Unreleased\n\n### Changed\n\n- no released version yet\n"
	workcellPath := writeWorkcellVersionFixture(t, &changelog)

	got := runWorkcellVersion(t, workcellPath)
	if got != "workcell unknown\n" {
		t.Fatalf("workcell --version output = %q, want %q", got, "workcell unknown\n")
	}
}

func writeWorkcellVersionFixture(tb testing.TB, changelog *string) string {
	tb.Helper()
	if runtime.GOOS == "windows" {
		tb.Skip("scripts/workcell is a POSIX shell entrypoint")
	}

	sourceRoot := repoRoot(tb)
	fixtureRoot := tb.TempDir()
	fixtureScriptsDir := filepath.Join(fixtureRoot, "scripts")
	if err := os.MkdirAll(fixtureScriptsDir, 0o755); err != nil {
		tb.Fatal(err)
	}

	sourceWorkcellPath := filepath.Join(sourceRoot, "scripts", "workcell")
	launcher, err := os.ReadFile(sourceWorkcellPath)
	if err != nil {
		tb.Fatal(err)
	}
	fixtureWorkcellPath := filepath.Join(fixtureScriptsDir, "workcell")
	if err := os.WriteFile(fixtureWorkcellPath, launcher, 0o755); err != nil {
		tb.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(sourceRoot, "scripts", "lib"), filepath.Join(fixtureScriptsDir, "lib")); err != nil {
		tb.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sourceRoot, "runtime"), filepath.Join(fixtureRoot, "runtime")); err != nil {
		tb.Fatal(err)
	}

	if changelog != nil {
		if err := os.WriteFile(filepath.Join(fixtureRoot, "CHANGELOG.md"), []byte(*changelog), 0o644); err != nil {
			tb.Fatal(err)
		}
	}

	return fixtureWorkcellPath
}

func runWorkcellVersion(tb testing.TB, workcellPath string) string {
	tb.Helper()

	cmd := exec.Command(workcellPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("%s --version failed: %v\n%s", workcellPath, err, output)
	}
	return string(output)
}
