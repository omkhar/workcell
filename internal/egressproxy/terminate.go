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
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// MaxCALifetime caps the session CA lifetime.
const MaxCALifetime = 24 * time.Hour

var discardLog = log.New(io.Discard, "", 0)

var (
	headerNamePattern  = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	placeholderPattern = regexp.MustCompile(`^wcph-[0-9a-f]{32}$`)
	// placeholderAnywhere matches any placeholder in case-folded text.
	placeholderAnywhere = regexp.MustCompile(`wcph-[0-9a-f]{32}`)
)

// Broker returns the real value of a brokered header. The credential broker
// client implements it; the proxy never holds a real value between requests.
type Broker interface {
	Lookup(ctx context.Context, host, header string) (string, error)
}

// TerminateRule names a host whose TLS the proxy terminates and the header
// whose placeholder value it swaps for the brokered value.
type TerminateRule struct {
	Host        string
	Header      string
	Placeholder string
}

// ParseTerminateRule parses host=header=placeholder.
func ParseTerminateRule(text string) (TerminateRule, error) {
	parts := strings.Split(text, "=")
	if len(parts) != 3 {
		return TerminateRule{}, fmt.Errorf("terminate rule must be host=header=placeholder: %q", text)
	}
	r := TerminateRule{Host: strings.ToLower(parts[0]), Header: parts[1], Placeholder: parts[2]}
	if _, err := ParseAllowlist(r.Host+":443", "terminate rule"); err != nil {
		return TerminateRule{}, err
	}
	if !headerNamePattern.MatchString(r.Header) {
		return TerminateRule{}, fmt.Errorf("terminate rule has an invalid header name: %q", r.Header)
	}
	if !placeholderPattern.MatchString(r.Placeholder) {
		return TerminateRule{}, fmt.Errorf("terminate rule placeholder must be wcph-<32 hex>: %q", r.Placeholder)
	}
	return r, nil
}

