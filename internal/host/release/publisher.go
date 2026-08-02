// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	githubAPIOrigin, githubUploadsOrigin         = "https://api.github.com", "https://uploads.github.com"
	githubAPIVersion                             = "2022-11-28"
	maxGitHubResponseBytes                       = 4 * 1024 * 1024
	maxGitHubReleasePages, githubReleasePageSize = 100, 25
	maxGitHubCredentialBytes                     = 4096
)

type githubHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type githubReleasePublisher struct {
	client     githubHTTPClient
	afterStage func([]localAsset)
}

type githubReleaseAsset struct {
	ID     *int64  `json:"id"`
	Name   *string `json:"name"`
	Size   *int64  `json:"size"`
	State  *string `json:"state"`
	Digest *string `json:"digest"`
}

type githubTagRecord struct {
	Ref    string `json:"ref"`
	SHA    string `json:"sha"`
	Tag    string `json:"tag"`
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

// PublishGitHubRelease publishes the exact Workcell asset inventory through a
// draft GitHub release. Every source is copied into an unlinked read-only stage
// before the first API mutation, and uploads read only from those staged
// handles.
func PublishGitHubRelease(ctx context.Context, repository, token, tag string, expectedTag TagExpectation, paths []string) (releaseID int64, retErr error) {
	client := &http.Client{
		Timeout: 15 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("GitHub release publisher refuses HTTP redirects")
		},
	}
	publisher := githubReleasePublisher{client: client}
	return publisher.publish(ctx, repository, token, tag, expectedTag, paths)
}

func (publisher githubReleasePublisher) publish(ctx context.Context, repository, token, tag string, expectedTag TagExpectation, paths []string) (releaseID int64, retErr error) {
	if ctx == nil {
		return 0, inputErrorf("release publication context must not be nil")
	}
	if publisher.client == nil {
		return 0, errors.New("GitHub release publisher HTTP client must not be nil")
	}
	if err := validateRepository(repository); err != nil {
		return 0, err
	}
	if err := validateGitHubCredential(token); err != nil {
		return 0, err
	}
	policy, err := ClassifyTag(tag)
	if err != nil {
		return 0, err
	}
	if err := validateTagExpectation(expectedTag); err != nil {
		return 0, err
	}

	assets, err := inspectWorkcellAssets(tag, paths)
	if err != nil {
		return 0, err
	}
	defer func() {
		retErr = errors.Join(retErr, closeLocalAssets(assets))
	}()
	if publisher.afterStage != nil {
		publisher.afterStage(assets)
	}
	if err := publisher.verifyTagBinding(ctx, repository, token, tag, expectedTag); err != nil {
		return 0, err
	}

	releaseRecord, found, err := publisher.findRelease(ctx, repository, token, tag)
	if err != nil {
		return 0, err
	}
	if !found {
		releaseRecord, err = publisher.createDraft(ctx, repository, token, tag, policy)
		if err != nil {
			return 0, err
		}
	}
	if err := validateMutableDraft(repository, tag, policy, releaseRecord); err != nil {
		return 0, err
	}
	releaseID = *releaseRecord.ID

	existingAssets, err := decodeReleaseAssets(releaseRecord)
	if err != nil {
		return 0, err
	}
	if err := validateExistingDraftAssets(tag, existingAssets); err != nil {
		return 0, err
	}
	for _, existing := range existingAssets {
		if err := publisher.deleteAsset(ctx, repository, token, *existing.ID); err != nil {
			return 0, err
		}
	}
	for index := range assets {
		if err := publisher.uploadAsset(ctx, repository, token, releaseID, &assets[index]); err != nil {
			return 0, err
		}
	}

	beforePublish, err := publisher.getReleaseByID(ctx, repository, token, releaseID)
	if err != nil {
		return 0, err
	}
	if err := validateMutableDraft(repository, tag, policy, beforePublish); err != nil {
		return 0, err
	}
	if err := validateExactUploadedAssets(beforePublish, assets); err != nil {
		return 0, err
	}
	if err := publisher.verifyTagBinding(ctx, repository, token, tag, expectedTag); err != nil {
		return 0, fmt.Errorf("reverify release tag before publication: %w", err)
	}
	if err := closeLocalAssets(assets); err != nil {
		return 0, fmt.Errorf("close sealed release assets before publishing draft: %w", err)
	}

	published, err := publisher.publishDraft(ctx, repository, token, tag, policy, releaseID)
	if err != nil {
		return releaseID, fmt.Errorf(
			"GitHub release %q publication state is ambiguous after the publish request for id %d; inspect the hosted release before any recovery action: %w",
			tag,
			releaseID,
			err,
		)
	}
	if err := validatePublishedRelease(repository, tag, policy, releaseID, published, assets); err != nil {
		return releaseID, postPublicationVerificationError(tag, releaseID, err)
	}

	observed, err := publisher.getReleaseByID(ctx, repository, token, releaseID)
	if err != nil {
		return releaseID, postPublicationVerificationError(tag, releaseID, err)
	}
	if err := validatePublishedRelease(repository, tag, policy, releaseID, observed, assets); err != nil {
		return releaseID, postPublicationVerificationError(tag, releaseID, err)
	}
	if err := publisher.validateLatest(ctx, repository, token, tag, policy, releaseID, assets); err != nil {
		return releaseID, postPublicationVerificationError(tag, releaseID, err)
	}
	return releaseID, nil
}

