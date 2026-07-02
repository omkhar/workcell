// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hoststate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateProfileNameAccepts(t *testing.T) {
	t.Parallel()
	cases := []string{
		"wcl-target",
		"wcl",
		"a",
		"A1",
		"profile.with.dots",
		"under_score",
		"hy-phen",
	}
	for _, name := range cases {
		if err := ValidateProfileName(name); err != nil {
			t.Errorf("ValidateProfileName(%q) error = %v, want nil", name, err)
		}
	}
}

func TestValidateProfileNameRejects(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		".",
		"..",
		"-leading-dash",
		"_leading-underscore",
		".leading-dot",
		"contains/slash",
		"contains space",
		"x" + string(make([]byte, 64)), // > 64 chars
	}
	for _, name := range cases {
		err := ValidateProfileName(name)
		if err == nil {
			t.Errorf("ValidateProfileName(%q) = nil, want error", name)
			continue
		}
		var keyErr *InvalidStateKeyError
		if !errors.As(err, &keyErr) {
			t.Errorf("ValidateProfileName(%q) = %v, want *InvalidStateKeyError", name, err)
		}
	}
}

func TestProfileDir(t *testing.T) {
	t.Parallel()
	got, err := ProfileDir("/state/colima", "wcl-target")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/state/colima/wcl-target" {
		t.Errorf("ProfileDir = %q", got)
	}
}

func TestProfileLimaDiskAndStorePaths(t *testing.T) {
	t.Parallel()
	lima, err := ProfileLimaDir("/state/colima", "wcl-target")
	if err != nil {
		t.Fatal(err)
	}
	if lima != "/state/colima/_lima/colima-wcl-target" {
		t.Errorf("ProfileLimaDir = %q", lima)
	}
	disk, err := ProfileDiskDir("/state/colima", "wcl-target")
	if err != nil {
		t.Fatal(err)
	}
	if disk != "/state/colima/_lima/_disks/colima-wcl-target" {
		t.Errorf("ProfileDiskDir = %q", disk)
	}
	store, err := ProfileStorePath("/state/colima", "wcl-target")
	if err != nil {
		t.Fatal(err)
	}
	if store != "/state/colima/_store/colima-wcl-target.json" {
		t.Errorf("ProfileStorePath = %q", store)
	}
}

func TestProfileTargetStateDir(t *testing.T) {
	t.Parallel()
	got, err := ProfileTargetStateDir("/state/targets", "local_vm", "colima", "wcl-target")
	if err != nil {
		t.Fatal(err)
	}
	want := "/state/targets/local_vm/colima/wcl-target"
	if got != want {
		t.Errorf("ProfileTargetStateDir = %q, want %q", got, want)
	}
}

func TestProfileAuditLogPathAndLegacy(t *testing.T) {
	t.Parallel()
	target, err := ProfileAuditLogPath("/state/targets", "local_vm", "colima", "wcl")
	if err != nil {
		t.Fatal(err)
	}
	if target != "/state/targets/local_vm/colima/wcl/workcell.audit.log" {
		t.Errorf("ProfileAuditLogPath = %q", target)
	}
	legacy, err := LegacyProfileAuditLogPath("/state/colima", "wcl")
	if err != nil {
		t.Fatal(err)
	}
	if legacy != "/state/colima/wcl/workcell.audit.log" {
		t.Errorf("LegacyProfileAuditLogPath = %q", legacy)
	}
}

func TestProfileSessionsDirPathAndLegacy(t *testing.T) {
	t.Parallel()
	target, err := ProfileSessionsDirPath("/state/targets", "local_vm", "colima", "wcl")
	if err != nil {
		t.Fatal(err)
	}
	if target != "/state/targets/local_vm/colima/wcl/sessions" {
		t.Errorf("ProfileSessionsDirPath = %q", target)
	}
	legacy, err := LegacyProfileSessionsDirPath("/state/colima", "wcl")
	if err != nil {
		t.Fatal(err)
	}
	if legacy != "/state/colima/wcl/sessions" {
		t.Errorf("LegacyProfileSessionsDirPath = %q", legacy)
	}
}

func TestProfileLockDirPath(t *testing.T) {
	t.Parallel()
	got, err := ProfileLockDirPath("/state", "local_vm", "colima", "wcl")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/state/locks/local_vm/colima/wcl.lock" {
		t.Errorf("ProfileLockDirPath = %q", got)
	}
}

