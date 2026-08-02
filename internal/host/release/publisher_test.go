// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type publisherClient struct {
	t                                *testing.T
	tag, repository, token           string
	releaseID                        int64
	stagingDone                      *bool
	draft, immutable, existing       bool
	assets                           []githubReleaseAsset
	uploaded                         map[string][]byte
	latestStatus                     int
	latestBody                       any
	beforePublish                    func()
	disableImmutableReleases         bool
	failTagRebindAfter, bindingReads int
	requests                         []string
}

func (client *publisherClient) Do(request *http.Request) (*http.Response, error) {
	client.t.Helper()
	if client.stagingDone != nil && !*client.stagingDone {
		client.t.Fatal("GitHub request started before every asset was staged")
	}
	if request.Header.Get("Authorization") != "Bearer "+client.token || request.Header.Get("X-GitHub-Api-Version") != githubAPIVersion {
		client.t.Fatalf("unexpected GitHub request headers")
	}
	client.requests = append(client.requests, request.Method+" "+request.URL.Host+request.URL.Path)
	policy, err := ClassifyTag(client.tag)
	if err != nil {
		client.t.Fatal(err)
	}
	makeLatest := "false"
	if policy.MakeLatest {
		makeLatest = "true"
	}
	listPath := "/repos/" + client.repository + "/releases"
	releasePath := fmt.Sprintf("%s/%d", listPath, client.releaseID)
	tagRefPath := "/repos/" + client.repository + "/git/ref/tags/" + client.tag
	immutableReleasesPath := "/repos/" + client.repository + "/immutable-releases"
	switch {
	case request.Method == http.MethodGet && request.URL.Host == "api.github.com" && request.URL.Path == "/repos/"+client.repository+"/git/tags/"+testTagObjectSHA:
		return jsonResponse(http.StatusOK, map[string]any{"sha": testTagObjectSHA, "tag": client.tag, "object": map[string]any{"type": "commit", "sha": testTagCommitSHA}}), nil
	case request.Method == http.MethodGet && request.URL.Host == "api.github.com" && request.URL.Path == tagRefPath:
		client.bindingReads++
		objectSHA := testTagObjectSHA
		if client.failTagRebindAfter > 0 && client.bindingReads > client.failTagRebindAfter {
			objectSHA = strings.Repeat("c", 40)
		}
		return jsonResponse(http.StatusOK, map[string]any{"ref": "refs/tags/" + client.tag, "object": map[string]any{"type": "tag", "sha": objectSHA}}), nil
	case request.Method == http.MethodGet && request.URL.Host == "api.github.com" && request.URL.Path == listPath:
		if request.URL.Query().Get("per_page") != strconv.Itoa(githubReleasePageSize) || request.URL.Query().Get("page") != "1" {
			client.t.Fatalf("unexpected release-list query %q", request.URL.RawQuery)
		}
		if client.existing {
			return jsonResponse(http.StatusOK, []any{client.record()}), nil
		}
		return jsonResponse(http.StatusOK, []any{}), nil
	case request.Method == http.MethodPost && request.URL.Host == "api.github.com" && request.URL.Path == listPath:
		var payload createPayload
		decodeJSONRequest(client.t, request, &payload)
		if payload.TagName != client.tag || !payload.Draft || payload.Prerelease != policy.Prerelease || payload.MakeLatest != "false" || !payload.GenerateReleaseNotes {
			client.t.Fatalf("unexpected create payload %#v", payload)
		}
		client.draft, client.immutable = true, false
		return jsonResponse(http.StatusCreated, client.record()), nil
	case request.Method == http.MethodDelete && request.URL.Host == "api.github.com" && strings.HasPrefix(request.URL.Path, listPath+"/assets/"):
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(request.URL.Path, listPath+"/assets/"), 10, 64)
		if parseErr != nil {
			client.t.Fatal(parseErr)
		}
		for index := range client.assets {
			if *client.assets[index].ID == id {
				client.assets = append(client.assets[:index], client.assets[index+1:]...)
				return jsonResponse(http.StatusNoContent, nil), nil
			}
		}
		client.t.Fatalf("delete requested unknown asset %d", id)
	case request.Method == http.MethodPost && request.URL.Host == "uploads.github.com" && request.URL.Path == releasePath+"/assets":
		name := request.URL.Query().Get("name")
		content, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			return nil, readErr
		}
		if name == "" || request.ContentLength != int64(len(content)) {
			client.t.Fatalf("invalid upload name=%q length=%d", name, request.ContentLength)
		}
		client.uploaded[name] = append([]byte(nil), content...)
		digest := sha256.Sum256(content)
		id, size, state, digestText := int64(len(client.assets)+100), int64(len(content)), "uploaded", "sha256:"+hex.EncodeToString(digest[:])
		asset := githubReleaseAsset{ID: &id, Name: &name, Size: &size, State: &state, Digest: &digestText}
		client.assets = append(client.assets, asset)
		return jsonResponse(http.StatusCreated, asset), nil
	case request.Method == http.MethodGet && request.URL.Host == "api.github.com" && request.URL.Path == releasePath:
		return jsonResponse(http.StatusOK, client.record()), nil
	case request.Method == http.MethodGet && request.URL.Host == "api.github.com" && request.URL.Path == immutableReleasesPath:
		if client.disableImmutableReleases {
			return jsonResponse(http.StatusOK, map[string]bool{"enabled": false}), nil
		}
		return jsonResponse(http.StatusOK, map[string]bool{"enabled": true}), nil
	case request.Method == http.MethodPatch && request.URL.Host == "api.github.com" && request.URL.Path == releasePath:
		if client.beforePublish != nil {
			client.beforePublish()
		}
		var payload publishPayload
		decodeJSONRequest(client.t, request, &payload)
		if payload.Draft || payload.Prerelease != policy.Prerelease || payload.MakeLatest != makeLatest {
			client.t.Fatalf("unexpected publish payload %#v", payload)
		}
		client.draft, client.immutable = false, true
		return jsonResponse(http.StatusOK, client.record()), nil
	case request.Method == http.MethodGet && request.URL.Host == "api.github.com" && request.URL.Path == listPath+"/latest":
		if client.latestStatus != 0 {
			return jsonResponse(client.latestStatus, client.latestBody), nil
		}
		return jsonResponse(http.StatusOK, client.record()), nil
	default:
		client.t.Fatalf("unexpected GitHub request %s %s", request.Method, request.URL)
	}
	return nil, nil
}