func postPublicationVerificationError(tag string, releaseID int64, err error) error {
	return fmt.Errorf(
		"GitHub release %q was published as id %d, but final verification failed; do not rewrite or retry this tag, inspect the hosted state and recover with the next patch release if needed: %w",
		tag,
		releaseID,
		err,
	)
}

func validateGitHubCredential(token string) error {
	if token == "" || len(token) > maxGitHubCredentialBytes || strings.TrimSpace(token) != token {
		return inputErrorf("GITHUB_TOKEN must be a non-empty credential without surrounding whitespace")
	}
	for _, character := range token {
		if character <= 0x20 || character == 0x7f {
			return inputErrorf("GITHUB_TOKEN must not contain whitespace or control characters")
		}
	}
	return nil
}

func (publisher githubReleasePublisher) verifyTagBinding(
	ctx context.Context,
	repository, token, tag string,
	expected TagExpectation,
) error {
	tagBody, err := publisher.get(ctx, token, fmt.Sprintf(
		"%s/repos/%s/git/tags/%s", githubAPIOrigin, repository, expected.ObjectSHA,
	))
	if err != nil {
		return fmt.Errorf("read GitHub annotated tag %q: %w", tag, err)
	}
	var annotated githubTagRecord
	if err := json.Unmarshal(tagBody, &annotated); err != nil {
		return fmt.Errorf("decode GitHub annotated tag %q: %w", tag, err)
	}
	if annotated.SHA != expected.ObjectSHA || annotated.Tag != tag || annotated.Object.Type != "commit" {
		return fmt.Errorf("GitHub annotated tag %q does not bind the expected tag object, tag name, and one commit", tag)
	}
	refBody, err := publisher.get(ctx, token, fmt.Sprintf(
		"%s/repos/%s/git/ref/tags/%s", githubAPIOrigin, repository, url.PathEscape(tag),
	))
	if err != nil {
		return fmt.Errorf("read GitHub release tag ref %q: %w", tag, err)
	}
	var reference githubTagRecord
	if err := json.Unmarshal(refBody, &reference); err != nil {
		return fmt.Errorf("decode GitHub release tag ref %q: %w", tag, err)
	}
	return validateTagBinding(tag, expected, GitHubTagBinding{
		Ref:             reference.Ref,
		ObjectType:      reference.Object.Type,
		ObjectSHA:       reference.Object.SHA,
		PeeledCommitSHA: annotated.Object.SHA,
	})
}

