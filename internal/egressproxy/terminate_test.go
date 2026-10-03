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

const testPlaceholder = "wcph-0123456789abcdef0123456789abcdef"

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

// terminateFixture runs an upstream that echoes the x-api-key it received and
// a proxy that terminates example.com and splices a.example.com.
func terminateFixture(t *testing.T, broker Broker) (proxyAddr string, ca *SessionCA, upstreamRoots *x509.CertPool, p *Proxy, log *bytes.Buffer) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Host+" "+strings.Join(r.Header.Values("X-Api-Key"), ","))
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
	if err := p.Terminate(ca, broker, []TerminateRule{{Host: "example.com", Header: "x-api-key", Placeholder: testPlaceholder}}); err != nil {
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
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
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
		{testPlaceholder + "x", "example.com " + testPlaceholder + "x"},
		{"Basic " + testPlaceholder, "example.com Basic " + testPlaceholder},
		{"", "example.com "},
	}
	for _, tc := range cases {
		if code, body := get(t, c, "https://example.com/", tc.sent); code != http.StatusOK || body != tc.want {
			t.Errorf("sent %q: upstream saw (%d, %q), want %q", tc.sent, code, body, tc.want)
		}
	}
	// Two values or a forged Host header must not change what reaches upstream.
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.Host = "evil.example"
	req.Header["X-Api-Key"] = []string{testPlaceholder, "user-key"}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if want := "example.com " + testPlaceholder + ",user-key"; string(body) != want {
		t.Errorf("two values with forged Host: upstream saw %q, want %q", body, want)
	}
	if n := broker.callCount(); n != 2 {
		t.Errorf("broker calls = %d, want 2 (placeholder values only)", n)
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
	got, err := ParseTerminateRule("API.example.com=x-api-key=" + testPlaceholder)
	if want := (TerminateRule{"api.example.com", "x-api-key", testPlaceholder}); err != nil || got != want {
		t.Fatalf("ParseTerminateRule = (%+v, %v), want %+v", got, err, want)
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
		"off allowlist": {{Host: "other.example.com", Header: "x-api-key", Placeholder: testPlaceholder}},
		"plain port":    {{Host: "db.example.com", Header: "x-api-key", Placeholder: testPlaceholder}},
		"duplicate":     {rule, rule},
	} {
		if err := p.Terminate(nil, nil, rules); err == nil {
			t.Errorf("%s: Terminate succeeded, want refusal", name)
		}
	}
}
