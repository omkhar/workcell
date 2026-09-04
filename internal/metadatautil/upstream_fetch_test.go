// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type upstreamHTTPDoerFunc func(*http.Request) (*http.Response, error)

func (fn upstreamHTTPDoerFunc) Do(request *http.Request) (*http.Response, error) { return fn(request) }

type upstreamRepeatedReader struct{ remaining int64 }

func (r *upstreamRepeatedReader) Read(content []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(content)) > r.remaining {
		content = content[:r.remaining]
	}
	for index := range content {
		content[index] = 'x'
	}
	r.remaining -= int64(len(content))
	return len(content), nil
}

func TestValidateGitHubAPIURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, url string
		want      bool
	}{
		{"valid", "https://api.github.com/repos/hadolint/hadolint/releases/latest", true},
		{"valid query", "https://api.github.com/repos/docker/actions-toolkit/contents/.github/buildx-releases.json?ref=main", true},
		{"http", "http://api.github.com/repos/hadolint/hadolint/releases/latest", false},
		{"userinfo", "https://token@api.github.com/repos/hadolint/hadolint/releases/latest", false},
		{"port", "https://api.github.com:443/repos/hadolint/hadolint/releases/latest", false},
		{"different host", "https://attacker.invalid/repos/hadolint/hadolint/releases/latest", false},
		{"fragment", "https://api.github.com/repos/hadolint/hadolint/releases/latest#fragment", false},
		{"escaped path", "https://api.github.com/repos/hadolint%2Fhadolint/releases/latest", false},
		{"outside repos", "https://api.github.com/user", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateGitHubAPIURL(tc.url); (err == nil) != tc.want {
				t.Fatalf("validateGitHubAPIURL(%q) error = %v, want valid=%t", tc.url, err, tc.want)
			}
		})
	}
}

func TestFetchGitHubAPI(t *testing.T) {
	t.Parallel()
	const token = "fixture-token"
	content, err := fetchGitHubAPI(context.Background(), upstreamHTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://api.github.com/repos/hadolint/hadolint/releases/latest" {
			t.Fatalf("request = %s %s", request.Method, request.URL)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q", got)
		}
		if got := request.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Fatalf("Accept = %q", got)
		}
		if got := request.Header.Get("Accept-Encoding"); got != "identity" {
			t.Fatalf("Accept-Encoding = %q", got)
		}
		return upstreamHTTPResponse(http.StatusOK, "{\"tag_name\":\"v1\"}"), nil
	}), "https://api.github.com/repos/hadolint/hadolint/releases/latest", token)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(content), `{"tag_name":"v1"}`; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestFetchGitHubAPIRejectsBadResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		response *http.Response
		err      error
		want     string
	}{
		{"transport", nil, errors.New("network"), "unable to download GitHub API metadata"},
		{"nil response", nil, nil, "returned no response"},
		{"redirect", upstreamHTTPResponse(http.StatusFound, ""), nil, "unexpected GitHub API response 302"},
		{"missing body", &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, nil, "has no body"},
		{"declared oversized", &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("small")), ContentLength: 5}, nil, "exceeds the size limit"},
		{"streamed oversized", &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(&upstreamRepeatedReader{remaining: 5}), ContentLength: -1}, nil, "exceeds the size limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := fetchGitHubAPIWithLimit(context.Background(), upstreamHTTPDoerFunc(func(*http.Request) (*http.Response, error) { return tc.response, tc.err }), "https://api.github.com/repos/hadolint/hadolint/releases/latest", "", 4)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fetchGitHubAPI() error = %v, want text %q", err, tc.want)
			}
		})
	}
}

func TestGitHubAPIClientRejectsAutomaticRedirects(t *testing.T) {
	t.Parallel()
	client := newGitHubAPIClient()
	if err := client.CheckRedirect(&http.Request{}, []*http.Request{{}}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect() error = %v, want http.ErrUseLastResponse", err)
	}
	if client.Timeout != upstreamHTTPTimeout {
		t.Fatalf("client timeout = %s, want %s", client.Timeout, upstreamHTTPTimeout)
	}
}

