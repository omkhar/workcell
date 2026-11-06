// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type retryFixtureBody struct {
	io.Reader
	closed *int
}

func (b retryFixtureBody) Close() error {
	*b.closed++
	return nil
}

func newRetryFixtureTransport(base http.RoundTripper, attempts int) *upstreamRetryTransport {
	return &upstreamRetryTransport{base: base, attempts: attempts, baseDelay: time.Millisecond, maxDelay: 5 * time.Millisecond}
}

func retryFixtureStatus(status int, closed *int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       retryFixtureBody{Reader: strings.NewReader("body"), closed: closed},
	}
}

func TestUpstreamRetryTransportRetriesTransientResults(t *testing.T) {
	certErr := &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
	cases := []struct {
		name      string
		results   []func(*int) (*http.Response, error)
		wantCalls int
		wantCode  int
		wantErr   bool
		wantClose int
	}{
		{
			name: "503 then 200",
			results: []func(*int) (*http.Response, error){
				func(c *int) (*http.Response, error) { return retryFixtureStatus(http.StatusServiceUnavailable, c), nil },
				func(c *int) (*http.Response, error) { return retryFixtureStatus(http.StatusOK, c), nil },
			},
			wantCalls: 2, wantCode: http.StatusOK, wantClose: 1,
		},
		{
			name: "transport error then 200",
			results: []func(*int) (*http.Response, error){
				func(*int) (*http.Response, error) { return nil, errors.New("connection reset by peer") },
				func(c *int) (*http.Response, error) { return retryFixtureStatus(http.StatusOK, c), nil },
			},
			wantCalls: 2, wantCode: http.StatusOK,
		},
		{
			name: "429 with quota left then 200",
			results: []func(*int) (*http.Response, error){
				func(c *int) (*http.Response, error) { return retryFixtureStatus(http.StatusTooManyRequests, c), nil },
				func(c *int) (*http.Response, error) { return retryFixtureStatus(http.StatusOK, c), nil },
			},
			wantCalls: 2, wantCode: http.StatusOK, wantClose: 1,
		},
		{
			name: "attempts run out on 502",
			results: []func(*int) (*http.Response, error){
				func(c *int) (*http.Response, error) { return retryFixtureStatus(http.StatusBadGateway, c), nil },
			},
			wantCalls: 3, wantCode: http.StatusBadGateway, wantClose: 2,
		},
		{
			name: "404 is final",
			results: []func(*int) (*http.Response, error){
				func(c *int) (*http.Response, error) { return retryFixtureStatus(http.StatusNotFound, c), nil },
			},
			wantCalls: 1, wantCode: http.StatusNotFound,
		},
		{
			name: "spent GitHub quota is final",
			results: []func(*int) (*http.Response, error){
				func(c *int) (*http.Response, error) {
					resp := retryFixtureStatus(http.StatusTooManyRequests, c)
					resp.Header.Set("X-RateLimit-Remaining", "0")
					return resp, nil
				},
			},
			wantCalls: 1, wantCode: http.StatusTooManyRequests,
		},
		{
			name: "TLS verification failure is final",
			results: []func(*int) (*http.Response, error){
				func(*int) (*http.Response, error) { return nil, certErr },
			},
			wantCalls: 1, wantErr: true,
		},
		{
			name: "cancelled context is final",
			results: []func(*int) (*http.Response, error){
				func(*int) (*http.Response, error) { return nil, context.Canceled },
			},
			wantCalls: 1, wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, closed := 0, 0
			transport := newRetryFixtureTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
				index := min(calls, len(tc.results)-1)
				calls++
				return tc.results[index](&closed)
			}), 3)
			req, err := http.NewRequest(http.MethodGet, "https://upstream.test/x", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := transport.RoundTrip(req)
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if tc.wantErr {
				if err == nil {
					t.Fatal("RoundTrip accepted a final transport error")
				}
				return
			}
			if err != nil || resp.StatusCode != tc.wantCode {
				t.Fatalf("RoundTrip = %v, %v; want status %d", resp, err, tc.wantCode)
			}
			if closed != tc.wantClose {
				t.Fatalf("closed %d discarded bodies, want %d", closed, tc.wantClose)
			}
		})
	}
}