func (publisher githubReleasePublisher) findRelease(ctx context.Context, repository, token, tag string) (listedRelease, bool, error) {
	var match listedRelease
	found := false
	for page := 1; page <= maxGitHubReleasePages; page++ {
		endpoint := fmt.Sprintf(
			"%s/repos/%s/releases?per_page=%d&page=%d",
			githubAPIOrigin,
			repository,
			githubReleasePageSize,
			page,
		)
		body, err := publisher.get(ctx, token, endpoint)
		if err != nil {
			return listedRelease{}, false, fmt.Errorf("list GitHub releases page %d: %w", page, err)
		}
		var rawItems []json.RawMessage
		if err := json.Unmarshal(body, &rawItems); err != nil {
			return listedRelease{}, false, fmt.Errorf("decode GitHub releases page %d: %w", page, err)
		}
		if rawItems == nil {
			return listedRelease{}, false, fmt.Errorf("decode GitHub releases page %d: expected JSON array, got null", page)
		}
		for index, raw := range rawItems {
			var item listedRelease
			if err := json.Unmarshal(raw, &item); err != nil {
				return listedRelease{}, false, fmt.Errorf("decode GitHub release page %d item %d: %w", page, index, err)
			}
			if err := validateListedRelease(item); err != nil {
				return listedRelease{}, false, fmt.Errorf("malformed GitHub release page %d item %d: %w", page, index, err)
			}
			if *item.TagName != tag {
				continue
			}
			if found {
				return listedRelease{}, false, fmt.Errorf("GitHub releases contain duplicate exact tag %q", tag)
			}
			match = item
			found = true
		}
		if len(rawItems) < githubReleasePageSize {
			return match, found, nil
		}
	}
	return listedRelease{}, false, fmt.Errorf("GitHub release list exceeded the fail-closed limit of %d pages", maxGitHubReleasePages)
}

func (publisher githubReleasePublisher) createDraft(ctx context.Context, repository, token, tag string, policy TagPolicy) (listedRelease, error) {
	payload := createPayload{
		TagName:              tag,
		Draft:                true,
		Prerelease:           policy.Prerelease,
		MakeLatest:           "false",
		GenerateReleaseNotes: true,
	}
	body, err := publisher.requestJSON(
		ctx,
		token,
		http.MethodPost,
		fmt.Sprintf("%s/repos/%s/releases", githubAPIOrigin, repository),
		payload,
		http.StatusCreated,
	)
	if err != nil {
		return listedRelease{}, fmt.Errorf("create GitHub draft release %q: %w", tag, err)
	}
	return decodeReleaseRecord(body, "created draft")
}

func (publisher githubReleasePublisher) deleteAsset(ctx context.Context, repository, token string, assetID int64) error {
	endpoint := fmt.Sprintf("%s/repos/%s/releases/assets/%d", githubAPIOrigin, repository, assetID)
	if _, err := publisher.request(ctx, token, http.MethodDelete, endpoint, "", nil, 0, http.StatusNoContent); err != nil {
		return fmt.Errorf("delete existing GitHub release asset %d: %w", assetID, err)
	}
	return nil
}

func (publisher githubReleasePublisher) uploadAsset(ctx context.Context, repository, token string, releaseID int64, asset *localAsset) error {
	if asset == nil {
		return errors.New("release asset must not be nil")
	}
	reader, err := rewindLocalAssetReader(asset)
	if err != nil {
		return err
	}
	query := url.Values{"name": []string{asset.name}}
	endpoint := fmt.Sprintf(
		"%s/repos/%s/releases/%d/assets?%s",
		githubUploadsOrigin,
		repository,
		releaseID,
		query.Encode(),
	)
	body, err := publisher.request(
		ctx,
		token,
		http.MethodPost,
		endpoint,
		"application/octet-stream",
		reader,
		asset.size,
		http.StatusCreated,
	)
	if err != nil {
		return fmt.Errorf("upload GitHub release asset %q: %w", asset.name, err)
	}
	var uploaded githubReleaseAsset
	if err := json.Unmarshal(body, &uploaded); err != nil {
		return fmt.Errorf("decode uploaded GitHub release asset %q: %w", asset.name, err)
	}
	return validateUploadedAsset(uploaded, asset)
}

func (publisher githubReleasePublisher) getReleaseByID(ctx context.Context, repository, token string, releaseID int64) (listedRelease, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/releases/%d", githubAPIOrigin, repository, releaseID)
	body, err := publisher.get(ctx, token, endpoint)
	if err != nil {
		return listedRelease{}, fmt.Errorf("read GitHub release %d: %w", releaseID, err)
	}
	return decodeReleaseRecord(body, "release")
}

