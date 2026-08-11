// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testGitHubReleaseAssetName = "actionlint_1.2.3_checksums.txt"
	testGitHubReleaseAssetURL  = "https://api.github.com/repos/rhysd/actionlint/releases/assets/424242"
	testGitHubReleaseRedirect  = "https://release-assets.githubusercontent.com/github-production-release-asset/12345678/01234567-89ab-cdef-0123-456789abcdef?sp=r&sv=fixture"
)

type githubReleaseAssetRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn githubReleaseAssetRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestGitHubReleaseAssetAPIURL(t *testing.T) {
	t.Parallel()

	validRelease := `{"assets":[{"id":424242,"name":"` + testGitHubReleaseAssetName + `","url":"https://attacker.invalid/ignored"}]}`
	for _, tc := range []struct {
		name       string
		repository string
		assetName  string
		document   string
		want       string
		wantError  string
	}{
		{name: "valid", repository: "rhysd/actionlint", assetName: testGitHubReleaseAssetName, document: validRelease, want: testGitHubReleaseAssetURL},
		{name: "invalid repository", repository: "rhysd/actionlint/extra", assetName: testGitHubReleaseAssetName, document: validRelease, wantError: "invalid GitHub release asset repository"},
		{name: "missing asset", repository: "rhysd/actionlint", assetName: "missing", document: validRelease, wantError: "unable to resolve one release asset"},
		{name: "duplicate asset", repository: "rhysd/actionlint", assetName: testGitHubReleaseAssetName, document: `{"assets":[{"id":1,"name":"` + testGitHubReleaseAssetName + `"},{"id":2,"name":"` + testGitHubReleaseAssetName + `"}]}`, wantError: "unable to resolve one release asset"},
		{name: "string asset ID", repository: "rhysd/actionlint", assetName: testGitHubReleaseAssetName, document: `{"assets":[{"id":"424242","name":"` + testGitHubReleaseAssetName + `"}]}`, wantError: "numeric release asset ID"},
		{name: "zero asset ID", repository: "rhysd/actionlint", assetName: testGitHubReleaseAssetName, document: `{"assets":[{"id":0,"name":"` + testGitHubReleaseAssetName + `"}]}`, wantError: "numeric release asset ID"},
		{name: "fractional asset ID", repository: "rhysd/actionlint", assetName: testGitHubReleaseAssetName, document: `{"assets":[{"id":1.5,"name":"` + testGitHubReleaseAssetName + `"}]}`, wantError: "numeric release asset ID"},
		{name: "malformed document", repository: "rhysd/actionlint", assetName: testGitHubReleaseAssetName, document: `{`, wantError: "parse GitHub release metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := githubReleaseAssetAPIURL(strings.NewReader(tc.document), tc.repository, tc.assetName)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("githubReleaseAssetAPIURL() error = %v, want text %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("githubReleaseAssetAPIURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidGitHubReleaseAssetRedirectURL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		url  string
		want bool
	}{
		{name: "valid", url: testGitHubReleaseRedirect, want: true},
		{name: "http", url: strings.Replace(testGitHubReleaseRedirect, "https://", "http://", 1)},
		{name: "userinfo", url: strings.Replace(testGitHubReleaseRedirect, "release-assets.githubusercontent.com", "user@release-assets.githubusercontent.com", 1)},
		{name: "port", url: strings.Replace(testGitHubReleaseRedirect, "release-assets.githubusercontent.com", "release-assets.githubusercontent.com:443", 1)},
		{name: "different host", url: strings.Replace(testGitHubReleaseRedirect, "release-assets.githubusercontent.com", "objects.githubusercontent.com", 1)},
		{name: "uppercase UUID", url: strings.Replace(testGitHubReleaseRedirect, "01234567-89ab", "01234567-89AB", 1)},
		{name: "zero repository ID", url: strings.Replace(testGitHubReleaseRedirect, "/12345678/", "/0/", 1)},
		{name: "extra path", url: strings.Replace(testGitHubReleaseRedirect, "?sp=", "/extra?sp=", 1)},
		{name: "escaped path", url: strings.Replace(testGitHubReleaseRedirect, "/12345678/", "/12345678%2F", 1)},
		{name: "no query", url: strings.Split(testGitHubReleaseRedirect, "?")[0]},
		{name: "empty query", url: strings.Split(testGitHubReleaseRedirect, "?")[0] + "?"},
		{name: "fragment", url: testGitHubReleaseRedirect + "#fragment"},
		{name: "space", url: testGitHubReleaseRedirect + " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := validGitHubReleaseAssetRedirectURL(tc.url); got != tc.want {
				t.Fatalf("validGitHubReleaseAssetRedirectURL(%q) = %t, want %t", tc.url, got, tc.want)
			}
		})
	}
}

