// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package egressproxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBlockedAddr(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"127.0.0.1":                true, // loopback
		"::1":                      true,
		"10.1.2.3":                 true, // private
		"172.16.0.1":               true,
		"192.168.1.1":              true,
		"fd00:ec2::254":            true, // AWS IPv6 metadata (ULA)
		"fc00::1":                  true,
		"169.254.169.254":          true, // metadata (link-local)
		"fe80::1":                  true,
		"0.0.0.0":                  true, // unspecified
		"::":                       true,
		"0.1.2.3":                  true, // "this network"
		"224.0.0.1":                true, // multicast
		"ff02::1":                  true,
		"255.255.255.255":          true, // broadcast
		"100.64.0.1":               true, // CGNAT
		"100.100.100.200":          true, // Alibaba metadata
		"192.0.0.192":              true, // Oracle metadata
		"::ffff:127.0.0.1":         true, // IPv4-mapped
		"::ffff:169.254.169.254":   true,
		"::ffff:10.0.0.1":          true,
		"::127.0.0.1":              true, // IPv4-compatible
		"64:ff9b::a9fe:a9fe":       true, // NAT64 of 169.254.169.254
		"64:ff9b:1::1":             true,
		"2002:a9fe:a9fe::1":        true, // 6to4
		"2001:0:4136:e378::1":      true, // Teredo
		"192.0.2.1":                true, // documentation
		"198.51.100.1":             true,
		"203.0.113.10":             true,
		"198.18.0.1":               true, // benchmarking
		"198.19.255.255":           true,
		"192.88.99.1":              true,
		"240.0.0.1":                true, // reserved
		"100::1":                   true, // discard-only
		"2001:2::1":                true,
		"2001:db8::1":              true, // documentation
		"3fff::1":                  true,
		"5f00::1":                  true,
		"198.17.255.255":           false, // just below benchmarking
		"198.20.0.0":               false, // just above benchmarking
		"8.8.8.8":                  false,
		"100.63.255.255":           false, // just below CGNAT
		"100.128.0.0":              false, // just above CGNAT
		"2606:4700:4700::1111":     false,
		"::ffff:8.8.8.8":           false,
		"2001:4860:4860::8888":     false,
		"2a00:1450:4001:80b::200e": false,
	}
	for text, want := range cases {
		if got := blockedAddr(netip.MustParseAddr(text)); got != want {
			t.Errorf("blockedAddr(%s) = %v, want %v", text, got, want)
		}
	}
	if !blockedAddr(netip.Addr{}) {
		t.Error("blockedAddr(zero Addr) = false, want true")
	}
}

// clientHello returns the bytes a TLS client sends first for serverName.
func clientHello(t testing.TB, serverName string) []byte {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		_ = tls.Client(client, &tls.Config{ServerName: serverName, InsecureSkipVerify: true}).Handshake()
	}()
	rec := &recordingConn{Conn: server}
	_ = tls.Server(rec, &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return nil, errors.New("stop")
	}}).Handshake()
	_ = server.Close()
	_ = client.Close()
	return bytes.Clone(rec.buf.Bytes())
}

// feed returns a conn whose reads yield data then EOF.
func feed(data []byte) net.Conn {
	client, server := net.Pipe()
	go func() {
		_, _ = client.Write(data)
		_ = client.Close()
	}()
	return server
}

func TestPeekSNI(t *testing.T) {
	t.Parallel()
	hello := clientHello(t, "api.example.com")
	conn := feed(hello)
	defer conn.Close()
	sni, read, ok := peekSNI(conn)
	if !ok || sni != "api.example.com" {
		t.Fatalf("peekSNI = (%q, ok=%v), want api.example.com", sni, ok)
	}
	if !bytes.Equal(read, hello) {
		t.Fatalf("peekSNI read %d bytes, want the %d-byte ClientHello", len(read), len(hello))
	}

	for name, data := range map[string][]byte{
		"empty":     nil,
		"plaintext": []byte("GET / HTTP/1.1\r\nHost: api.example.com\r\n\r\n"),
		"truncated": hello[:len(hello)/2],
		"no sni":    clientHello(t, ""),
	} {
		conn := feed(data)
		if sni, _, ok := peekSNI(conn); ok {
			t.Errorf("%s: peekSNI ok with sni %q, want failure", name, sni)
		}
		_ = conn.Close()
	}
}

func FuzzPeekSNI(f *testing.F) {
	f.Add(clientHello(f, "api.example.com"))
	f.Add(clientHello(f, ""))
	f.Add([]byte{0x16, 0x03, 0x01, 0x00, 0x05, 0x01, 0x00, 0x00, 0x01, 0x00})
	f.Add([]byte("GET / HTTP/1.1\r\n\r\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		conn := feed(data)
		defer conn.Close()
		sni, read, ok := peekSNI(conn)
		if ok && sni == "" {
			t.Fatal("peekSNI ok with empty SNI")
		}
		if !bytes.HasPrefix(data, read) {
			t.Fatal("peekSNI returned bytes that are not a prefix of the input")
		}
	})
}

