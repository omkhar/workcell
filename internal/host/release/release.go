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
	Prerelease           bool   `json:"prerelease"`
	MakeLatest           string `json:"make_latest"`
	GenerateReleaseNotes bool   `json:"generate_release_notes"`
}

type publishPayload struct {
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	MakeLatest string `json:"make_latest"`
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

type listedRelease struct {
	ID         *int64             `json:"id"`
	UploadURL  *string            `json:"upload_url"`
	TagName    *string            `json:"tag_name"`
	Draft      *bool              `json:"draft"`
	Prerelease *bool              `json:"prerelease"`
	Immutable  *bool              `json:"immutable"`
	Assets     *[]json.RawMessage `json:"assets"`
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
	policy, err := ClassifyTag(tagName)
	if err != nil {
		return err
	}
	payload := createPayload{
		TagName:              tagName,
		Draft:                true,
		Prerelease:           policy.Prerelease,
		MakeLatest:           "false",
		GenerateReleaseNotes: true,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, data, 0o644)
}

// WriteGitHubReleasePublishPayload emits the final metadata transition for the
// supported release class.
func WriteGitHubReleasePublishPayload(tagName, outputPath string) error {
	policy, err := ClassifyTag(tagName)
	if err != nil {
		return err
	}
	makeLatest := "false"
	if policy.MakeLatest {
		makeLatest = "true"
	}
	payload := publishPayload{
		Prerelease: policy.Prerelease,
		MakeLatest: makeLatest,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, data, 0o644)
}

// GitHubReleaseListPageSize validates that path contains one List releases
// response page and returns its item count.
func GitHubReleaseListPageSize(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var page []json.RawMessage
	if err := json.Unmarshal(data, &page); err != nil {
		return 0, fmt.Errorf("decode GitHub releases list page: %w", err)
	}
	if page == nil {
		return 0, fmt.Errorf("decode GitHub releases list page: expected JSON array, got null")
	}
	return len(page), nil
}

// WriteGitHubListedRelease selects exactly one authenticated List releases item
// with tagName across every supplied page. An absent tag produces an empty
// output file. Duplicate matches or malformed release objects fail closed.
func WriteGitHubListedRelease(tagName string, pagePaths []string, outputPath string) error {
	if _, err := ClassifyTag(tagName); err != nil {
		return err
	}
	if len(pagePaths) == 0 {
		return inputErrorf("at least one GitHub releases list page is required")
	}
	var match json.RawMessage
	for _, pagePath := range pagePaths {
		data, err := os.ReadFile(pagePath)
		if err != nil {
			return err
		}
		var page []json.RawMessage
		if err := json.Unmarshal(data, &page); err != nil {
			return fmt.Errorf("decode GitHub releases list page %q: %w", pagePath, err)
		}
		if page == nil {
			return fmt.Errorf("decode GitHub releases list page %q: expected JSON array, got null", pagePath)
		}
		for index, raw := range page {
			var item listedRelease
			if err := json.Unmarshal(raw, &item); err != nil {
				return fmt.Errorf("decode GitHub release list item %d in %q: %w", index, pagePath, err)
			}
			if err := validateListedRelease(item); err != nil {
				return fmt.Errorf("malformed GitHub release list item %d in %q: %w", index, pagePath, err)
			}
			if *item.TagName != tagName {
				continue
			}
			if match != nil {
				return fmt.Errorf("GitHub releases list contains duplicate exact tag %q", tagName)
			}
			match = append(json.RawMessage(nil), raw...)
		}
	}
	if match == nil {
		return os.WriteFile(outputPath, nil, 0o600)
	}
	return os.WriteFile(outputPath, match, 0o600)
}

func validateListedRelease(item listedRelease) error {
	if item.ID == nil || *item.ID <= 0 {
		return fmt.Errorf("missing or invalid id")
	}
	if item.UploadURL == nil || *item.UploadURL == "" {
		return fmt.Errorf("missing upload_url")
	}
	if item.TagName == nil || *item.TagName == "" {
		return fmt.Errorf("missing tag_name")
	}
	if item.Draft == nil {
		return fmt.Errorf("missing draft")
	}
	if item.Prerelease == nil {
		return fmt.Errorf("missing prerelease")
	}
	if item.Immutable == nil {
		return fmt.Errorf("missing immutable")
	}
	if item.Assets == nil {
		return fmt.Errorf("missing assets")
	}
	return nil
}

func validateReleaseUploadURL(repository string, item listedRelease) error {
	want := fmt.Sprintf(
		"https://uploads.github.com/repos/%s/releases/%d/assets{?name,label}",
		repository,
		*item.ID,
	)
	if *item.UploadURL != want {
		return fmt.Errorf("GitHub release upload_url = %q, want %q", *item.UploadURL, want)
	}
	return nil
}

// ValidateGitHubDraftRelease validates an exact-tag mutable draft and its
// repository-derived upload URL. Asset content authorization is intentionally
// owned by the staged-content publisher, not this metadata-only validator.
func ValidateGitHubDraftRelease(repository, tagName, responsePath string) error {
	if err := validateRepository(repository); err != nil {
		return err
	}
	policy, err := ClassifyTag(tagName)
	if err != nil {
		return err
	}
	item, err := readListedRelease(responsePath, "draft")
	if err != nil {
		return err
	}
	if *item.TagName != tagName {
		return fmt.Errorf("GitHub draft release tag_name = %q, want %q", *item.TagName, tagName)
	}
	if !*item.Draft {
		return fmt.Errorf("GitHub draft release draft = false, want true")
	}
	if *item.Prerelease != policy.Prerelease {
		return fmt.Errorf("GitHub draft release prerelease = %t, want %t for %s tag %q", *item.Prerelease, policy.Prerelease, policy.Kind, tagName)
	}
	if *item.Immutable {
		return fmt.Errorf("GitHub draft release immutable = true, want false")
	}
	if err := validateReleaseUploadURL(repository, item); err != nil {
		return err
	}
	return nil
}

// ValidateGitHubPublishedReleaseState verifies immutable published metadata and
// repository binding. It does not authorize asset bytes.
func ValidateGitHubPublishedReleaseState(repository, tagName, responsePath string) error {
	if err := validateRepository(repository); err != nil {
		return err
	}
	if _, err := ClassifyTag(tagName); err != nil {
		return err
	}
	item, err := readListedRelease(responsePath, "published")
	if err != nil {
		return err
	}
	if *item.TagName != tagName {
		return fmt.Errorf("GitHub published release tag_name = %q, want %q", *item.TagName, tagName)
	}
	if err := validateReleaseUploadURL(repository, item); err != nil {
		return err
	}
	return validatePublishedReleaseItem(tagName, item)
}

func readListedRelease(path, state string) (listedRelease, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return listedRelease{}, err
	}
	var item listedRelease
	if err := json.Unmarshal(data, &item); err != nil {
		return listedRelease{}, fmt.Errorf("decode GitHub %s release response: %w", state, err)
	}
	if err := validateListedRelease(item); err != nil {
		return listedRelease{}, fmt.Errorf("malformed GitHub %s release response: %w", state, err)
	}
	return item, nil
}