func (client *publisherClient) record() map[string]any {
	policy, err := ClassifyTag(client.tag)
	if err != nil {
		client.t.Fatal(err)
	}
	return releaseRecord(client.repository, client.tag, client.releaseID, client.draft, policy.Prerelease, client.immutable, client.assets)
}

func releaseRecord(repository, tag string, id int64, draft, prerelease, immutable bool, assets any) map[string]any {
	return map[string]any{"id": id, "upload_url": fmt.Sprintf("%s/repos/%s/releases/%d/assets{?name,label}", githubUploadsOrigin, repository, id), "tag_name": tag, "draft": draft, "prerelease": prerelease, "immutable": immutable, "assets": assets}
}

func jsonResponse(status int, value any) *http.Response {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}
}

func decodeJSONRequest(t *testing.T, request *http.Request, destination any) {
	t.Helper()
	if request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type %q", request.Header.Get("Content-Type"))
	}
	if err := json.NewDecoder(request.Body).Decode(destination); err != nil {
		t.Fatal(err)
	}
}

func writeAssets(t *testing.T, tag string) ([]string, map[string][]byte) {
	t.Helper()
	paths, content := make([]string, 0, maxReleaseAssetCount), make(map[string][]byte, maxReleaseAssetCount)
	root := t.TempDir()
	for index, name := range expectedWorkcellAssetNames(tag) {
		value := []byte(fmt.Sprintf("sealed asset %02d: %s\n", index, name))
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, value, 0o600); err != nil {
			t.Fatal(err)
		}
		paths, content[name] = append(paths, path), value
	}
	return paths, content
}