func (publisher githubReleasePublisher) publishDraft(ctx context.Context, repository, token, tag string, policy TagPolicy, releaseID int64) (listedRelease, error) {
	makeLatest := "false"
	if policy.MakeLatest {
		makeLatest = "true"
	}
	payload := publishPayload{
		Prerelease: policy.Prerelease,
		MakeLatest: makeLatest,
	}
	body, err := publisher.requestJSON(
		ctx,
		token,
		http.MethodPatch,
		fmt.Sprintf("%s/repos/%s/releases/%d", githubAPIOrigin, repository, releaseID),
		payload,
		http.StatusOK,
	)
	if err != nil {
		return listedRelease{}, fmt.Errorf("publish GitHub release %q: %w", tag, err)
	}
	return decodeReleaseRecord(body, "published release")
}

func (publisher githubReleasePublisher) validateLatest(
	ctx context.Context,
	repository, token, tag string,
	policy TagPolicy,
	releaseID int64,
	assets []localAsset,
) error {
	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIOrigin, repository)
	expectedStatuses := []int{http.StatusOK}
	if !policy.MakeLatest {
		expectedStatuses = append(expectedStatuses, http.StatusNotFound)
	}
	body, status, err := publisher.requestOneOf(ctx, token, http.MethodGet, endpoint, "", nil, 0, expectedStatuses...)
	if err != nil {
		return fmt.Errorf("read GitHub latest release after publishing %q: %w", tag, err)
	}
	if status == http.StatusNotFound {
		return nil
	}
	latest, err := decodeReleaseRecord(body, "latest release")
	if err != nil {
		return err
	}
	if policy.MakeLatest {
		return validatePublishedRelease(repository, tag, policy, releaseID, latest, assets)
	}
	latestPolicy, err := ClassifyTag(*latest.TagName)
	if err != nil || !latestPolicy.MakeLatest || *latest.Draft || *latest.Prerelease || !*latest.Immutable {
		return fmt.Errorf("GitHub latest release tag %q is not an immutable published final release after release-candidate publication", *latest.TagName)
	}
	return validateReleaseUploadURL(repository, latest)
}

func (publisher githubReleasePublisher) requestJSON(ctx context.Context, token, method, endpoint string, payload any, expectedStatus int) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode GitHub release request: %w", err)
	}
	return publisher.request(ctx, token, method, endpoint, "application/json", bytes.NewReader(body), int64(len(body)), expectedStatus)
}

func (publisher githubReleasePublisher) get(ctx context.Context, token, endpoint string) ([]byte, error) {
	return publisher.request(ctx, token, http.MethodGet, endpoint, "", nil, 0, http.StatusOK)
}

func (publisher githubReleasePublisher) request(
	ctx context.Context,
	token, method, endpoint, contentType string,
	body io.Reader,
	contentLength int64,
	expectedStatus int,
) ([]byte, error) {
	responseBody, _, err := publisher.requestOneOf(ctx, token, method, endpoint, contentType, body, contentLength, expectedStatus)
	return responseBody, err
}

func (publisher githubReleasePublisher) requestOneOf(
	ctx context.Context,
	token, method, endpoint, contentType string,
	body io.Reader,
	contentLength int64,
	expectedStatuses ...int,
) ([]byte, int, error) {
	if err := validateGitHubRequestURL(endpoint); err != nil {
		return nil, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, 0, fmt.Errorf("create GitHub API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
		request.ContentLength = contentLength
	}
	response, err := publisher.client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("perform GitHub API request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxGitHubResponseBytes+1))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read GitHub API response: %w", err)
	}
	if len(responseBody) > maxGitHubResponseBytes {
		return nil, response.StatusCode, fmt.Errorf("GitHub API response exceeds %d bytes", maxGitHubResponseBytes)
	}
	for _, expectedStatus := range expectedStatuses {
		if response.StatusCode == expectedStatus {
			return responseBody, response.StatusCode, nil
		}
	}
	statusError := fmt.Sprintf("GitHub API returned HTTP %d, want %s", response.StatusCode, formatHTTPStatuses(expectedStatuses))
	if message := githubAPIErrorMessage(responseBody); message != "" {
		statusError += ": " + message
	}
	return nil, response.StatusCode, errors.New(statusError)
}