// ValidateGitHubLatestReleaseResponse verifies GitHub's repository-wide latest
// pointer after publication. Final tags must become latest. Release candidates
// must leave a final release latest; a 404 is valid when none exists.
func ValidateGitHubLatestReleaseResponse(repository, tagName, httpStatus, responsePath string) error {
	if err := validateRepository(repository); err != nil {
		return err
	}
	policy, err := ClassifyTag(tagName)
	if err != nil {
		return err
	}
	if policy.MakeLatest {
		if httpStatus != "200" {
			return fmt.Errorf("GitHub latest release lookup returned HTTP %s after final tag %q; want 200", httpStatus, tagName)
		}
	} else {
		if httpStatus == "404" {
			return nil
		}
		if httpStatus != "200" {
			return fmt.Errorf("GitHub latest release lookup returned HTTP %s after release-candidate tag %q; want 200 or 404", httpStatus, tagName)
		}
	}
	item, err := readListedRelease(responsePath, "latest")
	if err != nil {
		return err
	}
	if err := validateReleaseUploadURL(repository, item); err != nil {
		return err
	}
	if policy.MakeLatest {
		if *item.TagName != tagName {
			return fmt.Errorf("GitHub latest release tag_name = %q, want final tag %q", *item.TagName, tagName)
		}
		return validatePublishedReleaseItem(tagName, item)
	}
	latestPolicy, err := ClassifyTag(*item.TagName)
	if err != nil || !latestPolicy.MakeLatest {
		return fmt.Errorf("GitHub latest release tag_name = %q after release-candidate tag %q; want a final release tag", *item.TagName, tagName)
	}
	return validatePublishedReleaseItem(*item.TagName, item)
}

func validatePublishedReleaseItem(tagName string, item listedRelease) error {
	if *item.Draft {
		return fmt.Errorf("GitHub published release draft = true, want false")
	}
	policy, err := ClassifyTag(tagName)
	if err != nil {
		return err
	}
	if *item.Prerelease != policy.Prerelease {
		return fmt.Errorf("GitHub published release prerelease = %t, want %t for %s tag %q", *item.Prerelease, policy.Prerelease, policy.Kind, tagName)
	}
	if !*item.Immutable {
		return fmt.Errorf("GitHub published release immutable = false, want true")
	}
	return nil
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
