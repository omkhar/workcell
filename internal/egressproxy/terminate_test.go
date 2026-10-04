// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package egressproxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testPlaceholder     = "wcph-0123456789abcdef0123456789abcdef"
	testAuthPlaceholder = "wcph-fedcba9876543210fedcba9876543210"
)

type fakeBroker struct {
	mu    sync.Mutex
	calls []string
	value string
	err   error
}

func (b *fakeBroker) Lookup(_ context.Context, host, header string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, host+" "+header)
	return b.value, b.err
}

func (b *fakeBroker) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

// terminateFixture runs an upstream that echoes the x-api-key and Authorization
// it received and a proxy that terminates example.com, with a rule for each of
// those headers, and splices a.example.com.
func terminateFixture(t *testing.T, broker Broker) (proxyAddr string, ca *SessionCA, upstreamRoots *x509.CertPool, p *Proxy, log *bytes.Buffer) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Host+" "+strings.Join(r.Header.Values("X-Api-Key"), ","))
		if auth := r.Header.Get("Authorization"); auth != "" {
			_, _ = io.WriteString(w, " auth="+auth)
		}
	}))
	t.Cleanup(server.Close)
	upstreamRoots = x509.NewCertPool()
	upstreamRoots.AddCert(server.Certificate())
	p, buf, _ := testProxy(t, "example.com:443 a.example.com:443", []netip.Addr{netip.MustParseAddr("8.8.8.8")}, server.Listener.Addr().String())
	p.upstreamRoots = upstreamRoots
	ca, err := NewSessionCA([]string{"example.com"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Terminate(ca, broker, []TerminateRule{
		{Host: "example.com", Header: "x-api-key", Placeholder: testPlaceholder},
		{Host: "example.com", Header: "authorization", Placeholder: testAuthPlaceholder},
	}); err != nil {
		t.Fatal(err)
	}
	return listen(t, p, 443), ca, upstreamRoots, p, buf
}

func caPool(t *testing.T, ca *SessionCA) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.CertPEM()) {
		t.Fatal("CertPEM is not a PEM certificate")
	}
	return pool
}

func clientVia(proxyAddr string, roots *x509.CertPool) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", proxyAddr)
			},
		},
	}
}

func get(t *testing.T, c *http.Client, url, key string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	return do(t, c, req)
}

func do(t *testing.T, c *http.Client, req *http.Request) (int, string) {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", req.URL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestTerminateSwapsOnlyExactPlaceholder(t *testing.T) {
	t.Parallel()
	broker := &fakeBroker{value: "real-secret"}
	addr, ca, upstreamRoots, _, _ := terminateFixture(t, broker)
	c := clientVia(addr, caPool(t, ca))
	cases := []struct{ sent, want string }{
		{testPlaceholder, "example.com real-secret"},
		{"Bearer " + testPlaceholder, "example.com Bearer real-secret"},
		{"user-key", "example.com user-key"},
		{"", "example.com "},
	}
	for _, tc := range cases {
		if code, body := get(t, c, "https://example.com/v1?key=user-key", tc.sent); code != http.StatusOK || body != tc.want {
			t.Errorf("sent %q: upstream saw (%d, %q), want %q", tc.sent, code, body, tc.want)
		}
	}
	// A forged Host header must not change what reaches upstream.
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Host = "evil.example"
	req.Header.Set("X-Api-Key", testPlaceholder)
	if _, body := do(t, c, req); body != "example.com real-secret" {
		t.Errorf("forged Host: upstream saw %q, want example.com real-secret", body)
	}
	if n := broker.callCount(); n != 3 {
		t.Errorf("broker calls = %d, want 3 (placeholder values only)", n)
	}
	if broker.calls[0] != "example.com x-api-key" {
		t.Errorf("broker call = %q, want host and header", broker.calls[0])
	}

	// A host outside the terminate set keeps the splice path: the client sees
	// the upstream certificate, not a session leaf, and nothing is swapped.
	if code, body := get(t, clientVia(addr, upstreamRoots), "https://a.example.com/", testPlaceholder); code != http.StatusOK || body != "a.example.com "+testPlaceholder {
		t.Errorf("spliced GET = (%d, %q), want the placeholder untouched", code, body)
	}
}

// Claude Code with apiKeyHelper sends the key in x-api-key and in
// Authorization; both rules for one host must swap.
func TestTerminateSwapsBothHeaders(t *testing.T) {
	t.Parallel()
	broker := &fakeBroker{value: "real-secret"}
	addr, ca, _, _, _ := terminateFixture(t, broker)
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Header.Set("X-Api-Key", testPlaceholder)
	req.Header.Set("Authorization", "Bearer "+testAuthPlaceholder)
	if code, body := do(t, clientVia(addr, caPool(t, ca)), req); code != http.StatusOK || body != "example.com real-secret auth=Bearer real-secret" {
		t.Fatalf("both headers: upstream saw (%d, %q), want both swapped", code, body)
	}
	if got := strings.Join(broker.calls, ","); got != "example.com x-api-key,example.com authorization" {
		t.Errorf("broker calls = %q, want one per header", got)
	}
}

