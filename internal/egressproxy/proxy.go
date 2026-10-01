// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package egressproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

// extECH is the encrypted_client_hello extension type.
const extECH = 0xfe0d

const (
	peekTimeout   = 10 * time.Second
	dialTimeout   = 10 * time.Second // per resolve and per dial attempt
	connectBudget = 3 * dialTimeout  // all dial attempts for one connection
	maxHelloBytes = 1 << 17          // above the 64 KiB handshake message limit plus record framing
	maxLogHost    = 253              // longest DNS name
	maxDenyKeys   = 4096
	maxConns      = 1024 // concurrent connections across all ports
	overflowHost  = "(overflow)"
)

// publicIPv6 is the only IPv6 block allocated for global unicast; everything
// outside it (IPv4-embedding forms, site-local, SRv6, reserved) is refused.
var publicIPv6 = netip.MustParsePrefix("2000::/3")

// blockedPrefixes adds the ranges that netip does not classify: shared,
// reserved or documentation IPv4 space and special blocks inside 2000::/3.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT; holds the 100.100.100.200 metadata address
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF assignments; holds the 192.0.0.192 metadata address
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments: Teredo, benchmarking, ORCHID
	netip.MustParsePrefix("2002::/16"),       // 6to4
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation (TEST-NET-1)
	netip.MustParsePrefix("192.88.99.0/24"),  // deprecated 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation (TEST-NET-2)
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation (TEST-NET-3)
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("3fff::/20"),       // documentation
}

// blockedAddr reports whether the proxy must refuse to connect to a.
// Loopback, link-local (169.254.169.254 metadata included), multicast,
// unspecified and broadcast are not global unicast; RFC 1918 and fc00::/7
// (fd00:ec2::254 metadata included) are private.
func blockedAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return true
	}
	if a.Is6() && !publicIPv6.Contains(a) {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Proxy admits allowlisted connections and logs each denial as one JSONL line.
// Overload refusals are best-effort and are dropped if the log writer stalls.
type Proxy struct {
	allow  *Allowlist
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	dial   func(ctx context.Context, addr netip.AddrPort) (net.Conn, error)

	slots chan struct{} // one token per live connection
	shed  chan uint16   // ports of shed connections awaiting a deny line

	mu      sync.Mutex
	denyLog io.Writer
	denies  map[denyKey]uint64
}

type denyKey struct {
	host   string
	port   uint16
	reason string
}

// New returns a proxy for allow that writes deny lines to denyLog.
func New(allow *Allowlist, denyLog io.Writer) *Proxy {
	var dialer net.Dialer
	p := &Proxy{
		allow: allow,
		lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
		dial: func(ctx context.Context, addr netip.AddrPort) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", addr.String())
		},
		slots:   make(chan struct{}, maxConns),
		shed:    make(chan uint16, 64),
		denyLog: denyLog,
		denies:  map[denyKey]uint64{},
	}
	go func() {
		for port := range p.shed {
			p.deny("", port, "overloaded")
		}
	}()
	return p
}

// Serve handles connections from ln, which receives traffic for port, until
// ln is closed.
func (p *Proxy) Serve(ln net.Listener, port uint16) error {
	for {
		conn, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return err
		}
		if err != nil {
			time.Sleep(50 * time.Millisecond) // e.g. EMFILE; retry instead of exiting
			continue
		}
		select {
		case p.slots <- struct{}{}:
			go func() {
				defer func() { <-p.slots }()
				p.handle(conn, port)
			}()
		default:
			_ = conn.Close() // at the limit: shed load instead of exhausting descriptors
			// The deny line goes through a queue so a blocked log cannot stall accept.
			// ponytail: a flood past the queue depth goes unlogged; add a dropped counter if audits need it.
			select {
			case p.shed <- port:
			default:
			}
		}
	}
}

func (p *Proxy) handle(client net.Conn, port uint16) {
	defer client.Close()
	host, replay, reason := p.route(client, port)
	if reason != "" {
		p.deny(host, port, reason)
		return
	}
	upstream, reason := p.connect(host, port)
	if reason != "" {
		p.deny(host, port, reason)
		return
	}
	defer upstream.Close()
	if _, err := upstream.Write(replay); err != nil {
		return
	}
	splice(client, upstream)
}

