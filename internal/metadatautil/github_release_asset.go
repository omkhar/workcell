// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	githubReleaseAssetMaxMetadataBytes = 200 << 20
	githubReleaseAssetMaxBytes         = 64 << 10
	githubReleaseAssetMaxTokenBytes    = 16 << 10
	githubReleaseAssetTimeout          = 60 * time.Second
	githubReleaseAssetConnectTimeout   = 15 * time.Second
	githubReleaseAssetUserAgent        = "workcell-upstream-pins/1.0"
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

type githubReleaseAssetDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// FetchGitHubReleaseAsset selects one asset from releaseJSON and downloads it.
// The authenticated API request cannot redirect automatically. A validated
// release-assets URL gets one separate request without credentials.
func FetchGitHubReleaseAsset(ctx context.Context, releaseJSON io.Reader, repository, assetName string) ([]byte, error) {
	token, err := githubReleaseAssetToken()
	if err != nil {
		return nil, err
	}
	return fetchGitHubReleaseAsset(ctx, newGitHubReleaseAssetClient(), releaseJSON, repository, assetName, token)
}

func newGitHubReleaseAssetClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   githubReleaseAssetConnectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	return &http.Client{
		Transport: transport,
		Timeout:   githubReleaseAssetTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func fetchGitHubReleaseAsset(ctx context.Context, client githubReleaseAssetDoer, releaseJSON io.Reader, repository, assetName, token string) ([]byte, error) {
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
		return readGitHubReleaseAssetBody(initial)
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
		return readGitHubReleaseAssetBody(redirect)
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
	content, err := io.ReadAll(io.LimitReader(releaseJSON, githubReleaseAssetMaxMetadataBytes+1))
	if err != nil {
		return "", fmt.Errorf("read GitHub release metadata: %w", err)
	}
	if len(content) > githubReleaseAssetMaxMetadataBytes {
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

func githubReleaseAssetRequest(ctx context.Context, client githubReleaseAssetDoer, rawURL, token string, initial bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", githubReleaseAssetUserAgent)
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

func readGitHubReleaseAssetBody(response *http.Response) ([]byte, error) {
	if response.Body == nil {
		return nil, errors.New("GitHub release asset response has no body")
	}
	if response.ContentLength > githubReleaseAssetMaxBytes {
		return nil, errors.New("GitHub release asset exceeds the size limit")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, githubReleaseAssetMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read GitHub release asset: %w", err)
	}
	if len(content) > githubReleaseAssetMaxBytes {
		return nil, errors.New("GitHub release asset exceeds the size limit")
	}
	return content, nil
}

func githubReleaseAssetToken() (string, error) {
	raw := os.Getenv("WORKCELL_GITHUB_API_TOKEN")
	if tokenFile := os.Getenv("WORKCELL_GITHUB_API_TOKEN_FILE"); tokenFile != "" {
		file, err := os.Open(tokenFile)
		if err != nil {
			return "", fmt.Errorf("read WORKCELL_GITHUB_API_TOKEN_FILE: %w", err)
		}
		defer file.Close()
		content, err := io.ReadAll(io.LimitReader(file, githubReleaseAssetMaxTokenBytes+1))
		if err != nil {
			return "", fmt.Errorf("read WORKCELL_GITHUB_API_TOKEN_FILE: %w", err)
		}
		if len(content) > githubReleaseAssetMaxTokenBytes {
			return "", errors.New("WORKCELL_GITHUB_API_TOKEN_FILE exceeds the size limit")
		}
		raw = string(content)
	}
	raw = strings.TrimRight(raw, "\n")
	if strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("WORKCELL_GITHUB_API_TOKEN_FILE and WORKCELL_GITHUB_API_TOKEN must contain exactly one token line")
	}
	return raw, nil
}