func TestFetchGitHubReleaseAssetDirect(t *testing.T) {
	t.Parallel()

	const token = "fixture-token"
	const body = "abc  actionlint_1.2.3_linux_amd64.tar.gz\n"
	requests := 0
	client := githubReleaseAssetTestClient(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != testGitHubReleaseAssetURL {
			t.Fatalf("request URL = %q, want %q", req.URL, testGitHubReleaseAssetURL)
		}
		if got := req.Header.Get("Accept"); got != "application/octet-stream" {
			t.Fatalf("Accept = %q", got)
		}
		if got := req.Header.Get("Accept-Encoding"); got != "identity" {
			t.Fatalf("Accept-Encoding = %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		return githubReleaseAssetResponse(http.StatusOK, nil, body), nil
	})

	got, err := fetchGitHubReleaseAsset(context.Background(), client, testGitHubReleaseDocument("424242"), "rhysd/actionlint", testGitHubReleaseAssetName, token, upstreamChecksumMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body || requests != 1 {
		t.Fatalf("content = %q, requests = %d", got, requests)
	}
}

func TestFetchGitHubReleaseAssetRedirectDropsCredentials(t *testing.T) {
	t.Parallel()

	const token = "fixture-token"
	const body = "abc  actionlint_1.2.3_linux_amd64.tar.gz\n"
	var requests []*http.Request
	client := githubReleaseAssetTestClient(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Clone(req.Context()))
		if len(requests) == 1 {
			return githubReleaseAssetResponse(http.StatusFound, http.Header{"Location": []string{testGitHubReleaseRedirect}}, ""), nil
		}
		return githubReleaseAssetResponse(http.StatusOK, nil, body), nil
	})

	got, err := fetchGitHubReleaseAsset(context.Background(), client, testGitHubReleaseDocument("424242"), "rhysd/actionlint", testGitHubReleaseAssetName, token, upstreamChecksumMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body || len(requests) != 2 {
		t.Fatalf("content = %q, requests = %d", got, len(requests))
	}
	if requests[0].URL.String() != testGitHubReleaseAssetURL || requests[0].Header.Get("Authorization") != "Bearer "+token {
		t.Fatalf("initial request = %#v", requests[0])
	}
	if requests[1].URL.String() != testGitHubReleaseRedirect {
		t.Fatalf("redirect URL = %q", requests[1].URL)
	}
	if requests[1].Header.Get("Authorization") != "" || requests[1].Header.Get("Accept") != "" || requests[1].Header.Get("Accept-Encoding") != "identity" {
		t.Fatalf("redirect retained initial headers: %#v", requests[1].Header)
	}
}

func TestFetchGitHubReleaseAssetRejectsResponses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		responses []*http.Response
		wantError string
	}{
		{name: "initial 301", responses: []*http.Response{githubReleaseAssetResponse(http.StatusMovedPermanently, nil, "")}, wantError: "unexpected GitHub release asset response 301"},
		{name: "missing location", responses: []*http.Response{githubReleaseAssetResponse(http.StatusFound, nil, "")}, wantError: "must include one Location header"},
		{name: "duplicate location", responses: []*http.Response{githubReleaseAssetResponse(http.StatusFound, http.Header{"Location": []string{testGitHubReleaseRedirect, testGitHubReleaseRedirect}}, "")}, wantError: "must include one Location header"},
		{name: "hostile location", responses: []*http.Response{githubReleaseAssetResponse(http.StatusFound, http.Header{"Location": []string{"https://attacker.invalid/file"}}, "")}, wantError: "outside the allowed endpoint"},
		{name: "second redirect", responses: []*http.Response{githubReleaseAssetResponse(http.StatusFound, http.Header{"Location": []string{testGitHubReleaseRedirect}}, ""), githubReleaseAssetResponse(http.StatusFound, http.Header{"Location": []string{testGitHubReleaseRedirect}}, "")}, wantError: "unexpected GitHub release asset redirect response 302"},
		{name: "declared oversized", responses: []*http.Response{{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("small")), ContentLength: upstreamChecksumMaxBytes + 1}}, wantError: "exceeds the size limit"},
		{name: "streamed oversized", responses: []*http.Response{githubReleaseAssetResponse(http.StatusOK, nil, strings.Repeat("x", upstreamChecksumMaxBytes+1))}, wantError: "exceeds the size limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			index := 0
			client := githubReleaseAssetTestClient(func(*http.Request) (*http.Response, error) {
				if index >= len(tc.responses) {
					t.Fatal("unexpected extra request")
				}
				response := tc.responses[index]
				index++
				return response, nil
			})
			_, err := fetchGitHubReleaseAsset(context.Background(), client, testGitHubReleaseDocument("424242"), "rhysd/actionlint", testGitHubReleaseAssetName, "fixture-token", upstreamChecksumMaxBytes)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("fetchGitHubReleaseAsset() error = %v, want text %q", err, tc.wantError)
			}
		})
	}
}

