// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package release validates, stages, and publishes GitHub release artifacts for
// workcell-hostutil. It keeps release state and content authorization together
// while leaving the public shell entrypoint as sanitized host-side glue.
package release

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type bundleManifest struct {
	ArchiveRef      string `json:"archive_ref"`
	BundleName      string `json:"bundle_name"`
	BundlePrefix    string `json:"bundle_prefix"`
	BundleSha256    string `json:"bundle_sha256"`
	ChecksumsSha256 string `json:"checksums_sha256"`
	SourceDateEpoch int64  `json:"source_date_epoch"`
}

// WriteReleaseBundleManifest writes the per-release bundle manifest
// (archive ref, names, sha256s, source-date epoch) the host-side
// release flow uses to verify and re-publish bundles.
func WriteReleaseBundleManifest(path, archiveRef, bundleName, bundlePrefix string, sourceDateEpoch int64, bundleSHA256, checksumsSHA256 string) error {
	manifest := bundleManifest{
		ArchiveRef:      archiveRef,
		BundleName:      bundleName,
		BundlePrefix:    bundlePrefix,
		BundleSha256:    bundleSHA256,
		ChecksumsSha256: checksumsSHA256,
		SourceDateEpoch: sourceDateEpoch,
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}