func TestProfileLatestLogPointerPath(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"debug", "file-trace", "transcript"} {
		got, err := ProfileLatestLogPointerPath("/state/targets", "local_vm", "colima", "wcl", kind)
		if err != nil {
			t.Fatalf("kind %q: %v", kind, err)
		}
		want := "/state/targets/local_vm/colima/wcl/workcell.latest-" + kind + "-log"
		if got != want {
			t.Errorf("kind %q: got %q want %q", kind, got, want)
		}
		legacy, err := LegacyProfileLatestLogPointerPath("/state/colima", "wcl", kind)
		if err != nil {
			t.Fatalf("legacy kind %q: %v", kind, err)
		}
		wantLegacy := "/state/colima/wcl/workcell.latest-" + kind + "-log"
		if legacy != wantLegacy {
			t.Errorf("legacy kind %q: got %q want %q", kind, legacy, wantLegacy)
		}
	}
}

func TestProfileLatestLogPointerPathRejectsUnsupportedKind(t *testing.T) {
	t.Parallel()
	_, err := ProfileLatestLogPointerPath("/state/targets", "local_vm", "colima", "wcl", "unknown")
	if !errors.Is(err, ErrUnsupportedLogPointerKind) {
		t.Errorf("err = %v, want ErrUnsupportedLogPointerKind", err)
	}
	_, err = LegacyProfileLatestLogPointerPath("/state/colima", "wcl", "unknown")
	if !errors.Is(err, ErrUnsupportedLogPointerKind) {
		t.Errorf("legacy err = %v, want ErrUnsupportedLogPointerKind", err)
	}
}

func TestProfileColimaConfigPathPicksFirstExisting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	limaDir := filepath.Join(root, "_lima", "colima-wcl")
	if err := os.MkdirAll(limaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	limaYaml := filepath.Join(limaDir, "lima.yaml")
	if err := os.WriteFile(limaYaml, []byte("v: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ProfileColimaConfigPath(root, "wcl")
	if err != nil {
		t.Fatal(err)
	}
	if got != limaYaml {
		t.Errorf("ProfileColimaConfigPath = %q, want %q", got, limaYaml)
	}
}

func TestProfileColimaConfigPathPrefersProfileColimaYaml(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	profileDir := filepath.Join(root, "wcl")
	limaDir := filepath.Join(root, "_lima", "colima-wcl")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(limaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(profileDir, "colima.yaml")
	second := filepath.Join(limaDir, "colima.yaml")
	third := filepath.Join(limaDir, "lima.yaml")
	for _, p := range []string{first, second, third} {
		if err := os.WriteFile(p, []byte("v: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ProfileColimaConfigPath(root, "wcl")
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Errorf("ProfileColimaConfigPath = %q, want %q", got, first)
	}
}

func TestProfileColimaConfigPathFallsBackToFirstCandidate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	got, err := ProfileColimaConfigPath(root, "wcl")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "wcl", "colima.yaml")
	if got != want {
		t.Errorf("ProfileColimaConfigPath = %q, want %q", got, want)
	}
}

func TestProfilePathHelpersRejectInvalidProfile(t *testing.T) {
	t.Parallel()
	check := func(name string, err error) {
		t.Helper()
		var keyErr *InvalidStateKeyError
		if !errors.As(err, &keyErr) {
			t.Errorf("%s err = %v, want *InvalidStateKeyError", name, err)
		}
	}
	_, err := ProfileDir("/state", "../escape")
	check("ProfileDir", err)
	_, err = ProfileLimaDir("/state", "../escape")
	check("ProfileLimaDir", err)
	_, err = ProfileDiskDir("/state", "../escape")
	check("ProfileDiskDir", err)
	_, err = ProfileStorePath("/state", "../escape")
	check("ProfileStorePath", err)
	_, err = ProfileTargetStateDir("/state", "local_vm", "colima", "../escape")
	check("ProfileTargetStateDir", err)
	_, err = ProfileLockDirPath("/state", "local_vm", "colima", "../escape")
	check("ProfileLockDirPath", err)
	_, err = ProfileColimaConfigPath("/state", "../escape")
	check("ProfileColimaConfigPath", err)
}