// testProxy returns a proxy whose resolver maps every host to addrs and whose
// dialer sends every dial to target, recording the address it was asked for.
func testProxy(t *testing.T, allow string, addrs []netip.Addr, target string) (*Proxy, *bytes.Buffer, *[]netip.AddrPort) {
	t.Helper()
	a, err := parseAllowlist(allow, "allowlist")
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	p := New(a, &log)
	var dialed []netip.AddrPort
	p.lookup = func(context.Context, string) ([]netip.Addr, error) { return addrs, nil }
	p.dial = func(ctx context.Context, addr netip.AddrPort) (net.Conn, error) {
		p.mu.Lock()
		dialed = append(dialed, addr)
		p.mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, "tcp", target)
	}
	return p, &log, &dialed
}

// listen serves p on a loopback listener for port and returns its address.
func listen(t *testing.T, p *Proxy, port uint16) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = p.Serve(ln, port) }()
	return ln.Addr().String()
}

type denyLine struct {
	Host   string `json:"host"`
	Port   uint16 `json:"port"`
	Reason string `json:"reason"`
	Count  uint64 `json:"count"`
}

func denies(t *testing.T, p *Proxy, log *bytes.Buffer) []denyLine {
	t.Helper()
	p.mu.Lock()
	text := log.String()
	p.mu.Unlock()
	var out []denyLine
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var d denyLine
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			t.Fatalf("deny line %q: %v", line, err)
		}
		out = append(out, d)
	}
	return out
}

func httpsGet(serverName, proxyAddr string, roots *x509.CertPool) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{ServerName: serverName, RootCAs: roots},
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", proxyAddr)
			},
		},
	}
	resp, err := client.Get("https://" + serverName + "/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func TestProxyTLSEndToEnd(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello through proxy")
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	public := netip.MustParseAddr("8.8.8.8")

	p, log, dialed := testProxy(t, "example.com:443", []netip.Addr{public}, server.Listener.Addr().String())
	addr := listen(t, p, 443)

	body, err := httpsGet("example.com", addr, roots)
	if err != nil || body != "hello through proxy" {
		t.Fatalf("allowed GET = (%q, %v), want the upstream body", body, err)
	}
	p.mu.Lock()
	got := append([]netip.AddrPort(nil), *dialed...)
	p.mu.Unlock()
	if want := netip.AddrPortFrom(public, 443); len(got) != 1 || got[0] != want {
		t.Fatalf("dialed %v, want [%v]", got, want)
	}

	// httptest's certificate also covers *.example.com, so only the proxy
	// can refuse this name.
	for range 2 {
		if _, err := httpsGet("other.example.com", addr, roots); err == nil {
			t.Fatal("GET with a non-allowlisted SNI succeeded, want refusal")
		}
	}
	want := []denyLine{
		{Host: "other.example.com", Port: 443, Reason: "sni_not_allowed", Count: 1},
		{Host: "other.example.com", Port: 443, Reason: "sni_not_allowed", Count: 2},
	}
	if d := denies(t, p, log); len(d) != 2 || d[0] != want[0] || d[1] != want[1] {
		t.Fatalf("deny lines = %+v, want %+v", d, want)
	}
}

func TestProxyRefusesBlockedResolution(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	// A public answer mixed with a metadata answer must still be refused.
	addrs := []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("::ffff:169.254.169.254")}
	p, log, dialed := testProxy(t, "example.com:443", addrs, server.Listener.Addr().String())
	addr := listen(t, p, 443)

	if _, err := httpsGet("example.com", addr, roots); err == nil {
		t.Fatal("GET to a host that resolves to a metadata address succeeded, want refusal")
	}
	p.mu.Lock()
	n := len(*dialed)
	p.mu.Unlock()
	if n != 0 {
		t.Fatalf("proxy dialed %d times, want 0", n)
	}
	want := denyLine{Host: "example.com", Port: 443, Reason: "blocked_address", Count: 1}
	if d := denies(t, p, log); len(d) != 1 || d[0] != want {
		t.Fatalf("deny lines = %+v, want [%+v]", d, want)
	}
}

func TestProxyTCPForward(t *testing.T) {
	t.Parallel()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	p, log, _ := testProxy(t, "db.example:5432", []netip.Addr{netip.MustParseAddr("8.8.8.8")}, echo.Addr().String())

	conn, err := net.Dial("tcp", listen(t, p, 5432))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("echo through proxy = (%q, %v), want ping", line, err)
	}

	other, err := net.Dial("tcp", listen(t, p, 5433))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_ = other.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := other.Read(make([]byte, 1)); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read on a non-allowlisted port = (%d, %v), want EOF", n, err)
	}
	want := denyLine{Port: 5433, Reason: "port_not_allowed", Count: 1}
	if d := denies(t, p, log); len(d) != 1 || d[0] != want {
		t.Fatalf("deny lines = %+v, want [%+v]", d, want)
	}
}

func TestDenyLogIsBounded(t *testing.T) {
	t.Parallel()
	p, log, _ := testProxy(t, "example.com:443", nil, "")
	p.deny(strings.Repeat("a", 1000), 443, "sni_not_allowed")
	if d := denies(t, p, log); len(d) != 1 || len(d[0].Host) != maxLogHost {
		t.Fatalf("deny host length = %+v, want %d", d, maxLogHost)
	}
	for i := range maxDenyKeys + 10 {
		p.deny(strconv.Itoa(i)+".example", 443, "sni_not_allowed")
	}
	if keys := len(p.denies); keys != maxDenyKeys+1 {
		t.Fatalf("deny counter keys = %d, want %d", keys, maxDenyKeys+1)
	}
	if !strings.Contains(log.String(), overflowHost) {
		t.Fatal("no overflow deny line after the key limit")
	}
}
