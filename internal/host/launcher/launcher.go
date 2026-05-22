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
)

var errNoMatch = errors.New("not found")

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
	for _, record := range records {
		name, _ := record["name"].(string)
		if name != profile {
			continue
		}
		status, _ := record["status"].(string)
		if status == "" {
			return "", errors.New("profile status missing status field")
		}
		return status, nil
	}
	return "", errNoMatch
}

func ProfileLockIsStale(lockDir string) (bool, error) {
	ownerPath := filepath.Join(lockDir, "owner.json")
	content, err := os.ReadFile(ownerPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}

	var owner struct {
		PID     int    `json:"pid"`
		Started string `json:"started"`
	}
	if err := json.Unmarshal(content, &owner); err != nil {
		return false, fmt.Errorf("parse profile lock owner metadata: %w", err)
	}
	if owner.PID <= 0 || owner.Started == "" {
		return false, fmt.Errorf("profile lock owner metadata is incomplete: %s", ownerPath)
	}

	if err := syscall.Kill(owner.PID, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return true, nil
		}
		return false, err
	}

	observed, err := ProcessStartTime(owner.PID)
	if err != nil {
		if killErr := syscall.Kill(owner.PID, 0); killErr != nil {
			if errors.Is(killErr, syscall.ESRCH) {
				return true, nil
			}
			return false, killErr
		}
		return false, err
	}
	return observed != owner.Started, nil
}

func AcquireProfileLock(lockDir string, pid int) (bool, error) {
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

func WriteProfileOwner(ownerPath string, pid int) error {
	started, err := ProcessStartTime(pid)
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
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
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