// A placeholder left anywhere after the swap fails closed with 502 and a deny
// line; it never reaches upstream.
func TestTerminateRefusesUnswappedPlaceholder(t *testing.T) {
	t.Parallel()
	addr, ca, _, p, log := terminateFixture(t, &fakeBroker{value: "real-secret"})
	c := clientVia(addr, caPool(t, ca))
	encoded := "%77cph-0123456789abcdef0123456789abcdef"
	cases := map[string]struct {
		target string
		header map[string][]string
	}{
		"query string":          {"/v1?key=" + testPlaceholder, nil},
		"encoded query string":  {"/v1?key=" + encoded, nil},
		"path":                  {"/v1/" + testPlaceholder, nil},
		"undecodable target":    {"/v1?x=%zz&key=" + encoded, nil},
		"other header":          {"/", map[string][]string{"X-Goog-Api-Key": {testPlaceholder}}},
		"other header upper":    {"/", map[string][]string{"X-Goog-Api-Key": {strings.ToUpper(testPlaceholder)}}},
		"header name":           {"/", map[string][]string{"X-" + testPlaceholder: {"v"}}},
		"suffixed value":        {"/", map[string][]string{"X-Api-Key": {testPlaceholder + "x"}}},
		"basic scheme":          {"/", map[string][]string{"X-Api-Key": {"Basic " + testPlaceholder}}},
		"two values":            {"/", map[string][]string{"X-Api-Key": {testPlaceholder, "user-key"}}},
		"wrong header for rule": {"/", map[string][]string{"Authorization": {"Bearer " + testPlaceholder}}},
		"one of two swapped":    {"/", map[string][]string{"X-Api-Key": {testPlaceholder}, "X-Other": {testAuthPlaceholder}}},
	}
	for name, tc := range cases {
		req, err := http.NewRequest(http.MethodGet, "https://example.com"+tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range tc.header {
			req.Header[k] = v
		}
		if code, body := do(t, c, req); code != http.StatusBadGateway || !strings.Contains(body, "unswapped") {
			t.Errorf("%s: got (%d, %q), want 502 from the proxy", name, code, body)
		}
	}
	// A trailer is declared before the body and delivered after it, so the
	// proxy refuses every request trailer, placeholder-named or benign.
	trailers := []string{"X-" + testPlaceholder, "X-Checksum"}
	for _, name := range trailers {
		req, err := http.NewRequest(http.MethodPost, "https://example.com/v1", strings.NewReader("body"))
		if err != nil {
			t.Fatal(err)
		}
		req.ContentLength = -1
		req.Header.Set("X-Api-Key", testPlaceholder)
		req.Trailer = http.Header{name: {"v"}}
		if code, body := do(t, c, req); code != http.StatusBadGateway || !strings.Contains(body, "unswapped") {
			t.Errorf("trailer %s: got (%d, %q), want 502 from the proxy", name, code, body)
		}
	}
	d := denies(t, p, log)
	total := len(cases) + len(trailers)
	want := denyLine{Host: "example.com", Port: 443, Reason: "placeholder_unswapped", Count: uint64(total)}
	if len(d) != total || d[len(d)-1] != want {
		t.Fatalf("deny lines = %+v, want %d ending in %+v", d, total, want)
	}
}

func TestTerminateBrokerErrorIs502(t *testing.T) {
	t.Parallel()
	broker := &fakeBroker{err: errors.New("no grant")}
	addr, ca, _, p, log := terminateFixture(t, broker)
	code, body := get(t, clientVia(addr, caPool(t, ca)), "https://example.com/", testPlaceholder)
	if code != http.StatusBadGateway || strings.Contains(body, testPlaceholder) {
		t.Fatalf("broker error GET = (%d, %q), want 502 without reaching upstream", code, body)
	}
	want := denyLine{Host: "example.com", Port: 443, Reason: "broker_denied", Count: 1}
	if d := denies(t, p, log); len(d) != 1 || d[0] != want {
		t.Fatalf("deny lines = %+v, want [%+v]", d, want)
	}
}

func TestTerminateOffersHTTP11Only(t *testing.T) {
	t.Parallel()
	addr, ca, _, _, _ := terminateFixture(t, &fakeBroker{})
	roots := caPool(t, ca)
	dial := func(protos ...string) (string, error) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, &tls.Config{ServerName: "example.com", RootCAs: roots, NextProtos: protos})
		if err != nil {
			return "", err
		}
		defer conn.Close()
		return conn.ConnectionState().NegotiatedProtocol, nil
	}
	if _, err := dial("h2"); err == nil {
		t.Error("h2-only handshake succeeded, want refusal")
	}
	if got, err := dial("h2", "http/1.1"); err != nil || got != "http/1.1" {
		t.Errorf("negotiated = (%q, %v), want http/1.1", got, err)
	}
}