func TestPublisherSealsFinalAndReleaseCandidateAssets(t *testing.T) {
	const repository, token = "example/workcell", "github_pat_test"
	for _, tc := range []struct {
		name, tag  string
		latestCode int
		latestBody any
	}{
		{name: "final", tag: "v1.0.0"},
		{name: "rc without final", tag: "v1.0.0-rc.3", latestCode: http.StatusNotFound, latestBody: map[string]string{"message": "Not Found"}},
		{name: "rc preserves final", tag: "v1.0.0-rc.3", latestCode: http.StatusOK, latestBody: releaseRecord(repository, "v0.9.0", 41, false, false, true, []any{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, original := writeAssets(t, tc.tag)
			staged := false
			client := &publisherClient{
				t: t, tag: tc.tag, repository: repository, token: token, releaseID: 42,
				stagingDone: &staged, assets: []githubReleaseAsset{}, uploaded: make(map[string][]byte),
				latestStatus: tc.latestCode, latestBody: tc.latestBody,
			}
			var handles []localAsset
			client.beforePublish = func() {
				for index := range handles {
					if _, err := handles[index].content.Seek(0, io.SeekStart); err == nil {
						t.Fatalf("sealed asset %q remains open before publication", handles[index].name)
					}
				}
			}
			publisher := githubReleasePublisher{client: client, afterStage: func(assets []localAsset) {
				handles = append([]localAsset(nil), assets...)
				for _, path := range paths {
					if err := os.WriteFile(path, []byte("MUTATED SOURCE\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				staged = true
			}}
			if id, err := publisher.publish(context.Background(), repository, token, tc.tag, TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}, paths); err != nil || id != 42 {
				t.Fatalf("publish() = id %d error %v", id, err)
			}
			for name, want := range original {
				if got := client.uploaded[name]; string(got) != string(want) {
					t.Fatalf("uploaded %q = %q, want sealed bytes %q", name, got, want)
				}
			}
			if client.draft || !client.immutable {
				t.Fatalf("finished draft=%t immutable=%t", client.draft, client.immutable)
			}
			wantSuffix := "GET api.github.com/repos/example/workcell/git/tags/" + testTagObjectSHA +
				"\nGET api.github.com/repos/example/workcell/git/ref/tags/" + tc.tag +
				"\nGET api.github.com/repos/example/workcell/immutable-releases" +
				"\nPATCH api.github.com/repos/example/workcell/releases/42\nGET api.github.com/repos/example/workcell/git/tags/" + testTagObjectSHA + "\nGET api.github.com/repos/example/workcell/git/ref/tags/" + tc.tag + "\nGET api.github.com/repos/example/workcell/releases/42\nGET api.github.com/repos/example/workcell/releases/latest"
			if !strings.HasSuffix(strings.Join(client.requests, "\n"), wantSuffix) {
				t.Fatalf("requests = %q, want final verification suffix %q", client.requests, wantSuffix)
			}
		})
	}
}

func TestPublisherMovedTagAfterPublicationRequiresNextPatchRecovery(t *testing.T) {
	paths, _ := writeAssets(t, "v1.0.0")
	client := &publisherClient{
		t: t, tag: "v1.0.0", repository: "example/workcell", token: "token", releaseID: 42,
		assets: []githubReleaseAsset{}, uploaded: make(map[string][]byte), failTagRebindAfter: 2,
	}
	id, err := (githubReleasePublisher{client: client}).publish(context.Background(), client.repository, client.token, client.tag, TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}, paths)
	for _, fragment := range []string{"was published", "do not rewrite or retry this tag", "next patch release", "annotated tag object SHA"} {
		if id != 42 || err == nil || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("publish() error = %v, want %q", err, fragment)
		}
	}
}

func TestPublisherRejectsMovedTagBeforePublication(t *testing.T) {
	paths, _ := writeAssets(t, "v1.0.0")
	client := &publisherClient{t: t, tag: "v1.0.0", repository: "example/workcell", token: "token", releaseID: 42,
		assets: []githubReleaseAsset{}, uploaded: make(map[string][]byte), failTagRebindAfter: 1}
	_, err := (githubReleasePublisher{client: client}).publish(context.Background(), client.repository, client.token, client.tag, TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}, paths)
	if err == nil || !strings.Contains(err.Error(), "reverify release tag") || slices.Contains(client.requests, "PATCH api.github.com/repos/example/workcell/releases/42") {
		t.Fatalf("publish() error=%v requests=%q", err, client.requests)
	}
}

func TestPublisherRejectsDisabledImmutableReleasesBeforePublication(t *testing.T) {
	paths, _ := writeAssets(t, "v1.0.0")
	client := &publisherClient{t: t, tag: "v1.0.0", repository: "example/workcell", token: "token", releaseID: 42,
		assets: []githubReleaseAsset{}, uploaded: make(map[string][]byte), disableImmutableReleases: true}
	_, err := (githubReleasePublisher{client: client}).publish(context.Background(), client.repository, client.token, client.tag, TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}, paths)
	if err == nil || !strings.Contains(err.Error(), "enabled = true") || slices.Contains(client.requests, "PATCH api.github.com/repos/example/workcell/releases/42") {
		t.Fatalf("publish() error=%v requests=%q", err, client.requests)
	}
}

