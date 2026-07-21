// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testDebianSnapshot = "20260720T000000Z"

type debianFixturePackage struct {
	name         string
	version      string
	architecture string
	filename     string
	content      []byte
	digest       string
	size         int64
}

func (p debianFixturePackage) sha256() string {
	if p.digest != "" {
		return p.digest
	}
	sum := sha256.Sum256(p.content)
	return hex.EncodeToString(sum[:])
}

func (p debianFixturePackage) metadataSize() int64 {
	if p.size != 0 {
		return p.size
	}
	return int64(len(p.content))
}

func TestResolveDebianBootstrapPinsHandlesPackageRotation(t *testing.T) {
	amd64 := debianFixturePackage{
		name: "openssl", version: "3.5.9-1~deb13u2", architecture: "amd64",
		filename: "pool/main/o/openssl/openssl_3.5.9-1~deb13u2_amd64.deb", content: []byte("openssl-amd64-rotated"),
	}
	arm64 := debianFixturePackage{
		name: "openssl", version: "3.5.9-1~deb13u2", architecture: "arm64",
		filename: "pool/main/o/openssl/openssl_3.5.9-1~deb13u2_arm64.deb", content: []byte("openssl-arm64-rotated"),
	}
	ca := debianFixturePackage{
		name: "ca-certificates", version: "20260701", architecture: "all",
		filename: "pool/main/c/ca-certificates/ca-certificates_20260701_all.deb", content: []byte("ca-certificates-rotated"),
	}
	client := newDebianFixtureClient(t, []debianFixturePackage{amd64, ca}, []debianFixturePackage{arm64, ca})

	pins, err := ResolveDebianBootstrapPins(context.Background(), client, "https://snapshot.test/archive/debian", testDebianSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if pins.OpenSSLAMD64.Filename != amd64.filename || pins.OpenSSLARM64.Filename != arm64.filename || pins.CACertificates.Filename != ca.filename {
		t.Fatalf("resolved package rotation incorrectly: %#v", pins)
	}
	if pins.OpenSSLAMD64.SHA256 != amd64.sha256() || pins.OpenSSLARM64.SHA256 != arm64.sha256() || pins.CACertificates.SHA256 != ca.sha256() {
		t.Fatalf("resolved package digests incorrectly: %#v", pins)
	}
}

func TestResolveDebianBootstrapPinsRejectsMissingArchitecture(t *testing.T) {
	ca := debianFixturePackage{name: "ca-certificates", version: "1", architecture: "all", filename: "pool/main/c/ca-certificates/ca-certificates_1_all.deb", content: []byte("ca")}
	amd64 := debianFixturePackage{name: "openssl", version: "1", architecture: "amd64", filename: "pool/main/o/openssl/openssl_1_amd64.deb", content: []byte("amd64")}
	client := newDebianFixtureClient(t, []debianFixturePackage{amd64, ca}, []debianFixturePackage{ca})

	_, err := ResolveDebianBootstrapPins(context.Background(), client, "https://snapshot.test/archive/debian", testDebianSnapshot)
	if err == nil || !strings.Contains(err.Error(), "expected exactly one openssl/arm64 package") {
		t.Fatalf("missing arm64 package error = %v", err)
	}
}

func TestResolveDebianBootstrapPinsRejectsCADigestDisagreement(t *testing.T) {
	amd64 := debianFixturePackage{name: "openssl", version: "1", architecture: "amd64", filename: "pool/main/o/openssl/openssl_1_amd64.deb", content: []byte("amd64")}
	arm64 := debianFixturePackage{name: "openssl", version: "1", architecture: "arm64", filename: "pool/main/o/openssl/openssl_1_arm64.deb", content: []byte("arm64")}
	caAMD64 := debianFixturePackage{name: "ca-certificates", version: "1", architecture: "all", filename: "pool/main/c/ca-certificates/ca-certificates_1_all.deb", content: []byte("ca")}
	caARM64 := caAMD64
	caARM64.digest = strings.Repeat("f", 64)
	client := newDebianFixtureClient(t, []debianFixturePackage{amd64, caAMD64}, []debianFixturePackage{arm64, caARM64})

	_, err := ResolveDebianBootstrapPins(context.Background(), client, "https://snapshot.test/archive/debian", testDebianSnapshot)
	if err == nil || !strings.Contains(err.Error(), "metadata disagreement") {
		t.Fatalf("CA digest disagreement error = %v", err)
	}
}

func TestResolveDebianBootstrapPinsRejectsDownloadedDigestMismatch(t *testing.T) {
	badDigest := strings.Repeat("0", 64)
	amd64 := debianFixturePackage{name: "openssl", version: "1", architecture: "amd64", filename: "pool/main/o/openssl/openssl_1_amd64.deb", content: []byte("amd64"), digest: badDigest}
	arm64 := debianFixturePackage{name: "openssl", version: "1", architecture: "arm64", filename: "pool/main/o/openssl/openssl_1_arm64.deb", content: []byte("arm64")}
	ca := debianFixturePackage{name: "ca-certificates", version: "1", architecture: "all", filename: "pool/main/c/ca-certificates/ca-certificates_1_all.deb", content: []byte("ca")}
	client := newDebianFixtureClient(t, []debianFixturePackage{amd64, ca}, []debianFixturePackage{arm64, ca})

	_, err := ResolveDebianBootstrapPins(context.Background(), client, "https://snapshot.test/archive/debian", testDebianSnapshot)
	if err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatalf("downloaded digest mismatch error = %v", err)
	}
}