func TestUpstreamFetchRequestFor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		args      []string
		url, host string
		max       int64
		timeout   int64
		wantErr   string
	}{
		{"go", []string{"go-releases"}, "https://go.dev/dl/?mode=json", "go.dev", upstreamMetadataMaxBytes, int64(upstreamHTTPTimeout), ""},
		{"x tools", []string{"x-tools-latest"}, "https://proxy.golang.org/golang.org/x/tools/@latest", "proxy.golang.org", upstreamMetadataMaxBytes, int64(upstreamHTTPTimeout), ""},
		{"rust channel", []string{"rust-channel"}, "https://static.rust-lang.org/dist/channel-rust-stable.toml", "static.rust-lang.org", upstreamMetadataMaxBytes, int64(upstreamHTTPTimeout), ""},
		{"rustup release", []string{"rustup-release"}, "https://static.rust-lang.org/rustup/release-stable.toml", "static.rust-lang.org", upstreamMetadataMaxBytes, int64(upstreamHTTPTimeout), ""},
		{"rustup checksum", []string{"rustup-checksum", "1.2.3", "x86_64-unknown-linux-gnu"}, "https://static.rust-lang.org/rustup/archive/1.2.3/x86_64-unknown-linux-gnu/rustup-init.sha256", "static.rust-lang.org", upstreamChecksumMaxBytes, int64(upstreamChecksumTimeout), ""},
		{"docker hub", []string{"dockerhub-binfmt-tags"}, "https://hub.docker.com/v2/repositories/tonistiigi/binfmt/tags?page_size=100", "hub.docker.com", upstreamMetadataMaxBytes, int64(upstreamHTTPTimeout), ""},
		{"unknown", []string{"https://attacker.invalid"}, "", "", 0, 0, "unknown upstream fetch profile"},
		{"bad rust version", []string{"rustup-checksum", "1.2.3?redirect", "x86_64-unknown-linux-gnu"}, "", "", 0, 0, "invalid rustup version"},
		{"bad target", []string{"rustup-checksum", "1.2.3", "attacker"}, "", "", 0, 0, "unsupported rustup target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request, err := upstreamFetchRequestFor(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("upstreamFetchRequestFor(%q) error = %v, want text %q", tc.args, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if request.url != tc.url || request.redirectHost != tc.host || request.maxBytes != tc.max || int64(request.timeout) != tc.timeout {
				t.Fatalf("request = %#v", request)
			}
		})
	}
}

func TestFetchUpstreamAndRedirectPolicy(t *testing.T) {
	t.Parallel()
	request := fixedUpstreamFetchRequest("https://go.dev/dl/?mode=json", "go.dev", 4, upstreamHTTPTimeout, "fixture metadata")
	content, err := fetchUpstream(context.Background(), upstreamHTTPDoerFunc(func(got *http.Request) (*http.Response, error) {
		if got.URL.String() != request.url || got.Header.Get("Accept-Encoding") != "identity" || got.Header.Get("Authorization") != "" {
			t.Fatalf("request = %#v", got)
		}
		return upstreamHTTPResponse(http.StatusOK, "body"), nil
	}), request)
	if err != nil || string(content) != "body" {
		t.Fatalf("fetchUpstream() = %q, %v", content, err)
	}
	client := newUpstreamFetchClient(request)
	good, err := http.NewRequest(http.MethodGet, "https://go.dev/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(good, nil); err != nil {
		t.Fatalf("same-origin HTTPS redirect rejected: %v", err)
	}
	for _, rawURL := range []string{
		"http://go.dev/next",
		"https://attacker.invalid/next",
		"https://token@go.dev/next",
		"https://go.dev/next#fragment",
		"https://go.dev/next%2Fpath",
	} {
		bad, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.CheckRedirect(&http.Request{URL: bad}, nil); err == nil || !strings.Contains(err.Error(), "outside the reviewed HTTPS origin") {
			t.Fatalf("redirect %q error = %v", rawURL, err)
		}
	}
	if err := client.CheckRedirect(good, []*http.Request{{}, {}, {}}); err == nil || !strings.Contains(err.Error(), "stopped after 3") {
		t.Fatalf("redirect-count error = %v", err)
	}
}

func TestFetchUpstreamRejectsBadResponses(t *testing.T) {
	t.Parallel()

	request := fixedUpstreamFetchRequest("https://go.dev/dl/?mode=json", "go.dev", 4, upstreamHTTPTimeout, "fixture metadata")
	for _, tc := range []struct {
		name     string
		response *http.Response
		err      error
		want     string
	}{
		{name: "transport", err: errors.New("network"), want: "unable to download fixture metadata"},
		{name: "nil response", want: "request returned no response"},
		{name: "bad status", response: upstreamHTTPResponse(http.StatusBadGateway, ""), want: "unexpected fixture metadata response 502"},
		{name: "missing body", response: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}, want: "has no body"},
		{name: "declared oversized", response: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("large")), ContentLength: 5}, want: "exceeds the size limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := fetchUpstream(context.Background(), upstreamHTTPDoerFunc(func(*http.Request) (*http.Response, error) {
				return tc.response, tc.err
			}), request)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fetchUpstream() error = %v, want text %q", err, tc.want)
			}
		})
	}
}

func TestReadBoundedHTTPBodyRejectsUnknownLengthOverflow(t *testing.T) {
	t.Parallel()
	response := upstreamHTTPResponse(http.StatusOK, "12345")
	if _, err := readBoundedHTTPBody(response, 4, "fixture"); err == nil || !strings.Contains(err.Error(), "exceeds the size limit") {
		t.Fatalf("readBoundedHTTPBody() error = %v", err)
	}
}

func upstreamHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}
}