func TestPublisherReplacesExpectedStaleDraftAsset(t *testing.T) {
	const tag, repository = "v1.0.0", "example/workcell"
	paths, _ := writeAssets(t, tag)
	id, name, size, state := int64(99), expectedWorkcellAssetNames(tag)[0], int64(0), "starter"
	client := &publisherClient{
		t: t, tag: tag, repository: repository, token: "token", releaseID: 42, draft: true, existing: true,
		assets: []githubReleaseAsset{{ID: &id, Name: &name, Size: &size, State: &state}}, uploaded: make(map[string][]byte),
	}
	if _, err := (githubReleasePublisher{client: client}).publish(context.Background(), repository, "token", tag, TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}, paths); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(client.requests, "DELETE api.github.com/repos/example/workcell/releases/assets/99") {
		t.Fatalf("requests = %q, want stale asset deletion", client.requests)
	}
}

type staticClient struct {
	requests, status int
	body             any
}

func (client *staticClient) Do(request *http.Request) (*http.Response, error) {
	client.requests++
	if marker := "/git/ref/tags/"; strings.Contains(request.URL.Path, marker) {
		tag := strings.TrimPrefix(request.URL.Path[strings.Index(request.URL.Path, marker):], marker)
		return jsonResponse(http.StatusOK, map[string]any{"ref": "refs/tags/" + tag, "object": map[string]any{"type": "tag", "sha": testTagObjectSHA}}), nil
	}
	if strings.HasSuffix(request.URL.Path, "/git/tags/"+testTagObjectSHA) {
		return jsonResponse(http.StatusOK, map[string]any{"sha": testTagObjectSHA, "tag": "v1.0.0", "object": map[string]any{"type": "commit", "sha": testTagCommitSHA}}), nil
	}
	return jsonResponse(client.status, client.body), nil
}

type listClient struct {
	t     *testing.T
	pages map[int][]any
}

func (client *listClient) Do(request *http.Request) (*http.Response, error) {
	client.t.Helper()
	page, err := strconv.Atoi(request.URL.Query().Get("page"))
	if err != nil || request.Method != http.MethodGet || request.URL.Query().Get("per_page") != strconv.Itoa(githubReleasePageSize) {
		client.t.Fatalf("unexpected list request %s", request.URL)
	}
	return jsonResponse(http.StatusOK, client.pages[page]), nil
}

func fullPage(repository string) []any {
	page := make([]any, githubReleasePageSize)
	for index := range page {
		page[index] = releaseRecord(repository, fmt.Sprintf("unrelated-%d", index), int64(index+1), true, false, false, []any{})
	}
	return page
}

func TestFindReleasePaginationAndDuplicateTags(t *testing.T) {
	const repository, tag = "example/workcell", "v1.0.0"
	t.Run("second page match", func(t *testing.T) {
		client := &listClient{t: t, pages: map[int][]any{1: fullPage(repository), 2: {releaseRecord(repository, tag, 4242, true, false, false, []any{})}}}
		record, found, err := (githubReleasePublisher{client: client}).findRelease(context.Background(), repository, "token", tag)
		if err != nil || !found || record.ID == nil || *record.ID != 4242 {
			t.Fatalf("findRelease() = found %t record %#v error %v", found, record, err)
		}
	})
	t.Run("duplicate across pages", func(t *testing.T) {
		first := fullPage(repository)
		first[0] = releaseRecord(repository, tag, 41, true, false, false, []any{})
		client := &listClient{t: t, pages: map[int][]any{1: first, 2: {releaseRecord(repository, tag, 42, true, false, false, []any{})}}}
		if _, _, err := (githubReleasePublisher{client: client}).findRelease(context.Background(), repository, "token", tag); err == nil || !strings.Contains(err.Error(), "duplicate exact tag") {
			t.Fatalf("findRelease() error = %v, want duplicate rejection", err)
		}
	})
}

func TestPublisherRejectsUnsafeStateBeforeMutation(t *testing.T) {
	const repository, tag = "example/workcell", "v1.0.0"
	paths, _ := writeAssets(t, tag)
	for _, tc := range []struct {
		name   string
		record map[string]any
		want   string
	}{
		{name: "unexpected asset", record: releaseRecord(repository, tag, 42, true, false, false, []any{map[string]any{"id": 100, "name": "unreviewed.bin"}}), want: "unexpected asset"},
		{name: "published mutable", record: releaseRecord(repository, tag, 42, false, false, false, []any{}), want: "already published"},
		{name: "published immutable", record: releaseRecord(repository, tag, 42, false, false, true, []any{}), want: "already published"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &staticClient{status: http.StatusOK, body: []any{tc.record}}
			_, err := (githubReleasePublisher{client: client}).publish(context.Background(), repository, "token", tag, TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}, paths)
			if err == nil || !strings.Contains(err.Error(), tc.want) || client.requests != 3 {
				t.Fatalf("publish() error=%v requests=%d", err, client.requests)
			}
		})
	}
}