// route picks the upstream host for a connection. On port 443 it reads the
// ClientHello and returns the bytes it read, which the caller must replay.
func (p *Proxy) route(client net.Conn, port uint16) (host string, replay []byte, reason string) {
	if port != tlsPort {
		host, ok := p.allow.forward[port]
		if !ok {
			return "", nil, "port_not_allowed"
		}
		return host, nil, ""
	}
	if client.SetReadDeadline(time.Now().Add(peekTimeout)) != nil {
		return "", nil, "no_sni"
	}
	sni, replay, ok := peekSNI(client)
	if !ok || client.SetReadDeadline(time.Time{}) != nil {
		return "", nil, "no_sni"
	}
	sni = asciiLower(sni)
	if !p.allow.sni[sni] {
		return sni, nil, "sni_not_allowed"
	}
	return sni, replay, ""
}

// asciiLower folds only A-Z. strings.ToLower is Unicode-aware: it maps the
// Kelvin sign (U+212A) to "k", so a non-ASCII SNI would match an ASCII
// allowlist entry while the replayed ClientHello keeps the other name.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// connect resolves host itself and dials a resolved address, so a later DNS
// answer cannot change the target. One blocked address refuses the host.
func (p *Proxy) connect(host string, port uint16) (net.Conn, string) {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	// A rooted name keeps the resolver from appending a DNS search suffix.
	addrs, err := p.lookup(ctx, host+".")
	if err != nil || len(addrs) == 0 {
		return nil, "resolve_failed"
	}
	for _, a := range addrs {
		if blockedAddr(a) {
			return nil, "blocked_address"
		}
	}
	deadline := time.Now().Add(connectBudget)
	for i, a := range addrs {
		// Share the remaining budget with the addresses still to try, so
		// silent early answers cannot starve a reachable later one.
		share := min(dialTimeout, time.Until(deadline)/time.Duration(len(addrs)-i))
		if share <= 0 {
			break
		}
		if conn, err := p.dialOne(share, netip.AddrPortFrom(a.Unmap(), port)); err == nil {
			return conn, ""
		}
	}
	return nil, "dial_failed"
}

// dialOne dials addr with its own timeout.
func (p *Proxy) dialOne(timeout time.Duration, addr netip.AddrPort) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return p.dial(ctx, addr)
}

// recordingConn records what the TLS stack reads and drops what it writes, so
// the client never sees a response from the proxy itself.
type recordingConn struct {
	net.Conn
	buf bytes.Buffer
}

var errHelloTooLarge = errors.New("client hello too large")

func (r *recordingConn) Read(b []byte) (int, error) {
	room := maxHelloBytes - r.buf.Len()
	if room <= 0 {
		return 0, errHelloTooLarge
	}
	if len(b) > room {
		b = b[:room]
	}
	n, err := r.Conn.Read(b)
	r.buf.Write(b[:n])
	return n, err
}

func (r *recordingConn) Write(b []byte) (int, error) { return len(b), nil }

// peekSNI parses the ClientHello with crypto/tls and aborts the handshake as
// soon as the SNI is known. It returns the bytes read from conn.
func peekSNI(conn net.Conn) (sni string, read []byte, ok bool) {
	rec := &recordingConn{Conn: conn}
	abort := errors.New("sni peeked")
	_ = tls.Server(rec, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			// The outer name of an ECH hello is not the name the upstream serves.
			for _, ext := range hello.Extensions {
				if ext == extECH {
					return nil, abort
				}
			}
			sni, ok = hello.ServerName, true
			return nil, abort
		},
	}).Handshake()
	return sni, rec.buf.Bytes(), ok && sni != ""
}

func splice(client, upstream net.Conn) {
	done := make(chan struct{})
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	// ponytail: no idle timeout; a peer that never closes holds its goroutines.
	go pipe(upstream, client)
	go pipe(client, upstream)
	<-done
	<-done
}

// deny writes one bounded JSONL line with the running count for this
// (host, port, reason). New keys past maxDenyKeys share one overflow host.
func (p *Proxy) deny(host string, port uint16, reason string) {
	if len(host) > maxLogHost {
		host = host[:maxLogHost]
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := denyKey{host, port, reason}
	if _, seen := p.denies[key]; !seen && len(p.denies) >= maxDenyKeys {
		key.host = overflowHost
	}
	p.denies[key]++
	line, _ := json.Marshal(struct {
		Host   string `json:"host"`
		Port   uint16 `json:"port"`
		Reason string `json:"reason"`
		Count  uint64 `json:"count"`
	}{key.host, port, reason, p.denies[key]})
	_, _ = p.denyLog.Write(append(line, '\n'))
}