// SessionCA issues leaf certificates for the terminated hosts only. Its key
// lives only in memory.
type SessionCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// NewSessionCA returns a CA whose critical name constraints permit only hosts
// and no IP address. notAfter must be in the future and within MaxCALifetime.
func NewSessionCA(hosts []string, notAfter time.Time) (*SessionCA, error) {
	now := time.Now()
	if !notAfter.After(now) || notAfter.Sub(now) > MaxCALifetime {
		return nil, fmt.Errorf("session CA expiry must be within %s from now", MaxCALifetime)
	}
	if len(hosts) == 0 {
		return nil, errors.New("session CA needs at least one host")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		Subject:                     pkix.Name{CommonName: "Workcell session CA"},
		NotBefore:                   now.Add(-time.Minute),
		NotAfter:                    notAfter,
		KeyUsage:                    x509.KeyUsageCertSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLen:                  0,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         hosts,
		ExcludedIPRanges: []*net.IPNet{
			{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
			{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
		},
	}
	cert, err := sign(template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	return &SessionCA{cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

// sign issues template under parent, whose private key is signer.
func sign(template, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	template.SerialNumber = serial
	der, err := x509.CreateCertificate(rand.Reader, template, parent, pub, signer)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// CertPEM returns the CA certificate for clients to trust.
func (ca *SessionCA) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

// leaf returns the cached leaf for host, issuing it on first use.
func (ca *SessionCA) leaf(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if c, ok := ca.leaves[host]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	cert, err := ca.issue(host, key)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}
	ca.leaves[host] = c
	return c, nil
}

func (ca *SessionCA) issue(host string, key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	return sign(&x509.Certificate{
		Subject:     pkix.Name{CommonName: host},
		DNSNames:    []string{host},
		NotBefore:   ca.cert.NotBefore,
		NotAfter:    ca.cert.NotAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca.cert, &key.PublicKey, ca.key)
}

// Terminate makes p terminate TLS for each rule host, which must be on the
// SNI allowlist, and swap each rule's placeholder header via broker. A host
// can have one rule per header.
func (p *Proxy) Terminate(ca *SessionCA, broker Broker, rules []TerminateRule) error {
	terminate := map[string][]TerminateRule{}
	seen := map[string]bool{}
	for _, r := range rules {
		if !p.allow.sni[r.Host] {
			return fmt.Errorf("terminate host %s is not on the port 443 allowlist", r.Host)
		}
		key := r.Host + " " + strings.ToLower(r.Header)
		if seen[key] {
			return fmt.Errorf("terminate host %s has more than one rule for header %s", r.Host, r.Header)
		}
		seen[key] = true
		terminate[r.Host] = append(terminate[r.Host], r)
	}
	p.ca, p.broker, p.terminate = ca, broker, terminate
	p.transport = &http.Transport{
		ForceAttemptHTTP2: false,
		IdleConnTimeout:   90 * time.Second,
		TLSClientConfig:   &tls.Config{RootCAs: p.upstreamRoots, MinVersion: tls.VersionTLS12},
		DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if _, ok := p.terminate[host]; !ok {
				return nil, fmt.Errorf("upstream %s is not a terminate host", host)
			}
			conn, reason := p.connect(host, tlsPort)
			if reason != "" {
				p.deny(host, tlsPort, reason)
				return nil, errors.New(reason)
			}
			return conn, nil
		},
	}
	return nil
}

// replayConn yields the replayed ClientHello bytes before the rest of the
// client stream.
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// oneConnListener hands out one connection, then blocks until it closes.
type oneConnListener struct {
	conn chan net.Conn
	done chan struct{}
	once sync.Once
	addr net.Addr
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conn:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *oneConnListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *oneConnListener) Addr() net.Addr { return l.addr }

// closeNotifyConn closes its listener when the HTTP server closes it.
type closeNotifyConn struct {
	net.Conn
	ln *oneConnListener
}

func (c *closeNotifyConn) Close() error {
	err := c.Conn.Close()
	_ = c.ln.Close()
	return err
}

// serveTerminated terminates TLS for host with a session leaf and proxies
// HTTP/1.1 requests to the real host, swapping the placeholder headers. It
// refuses a request that still carries a placeholder after the swap.
func (p *Proxy) serveTerminated(client net.Conn, host string, replay []byte) {
	rules := p.terminate[host]
	tlsConn := tls.Server(&replayConn{Conn: client, r: io.MultiReader(bytes.NewReader(replay), client)}, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return p.ca.leaf(host)
		},
	})
	ln := &oneConnListener{conn: make(chan net.Conn, 1), done: make(chan struct{}), addr: client.LocalAddr()}
	ln.conn <- &closeNotifyConn{Conn: tlsConn, ln: ln}
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1,
		ErrorLog:      discardLog,
		Transport:     p.transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = "https"
			r.Out.URL.Host = host
			r.Out.Host = host
		},
	}
	// ponytail: no idle timeout, like splice; a peer that never closes holds the conn.
	srv := &http.Server{
		ReadHeaderTimeout: peekTimeout,
		ErrorLog:          discardLog,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, rule := range rules {
				prefix, ok := placeholderPrefix(r.Header.Values(rule.Header), rule.Placeholder)
				if !ok {
					continue
				}
				value, err := p.broker.Lookup(r.Context(), host, rule.Header)
				if err != nil {
					p.deny(host, tlsPort, "broker_denied")
					http.Error(w, "credential broker denied the request", http.StatusBadGateway)
					return
				}
				r.Header.Set(rule.Header, prefix+value)
			}
			if carriesPlaceholder(r) {
				p.deny(host, tlsPort, "placeholder_unswapped")
				http.Error(w, "request carries an unswapped credential placeholder", http.StatusBadGateway)
				return
			}
			proxy.ServeHTTP(w, r)
		}),
	}
	_ = srv.Serve(ln)
}

// placeholderPrefix reports whether values is exactly one value equal to
// placeholder or "Bearer " + placeholder, and returns the prefix to keep.
func placeholderPrefix(values []string, placeholder string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	switch values[0] {
	case placeholder:
		return "", true
	case "Bearer " + placeholder:
		return "Bearer ", true
	}
	return "", false
}

// carriesPlaceholder reports whether any header name or value, or the request
// target raw or percent-decoded, holds a placeholder in any letter case. A
// target that does not decode counts as a hit, so an escape cannot hide one.
func carriesPlaceholder(r *http.Request) bool {
	has := func(text string) bool { return placeholderAnywhere.MatchString(strings.ToLower(text)) }
	decoded, err := url.PathUnescape(r.RequestURI)
	if err != nil || has(r.RequestURI) || has(decoded) {
		return true
	}
	for name, values := range r.Header {
		if has(name) {
			return true
		}
		for _, v := range values {
			if has(v) {
				return true
			}
		}
	}
	return false
}
