// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

const (
	upstreamRetryAttempts  = 4
	upstreamRetryBaseDelay = 2 * time.Second
	upstreamRetryMaxDelay  = 30 * time.Second
	upstreamRetryDrainMax  = 64 << 10
	// upstreamRetryBufferMax is above every caller's body limit, so the whole
	// body of a response that a caller accepts is read before it is returned,
	// and a connection that breaks mid-body is retried. A longer body streams
	// past this prefix and its caller's own limit refuses it.
	upstreamRetryBufferMax = 256 << 20
)

// ErrUpstreamUnavailable marks a refusal because an upstream endpoint did not
// answer after the bounded retries: a transport error, a 429, or a 5xx gateway
// status. It never marks a refused redirect, a TLS verification failure, or a
// response that the reviewed parser rejected.
var ErrUpstreamUnavailable = errors.New("upstream unavailable")

// upstreamRetryTransport retries one idempotent request with no body when the
// upstream is unavailable, also when the connection breaks while it reads a
// 200 body. The client timeout puts a deadline on the request context, so that
// deadline also bounds every retry and every wait.
type upstreamRetryTransport struct {
	base      http.RoundTripper
	attempts  int
	baseDelay time.Duration
	maxDelay  time.Duration
	bufferMax int64
}

func newUpstreamRetryTransport(base http.RoundTripper) *upstreamRetryTransport {
	return &upstreamRetryTransport{
		base:      base,
		attempts:  upstreamRetryAttempts,
		baseDelay: upstreamRetryBaseDelay,
		maxDelay:  upstreamRetryMaxDelay,
		bufferMax: upstreamRetryBufferMax,
	}
}

func (t *upstreamRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !upstreamRequestIsRetryable(req) {
		return t.base.RoundTrip(req)
	}
	attempts := max(t.attempts, 1)
	for attempt := 1; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err == nil {
			resp, err = bufferUpstreamBody(resp, t.bufferMax)
		}
		if attempt >= attempts || !upstreamResultIsTransient(resp, err) {
			return resp, err
		}
		delay := t.backoff(attempt, resp)
		if !upstreamDelayFits(req.Context(), delay) {
			return resp, err
		}
		discardUpstreamResponse(resp)
		if waitErr := sleepContext(req.Context(), delay); waitErr != nil {
			return nil, waitErr
		}
	}
}

// bufferUpstreamBody reads a 200 body of at most limit bytes before RoundTrip
// returns it. A read error closes the response and returns
// the error as a transport error, so the retry loop can try again. A body with
// a larger Content-Length is returned unread.
func bufferUpstreamBody(resp *http.Response, limit int64) (*http.Response, error) {
	if limit <= 0 || resp.StatusCode != http.StatusOK || resp.Body == nil || resp.Body == http.NoBody || resp.ContentLength > limit {
		return resp, nil
	}
	var prefix bytes.Buffer
	if resp.ContentLength > 0 {
		prefix.Grow(int(resp.ContentLength))
	}
	if _, err := io.Copy(&prefix, io.LimitReader(resp.Body, limit)); err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("read upstream response body: %w", err)
	}
	if int64(prefix.Len()) < limit {
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(prefix.Bytes()))
		resp.ContentLength = int64(prefix.Len())
		return resp, nil
	}
	// An unknown length reached the cap: keep the prefix and stream the rest.
	resp.Body = upstreamPrefixedBody{Reader: io.MultiReader(bytes.NewReader(prefix.Bytes()), resp.Body), Closer: resp.Body}
	return resp, nil
}

type upstreamPrefixedBody struct {
	io.Reader
	io.Closer
}

func upstreamRequestIsRetryable(req *http.Request) bool {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return false
	}
	return req.Body == nil || req.Body == http.NoBody
}

// upstreamResultIsTransient reports whether one attempt failed in a way that a
// later attempt can fix. A spent GitHub quota is not transient: its reset can
// be an hour away, and githubRateLimitError reports it to the operator.
func upstreamResultIsTransient(resp *http.Response, err error) bool {
	if err != nil {
		return upstreamTransportErrorIsTransient(err)
	}
	if resp == nil {
		return false
	}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return resp.Header.Get("X-RateLimit-Remaining") != "0"
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func upstreamTransportErrorIsTransient(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var certErr *tls.CertificateVerificationError
	return !errors.As(err, &certErr)
}

// UpstreamStatusIsTransient reports whether a final HTTP status means the
// upstream was unavailable rather than that it refused the request.
func UpstreamStatusIsTransient(resp *http.Response) bool {
	return upstreamResultIsTransient(resp, nil)
}

func (t *upstreamRetryTransport) backoff(attempt int, resp *http.Response) time.Duration {
	delay := t.baseDelay << (attempt - 1)
	if delay <= 0 || delay > t.maxDelay {
		delay = t.maxDelay
	}
	if resp != nil {
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 {
			delay = min(time.Duration(seconds)*time.Second, t.maxDelay)
		}
	}
	if delay > 0 {
		// Up to 25% jitter keeps parallel refresh jobs from retrying in step.
		delay += time.Duration(rand.Int64N(int64(delay)/4 + 1))
	}
	return delay
}

func upstreamDelayFits(ctx context.Context, delay time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return !ok || time.Until(deadline) > delay
}

func discardUpstreamResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, upstreamRetryDrainMax))
	_ = resp.Body.Close()
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
