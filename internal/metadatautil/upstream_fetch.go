// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	upstreamMetadataMaxBytes = 200 << 20
	upstreamChecksumMaxBytes = 64 << 10
	githubAPIMaxTokenBytes   = 16 << 10
	upstreamHTTPTimeout      = 120 * time.Second
	upstreamChecksumTimeout  = 60 * time.Second
	upstreamConnectTimeout   = 15 * time.Second
	upstreamUserAgent        = "workcell-upstream-pins/1.0"
)

var upstreamRustVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type upstreamHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type upstreamFetchRequest struct {
	url                 string
	redirectHost        string
	maxBytes            int64
	timeout             time.Duration
	responseDescription string
}

// FetchGitHubAPI gets one bounded GitHub API document. It sends a GitHub token
// only to the reviewed GitHub API origin and refuses all redirects.
func FetchGitHubAPI(ctx context.Context, rawURL string) ([]byte, error) {
	token, err := githubAPIToken()
	if err != nil {
		return nil, err
	}
	return fetchGitHubAPI(ctx, newGitHubAPIClient(), rawURL, token)
}

func fetchGitHubAPI(ctx context.Context, client upstreamHTTPDoer, rawURL, token string) ([]byte, error) {
	return fetchGitHubAPIWithLimit(ctx, client, rawURL, token, upstreamMetadataMaxBytes)
}

func fetchGitHubAPIWithLimit(ctx context.Context, client upstreamHTTPDoer, rawURL, token string, maxBytes int64) ([]byte, error) {
	if err := validateGitHubAPIURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", upstreamUserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("unable to download GitHub API metadata")
	}
	if resp == nil {
		return nil, errors.New("GitHub API request returned no response")
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected GitHub API response %d", resp.StatusCode)
	}
	return readBoundedHTTPBody(resp, maxBytes, "GitHub API metadata")
}

func newGitHubAPIClient() *http.Client {
	return &http.Client{
		Transport: newUpstreamHTTPTransport(),
		Timeout:   upstreamHTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validateGitHubAPIURL(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return errors.New("invalid GitHub API URL")
	}
	if parsed.Scheme != "https" ||
		parsed.Host != "api.github.com" ||
		parsed.User != nil ||
		parsed.Fragment != "" ||
		parsed.RawPath != "" ||
		parsed.Opaque != "" ||
		!strings.HasPrefix(parsed.Path, "/repos/") {
		return errors.New("GitHub API URL is outside the allowed endpoint")
	}
	return nil
}

// FetchUpstream gets a bounded document from one reviewed public endpoint.
// The profile names deliberately form a closed set so the updater cannot turn
// release metadata into an arbitrary network destination.
func FetchUpstream(ctx context.Context, args []string) ([]byte, error) {
	request, err := upstreamFetchRequestFor(args)
	if err != nil {
		return nil, err
	}
	return fetchUpstream(ctx, newUpstreamFetchClient(request), request)
}

func fetchUpstream(ctx context.Context, client upstreamHTTPDoer, request upstreamFetchRequest) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, request.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", upstreamUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unable to download %s", request.responseDescription)
	}
	if resp == nil {
		return nil, fmt.Errorf("%s request returned no response", request.responseDescription)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected %s response %d", request.responseDescription, resp.StatusCode)
	}
	return readBoundedHTTPBody(resp, request.maxBytes, request.responseDescription)
}

func upstreamFetchRequestFor(args []string) (upstreamFetchRequest, error) {
	if len(args) == 0 {
		return upstreamFetchRequest{}, errors.New("missing upstream fetch profile")
	}
	switch args[0] {
	case "go-releases":
		if len(args) != 1 {
			return upstreamFetchRequest{}, errors.New("go-releases does not accept arguments")
		}
		return fixedUpstreamFetchRequest("https://go.dev/dl/?mode=json", "go.dev", upstreamMetadataMaxBytes, upstreamHTTPTimeout, "Go release metadata"), nil
	case "rust-channel":
		if len(args) != 1 {
			return upstreamFetchRequest{}, errors.New("rust-channel does not accept arguments")
		}
		return fixedUpstreamFetchRequest("https://static.rust-lang.org/dist/channel-rust-stable.toml", "static.rust-lang.org", upstreamMetadataMaxBytes, upstreamHTTPTimeout, "Rust channel metadata"), nil
	case "rustup-release":
		if len(args) != 1 {
			return upstreamFetchRequest{}, errors.New("rustup-release does not accept arguments")
		}
		return fixedUpstreamFetchRequest("https://static.rust-lang.org/rustup/release-stable.toml", "static.rust-lang.org", upstreamMetadataMaxBytes, upstreamHTTPTimeout, "rustup release metadata"), nil
	case "rustup-checksum":
		if len(args) != 3 {
			return upstreamFetchRequest{}, errors.New("rustup-checksum requires VERSION and TARGET")
		}
		version := args[1]
		if !upstreamRustVersionPattern.MatchString(version) {
			return upstreamFetchRequest{}, fmt.Errorf("invalid rustup version %q", version)
		}
		target := args[2]
		switch target {
		case "x86_64-unknown-linux-gnu", "aarch64-unknown-linux-gnu":
		default:
			return upstreamFetchRequest{}, fmt.Errorf("unsupported rustup target %q", target)
		}
		return fixedUpstreamFetchRequest(fmt.Sprintf("https://static.rust-lang.org/rustup/archive/%s/%s/rustup-init.sha256", version, target), "static.rust-lang.org", upstreamChecksumMaxBytes, upstreamChecksumTimeout, "rustup checksum"), nil
	case "dockerhub-binfmt-tags":
		if len(args) != 1 {
			return upstreamFetchRequest{}, errors.New("dockerhub-binfmt-tags does not accept arguments")
		}
		return fixedUpstreamFetchRequest("https://hub.docker.com/v2/repositories/tonistiigi/binfmt/tags?page_size=100", "hub.docker.com", upstreamMetadataMaxBytes, upstreamHTTPTimeout, "Docker Hub binfmt tags"), nil
	default:
		return upstreamFetchRequest{}, fmt.Errorf("unknown upstream fetch profile %q", args[0])
	}
}

func fixedUpstreamFetchRequest(rawURL, redirectHost string, maxBytes int64, timeout time.Duration, responseDescription string) upstreamFetchRequest {
	return upstreamFetchRequest{
		url:                 rawURL,
		redirectHost:        redirectHost,
		maxBytes:            maxBytes,
		timeout:             timeout,
		responseDescription: responseDescription,
	}
}

func newUpstreamFetchClient(request upstreamFetchRequest) *http.Client {
	return &http.Client{
		Transport: newUpstreamHTTPTransport(),
		Timeout:   request.timeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("stopped after 3 upstream redirects")
			}
			if next.URL.Scheme != "https" ||
				next.URL.Host != request.redirectHost ||
				next.URL.User != nil ||
				next.URL.Fragment != "" ||
				next.URL.RawPath != "" ||
				next.URL.Opaque != "" {
				return errors.New("upstream redirect is outside the reviewed HTTPS origin")
			}
			return nil
		},
	}
}

func newUpstreamHTTPTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   upstreamConnectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	return transport
}

func readBoundedHTTPBody(response *http.Response, maxBytes int64, description string) ([]byte, error) {
	if response.Body == nil {
		return nil, fmt.Errorf("%s response has no body", description)
	}
	if response.ContentLength > maxBytes {
		return nil, fmt.Errorf("%s exceeds the size limit", description)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", description, err)
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds the size limit", description)
	}
	return content, nil
}
