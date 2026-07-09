// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package auditseal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// fsyncDir is the directory-durability fsync for the seal's parent, indirected
// so a durability test can inject a writeback failure. It mirrors the keystore's
// same-named seam.
var fsyncDir = func(fd int) error { return unix.Fsync(fd) }

// SealPathForRecord returns the seal sidecar path beside a durable session
// record. It deliberately does NOT end in ".json" so the sessions discovery glob
// (which reads every "*.json" as a record) does not mistake it for one; its
// contents are still JSON.
//
//	.../<session-id>.json      -> .../<session-id>.audit-sig
func SealPathForRecord(recordPath string) string {
	base := recordPath
	if strings.HasSuffix(base, ".json") {
		base = strings.TrimSuffix(base, ".json")
	}
	return base + ".audit-sig"
}

// WriteSeal writes seal to path as owner-only JSON, replacing atomically.
func WriteSeal(path string, seal Seal) error {
	data, err := json.MarshalIndent(seal, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	remove = false
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	// Fsync the parent directory so the renamed entry (a first-write or a
	// replace) is durable: on filesystems where rename durability requires a
	// parent-dir fsync, a power loss after WriteSeal returns could otherwise lose
	// the directory entry, leaving a completed session with no .audit-sig sidecar.
	// Fail closed on any open/fsync error so a non-durable seal never reports
	// success. O_NOFOLLOW mirrors the keystore's directory-open discipline.
	dir, err := os.OpenFile(filepath.Dir(path), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	if err := fsyncDir(int(dir.Fd())); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}

// ReadSeal parses a seal sidecar with strict JSON decoding (rejecting unexpected fields or trailing content).
func ReadSeal(path string) (Seal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Seal{}, err
	}
	var seal Seal
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&seal); err != nil {
		return Seal{}, fmt.Errorf("auditseal: decode seal %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Seal{}, fmt.Errorf("auditseal: seal %s has unexpected trailing content", path)
	}
	return seal, nil
}