func TestPublisherRejectsInputsBeforeNetwork(t *testing.T) {
	paths, _ := writeAssets(t, "v1.0.0")
	for _, tc := range []struct {
		name, tag string
		paths     []string
		want      string
	}{
		{name: "missing asset", tag: "v1.0.0", paths: paths[:len(paths)-1], want: "missing required basename"},
		{name: "unsupported tag", tag: "v1.0.0-beta.1", want: "unsupported release tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &staticClient{status: http.StatusInternalServerError}
			_, err := (githubReleasePublisher{client: client}).publish(context.Background(), "example/workcell", "token", tc.tag, TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}, tc.paths)
			if err == nil || !strings.Contains(err.Error(), tc.want) || client.requests != 0 {
				t.Fatalf("publish() error=%v requests=%d", err, client.requests)
			}
		})
	}
}

func TestGitHubRequestURLTrust(t *testing.T) {
	for _, endpoint := range []string{"http://api.github.com/x", "https://api.github.com.evil.test/x", "https://user@api.github.com/x", "https://api.github.com/x#fragment", "://bad"} {
		if err := validateGitHubRequestURL(endpoint); err == nil {
			t.Fatalf("validateGitHubRequestURL(%q) succeeded", endpoint)
		}
	}
	for _, endpoint := range []string{"https://api.github.com/x", "https://uploads.github.com/x"} {
		if err := validateGitHubRequestURL(endpoint); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGitHubCredentialPolicy(t *testing.T) {
	for _, credential := range []string{"", " token", "token ", "token\nvalue", strings.Repeat("x", maxGitHubCredentialBytes+1)} {
		if err := validateGitHubCredential(credential); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("validateGitHubCredential(%q) error = %v, want invalid input", credential, err)
		}
	}
}

func TestMutableDraftBindsRepositoryUploadURL(t *testing.T) {
	record := releaseRecord("attacker/workcell", "v1.0.0", 42, true, false, false, []any{})
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReleaseRecord(body, "test draft")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ClassifyTag("v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseUploadURL("ATTACKER/WORKCELL", decoded); err != nil {
		t.Fatal(err)
	}
	confusable := strings.Replace(*decoded.UploadURL, "uploads", "uploadſ", 1)
	decoded.UploadURL = &confusable
	if err := validateReleaseUploadURL("ATTACKER/WORKCELL", decoded); err == nil {
		t.Fatal("Unicode-confusable upload host accepted")
	}
	if err := validateMutableDraft("example/workcell", "v1.0.0", policy, decoded); err == nil || !strings.Contains(err.Error(), "upload_url") {
		t.Fatalf("validateMutableDraft() error = %v, want repository-bound upload URL rejection", err)
	}
}

func TestUploadedAssetRequiresExactDigest(t *testing.T) {
	content := []byte("sealed bytes")
	digest := sha256.Sum256(content)
	expected := localAsset{name: "asset.bin", size: int64(len(content)), sha256: hex.EncodeToString(digest[:])}
	id, name, size, state := int64(1), expected.name, expected.size, "uploaded"
	for _, digest := range []*string{nil, pointer(""), pointer("sha256:" + strings.Repeat("0", 64))} {
		if err := validateUploadedAsset(githubReleaseAsset{ID: &id, Name: &name, Size: &size, State: &state, Digest: digest}, &expected); err == nil || !strings.Contains(err.Error(), "digest") {
			t.Fatalf("validateUploadedAsset() error = %v", err)
		}
	}
}

func TestGitHubErrorsAreBoundedAndSanitized(t *testing.T) {
	message := strings.Repeat("unsafe\n", 100)
	body, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		t.Fatal(err)
	}
	if got := githubAPIErrorMessage(body); got == "" || strings.ContainsAny(got, "\r\n\t") || len([]rune(got)) > 513 {
		t.Fatalf("githubAPIErrorMessage() = %q", got)
	}
	client := &staticClient{status: http.StatusForbidden, body: map[string]string{"message": "release permission denied"}}
	_, err = (githubReleasePublisher{client: client}).request(context.Background(), "token", http.MethodGet, githubAPIOrigin+"/repos/example/workcell/releases", "", nil, 0, http.StatusOK)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "release permission denied") {
		t.Fatalf("request() error = %v", err)
	}
}

func pointer(value string) *string { return &value }