func TestGitHubAPITokenPrecedenceAndLinePolicy(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name         string
		workcell     string
		github       string
		gh           string
		file         string
		fileContents string
		want         string
		wantErr      string
	}{
		{name: "workcell wins", workcell: "workcell-token", github: "github-token", gh: "gh-token", file: tokenFile, want: "workcell-token"},
		{name: "github wins", github: "github-token", gh: "gh-token", file: tokenFile, want: "github-token"},
		{name: "gh wins", gh: "gh-token", file: tokenFile, want: "gh-token"},
		{name: "file fallback", file: tokenFile, want: "file-token"},
		{name: "trailing newline file", file: tokenFile, fileContents: "fixture-token\n", want: "fixture-token"},
		{name: "trailing newline environment", workcell: "fixture-token\n", file: tokenFile, wantErr: "exactly one token line"},
		{name: "carriage return environment", workcell: "fixture-token\r", file: tokenFile, wantErr: "exactly one token line"},
		{name: "internal newline file", file: tokenFile, fileContents: "fixture\ntoken\n", wantErr: "exactly one token line"},
		{name: "oversized file", file: tokenFile, fileContents: strings.Repeat("x", githubAPIMaxTokenBytes+1), wantErr: "size limit"},
		{name: "environment bypasses unreadable file", workcell: "workcell-token", file: filepath.Join(t.TempDir(), "missing"), want: "workcell-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fileContents != "" {
				if err := os.WriteFile(tokenFile, []byte(tc.fileContents), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(tokenFile, []byte("file-token\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("WORKCELL_GITHUB_API_TOKEN", tc.workcell)
			t.Setenv("GITHUB_TOKEN", tc.github)
			t.Setenv("GH_TOKEN", tc.gh)
			t.Setenv("WORKCELL_GITHUB_API_TOKEN_FILE", tc.file)
			got, err := githubAPIToken()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("githubAPIToken() error = %v, want text %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("githubAPIToken() = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestGitHubReleaseAssetLimit(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		assetClass string
		want       int64
		wantErr    string
	}{
		{assetClass: "checksum", want: upstreamChecksumMaxBytes},
		{assetClass: "archive", want: upstreamMetadataMaxBytes},
		{assetClass: "unknown", wantErr: "unknown GitHub release asset class"},
	} {
		t.Run(tc.assetClass, func(t *testing.T) {
			t.Parallel()
			got, err := githubReleaseAssetLimit(tc.assetClass)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("githubReleaseAssetLimit(%q) error = %v, want text %q", tc.assetClass, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("githubReleaseAssetLimit(%q) = %d, %v, want %d", tc.assetClass, got, err, tc.want)
			}
		})
	}
}

func TestGitHubReleaseAssetClientRejectsAutomaticRedirects(t *testing.T) {
	t.Parallel()

	client := newGitHubReleaseAssetClient()
	err := client.CheckRedirect(&http.Request{}, []*http.Request{{}})
	if !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect() error = %v, want http.ErrUseLastResponse", err)
	}
	if client.Timeout != githubReleaseAssetTimeout {
		t.Fatalf("client timeout = %s, want %s", client.Timeout, githubReleaseAssetTimeout)
	}
}

func testGitHubReleaseDocument(assetID string) io.Reader {
	return strings.NewReader(`{"assets":[{"id":` + assetID + `,"name":"` + testGitHubReleaseAssetName + `"}]}`)
}

func githubReleaseAssetResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: -1,
	}
}

func githubReleaseAssetTestClient(roundTrip githubReleaseAssetRoundTripFunc) *http.Client {
	return &http.Client{
		Transport: roundTrip,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