func validateGitHubRequestURL(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse GitHub API endpoint: %w", err)
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("GitHub API endpoint must be an HTTPS URL without userinfo or fragment")
	}
	if parsed.Host != "api.github.com" && parsed.Host != "uploads.github.com" {
		return fmt.Errorf("GitHub API endpoint host %q is not trusted", parsed.Host)
	}
	return nil
}

func formatHTTPStatuses(statuses []int) string {
	formatted := make([]string, 0, len(statuses))
	for _, status := range statuses {
		formatted = append(formatted, strconv.Itoa(status))
	}
	return strings.Join(formatted, " or ")
}

func githubAPIErrorMessage(body []byte) string {
	var response struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ""
	}
	message := strings.TrimSpace(response.Message)
	message = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, message)
	const maxMessageRunes = 512
	runes := []rune(message)
	if len(runes) > maxMessageRunes {
		message = string(runes[:maxMessageRunes]) + "…"
	}
	return strings.Join(strings.Fields(message), " ")
}

func decodeReleaseRecord(body []byte, state string) (listedRelease, error) {
	var record listedRelease
	if err := json.Unmarshal(body, &record); err != nil {
		return listedRelease{}, fmt.Errorf("decode GitHub %s response: %w", state, err)
	}
	if err := validateListedRelease(record); err != nil {
		return listedRelease{}, fmt.Errorf("malformed GitHub %s response: %w", state, err)
	}
	return record, nil
}

func validateMutableDraft(repository, tag string, policy TagPolicy, record listedRelease) error {
	if err := validateListedRelease(record); err != nil {
		return err
	}
	if *record.TagName != tag {
		return fmt.Errorf("GitHub draft release tag_name = %q, want %q", *record.TagName, tag)
	}
	if !*record.Draft {
		if *record.Immutable {
			return fmt.Errorf("GitHub release %q is already published and immutable; patch main and cut the next patch release instead", tag)
		}
		return fmt.Errorf("GitHub release %q is already published; Workcell only uploads assets to draft releases, so patch main and cut the next patch release instead", tag)
	}
	if *record.Immutable {
		return fmt.Errorf("GitHub draft release %q unexpectedly reports immutable = true", tag)
	}
	if *record.Prerelease != policy.Prerelease {
		return fmt.Errorf("GitHub draft release prerelease = %t, want %t for %s tag %q", *record.Prerelease, policy.Prerelease, policy.Kind, tag)
	}
	return validateReleaseUploadURL(repository, record)
}

