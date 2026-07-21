// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	debianSnapshotArchiveBase = "https://snapshot.debian.org/archive/debian"
	maxPackagesGzipBytes      = 64 << 20
	maxPackagesDecodedBytes   = 512 << 20
	maxPackagesLineBytes      = 1 << 20
	maxPackagesStanzaBytes    = 4 << 20
	maxPackagesStanzaFields   = 256
	maxPackagesStanzaLines    = 4096
	maxBootstrapPackageBytes  = 128 << 20
)

var (
	debianSnapshotPattern       = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)
	debianDigestPattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	debianSafeFilenamePattern   = regexp.MustCompile(`^pool/[A-Za-z0-9.+~_/-]+\.deb$`)
	debianOpenSSLAMD64Pattern   = regexp.MustCompile(`^pool/main/o/openssl/openssl_[A-Za-z0-9.+~_-]+_amd64\.deb$`)
	debianOpenSSLARM64Pattern   = regexp.MustCompile(`^pool/main/o/openssl/openssl_[A-Za-z0-9.+~_-]+_arm64\.deb$`)
	debianCACertificatesPattern = regexp.MustCompile(`^pool/main/c/ca-certificates/ca-certificates_[A-Za-z0-9.+~_-]+_all\.deb$`)
)

// DebianBootstrapPackage is the immutable package record taken from a Debian
// snapshot Packages.gz index and verified against the referenced .deb bytes.
type DebianBootstrapPackage struct {
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Filename     string `json:"filename"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
}

// DebianBootstrapPins is the complete resolved snapshot TLS bootstrap tuple.
type DebianBootstrapPins struct {
	Snapshot       string                 `json:"snapshot"`
	OpenSSLAMD64   DebianBootstrapPackage `json:"openssl_amd64"`
	OpenSSLARM64   DebianBootstrapPackage `json:"openssl_arm64"`
	CACertificates DebianBootstrapPackage `json:"ca_certificates"`
}

type debianPackageIndex struct {
	OpenSSL        DebianBootstrapPackage
	CACertificates DebianBootstrapPackage
}

// ResolveDebianBootstrapPins resolves and byte-verifies the package tuple for
// one snapshot. A snapshot is unsuitable unless both architecture indexes
// contain the same OpenSSL version and agree on the architecture-independent
// CA package. Packages.gz is trusted through authenticated HTTPS from the
// configured snapshot endpoint; this resolver does not claim independent
// Debian OpenPGP verification of that metadata.
func ResolveDebianBootstrapPins(ctx context.Context, client *http.Client, archiveBaseURL, snapshot string) (DebianBootstrapPins, error) {
	var zero DebianBootstrapPins
	if err := validateDebianSnapshot(snapshot); err != nil {
		return zero, err
	}
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	archiveBaseURL = strings.TrimRight(archiveBaseURL, "/")
	if err := validateHTTPBaseURL(archiveBaseURL); err != nil {
		return zero, err
	}
	parsedBaseURL, _ := url.Parse(archiveBaseURL)
	client = restrictDebianRedirects(client, parsedBaseURL)

	amd64, err := fetchDebianPackageIndex(ctx, client, archiveBaseURL, snapshot, "amd64")
	if err != nil {
		return zero, fmt.Errorf("resolve amd64 bootstrap packages: %w", err)
	}
	arm64, err := fetchDebianPackageIndex(ctx, client, archiveBaseURL, snapshot, "arm64")
	if err != nil {
		return zero, fmt.Errorf("resolve arm64 bootstrap packages: %w", err)
	}
	if amd64.OpenSSL.Version != arm64.OpenSSL.Version {
		return zero, fmt.Errorf("OpenSSL version disagreement between amd64 (%s) and arm64 (%s)", amd64.OpenSSL.Version, arm64.OpenSSL.Version)
	}
	if amd64.CACertificates != arm64.CACertificates {
		return zero, errors.New("ca-certificates metadata disagreement between amd64 and arm64 Packages.gz indexes")
	}

	pins := DebianBootstrapPins{
		Snapshot:       snapshot,
		OpenSSLAMD64:   amd64.OpenSSL,
		OpenSSLARM64:   arm64.OpenSSL,
		CACertificates: amd64.CACertificates,
	}
	if err := validateDebianBootstrapPins(pins); err != nil {
		return zero, err
	}
	for _, pkg := range []DebianBootstrapPackage{pins.OpenSSLAMD64, pins.OpenSSLARM64, pins.CACertificates} {
		if err := verifyDebianPackageBytes(ctx, client, archiveBaseURL, snapshot, pkg); err != nil {
			return zero, err
		}
	}
	return pins, nil
}

// ResolveDefaultDebianBootstrapPins uses the reviewed snapshot.debian.org
// archive endpoint for the resolver command.
func ResolveDefaultDebianBootstrapPins(snapshot string) (DebianBootstrapPins, error) {
	return ResolveDebianBootstrapPins(context.Background(), nil, debianSnapshotArchiveBase, snapshot)
}

func validateHTTPBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid Debian archive base URL %q", raw)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("Debian archive base URL must use HTTPS: %q", raw)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("Debian archive base URL must not contain userinfo, a query, or a fragment: %q", raw)
	}
	return nil
}

func restrictDebianRedirects(client *http.Client, baseURL *url.URL) *http.Client {
	restricted := *client
	original := restricted.CheckRedirect
	restricted.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, baseURL.Host) || req.URL.User != nil {
			return fmt.Errorf("refuse Debian archive redirect outside reviewed HTTPS origin: %s", req.URL.Redacted())
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 Debian archive redirects")
		}
		if original != nil {
			return original(req, via)
		}
		return nil
	}
	return &restricted
}

func fetchDebianPackageIndex(ctx context.Context, client *http.Client, archiveBaseURL, snapshot, architecture string) (debianPackageIndex, error) {
	var zero debianPackageIndex
	indexURL := fmt.Sprintf("%s/%s/dists/trixie/main/binary-%s/Packages.gz", archiveBaseURL, snapshot, architecture)
	body, err := fetchBounded(ctx, client, indexURL, maxPackagesGzipBytes)
	if err != nil {
		return zero, err
	}
	defer body.Close()

	gz, err := gzip.NewReader(body)
	if err != nil {
		return zero, fmt.Errorf("open %s as gzip: %w", indexURL, err)
	}
	defer gz.Close()

	var opensslMatches []DebianBootstrapPackage
	var caMatches []DebianBootstrapPackage
	err = scanDebianPackageStanzas(io.LimitReader(gz, maxPackagesDecodedBytes+1), func(stanza map[string]string) error {
		switch {
		case stanza["Package"] == "openssl" && stanza["Architecture"] == architecture:
			pkg, err := debianPackageFromStanza(stanza)
			if err != nil {
				return fmt.Errorf("openssl/%s: %w", architecture, err)
			}
			opensslMatches = append(opensslMatches, pkg)
		case stanza["Package"] == "ca-certificates" && stanza["Architecture"] == "all":
			pkg, err := debianPackageFromStanza(stanza)
			if err != nil {
				return fmt.Errorf("ca-certificates/all: %w", err)
			}
			caMatches = append(caMatches, pkg)
		}
		return nil
	})
	if err != nil {
		return zero, fmt.Errorf("parse %s: %w", indexURL, err)
	}
	opensslPackage, err := uniqueDebianPackage(opensslMatches, "openssl", architecture)
	if err != nil {
		return zero, err
	}
	caPackage, err := uniqueDebianPackage(caMatches, "ca-certificates", "all")
	if err != nil {
		return zero, err
	}
	return debianPackageIndex{OpenSSL: opensslPackage, CACertificates: caPackage}, nil
}

func fetchBounded(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", rawURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: unexpected status %s", rawURL, resp.Status)
	}
	if resp.ContentLength > maxBytes {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: response exceeds %d-byte limit", rawURL, maxBytes)
	}
	return &boundedReadCloser{Reader: io.LimitReader(resp.Body, maxBytes+1), Closer: resp.Body, max: maxBytes}, nil
}

type boundedReadCloser struct {
	io.Reader
	io.Closer
	max  int64
	read int64
}

func (r *boundedReadCloser) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += int64(n)
	if r.read > r.max {
		return n, fmt.Errorf("response exceeds %d-byte limit", r.max)
	}
	return n, err
}

func scanDebianPackageStanzas(reader io.Reader, visit func(map[string]string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxPackagesLineBytes)
	current := make(map[string]string)
	lineNumber := 0
	decodedBytes := int64(0)
	stanzaBytes := int64(0)
	stanzaLines := 0
	lastField := ""
	flush := func() error {
		if len(current) > 0 {
			if err := visit(current); err != nil {
				return err
			}
			current = make(map[string]string)
		}
		stanzaBytes = 0
		stanzaLines = 0
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		lineNumber++
		decodedBytes += int64(len(line)) + 1
		stanzaBytes += int64(len(line)) + 1
		stanzaLines++
		if decodedBytes > maxPackagesDecodedBytes {
			return fmt.Errorf("decoded Packages metadata exceeds %d-byte limit", maxPackagesDecodedBytes)
		}
		if stanzaBytes > maxPackagesStanzaBytes {
			return fmt.Errorf("line %d: package stanza exceeds %d-byte limit", lineNumber, maxPackagesStanzaBytes)
		}
		if stanzaLines > maxPackagesStanzaLines {
			return fmt.Errorf("line %d: package stanza exceeds %d-line limit", lineNumber, maxPackagesStanzaLines)
		}
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
			lastField = ""
		case strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t"):
			if lastField == "" {
				return fmt.Errorf("line %d: continuation without a field", lineNumber)
			}
			// Folded fields are legal Debian control syntax, but all fields used
			// for the bootstrap security decision are scalar. Ignore bounded
			// continuation bytes for unrelated fields because none are consumed.
			if isDebianBootstrapScalarField(lastField) {
				return fmt.Errorf("line %d: continuation is not allowed for scalar field %s", lineNumber, lastField)
			}
		default:
			name, value, ok := strings.Cut(line, ":")
			if !ok || name == "" || strings.TrimSpace(name) != name {
				return fmt.Errorf("line %d: malformed field", lineNumber)
			}
			if _, duplicate := current[name]; duplicate {
				return fmt.Errorf("line %d: duplicate field %s", lineNumber, name)
			}
			if len(current) >= maxPackagesStanzaFields {
				return fmt.Errorf("line %d: package stanza exceeds %d-field limit", lineNumber, maxPackagesStanzaFields)
			}
			current[name] = strings.TrimSpace(value)
			lastField = name
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan Packages metadata: %w", err)
	}
	return flush()
}

func isDebianBootstrapScalarField(name string) bool {
	switch name {
	case "Package", "Version", "Architecture", "Filename", "Size", "SHA256":
		return true
	default:
		return false
	}
}

func uniqueDebianPackage(matches []DebianBootstrapPackage, packageName, architecture string) (DebianBootstrapPackage, error) {
	if len(matches) != 1 {
		return DebianBootstrapPackage{}, fmt.Errorf("expected exactly one %s/%s package, found %d", packageName, architecture, len(matches))
	}
	return matches[0], nil
}

func debianPackageFromStanza(stanza map[string]string) (DebianBootstrapPackage, error) {
	pkg := DebianBootstrapPackage{
		Version:      stanza["Version"],
		Architecture: stanza["Architecture"],
		Filename:     stanza["Filename"],
		SHA256:       stanza["SHA256"],
	}
	if pkg.Version == "" || pkg.Architecture == "" || pkg.Filename == "" || pkg.SHA256 == "" || stanza["Size"] == "" {
		return DebianBootstrapPackage{}, errors.New("missing Version, Architecture, Filename, SHA256, or Size")
	}
	clean := path.Clean(pkg.Filename)
	if clean != pkg.Filename || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || !debianSafeFilenamePattern.MatchString(clean) {
		return DebianBootstrapPackage{}, fmt.Errorf("unsafe package filename %q", pkg.Filename)
	}
	if !debianDigestPattern.MatchString(pkg.SHA256) {
		return DebianBootstrapPackage{}, fmt.Errorf("malformed SHA256 %q", pkg.SHA256)
	}
	size, err := strconv.ParseInt(stanza["Size"], 10, 64)
	if err != nil || size <= 0 || size > maxBootstrapPackageBytes {
		return DebianBootstrapPackage{}, fmt.Errorf("invalid package Size %q", stanza["Size"])
	}
	pkg.Size = size
	return pkg, nil
}

func verifyDebianPackageBytes(ctx context.Context, client *http.Client, archiveBaseURL, snapshot string, pkg DebianBootstrapPackage) error {
	rawURL := fmt.Sprintf("%s/%s/%s", archiveBaseURL, snapshot, pkg.Filename)
	body, err := fetchBounded(ctx, client, rawURL, pkg.Size)
	if err != nil {
		return fmt.Errorf("verify %s: %w", pkg.Filename, err)
	}
	defer body.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, body)
	if err != nil {
		return fmt.Errorf("verify %s: %w", pkg.Filename, err)
	}
	if written != pkg.Size {
		return fmt.Errorf("verify %s: size mismatch: metadata=%d downloaded=%d", pkg.Filename, pkg.Size, written)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != pkg.SHA256 {
		return fmt.Errorf("verify %s: SHA256 mismatch: metadata=%s downloaded=%s", pkg.Filename, pkg.SHA256, got)
	}
	return nil
}

func validateDebianBootstrapPins(pins DebianBootstrapPins) error {
	if err := validateDebianSnapshot(pins.Snapshot); err != nil {
		return err
	}
	checks := []struct {
		pkg  DebianBootstrapPackage
		name string
		arch string
		path *regexp.Regexp
	}{
		{pins.OpenSSLAMD64, "openssl", "amd64", debianOpenSSLAMD64Pattern},
		{pins.OpenSSLARM64, "openssl", "arm64", debianOpenSSLARM64Pattern},
		{pins.CACertificates, "ca-certificates", "all", debianCACertificatesPattern},
	}
	for _, check := range checks {
		if check.pkg.Version == "" || len(check.pkg.Version) > 256 {
			return fmt.Errorf("invalid %s/%s Version", check.name, check.arch)
		}
		if check.pkg.Architecture != check.arch {
			return fmt.Errorf("invalid %s/%s Architecture %q", check.name, check.arch, check.pkg.Architecture)
		}
		if !check.path.MatchString(check.pkg.Filename) || path.Clean(check.pkg.Filename) != check.pkg.Filename {
			return fmt.Errorf("invalid %s/%s Filename %q", check.name, check.arch, check.pkg.Filename)
		}
		if !debianDigestPattern.MatchString(check.pkg.SHA256) {
			return fmt.Errorf("invalid %s/%s SHA256", check.name, check.arch)
		}
		if check.pkg.Size <= 0 || check.pkg.Size > maxBootstrapPackageBytes {
			return fmt.Errorf("invalid %s/%s Size %d", check.name, check.arch, check.pkg.Size)
		}
	}
	if pins.OpenSSLAMD64.Version != pins.OpenSSLARM64.Version {
		return errors.New("OpenSSL versions must agree across architectures")
	}
	amd64Stem := strings.TrimSuffix(pins.OpenSSLAMD64.Filename, "_amd64.deb")
	arm64Stem := strings.TrimSuffix(pins.OpenSSLARM64.Filename, "_arm64.deb")
	if amd64Stem != arm64Stem {
		return errors.New("OpenSSL package filename versions must agree across architectures")
	}
	return nil
}

func validateDebianSnapshot(snapshot string) error {
	if !debianSnapshotPattern.MatchString(snapshot) {
		return fmt.Errorf("invalid Debian snapshot timestamp %q", snapshot)
	}
	if _, err := time.Parse("20060102T150405Z", snapshot); err != nil {
		return fmt.Errorf("invalid Debian snapshot timestamp %q", snapshot)
	}
	return nil
}
