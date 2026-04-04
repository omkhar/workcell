package hostutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/pathutil"
)

type releaseCreatePayload struct {
	TagName              string `json:"tag_name"`
	GenerateReleaseNotes bool   `json:"generate_release_notes"`
}

type releaseAsset struct {
	Name string `json:"name"`
	ID   *int64 `json:"id"`
}

type releaseResponse struct {
	ID        int64          `json:"id"`
	UploadURL string         `json:"upload_url"`
	Assets    []releaseAsset `json:"assets"`
}

type releaseBundleManifest struct {
	ArchiveRef      string `json:"archive_ref"`
	BundleName      string `json:"bundle_name"`
	BundlePrefix    string `json:"bundle_prefix"`
	BundleSha256    string `json:"bundle_sha256"`
	ChecksumsSha256 string `json:"checksums_sha256"`
	SourceDateEpoch int64  `json:"source_date_epoch"`
}

func CanonicalizePath(path string) (string, error) {
	return CanonicalizePathFrom(path, "")
}

func CanonicalizePathFrom(path, base string) (string, error) {
	expanded, err := expandUser(path)
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(expanded) {
		switch {
		case base == "":
			base, err = os.Getwd()
			if err != nil {
				return "", err
			}
		case !filepath.IsAbs(base):
			base, err = filepath.Abs(base)
			if err != nil {
				return "", err
			}
		}
		expanded = filepath.Join(base, expanded)
	}

	return pathutil.ResolveBestEffort(filepath.Clean(expanded))
}

func RealHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return CanonicalizePath(home)
}

func WriteGitHubReleaseCreatePayload(tagName, outputPath string) error {
	payload := releaseCreatePayload{
		TagName:              tagName,
		GenerateReleaseNotes: true,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, data, 0o644)
}

func WriteGitHubReleaseMetadata(releaseJSONPath string, assetPaths []string, outputPath string) error {
	data, err := os.ReadFile(releaseJSONPath)
	if err != nil {
		return err
	}

	var release releaseResponse
	if err := json.Unmarshal(data, &release); err != nil {
		return err
	}

	uploadURL, _, _ := strings.Cut(release.UploadURL, "{")
	assetIDs := make(map[string]*int64, len(release.Assets))
	for _, asset := range release.Assets {
		asset := asset
		assetIDs[asset.Name] = asset.ID
	}

	var buffer bytes.Buffer
	writeField := func(value string) {
		_, _ = buffer.WriteString(value)
		_ = buffer.WriteByte(0)
	}

	writeField(fmt.Sprint(release.ID))
	writeField(uploadURL)
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

func EncodeReleaseAssetName(name string) string {
	return strings.ReplaceAll(url.PathEscape(name), "+", "%2B")
}

func WriteReleaseBundleManifest(path, archiveRef, bundleName, bundlePrefix string, sourceDateEpoch int64, bundleSHA256, checksumsSHA256 string) error {
	manifest := releaseBundleManifest{
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

func expandUser(path string) (string, error) {
	return pathutil.ExpandUserPathBestEffort(path)
}
