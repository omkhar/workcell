// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
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
	maxBootstrapPlanBytes     = 64 << 10
	maxBootstrapPlanJSONDepth = 32
)

var (
	debianSnapshotPattern       = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)
	debianDigestPattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	debianSafeFilenamePattern   = regexp.MustCompile(`^pool/[A-Za-z0-9.+~_/-]+\.deb$`)
	debianOpenSSLAMD64Pattern   = regexp.MustCompile(`^pool/main/o/openssl/openssl_[A-Za-z0-9.+~_-]+_amd64\.deb$`)
	debianOpenSSLARM64Pattern   = regexp.MustCompile(`^pool/main/o/openssl/openssl_[A-Za-z0-9.+~_-]+_arm64\.deb$`)
	debianCACertificatesPattern = regexp.MustCompile(`^pool/main/c/ca-certificates/ca-certificates_[A-Za-z0-9.+~_-]+_all\.deb$`)
)

var syncDebianBootstrapDirectory = func(fd int, _ string) error {
	return unix.Fsync(fd)
}

const DebianBootstrapManifestRelPath = "runtime/container/debian-bootstrap.env"

// DebianBootstrapPackage is the immutable package record taken from a Debian
// snapshot Packages.gz index and verified against the referenced .deb bytes.
type DebianBootstrapPackage struct {
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Filename     string `json:"filename"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
}

// DebianBootstrapPins is the complete snapshot TLS bootstrap tuple published
// through the one manifest consumed by both Dockerfiles.
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

type DebianBootstrapManifest struct {
	Snapshot             string `json:"snapshot"`
	OpenSSLAMD64Path     string `json:"openssl_amd64_path"`
	OpenSSLAMD64SHA256   string `json:"openssl_amd64_sha256"`
	OpenSSLARM64Path     string `json:"openssl_arm64_path"`
	OpenSSLARM64SHA256   string `json:"openssl_arm64_sha256"`
	CACertificatesPath   string `json:"ca_certificates_path"`
	CACertificatesSHA256 string `json:"ca_certificates_sha256"`
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
// archive endpoint. It is the production entrypoint used by the pin updater.
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

var debianBootstrapManifestFields = []struct {
	Name  string
	Value func(DebianBootstrapManifest) string
}{
	{"DEBIAN_SNAPSHOT", func(p DebianBootstrapManifest) string { return p.Snapshot }},
	{"DEBIAN_OPENSSL_AMD64_PATH", func(p DebianBootstrapManifest) string { return p.OpenSSLAMD64Path }},
	{"DEBIAN_OPENSSL_AMD64_SHA256", func(p DebianBootstrapManifest) string { return p.OpenSSLAMD64SHA256 }},
	{"DEBIAN_OPENSSL_ARM64_PATH", func(p DebianBootstrapManifest) string { return p.OpenSSLARM64Path }},
	{"DEBIAN_OPENSSL_ARM64_SHA256", func(p DebianBootstrapManifest) string { return p.OpenSSLARM64SHA256 }},
	{"DEBIAN_CA_CERTIFICATES_PATH", func(p DebianBootstrapManifest) string { return p.CACertificatesPath }},
	{"DEBIAN_CA_CERTIFICATES_SHA256", func(p DebianBootstrapManifest) string { return p.CACertificatesSHA256 }},
}

// ApplyDebianBootstrapPins validates a complete resolution plan and atomically
// replaces the one manifest consumed by both shipped Dockerfiles. repoRoot must
// be the trusted, physical repository root; every path below it is opened
// descriptor-relatively without following symlinks.
func ApplyDebianBootstrapPins(planPath, repoRoot string) error {
	data, err := readRegularFileBounded(planPath, maxBootstrapPlanBytes)
	if err != nil {
		return err
	}
	if err := rejectDebianBootstrapDuplicateJSONKeys(data); err != nil {
		return fmt.Errorf("decode Debian bootstrap plan: %w", err)
	}
	var pins DebianBootstrapPins
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pins); err != nil {
		return fmt.Errorf("decode Debian bootstrap plan: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("decode Debian bootstrap plan: %w", err)
	}
	if err := validateDebianBootstrapPlanFields(data); err != nil {
		return fmt.Errorf("decode Debian bootstrap plan: %w", err)
	}
	if err := validateDebianBootstrapPins(pins); err != nil {
		return err
	}
	manifest := manifestFromPins(pins)
	updated := renderDebianBootstrapManifest(manifest)
	directoryFD, err := openDebianBootstrapManifestDirectory(repoRoot)
	if err != nil {
		return err
	}
	defer unix.Close(directoryFD)
	manifestName := filepath.Base(DebianBootstrapManifestRelPath)
	manifestFD, err := unix.Openat(directoryFD, manifestName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open Debian bootstrap manifest: %w", err)
	}
	original, mode, err := readRegularOpenFile(manifestFD, manifestName, 4096)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(repoRoot, DebianBootstrapManifestRelPath)
	if mode&0o111 != 0 {
		return fmt.Errorf("Debian bootstrap manifest must not be executable: %s", manifestPath)
	}
	if _, err := parseDebianBootstrapManifest(string(original)); err != nil {
		return fmt.Errorf("validate current Debian bootstrap manifest: %w", err)
	}
	if string(original) == updated {
		if err := syncDebianBootstrapDirectory(directoryFD, "reuse"); err != nil {
			return fmt.Errorf("sync Debian bootstrap manifest directory for existing content: %w", err)
		}
		return nil
	}
	temp, tempName, err := createDebianBootstrapTemp(directoryFD)
	if err != nil {
		return fmt.Errorf("stage %s: %w", manifestPath, err)
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = unix.Unlinkat(directoryFD, tempName, 0)
		}
	}()
	if err := writeDebianBootstrapTemp(temp, updated, mode); err != nil {
		return fmt.Errorf("stage %s: %w", manifestPath, err)
	}
	if err := unix.Renameat(directoryFD, tempName, directoryFD, manifestName); err != nil {
		return fmt.Errorf("publish %s: %w", manifestPath, err)
	}
	removeTemp = false
	if err := syncDebianBootstrapDirectory(directoryFD, "publish"); err != nil {
		return fmt.Errorf("manifest published but directory durability is uncertain: %w", err)
	}
	return nil
}

func openDebianBootstrapManifestDirectory(repoRoot string) (int, error) {
	if !filepath.IsAbs(repoRoot) || filepath.Clean(repoRoot) != repoRoot {
		return -1, fmt.Errorf("repository root must be an absolute clean path: %s", repoRoot)
	}
	currentFD, err := unix.Open(repoRoot, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("open trusted repository root: %w", err)
	}
	for _, component := range strings.Split(filepath.Dir(DebianBootstrapManifestRelPath), string(filepath.Separator)) {
		nextFD, openErr := unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		closeErr := unix.Close(currentFD)
		if openErr != nil {
			return -1, fmt.Errorf("open Debian bootstrap manifest directory component %q: %w", component, openErr)
		}
		if closeErr != nil {
			_ = unix.Close(nextFD)
			return -1, fmt.Errorf("close Debian bootstrap manifest directory parent: %w", closeErr)
		}
		currentFD = nextFD
	}
	return currentFD, nil
}

func createDebianBootstrapTemp(directoryFD int) (*os.File, string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return nil, "", err
		}
		name := fmt.Sprintf(".debian-bootstrap-%x.tmp", suffix[:])
		fd, err := unix.Openat(directoryFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
		if err == nil {
			file := os.NewFile(uintptr(fd), name)
			if file == nil {
				_ = unix.Close(fd)
				return nil, "", errors.New("create Debian bootstrap staging file")
			}
			return file, name, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("unable to allocate Debian bootstrap staging file")
}

func writeDebianBootstrapTemp(temp *os.File, content string, mode os.FileMode) (err error) {
	defer func() {
		if closeErr := temp.Close(); err == nil {
			err = closeErr
		}
	}()
	written, err := temp.WriteString(content)
	if err != nil {
		return err
	}
	if written != len(content) {
		return io.ErrShortWrite
	}
	if err = temp.Chmod(mode); err != nil {
		return err
	}
	return temp.Sync()
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

func manifestFromPins(pins DebianBootstrapPins) DebianBootstrapManifest {
	return DebianBootstrapManifest{
		Snapshot: pins.Snapshot, OpenSSLAMD64Path: pins.OpenSSLAMD64.Filename,
		OpenSSLAMD64SHA256: pins.OpenSSLAMD64.SHA256, OpenSSLARM64Path: pins.OpenSSLARM64.Filename,
		OpenSSLARM64SHA256: pins.OpenSSLARM64.SHA256, CACertificatesPath: pins.CACertificates.Filename,
		CACertificatesSHA256: pins.CACertificates.SHA256,
	}
}

func renderDebianBootstrapManifest(manifest DebianBootstrapManifest) string {
	var builder strings.Builder
	for _, field := range debianBootstrapManifestFields {
		fmt.Fprintf(&builder, "%s=%s\n", field.Name, field.Value(manifest))
	}
	return builder.String()
}

func parseDebianBootstrapManifest(content string) (DebianBootstrapManifest, error) {
	var zero DebianBootstrapManifest
	if len(content) > 4096 {
		return zero, errors.New("manifest exceeds the 4096-byte limit")
	}
	if strings.Contains(content, "\r") || !strings.HasSuffix(content, "\n") {
		return zero, errors.New("manifest must use LF lines and end with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if len(lines) != len(debianBootstrapManifestFields) {
		return zero, fmt.Errorf("manifest must contain exactly %d fields", len(debianBootstrapManifestFields))
	}
	values := make(map[string]string, len(lines))
	for i, field := range debianBootstrapManifestFields {
		name, value, ok := strings.Cut(lines[i], "=")
		if !ok || name != field.Name || value == "" {
			return zero, fmt.Errorf("manifest line %d must define %s", i+1, field.Name)
		}
		values[name] = value
	}
	manifest := DebianBootstrapManifest{
		Snapshot: values["DEBIAN_SNAPSHOT"], OpenSSLAMD64Path: values["DEBIAN_OPENSSL_AMD64_PATH"],
		OpenSSLAMD64SHA256: values["DEBIAN_OPENSSL_AMD64_SHA256"], OpenSSLARM64Path: values["DEBIAN_OPENSSL_ARM64_PATH"],
		OpenSSLARM64SHA256: values["DEBIAN_OPENSSL_ARM64_SHA256"], CACertificatesPath: values["DEBIAN_CA_CERTIFICATES_PATH"],
		CACertificatesSHA256: values["DEBIAN_CA_CERTIFICATES_SHA256"],
	}
	if err := validateDebianSnapshot(manifest.Snapshot); err != nil {
		return zero, err
	}
	for _, check := range []struct {
		name    string
		value   string
		pattern *regexp.Regexp
	}{
		{"DEBIAN_OPENSSL_AMD64_PATH", manifest.OpenSSLAMD64Path, debianOpenSSLAMD64Pattern},
		{"DEBIAN_OPENSSL_AMD64_SHA256", manifest.OpenSSLAMD64SHA256, debianDigestPattern},
		{"DEBIAN_OPENSSL_ARM64_PATH", manifest.OpenSSLARM64Path, debianOpenSSLARM64Pattern},
		{"DEBIAN_OPENSSL_ARM64_SHA256", manifest.OpenSSLARM64SHA256, debianDigestPattern},
		{"DEBIAN_CA_CERTIFICATES_PATH", manifest.CACertificatesPath, debianCACertificatesPattern},
		{"DEBIAN_CA_CERTIFICATES_SHA256", manifest.CACertificatesSHA256, debianDigestPattern},
	} {
		if !check.pattern.MatchString(check.value) {
			return zero, fmt.Errorf("manifest contains malformed %s", check.name)
		}
	}
	if strings.TrimSuffix(manifest.OpenSSLAMD64Path, "_amd64.deb") != strings.TrimSuffix(manifest.OpenSSLARM64Path, "_arm64.deb") {
		return zero, errors.New("manifest OpenSSL package filename versions must agree across architectures")
	}
	return manifest, nil
}

// ReadDebianBootstrapManifest validates and returns the canonical shared pin
// manifest without evaluating it as shell code.
func ReadDebianBootstrapManifest(manifestPath string) (DebianBootstrapManifest, error) {
	content, err := readRegularFileBounded(manifestPath, 4096)
	if err != nil {
		return DebianBootstrapManifest{}, err
	}
	return parseDebianBootstrapManifest(string(content))
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

func readRegularFileBounded(filePath string, maxBytes int64) ([]byte, error) {
	fd, err := unix.Open(filePath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	content, _, err := readRegularOpenFile(fd, filePath, maxBytes)
	return content, err
}

func readRegularOpenFile(fd int, label string, maxBytes int64) ([]byte, os.FileMode, error) {
	file := os.NewFile(uintptr(fd), label)
	if file == nil {
		_ = unix.Close(fd)
		return nil, 0, fmt.Errorf("unable to open file: %s", label)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("file must be regular: %s", label)
	}
	if info.Size() > maxBytes {
		return nil, 0, fmt.Errorf("file exceeds %d-byte limit: %s", maxBytes, label)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(content)) > maxBytes {
		return nil, 0, fmt.Errorf("file exceeds %d-byte limit: %s", maxBytes, label)
	}
	return content, info.Mode().Perm(), nil
}

func validateDebianBootstrapPlanFields(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	packageNames := []string{"openssl_amd64", "openssl_arm64", "ca_certificates"}
	if err := validateExactJSONFields(root, append([]string{"snapshot"}, packageNames...)); err != nil {
		return err
	}
	for _, name := range packageNames {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(root[name], &fields); err != nil {
			return fmt.Errorf("field %q must be an object: %w", name, err)
		}
		if err := validateExactJSONFields(fields, []string{"version", "architecture", "filename", "sha256", "size"}); err != nil {
			return fmt.Errorf("field %q: %w", name, err)
		}
	}
	return nil
}

func validateExactJSONFields(actual map[string]json.RawMessage, expected []string) error {
	allowed := make(map[string]struct{}, len(expected))
	for _, name := range expected {
		allowed[name] = struct{}{}
	}
	var unknown []string
	for name := range actual {
		if _, ok := allowed[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown field %q", unknown[0])
	}
	for _, name := range expected {
		if _, ok := actual[name]; !ok {
			return fmt.Errorf("missing field %q", name)
		}
	}
	return nil
}

func rejectDebianBootstrapDuplicateJSONKeys(raw []byte) error {
	return scanDebianBootstrapJSONValue(json.NewDecoder(bytes.NewReader(raw)), 0)
}

func scanDebianBootstrapJSONValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	if depth >= maxBootstrapPlanJSONDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", maxBootstrapPlanJSONDepth)
	}
	if delimiter == '{' {
		seen := make(map[string]struct{})
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("JSON object key must be a string")
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = struct{}{}
			if err := scanDebianBootstrapJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	} else if delimiter == '[' {
		for decoder.More() {
			if err := scanDebianBootstrapJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}
