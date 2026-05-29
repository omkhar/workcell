// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package release carries the GitHub-release helpers the
// workcell-hostutil binary uses when assembling and uploading a
// release. The functions previously lived in internal/host/launcher
// alongside path canonicalization and session helpers; they were
// split out to keep the per-concern boundaries cleaner, matching the
// /sethify Run-1 plan to break the hostutil god-package into focused
// subpackages.
package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type createPayload struct {
	TagName              string `json:"tag_name"`
	Draft                bool   `json:"draft"`
	GenerateReleaseNotes bool   `json:"generate_release_notes"`
}

type asset struct {
	Name string `json:"name"`
	ID   *int64 `json:"id"`
}

type response struct {
	ID        int64   `json:"id"`
	UploadURL string  `json:"upload_url"`
	Draft     bool    `json:"draft"`
	Immutable bool    `json:"immutable"`
	Assets    []asset `json:"assets"`
}

type bundleManifest struct {
	ArchiveRef      string `json:"archive_ref"`
	BundleName      string `json:"bundle_name"`
	BundlePrefix    string `json:"bundle_prefix"`
	BundleSha256    string `json:"bundle_sha256"`
	ChecksumsSha256 string `json:"checksums_sha256"`
	SourceDateEpoch int64  `json:"source_date_epoch"`
}

// WriteGitHubReleaseCreatePayload emits the JSON body the GitHub
// "create a release" REST call needs.
func WriteGitHubReleaseCreatePayload(tagName, outputPath string) error {
	payload := createPayload{
		TagName:              tagName,
		Draft:                true,
		GenerateReleaseNotes: true,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, data, 0o644)
}

// WriteGitHubReleaseMetadata reads a GitHub release response JSON file
// and writes a NUL-separated record file (release ID, upload URL, draft
// flag, immutable flag, per-asset name+id pairs) for the host-side
// uploader to consume.
func WriteGitHubReleaseMetadata(releaseJSONPath string, assetPaths []string, outputPath string) error {
	data, err := os.ReadFile(releaseJSONPath)
	if err != nil {
		return err
	}

	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return err
	}

	uploadURL, _, _ := strings.Cut(resp.UploadURL, "{")
	assetIDs := make(map[string]*int64, len(resp.Assets))
	for _, asset := range resp.Assets {
		assetIDs[asset.Name] = asset.ID
	}

	var buffer bytes.Buffer
	writeField := func(value string) {
		_, _ = buffer.WriteString(value)
		_ = buffer.WriteByte(0)
	}

	writeField(fmt.Sprint(resp.ID))
	writeField(uploadURL)
	writeField(fmt.Sprint(resp.Draft))
	writeField(fmt.Sprint(resp.Immutable))
	for _, assetPath := range assetPaths {
		name := filepath.Base(assetPath)
		writeField(name)
		if assetID := assetIDs[name]; assetID != nil {
			writeField(fmt.Sprint(*assetID))
		} else {
			writeField("")
		}
	}

	return os.WriteFile(outputPath, buffer.Bytes(), 0o644)
}

// EncodeReleaseAssetName URL-encodes name for the GitHub release asset
// upload path, with `+` escaped explicitly because GitHub's upload
// endpoint treats it as a space otherwise.
func EncodeReleaseAssetName(name string) string {
	return strings.ReplaceAll(url.PathEscape(name), "+", "%2B")
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
