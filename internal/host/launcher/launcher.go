// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	errNoMatch = errors.New("not found")
	// ErrProfileLockTransitionBusy reports a contended transition guard.
	ErrProfileLockTransitionBusy = errors.New("profile lock transition guard is busy")
)

func RandomHex(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("random hex size must be positive")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func IsNoMatch(err error) bool {
	return errors.Is(err, errNoMatch)
}

func ColimaProfileStatus(listJSON []byte, profile string) (string, error) {
	records, err := decodeJSONObjectSequence(listJSON)
	if err != nil {
		return "", err
	}
	var matchedStatus string
	for _, record := range records {
		name, ok := record["name"].(string)
		if !ok || name == "" {
			return "", errors.New("profile inventory record has missing or invalid name field")
		}
		if name != profile {
			continue
		}
		status, _ := record["status"].(string)
		if status == "" {
			return "", errors.New("profile status missing status field")
		}
		if matchedStatus != "" {
			return "", errors.New("profile inventory contains duplicate names")
		}
		matchedStatus = status
	}
	if matchedStatus != "" {
		return matchedStatus, nil
	}
	return "", errNoMatch
}

func ColimaProfileProcessPIDs(psOutput []byte, profile string) ([]int, error) {
	if profile == "" {
		return nil, errors.New("colima profile is required")
	}
	instance := "colima-" + profile
	var pids []int
	seen := make(map[int]struct{})
	for _, line := range strings.Split(string(psOutput), "\n") {
		id, command, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		command = strings.TrimSpace(command)
		fields := strings.Fields(command)
		hostagent := len(fields) >= 2 && filepath.Base(fields[0]) == "limactl" && fields[1] == "hostagent" &&
			(strings.Contains(command, "/"+instance+"/") || strings.HasSuffix(command, " "+instance))
		mux := strings.HasPrefix(command, "ssh: ") &&
			strings.HasSuffix(command, "/"+instance+"/ssh.sock [mux]")
		if !hostagent && !mux {
			continue
		}
		pid, err := strconv.Atoi(id)
		if err != nil {
			return nil, fmt.Errorf("parse Colima profile process pid: %w", err)
		}
		if pid <= 0 || strconv.Itoa(pid) != id {
			return nil, fmt.Errorf("non-canonical Colima profile process pid: %s", id)
		}
		if _, exists := seen[pid]; exists {
			return nil, fmt.Errorf("duplicate Colima profile process pid: %d", pid)
		}
		seen[pid] = struct{}{}
		pids = append(pids, pid)
	}
	return pids, nil
}

type profileLockOwner struct {
	PID     int    `json:"pid"`
	Started string `json:"started"`
}

func readProfileLockOwner(lockDir string) (profileLockOwner, error) {
	ownerPath := filepath.Join(lockDir, "owner.json")
	content, err := os.ReadFile(ownerPath)
	if err != nil {
		return profileLockOwner{}, err
	}

	var owner profileLockOwner
	if err := json.Unmarshal(content, &owner); err != nil {
		return profileLockOwner{}, fmt.Errorf("parse profile lock owner metadata: %w", err)
	}
	if owner.PID <= 0 || owner.Started == "" {
		return profileLockOwner{}, fmt.Errorf("profile lock owner metadata is incomplete: %s", ownerPath)
	}
	return owner, nil
}

func observedProfileLockGeneration(owner profileLockOwner) (string, error) {
	if strings.HasPrefix(owner.Started, "darwin:") || strings.HasPrefix(owner.Started, "linux:") {
		return processGeneration(owner.PID)
	}
	return ProcessStartTime(owner.PID)
}

func ProfileLockIsStale(lockDir string) (bool, error) {
	owner, err := readProfileLockOwner(lockDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}

	observed, err := observedProfileLockGeneration(owner)
	if err != nil {
		if IsProcessGone(err) {
			return true, nil
		}
		return false, err
	}
	return observed != owner.Started, nil
}

// withProfileLockTransition serializes lock acquisition, stale reclaim, and
// release. The sibling guard file persists so all callers lock the same inode.
// The kernel releases the advisory lock if a helper process exits unexpectedly.
func withProfileLockTransition(lockDir string, fn func() error) error {
	if lockDir == "" || filepath.Clean(lockDir) == string(filepath.Separator) {
		return errors.New("profile lock directory is required")
	}
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o755); err != nil {
		return err
	}

	guardPath := lockDir + ".guard"
	fd, err := unix.Open(guardPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open profile lock transition guard: %w", err)
	}
	defer unix.Close(fd) //nolint:errcheck // the operation result is authoritative
	if err := unix.Fchmod(fd, 0o600); err != nil {
		return fmt.Errorf("set profile lock transition guard mode: %w", err)
	}
	for {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
				return ErrProfileLockTransitionBusy
			}
			return fmt.Errorf("lock profile transition guard: %w", err)
		}
		break
	}
	defer unix.Flock(fd, unix.LOCK_UN) //nolint:errcheck // closing the descriptor also releases the lock
	return fn()
}

func acquireProfileLockUncoordinated(lockDir string, pid int) (bool, error) {
	parentDir := filepath.Dir(lockDir)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return false, err
	}

	tempDir, err := os.MkdirTemp(parentDir, filepath.Base(lockDir)+".pending.")
	if err != nil {
		return false, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tempDir)
		}
	}()

	if err := WriteProfileOwner(filepath.Join(tempDir, "owner.json"), pid); err != nil {
		return false, err
	}
	if err := os.Rename(tempDir, lockDir); err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, syscall.EEXIST) || errors.Is(err, syscall.ENOTEMPTY) {
			return false, nil
		}
		return false, err
	}

	cleanup = false
	return true, nil
}

