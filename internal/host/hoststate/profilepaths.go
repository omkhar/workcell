// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hoststate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ValidateProfileName mirrors the bash validate_state_key_name regex for
// Colima profile names. It rejects empty inputs, path separators, and
// the reserved "." / ".." entries with the same exit-code-2 semantics as
// the legacy bash helper.
func ValidateProfileName(profile string) error {
	return ValidateStateKeyName("Colima profile name", profile)
}

// ValidateStateKeyName mirrors the bash validate_state_key_name helper:
// 1-64 chars, [A-Za-z0-9._-], must start with [A-Za-z0-9], and may not
// be "." or "..".
func ValidateStateKeyName(label, value string) error {
	if !stateKeyPattern.MatchString(value) {
		return &InvalidStateKeyError{
			Label: label,
			Value: value,
			Hint:  "Use only letters, numbers, '.', '_', or '-' and do not include path separators.",
		}
	}
	if value == "." || value == ".." {
		return &InvalidStateKeyError{Label: label, Value: value}
	}
	return nil
}

var stateKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// InvalidStateKeyError is returned when a profile or target id fails the
// validate_state_key_name shape check. It carries the bash error label
// so callers can render byte-identical diagnostics.
type InvalidStateKeyError struct {
	Label string
	Value string
	Hint  string
}

func (e *InvalidStateKeyError) Error() string {
	return fmt.Sprintf("Invalid %s: %s", e.Label, e.Value)
}

// ProfileDir returns "${colimaStateRoot}/${profile}".  It mirrors the
// bash profile_dir helper and validates the profile name.
func ProfileDir(colimaStateRoot, profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	return filepath.Join(colimaStateRoot, profile), nil
}

// ProfileLimaDir mirrors the bash profile_lima_dir helper.
func ProfileLimaDir(colimaStateRoot, profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	return filepath.Join(colimaStateRoot, "_lima", "colima-"+profile), nil
}

// ProfileDiskDir mirrors the bash profile_disk_dir helper.
func ProfileDiskDir(colimaStateRoot, profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	return filepath.Join(colimaStateRoot, "_lima", "_disks", "colima-"+profile), nil
}

// ProfileStorePath mirrors the Colima profile metadata location used by
// Colima 0.10. Workcell removes it during managed profile refresh so
// stale VM resource settings cannot survive profile recreation.
func ProfileStorePath(colimaStateRoot, profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	return filepath.Join(colimaStateRoot, "_store", "colima-"+profile+".json"), nil
}

// ProfileTargetStateDir mirrors the bash profile_target_state_dir
// helper.  The bash version resolves target_kind and target_provider
// from environment-derived getters; Go exposes them as explicit args so
// the function stays pure and unit-testable.
func ProfileTargetStateDir(targetStateRoot, targetKind, targetProvider, profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	return filepath.Join(targetStateRoot, targetKind, targetProvider, profile), nil
}

// ProfileAuditLogPath mirrors profile_audit_log_path.
func ProfileAuditLogPath(targetStateRoot, targetKind, targetProvider, profile string) (string, error) {
	dir, err := ProfileTargetStateDir(targetStateRoot, targetKind, targetProvider, profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workcell.audit.log"), nil
}

// LegacyProfileAuditLogPath mirrors legacy_profile_audit_log_path.
func LegacyProfileAuditLogPath(colimaStateRoot, profile string) (string, error) {
	dir, err := ProfileDir(colimaStateRoot, profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workcell.audit.log"), nil
}

// ProfileSessionsDirPath mirrors profile_sessions_dir_path.
func ProfileSessionsDirPath(targetStateRoot, targetKind, targetProvider, profile string) (string, error) {
	dir, err := ProfileTargetStateDir(targetStateRoot, targetKind, targetProvider, profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// LegacyProfileSessionsDirPath mirrors legacy_profile_sessions_dir_path.
func LegacyProfileSessionsDirPath(colimaStateRoot, profile string) (string, error) {
	dir, err := ProfileDir(colimaStateRoot, profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

// ProfileLockDirPath mirrors profile_lock_dir_path.
func ProfileLockDirPath(workcellStateRoot, targetKind, targetProvider, profile string) (string, error) {
	if err := ValidateProfileName(profile); err != nil {
		return "", err
	}
	return filepath.Join(workcellStateRoot, "locks", targetKind, targetProvider, profile+".lock"), nil
}

// LatestLogPointerKinds enumerates the bash-accepted kind values for
// profile_latest_log_pointer_path. Matching the bash case statement
// keeps the Go helper a drop-in equivalent.
var LatestLogPointerKinds = map[string]struct{}{
	"debug":      {},
	"file-trace": {},
	"transcript": {},
}

// ErrUnsupportedLogPointerKind is returned when an invalid kind is
// passed to ProfileLatestLogPointerPath. The bash helper exits 2 on
// this case; callers translate the error accordingly.
var ErrUnsupportedLogPointerKind = errors.New("unsupported latest log pointer kind")

// ProfileLatestLogPointerPath mirrors profile_latest_log_pointer_path.
func ProfileLatestLogPointerPath(targetStateRoot, targetKind, targetProvider, profile, kind string) (string, error) {
	if _, ok := LatestLogPointerKinds[kind]; !ok {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedLogPointerKind, kind)
	}
	dir, err := ProfileTargetStateDir(targetStateRoot, targetKind, targetProvider, profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workcell.latest-"+kind+"-log"), nil
}

// LegacyProfileLatestLogPointerPath mirrors
// legacy_profile_latest_log_pointer_path.
func LegacyProfileLatestLogPointerPath(colimaStateRoot, profile, kind string) (string, error) {
	if _, ok := LatestLogPointerKinds[kind]; !ok {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedLogPointerKind, kind)
	}
	dir, err := ProfileDir(colimaStateRoot, profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "workcell.latest-"+kind+"-log"), nil
}

// ProfileColimaConfigPath returns the first existing config candidate
// for the named profile, or the canonical first candidate if none of
// the three legacy locations exist.  This matches the bash helper at
// scripts/workcell:699.
func ProfileColimaConfigPath(colimaStateRoot, profile string) (string, error) {
	profileDir, err := ProfileDir(colimaStateRoot, profile)
	if err != nil {
		return "", err
	}
	limaDir, err := ProfileLimaDir(colimaStateRoot, profile)
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(profileDir, "colima.yaml"),
		filepath.Join(limaDir, "colima.yaml"),
		filepath.Join(limaDir, "lima.yaml"),
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return candidates[0], nil
}
