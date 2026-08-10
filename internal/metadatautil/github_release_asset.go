// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	githubReleaseAssetTimeout = 60 * time.Second
)

var (
	githubReleaseRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	githubReleaseAssetIDPattern    = regexp.MustCompile(`^[1-9][0-9]*$`)
	githubReleaseRedirectPath      = regexp.MustCompile(`^/github-production-release-asset/[1-9][0-9]*/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type githubReleaseAssetDocument struct {
	Assets []struct {
		ID   json.RawMessage `json:"id"`
		Name string          `json:"name"`
	} `json:"assets"`
}

// FetchGitHubReleaseAsset selects one asset from releaseJSON and downloads it.
// The authenticated API request cannot redirect automatically. A validated
// release-assets URL gets one separate request without credentials.
func FetchGitHubReleaseAsset(ctx context.Context, releaseJSON io.Reader, repository, assetName string) ([]byte, error) {
	return FetchGitHubReleaseAssetClass(ctx, releaseJSON, repository, assetName, "checksum")
}

// FetchGitHubReleaseAssetClass selects one release asset in a reviewed size
// class. Checksums stay small while release archives use the updater's bounded
// metadata-size ceiling.
func FetchGitHubReleaseAssetClass(ctx context.Context, releaseJSON io.Reader, repository, assetName, assetClass string) ([]byte, error) {
	maxBytes, err := githubReleaseAssetLimit(assetClass)
	if err != nil {
		return nil, err
	}
	token, err := githubAPIToken()
	if err != nil {
		return nil, err
	}
	return fetchGitHubReleaseAsset(ctx, newGitHubReleaseAssetClient(), releaseJSON, repository, assetName, token, maxBytes)
}

func newGitHubReleaseAssetClient() *http.Client {
	return &http.Client{
		Transport: newUpstreamHTTPTransport(),
		Timeout:   githubReleaseAssetTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func fetchGitHubReleaseAsset(ctx context.Context, client upstreamHTTPDoer, releaseJSON io.Reader, repository, assetName, token string, maxBytes int64) ([]byte, error) {
	apiURL, err := githubReleaseAssetAPIURL(releaseJSON, repository, assetName)
	if err != nil {
		return nil, err
	}
	initial, err := githubReleaseAssetRequest(ctx, client, apiURL, token, true)
	if err != nil {
		return nil, fmt.Errorf("unable to download the GitHub release asset")
	}
	if initial == nil {
		return nil, errors.New("GitHub release asset request returned no response")
	}
	if initial.Body != nil {
		defer initial.Body.Close()
	}

	switch initial.StatusCode {
	case http.StatusOK:
		return readGitHubReleaseAssetBody(initial, maxBytes)
	case http.StatusFound:
		locations := initial.Header.Values("Location")
		if len(locations) != 1 || locations[0] == "" {
			return nil, errors.New("GitHub release asset redirect must include one Location header")
		}
		if !validGitHubReleaseAssetRedirectURL(locations[0]) {
			return nil, errors.New("GitHub release asset redirect is outside the allowed endpoint")
		}
		redirect, requestErr := githubReleaseAssetRequest(ctx, client, locations[0], "", false)
		if requestErr != nil {
			return nil, errors.New("unable to download the GitHub release asset redirect")
		}
		if redirect == nil {
			return nil, errors.New("GitHub release asset redirect returned no response")
		}
		if redirect.Body != nil {
			defer redirect.Body.Close()
		}
		if redirect.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected GitHub release asset redirect response %d", redirect.StatusCode)
		}
		return readGitHubReleaseAssetBody(redirect, maxBytes)
	default:
		return nil, fmt.Errorf("unexpected GitHub release asset response %d", initial.StatusCode)
	}
}

func githubReleaseAssetAPIURL(releaseJSON io.Reader, repository, assetName string) (string, error) {
	if !githubReleaseRepositoryPattern.MatchString(repository) {
		return "", fmt.Errorf("invalid GitHub release asset repository: %s", repository)
	}
	if assetName == "" || strings.ContainsAny(assetName, "\r\n") {
		return "", errors.New("GitHub release asset name must be one line")
	}
	content, err := io.ReadAll(io.LimitReader(releaseJSON, upstreamMetadataMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read GitHub release metadata: %w", err)
	}
	if len(content) > upstreamMetadataMaxBytes {
		return "", errors.New("GitHub release metadata exceeds the size limit")
	}
	var document githubReleaseAssetDocument
	if err := json.Unmarshal(content, &document); err != nil {
		return "", fmt.Errorf("parse GitHub release metadata: %w", err)
	}
	var assetID string
	matches := 0
	for _, asset := range document.Assets {
		if asset.Name != assetName {
			continue
		}
		matches++
		candidate := string(asset.ID)
		if githubReleaseAssetIDPattern.MatchString(candidate) {
			assetID = candidate
		}
	}
	if matches != 1 {
		return "", fmt.Errorf("unable to resolve one release asset named %s", assetName)
	}
	if assetID == "" {
		return "", fmt.Errorf("unable to resolve a numeric release asset ID for %s", assetName)
	}
	return fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%s", repository, assetID), nil
}

func githubReleaseAssetRequest(ctx context.Context, client upstreamHTTPDoer, rawURL, token string, initial bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", upstreamUserAgent)
	if initial {
		req.Header.Set("Accept", "application/octet-stream")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return client.Do(req)
}

func validGitHubReleaseAssetRedirectURL(rawURL string) bool {
	if rawURL == "" || strings.ContainsAny(rawURL, " \t\r\n#") {
		return false
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" &&
		parsed.Host == "release-assets.githubusercontent.com" &&
		parsed.User == nil &&
		parsed.Fragment == "" &&
		parsed.RawPath == "" &&
		parsed.RawQuery != "" &&
		githubReleaseRedirectPath.MatchString(parsed.Path)
}

func readGitHubReleaseAssetBody(response *http.Response, maxBytes int64) ([]byte, error) {
	return readBoundedHTTPBody(response, maxBytes, "GitHub release asset")
}

func githubReleaseAssetLimit(assetClass string) (int64, error) {
	switch assetClass {
	case "checksum":
		return upstreamChecksumMaxBytes, nil
	case "archive":
		return upstreamMetadataMaxBytes, nil
	default:
		return 0, fmt.Errorf("unknown GitHub release asset class %q", assetClass)
	}
}

func githubAPIToken() (string, error) {
	raw := os.Getenv("WORKCELL_GITHUB_API_TOKEN")
	fromFile := false
	if raw == "" {
		raw = os.Getenv("GITHUB_TOKEN")
	}
	if raw == "" {
		raw = os.Getenv("GH_TOKEN")
	}
	if raw == "" && os.Getenv("WORKCELL_GITHUB_API_TOKEN_FILE") != "" {
		fromFile = true
		tokenFile := os.Getenv("WORKCELL_GITHUB_API_TOKEN_FILE")
		file, err := os.Open(tokenFile)
		if err != nil {
			return "", fmt.Errorf("read WORKCELL_GITHUB_API_TOKEN_FILE: %w", err)
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, githubAPIMaxTokenBytes+1))
		if err != nil {
			return "", fmt.Errorf("read WORKCELL_GITHUB_API_TOKEN_FILE: %w", err)
		}
		if len(content) > githubAPIMaxTokenBytes {
			return "", errors.New("WORKCELL_GITHUB_API_TOKEN_FILE exceeds the size limit")
		}
		raw = string(content)
	}
	if fromFile {
		raw = strings.TrimRight(raw, "\n")
	}
	if strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("GitHub API token must contain exactly one token line")
	}
	return raw, nil
}