// AcquireProfileLock atomically acquires a free lock or reclaims a stale lock.
// The transition guard prevents an earlier stale decision from deleting a new
// holder that acquired the directory after another process released it.
func AcquireProfileLock(lockDir string, pid int) (bool, error) {
	var acquired bool
	err := withProfileLockTransition(lockDir, func() error {
		var err error
		acquired, err = acquireProfileLockUncoordinated(lockDir, pid)
		if err != nil || acquired {
			return err
		}
		stale, err := ProfileLockIsStale(lockDir)
		if err != nil {
			return err
		}
		if !stale {
			return nil
		}
		if err := os.RemoveAll(lockDir); err != nil {
			return fmt.Errorf("remove stale profile lock: %w", err)
		}
		acquired, err = acquireProfileLockUncoordinated(lockDir, pid)
		return err
	})
	if errors.Is(err, ErrProfileLockTransitionBusy) {
		return false, nil
	}
	return acquired, err
}

// ReleaseProfileLock removes only the caller's lock while holding the same
// transition guard used by acquisition and stale reclaim.
func ReleaseProfileLock(lockDir string, pid int) error {
	return withProfileLockTransition(lockDir, func() error {
		owner, err := readProfileLockOwner(lockDir)
		if err != nil {
			return err
		}
		if owner.PID != pid {
			return fmt.Errorf("profile lock owner pid is %d, not %d", owner.PID, pid)
		}
		started, err := observedProfileLockGeneration(owner)
		if err != nil {
			return err
		}
		if owner.Started != started {
			return errors.New("profile lock owner process generation changed")
		}
		if err := os.RemoveAll(lockDir); err != nil {
			return fmt.Errorf("release profile lock: %w", err)
		}
		return nil
	})
}

func WriteProfileOwner(ownerPath string, pid int) error {
	started, err := processGeneration(pid)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"pid":     pid,
		"started": started,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(ownerPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(ownerPath, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(ownerPath, 0o600)
}

// ProcessStartTime returns the `ps -o lstart=` value for pid, or an error
// satisfying IsProcessGone if pid no longer exists.
func ProcessStartTime(pid int) (string, error) {
	// Use cmd.Output() so stderr is captured separately in
	// (*exec.ExitError).Stderr.  cmd.CombinedOutput() leaves that field
	// nil, which previously caused the classifier below to treat every
	// non-zero exit as a "process gone" result and release profile locks
	// for live PIDs whenever ps itself was unhappy (PATH, permissions,
	// transient EAGAIN).
	//
	// Resolve ps against the trusted-host PATH allowlist instead of
	// $PATH: this function gates profile-lock liveness, so a PATH-
	// shadow on ps could persuade us a live profile is gone (and let
	// the next launch steal its lock) or that a dead profile is alive
	// (and refuse to clean up).  trustedPSPath() returns the first ps
	// binary on the same hardcoded set scripts/workcell uses for
	// TRUSTED_HOST_PATH; if none exists, we fall back to PATH lookup
	// rather than fail-closed (callers tolerate the IsProcessGone
	// signal but not a hard "ps missing" error mid-cleanup).
	cmd := exec.Command(trustedPSPath(), "-o", "lstart=", "-p", strconv.Itoa(pid))
	output, err := cmd.Output()
	if err != nil {
		// `ps -p PID` exits non-zero with empty stdout AND empty stderr
		// when the process does not exist.  Anything else (non-empty
		// stderr, non-ExitError) is a genuine failure we propagate so
		// callers can distinguish it from a definitively-gone PID.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(strings.TrimSpace(string(output))) == 0 && len(strings.TrimSpace(string(exitErr.Stderr))) == 0 {
			return "", processGoneErr{pid: pid}
		}
		return "", err
	}
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return "", processGoneErr{pid: pid}
	}
	return trimmed, nil
}

// trustedPSPath returns the first existing absolute ps binary from the
// same hardcoded list scripts/workcell uses for TRUSTED_HOST_PATH, so
// PATH-shadow attacks on ps cannot subvert the profile-lock liveness
// classifier above.  Falls back to bare "ps" if none of the trusted
// locations have ps (callers tolerate exec failures via
// processGoneErr; an unusable host should not silently leak locks).
func trustedPSPath() string {
	for _, dir := range []string{"/bin", "/usr/bin", "/sbin", "/usr/sbin", "/opt/homebrew/bin", "/usr/local/bin"} {
		candidate := dir + "/ps"
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}
	return "ps"
}

// IsProcessGone reports whether err came from ProcessStartTime because
// the target PID could not be observed (process has exited).
func IsProcessGone(err error) bool {
	var gone processGoneErr
	return errors.As(err, &gone)
}

type processGoneErr struct {
	pid int
}

func (e processGoneErr) Error() string {
	return fmt.Sprintf("process %d not found", e.pid)
}

func decodeJSONObjectSequence(raw []byte) ([]map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errNoMatch
	}

	if trimmed[0] == '[' {
		var records []map[string]any
		if err := json.Unmarshal(trimmed, &records); err != nil {
			return nil, err
		}
		return records, nil
	}

	var records []map[string]any
	for _, line := range bytes.Split(trimmed, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}
