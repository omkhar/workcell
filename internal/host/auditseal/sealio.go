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
)

// SealPathForRecord returns the seal sidecar path for a durable session record
// path. The seal lives beside the record (host-owned) so `session verify` finds
// it from the same location the record was discovered. The sidecar deliberately
// does NOT end in ".json" so it is not itself mistaken for a session record by
// the sessions discovery glob (which reads every "*.json" as a record); its
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

// WriteSeal writes seal to path as pretty JSON with owner-only permissions,
// replacing any existing seal atomically.
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
	return os.Chmod(path, 0o600)
}

// ReadSeal parses a seal sidecar from path with strict JSON decoding so an
// unexpected field or trailing content is rejected rather than silently ignored.
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