func TestSessionCANameConstraints(t *testing.T) {
	t.Parallel()
	ca, err := NewSessionCA([]string{"api.example.com"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !ca.cert.IsCA || ca.cert.MaxPathLen != 0 || !ca.cert.MaxPathLenZero || !ca.cert.PermittedDNSDomainsCritical {
		t.Fatalf("CA = IsCA %v MaxPathLen %d zero %v critical %v", ca.cert.IsCA, ca.cert.MaxPathLen, ca.cert.MaxPathLenZero, ca.cert.PermittedDNSDomainsCritical)
	}
	if ca.key.Curve != elliptic.P256() {
		t.Fatal("CA key is not P-256")
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)
	verify := func(name string, leaf *x509.Certificate) error {
		_, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots})
		return err
	}
	own, err := ca.leaf("api.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify("api.example.com", own.Leaf); err != nil {
		t.Fatalf("on-list leaf: %v", err)
	}
	if again, _ := ca.leaf("api.example.com"); again != own {
		t.Error("leaf was issued twice for one host")
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for _, name := range []string{"evil.example", "example.com"} {
		off, err := ca.issue(name, key)
		if err != nil {
			t.Fatal(err)
		}
		var invalid x509.CertificateInvalidError
		if err := verify(name, off); !errors.As(err, &invalid) || invalid.Reason != x509.CANotAuthorizedForThisName {
			t.Errorf("off-list leaf %s: verify error = %v, want CANotAuthorizedForThisName", name, err)
		}
	}
	ipLeaf := &x509.Certificate{IPAddresses: []net.IP{net.ParseIP("8.8.8.8")}, NotBefore: ca.cert.NotBefore, NotAfter: ca.cert.NotAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	signed, err := sign(ipLeaf, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify("8.8.8.8", signed); err == nil {
		t.Error("IP leaf verified, want refusal by the excluded IP ranges")
	}
}

func TestNewSessionCARefusesBadInput(t *testing.T) {
	t.Parallel()
	for name, notAfter := range map[string]time.Time{
		"past":       time.Now().Add(-time.Minute),
		"over a day": time.Now().Add(MaxCALifetime + time.Minute),
	} {
		if _, err := NewSessionCA([]string{"example.com"}, notAfter); err == nil {
			t.Errorf("%s: NewSessionCA succeeded, want refusal", name)
		}
	}
	if _, err := NewSessionCA(nil, time.Now().Add(time.Hour)); err == nil {
		t.Error("no hosts: NewSessionCA succeeded, want refusal")
	}
	if _, err := NewSessionCA([]string{"example.com"}, time.Now().Add(MaxCALifetime-time.Minute)); err != nil {
		t.Errorf("just under a day: %v", err)
	}
}

func TestParseTerminateRule(t *testing.T) {
	t.Parallel()
	want := TerminateRule{"api.example.com", "x-api-key", testPlaceholder}
	for _, host := range []string{"API.example.com", "api.example.com."} {
		got, err := ParseTerminateRule(host + "=x-api-key=" + testPlaceholder)
		if err != nil || got != want {
			t.Fatalf("ParseTerminateRule(%q...) = (%+v, %v), want %+v", host, got, err, want)
		}
	}
	for _, text := range []string{
		"example.com=x-api-key",
		"example.com=x-api-key=" + testPlaceholder + "=x",
		"192.0.2.1=x-api-key=" + testPlaceholder,
		"=x-api-key=" + testPlaceholder,
		"example.com=x api key=" + testPlaceholder,
		"example.com=x-api-key=secret",
		"example.com=x-api-key=wcph-0123",
	} {
		if _, err := ParseTerminateRule(text); err == nil {
			t.Errorf("ParseTerminateRule(%q) succeeded, want refusal", text)
		}
	}
}

func TestTerminateRefusesBadRules(t *testing.T) {
	t.Parallel()
	p, _, _ := testProxy(t, "example.com:443 db.example.com:5432", nil, "")
	rule := TerminateRule{Host: "example.com", Header: "x-api-key", Placeholder: testPlaceholder}
	for name, rules := range map[string][]TerminateRule{
		"off allowlist":  {{Host: "other.example.com", Header: "x-api-key", Placeholder: testPlaceholder}},
		"plain port":     {{Host: "db.example.com", Header: "x-api-key", Placeholder: testPlaceholder}},
		"duplicate":      {rule, rule},
		"duplicate case": {rule, {Host: "example.com", Header: "X-API-KEY", Placeholder: testAuthPlaceholder}},
	} {
		if err := p.Terminate(nil, nil, rules); err == nil {
			t.Errorf("%s: Terminate succeeded, want refusal", name)
		}
	}
}