func decodeReleaseAssets(record listedRelease) ([]githubReleaseAsset, error) {
	if record.Assets == nil {
		return nil, errors.New("GitHub release response is missing assets")
	}
	assets := make([]githubReleaseAsset, 0, len(*record.Assets))
	for index, raw := range *record.Assets {
		var asset githubReleaseAsset
		if err := json.Unmarshal(raw, &asset); err != nil {
			return nil, fmt.Errorf("decode GitHub release asset %d: %w", index, err)
		}
		if asset.ID == nil || *asset.ID <= 0 || asset.Name == nil || *asset.Name == "" {
			return nil, fmt.Errorf("GitHub release asset %d is missing a valid id or name", index)
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func validateExistingDraftAssets(tag string, assets []githubReleaseAsset) error {
	expected := make(map[string]struct{}, maxReleaseAssetCount)
	for _, name := range expectedWorkcellAssetNames(tag) {
		expected[name] = struct{}{}
	}
	seenNames := make(map[string]struct{}, len(assets))
	seenIDs := make(map[int64]struct{}, len(assets))
	for _, asset := range assets {
		if _, ok := expected[*asset.Name]; !ok {
			return fmt.Errorf("GitHub draft release contains unexpected asset %q; remove or disposition it before publication", *asset.Name)
		}
		if _, duplicate := seenNames[*asset.Name]; duplicate {
			return fmt.Errorf("GitHub draft release contains duplicate asset name %q", *asset.Name)
		}
		if _, duplicate := seenIDs[*asset.ID]; duplicate {
			return fmt.Errorf("GitHub draft release contains duplicate asset id %d", *asset.ID)
		}
		seenNames[*asset.Name] = struct{}{}
		seenIDs[*asset.ID] = struct{}{}
	}
	return nil
}

func validateUploadedAsset(uploaded githubReleaseAsset, expected *localAsset) error {
	if uploaded.ID == nil || *uploaded.ID <= 0 {
		return fmt.Errorf("uploaded GitHub release asset %q is missing a valid id", expected.name)
	}
	if uploaded.Name == nil {
		return fmt.Errorf("uploaded GitHub release asset is missing name, want %q", expected.name)
	}
	if *uploaded.Name != expected.name {
		return fmt.Errorf("uploaded GitHub release asset name = %q, want %q", *uploaded.Name, expected.name)
	}
	if uploaded.Size == nil {
		return fmt.Errorf("uploaded GitHub release asset %q is missing size, want %d", expected.name, expected.size)
	}
	if *uploaded.Size != expected.size {
		return fmt.Errorf("uploaded GitHub release asset %q size = %d, want %d", expected.name, *uploaded.Size, expected.size)
	}
	if uploaded.State == nil {
		return fmt.Errorf("uploaded GitHub release asset %q is missing state, want uploaded", expected.name)
	}
	if *uploaded.State != "uploaded" {
		return fmt.Errorf("uploaded GitHub release asset %q state = %q, want uploaded", expected.name, *uploaded.State)
	}
	wantDigest := "sha256:" + expected.sha256
	if uploaded.Digest == nil {
		return fmt.Errorf("uploaded GitHub release asset %q is missing digest, want %q", expected.name, wantDigest)
	}
	if *uploaded.Digest != wantDigest {
		return fmt.Errorf("uploaded GitHub release asset %q digest = %q, want %q", expected.name, *uploaded.Digest, wantDigest)
	}
	return nil
}

func validateExactUploadedAssets(record listedRelease, expected []localAsset) error {
	observed, err := decodeReleaseAssets(record)
	if err != nil {
		return err
	}
	if len(observed) != len(expected) {
		return fmt.Errorf("GitHub release contains %d assets, want exact staged inventory of %d", len(observed), len(expected))
	}
	byName := make(map[string]githubReleaseAsset, len(observed))
	for _, asset := range observed {
		if _, duplicate := byName[*asset.Name]; duplicate {
			return fmt.Errorf("GitHub release contains duplicate asset name %q", *asset.Name)
		}
		byName[*asset.Name] = asset
	}
	for index := range expected {
		asset, ok := byName[expected[index].name]
		if !ok {
			return fmt.Errorf("GitHub release is missing staged asset %q", expected[index].name)
		}
		if err := validateUploadedAsset(asset, &expected[index]); err != nil {
			return err
		}
	}
	return nil
}

func validatePublishedRelease(
	repository, tag string,
	policy TagPolicy,
	releaseID int64,
	record listedRelease,
	assets []localAsset,
) error {
	if err := validateListedRelease(record); err != nil {
		return err
	}
	if *record.ID != releaseID || *record.TagName != tag {
		return fmt.Errorf("published GitHub release identity is id %d tag %q, want id %d tag %q", *record.ID, *record.TagName, releaseID, tag)
	}
	if *record.Draft {
		return fmt.Errorf("published GitHub release %q still reports draft = true", tag)
	}
	if *record.Prerelease != policy.Prerelease {
		return fmt.Errorf("published GitHub release prerelease = %t, want %t for %s tag %q", *record.Prerelease, policy.Prerelease, policy.Kind, tag)
	}
	if !*record.Immutable {
		return fmt.Errorf("published GitHub release %q is not immutable", tag)
	}
	if err := validateReleaseUploadURL(repository, record); err != nil {
		return fmt.Errorf("published %w", err)
	}
	return validateExactUploadedAssets(record, assets)
}