func TestResolveDebianBootstrapPinsRejectsMalformedMetadata(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/Packages.gz") {
			var body bytes.Buffer
			gz := gzip.NewWriter(&body)
			_, _ = gz.Write([]byte("Package openssl\n"))
			_ = gz.Close()
			return httpFixtureResponse(http.StatusOK, body.Bytes()), nil
		}
		return httpFixtureResponse(http.StatusNotFound, nil), nil
	})}

	_, err := ResolveDebianBootstrapPins(context.Background(), client, "https://snapshot.test/archive/debian", testDebianSnapshot)
	if err == nil || !strings.Contains(err.Error(), "malformed field") {
		t.Fatalf("malformed Packages.gz error = %v", err)
	}
}

func TestResolveDebianBootstrapPinsRejectsUntrustedTransport(t *testing.T) {
	for _, base := range []string{
		"http://snapshot.test/archive/debian",
		"https://user@snapshot.test/archive/debian",
		"https://snapshot.test/archive/debian?query=1",
		"https://snapshot.test/archive/debian#fragment",
	} {
		if _, err := ResolveDebianBootstrapPins(context.Background(), &http.Client{}, base, testDebianSnapshot); err == nil {
			t.Fatalf("accepted untrusted archive URL %q", base)
		}
	}
	for _, location := range []string{"http://snapshot.test/file/index", "https://other.test/file/index"} {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			response := httpFixtureResponse(http.StatusFound, nil)
			response.Header.Set("Location", location)
			return response, nil
		})}
		_, err := ResolveDebianBootstrapPins(context.Background(), client, "https://snapshot.test/archive/debian", testDebianSnapshot)
		if err == nil || !strings.Contains(err.Error(), "redirect outside reviewed HTTPS origin") {
			t.Fatalf("redirect %q error = %v", location, err)
		}
	}
}

func TestResolveDebianBootstrapPinsRejectsShortPackage(t *testing.T) {
	amd64 := debianFixturePackage{name: "openssl", version: "1", architecture: "amd64", filename: "pool/main/o/openssl/openssl_1_amd64.deb", content: []byte("amd64"), size: 7}
	arm64 := debianFixturePackage{name: "openssl", version: "1", architecture: "arm64", filename: "pool/main/o/openssl/openssl_1_arm64.deb", content: []byte("arm64")}
	ca := debianFixturePackage{name: "ca-certificates", version: "1", architecture: "all", filename: "pool/main/c/ca-certificates/ca-certificates_1_all.deb", content: []byte("ca")}
	_, err := ResolveDebianBootstrapPins(context.Background(), newDebianFixtureClient(t, []debianFixturePackage{amd64, ca}, []debianFixturePackage{arm64, ca}), "https://snapshot.test/archive/debian", testDebianSnapshot)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("short package error = %v", err)
	}
}