func TestUpstreamRetryTransportSkipsUnsafeRequests(t *testing.T) {
	for name, build := range map[string]func() (*http.Request, error){
		"post": func() (*http.Request, error) { return http.NewRequest(http.MethodPost, "https://upstream.test/x", nil) },
		"get with body": func() (*http.Request, error) {
			return http.NewRequest(http.MethodGet, "https://upstream.test/x", strings.NewReader("x"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls, closed := 0, 0
			transport := newRetryFixtureTransport(roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return retryFixtureStatus(http.StatusServiceUnavailable, &closed), nil
			}), 4)
			req, err := build()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := transport.RoundTrip(req); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("calls = %d, want 1", calls)
			}
		})
	}
}

func TestUpstreamRetryTransportStopsAtDeadline(t *testing.T) {
	calls, closed := 0, 0
	transport := &upstreamRetryTransport{
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return retryFixtureStatus(http.StatusServiceUnavailable, &closed), nil
		}),
		attempts:  4,
		baseDelay: time.Hour,
		maxDelay:  time.Hour,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://upstream.test/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	resp, err := transport.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("RoundTrip = %v, %v; want the last 503", resp, err)
	}
	if calls != 1 || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("calls = %d after %s; a wait past the deadline must not start", calls, time.Since(started))
	}
}

func TestUpstreamRetryBackoff(t *testing.T) {
	transport := &upstreamRetryTransport{baseDelay: 2 * time.Second, maxDelay: 30 * time.Second}
	for attempt, base := range map[int]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 6: 30 * time.Second, 70: 30 * time.Second} {
		delay := transport.backoff(attempt, nil)
		if delay < base || delay > base+base/4 {
			t.Fatalf("backoff(%d) = %s, want %s plus at most 25%%", attempt, delay, base)
		}
	}
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"3600"}}}
	if delay := transport.backoff(1, resp); delay > 30*time.Second+30*time.Second/4 {
		t.Fatalf("Retry-After backoff = %s, want the 30s cap", delay)
	}
	resp.Header.Set("Retry-After", "5")
	if delay := transport.backoff(1, resp); delay < 5*time.Second || delay > 5*time.Second+5*time.Second/4 {
		t.Fatalf("Retry-After backoff = %s, want 5s", delay)
	}
}

func TestFetchBoundedMarksUpstreamOutages(t *testing.T) {
	certErr := &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}
	cases := map[string]struct {
		transport   roundTripFunc
		unavailable bool
	}{
		"503": {transport: func(*http.Request) (*http.Response, error) {
			return httpFixtureResponse(http.StatusServiceUnavailable, nil), nil
		}, unavailable: true},
		"connection reset": {transport: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection reset by peer")
		}, unavailable: true},
		"404": {transport: func(*http.Request) (*http.Response, error) {
			return httpFixtureResponse(http.StatusNotFound, nil), nil
		}},
		"403": {transport: func(*http.Request) (*http.Response, error) {
			return httpFixtureResponse(http.StatusForbidden, nil), nil
		}},
		"TLS verification": {transport: func(*http.Request) (*http.Response, error) {
			return nil, certErr
		}},
		"refused redirect": {transport: func(*http.Request) (*http.Response, error) {
			return nil, errDebianRedirectRefused
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: tc.transport}
			_, err := fetchBounded(context.Background(), client, "https://snapshot.test/file", 4)
			if err == nil {
				t.Fatal("bounded fetch accepted a bad response")
			}
			if got := errors.Is(err, ErrUpstreamUnavailable); got != tc.unavailable {
				t.Fatalf("errors.Is(%v, ErrUpstreamUnavailable) = %v, want %v", err, got, tc.unavailable)
			}
		})
	}
}