func TestFetchBoundedRejectsBadResponses(t *testing.T) {
	responses := []*http.Response{
		httpFixtureResponse(http.StatusServiceUnavailable, nil),
		httpFixtureResponse(http.StatusOK, []byte("12345")),
		httpFixtureResponse(http.StatusOK, []byte("12345")),
	}
	responses[2].ContentLength = -1
	for _, response := range responses {
		response := response
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response, nil })}
		body, err := fetchBounded(context.Background(), client, "https://snapshot.test/file", 4)
		if err == nil {
			_, err = io.ReadAll(body)
			_ = body.Close()
		}
		if err == nil {
			t.Fatal("bounded fetch accepted a bad response")
		}
	}
}

func TestScanDebianPackageStanzasRejectsScalarContinuations(t *testing.T) {
	for _, field := range []string{"Package", "Version", "Architecture", "Filename", "Size", "SHA256"} {
		t.Run(field, func(t *testing.T) {
			metadata := fmt.Sprintf("%s: value\n continuation\n\n", field)
			err := scanDebianPackageStanzas(strings.NewReader(metadata), func(map[string]string) error { return nil })
			if err == nil || !strings.Contains(err.Error(), "continuation is not allowed for scalar field "+field) {
				t.Fatalf("scalar continuation error = %v", err)
			}
		})
	}
}

func TestScanDebianPackageStanzasEnforcesResourceBounds(t *testing.T) {
	var fields strings.Builder
	for i := 0; i <= maxPackagesStanzaFields; i++ {
		fmt.Fprintf(&fields, "X%d: value\n", i)
	}
	for name, metadata := range map[string]string{
		"line":   "Description: " + strings.Repeat("x", maxPackagesLineBytes) + "\n",
		"lines":  "Description: x\n" + strings.Repeat(" x\n", maxPackagesStanzaLines),
		"stanza": "Description: x\n" + strings.Repeat(" "+strings.Repeat("x", 900000)+"\n", 5),
		"fields": fields.String(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := scanDebianPackageStanzas(strings.NewReader(metadata), func(map[string]string) error { return nil }); err == nil {
				t.Fatal("oversized package metadata was accepted")
			}
		})
	}
}

func newDebianFixtureClient(t *testing.T, amd64Packages, arm64Packages []debianFixturePackage) *http.Client {
	t.Helper()
	artifacts := make(map[string][]byte)
	for _, pkg := range append(append([]debianFixturePackage{}, amd64Packages...), arm64Packages...) {
		artifacts["/archive/debian/"+testDebianSnapshot+"/"+pkg.filename] = pkg.content
	}
	indexes := map[string][]byte{
		"amd64": gzipDebianPackages(t, amd64Packages),
		"arm64": gzipDebianPackages(t, arm64Packages),
	}
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "snapshot.test" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Fatalf("unexpected Debian request: %s %s Accept-Encoding=%q", r.Method, r.URL, r.Header.Get("Accept-Encoding"))
		}
		for architecture, body := range indexes {
			if r.URL.Path == "/archive/debian/"+testDebianSnapshot+"/dists/trixie/main/binary-"+architecture+"/Packages.gz" {
				return httpFixtureResponse(http.StatusOK, body), nil
			}
		}
		if body, ok := artifacts[r.URL.Path]; ok {
			return httpFixtureResponse(http.StatusOK, body), nil
		}
		return httpFixtureResponse(http.StatusNotFound, nil), nil
	})}
}

func httpFixtureResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header),
	}
}

func gzipDebianPackages(t *testing.T, packages []debianFixturePackage) []byte {
	t.Helper()
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	for _, pkg := range packages {
		_, err := fmt.Fprintf(gz, "Package: %s\nVersion: %s\nArchitecture: %s\nFilename: %s\nSize: %d\nSHA256: %s\n\n", pkg.name, pkg.version, pkg.architecture, pkg.filename, pkg.metadataSize(), pkg.sha256())
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}
