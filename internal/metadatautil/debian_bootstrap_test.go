// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

func TestApplyDebianBootstrapPinsAtomicallyUpdatesSharedManifest(t *testing.T) {
	root, manifestPath := writeDebianManifestRepo(t, testDebianBootstrapManifest(), 0o644)
	planPath := filepath.Join(root, "plan.json")
	pins := testDebianBootstrapPins()
	writeDebianPlan(t, planPath, pins)

	if err := ApplyDebianBootstrapPins(planPath, root); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != renderDebianBootstrapManifest(manifestFromPins(pins)) {
		t.Fatalf("atomic manifest content mismatch:\n%s", content)
	}
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("manifest mode = %o, want 644", info.Mode().Perm())
	}
	for _, want := range []string{pins.Snapshot, pins.OpenSSLAMD64.Filename, pins.OpenSSLAMD64.SHA256, pins.OpenSSLARM64.Filename, pins.CACertificates.Filename} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("manifest does not contain %q after atomic apply:\n%s", want, content)
		}
	}
}

func TestApplyDebianBootstrapPinsValidationFailureDoesNotMutateManifest(t *testing.T) {
	valid, _ := json.Marshal(testDebianBootstrapPins())
	for _, tc := range []struct{ name, plan, want string }{
		{"unknown", strings.Replace(string(valid), "{", `{"unknown":true,`, 1), "unknown field"},
		{"trailing", string(valid) + `{}`, "multiple JSON values"},
		{"duplicate top", strings.Replace(string(valid), `"snapshot":`, `"snapshot":"20260719T000000Z","snapshot":`, 1), "duplicate JSON key"},
		{"duplicate nested", strings.Replace(string(valid), `"sha256":`, `"sha256":"`+strings.Repeat("f", 64)+`","sha256":`, 1), "duplicate JSON key"},
		{"case alias top", strings.Replace(string(valid), `"snapshot":`, `"Snapshot":"20260719T000000Z","snapshot":`, 1), "unknown field"},
		{"case alias nested", strings.Replace(string(valid), `"sha256":`, `"SHA256":"`+strings.Repeat("f", 64)+`","sha256":`, 1), "unknown field"},
		{"wrong case only", strings.Replace(string(valid), `"snapshot":`, `"Snapshot":`, 1), "unknown field"},
		{"excessive depth", strings.Repeat("[", maxBootstrapPlanJSONDepth+1) + "0" + strings.Repeat("]", maxBootstrapPlanJSONDepth+1), "JSON nesting exceeds"},
		{"bad date", strings.Replace(string(valid), testDebianSnapshot, "20261399T999999Z", 1), "invalid Debian snapshot"},
		{"mixed versions", strings.Replace(string(valid), "openssl_3.5.9-1~deb13u2_arm64.deb", "openssl_3.5.8-1~deb13u2_arm64.deb", 1), "filename versions must agree"},
		{"traversal", strings.Replace(string(valid), "pool/main/o/openssl/openssl_3.5.9-1~deb13u2_amd64.deb", "pool/main/o/openssl/../../escape_amd64.deb", 1), "invalid openssl/amd64 Filename"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := testDebianBootstrapManifest()
			root, manifestPath := writeDebianManifestRepo(t, original, 0o644)
			planPath := filepath.Join(root, "plan.json")
			if err := os.WriteFile(planPath, []byte(tc.plan), 0o600); err != nil {
				t.Fatal(err)
			}
			err := ApplyDebianBootstrapPins(planPath, root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("atomic validation error = %v, want %q", err, tc.want)
			}
			if got, _ := os.ReadFile(manifestPath); string(got) != original {
				t.Fatal("manifest mutated on failed atomic apply")
			}
		})
	}
}

func TestParseDebianBootstrapManifestRejectsTrailingUnterminatedRecord(t *testing.T) {
	_, err := parseDebianBootstrapManifest(testDebianBootstrapManifest() + "malicious-command")
	if err == nil {
		t.Fatal("manifest parser accepted an unterminated eighth record")
	}
}

func TestDebianBootstrapFileReadsRejectUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "valid.env")
	if err := os.WriteFile(valid, []byte(testDebianBootstrapManifest()), 0o644); err != nil {
		t.Fatal(err)
	}
	oversized, symlink := filepath.Join(root, "oversized.env"), filepath.Join(root, "symlink.env")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), 4097), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{oversized, symlink, "/dev/null"} {
		if _, err := ReadDebianBootstrapManifest(path); err == nil {
			t.Fatalf("accepted unsafe manifest %s", path)
		}
	}
	planLink := filepath.Join(root, "plan-link.json")
	plan := filepath.Join(root, "plan.json")
	writeDebianPlan(t, plan, testDebianBootstrapPins())
	repoRoot, _ := writeDebianManifestRepo(t, testDebianBootstrapManifest(), 0o644)
	if err := os.Symlink(plan, planLink); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDebianBootstrapPins(planLink, repoRoot); err == nil {
		t.Fatal("accepted symlink plan")
	}
	oversizedPlan := filepath.Join(root, "oversized-plan.json")
	if err := os.WriteFile(oversizedPlan, bytes.Repeat([]byte(" "), maxBootstrapPlanBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ApplyDebianBootstrapPins(oversizedPlan, repoRoot); err == nil {
		t.Fatal("accepted oversized plan")
	}
}

func TestApplyDebianBootstrapPinsRejectsExecutableManifestWithoutMutation(t *testing.T) {
	original := testDebianBootstrapManifest()
	root, manifest := writeDebianManifestRepo(t, original, 0o755)
	plan := filepath.Join(root, "plan.json")
	writeDebianPlan(t, plan, testDebianBootstrapPins())
	if err := ApplyDebianBootstrapPins(plan, root); err == nil {
		t.Fatal("accepted executable manifest")
	}
	if content, _ := os.ReadFile(manifest); string(content) != original {
		t.Fatal("executable manifest mutated on rejection")
	}
}

func TestApplyDebianBootstrapPinsRetriesDirectorySyncForExistingContent(t *testing.T) {
	root, manifestPath := writeDebianManifestRepo(t, testDebianBootstrapManifest(), 0o644)
	planPath := filepath.Join(root, "plan.json")
	pins := testDebianBootstrapPins()
	writeDebianPlan(t, planPath, pins)
	originalSync := syncDebianBootstrapDirectory
	t.Cleanup(func() { syncDebianBootstrapDirectory = originalSync })
	var phases []string
	syncDebianBootstrapDirectory = func(_ int, phase string) error {
		phases = append(phases, phase)
		if len(phases) == 1 {
			return errors.New("injected directory sync failure")
		}
		return nil
	}

	if err := ApplyDebianBootstrapPins(planPath, root); err == nil || !strings.Contains(err.Error(), "durability is uncertain") {
		t.Fatalf("first apply should fail closed after publish sync failure, got %v", err)
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != renderDebianBootstrapManifest(manifestFromPins(pins)) {
		t.Fatal("first apply did not publish the complete new manifest before the injected sync failure")
	}
	if err := ApplyDebianBootstrapPins(planPath, root); err != nil {
		t.Fatalf("retry should resync identical published content: %v", err)
	}
	if strings.Join(phases, ",") != "publish,reuse" {
		t.Fatalf("directory sync phases = %v, want publish then reuse", phases)
	}
}

func TestApplyDebianBootstrapPinsRejectsManifestSymlinkEscapes(t *testing.T) {
	for _, parentSymlink := range []bool{false, true} {
		name := "manifest"
		if parentSymlink {
			name = "parent"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			outsideManifest := filepath.Join(outside, filepath.Base(DebianBootstrapManifestRelPath))
			original := testDebianBootstrapManifest()
			if err := os.WriteFile(outsideManifest, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "runtime"), 0o755); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(root, DebianBootstrapManifestRelPath)
			if parentSymlink {
				if err := os.Symlink(outside, filepath.Dir(manifestPath)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideManifest, manifestPath); err != nil {
					t.Fatal(err)
				}
			}
			plan := filepath.Join(root, "plan.json")
			writeDebianPlan(t, plan, testDebianBootstrapPins())
			if err := ApplyDebianBootstrapPins(plan, root); err == nil {
				t.Fatalf("accepted %s symlink escape", name)
			}
			if content, err := os.ReadFile(outsideManifest); err != nil || string(content) != original {
				t.Fatalf("outside manifest changed through %s symlink: %v", name, err)
			}
		})
	}
}

func TestWriteDebianBootstrapTempPropagatesWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readonly")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDebianBootstrapTemp(file, "content", 0o644); err == nil {
		t.Fatal("read-only staging file accepted a write")
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

func testDebianBootstrapPins() DebianBootstrapPins {
	return DebianBootstrapPins{
		Snapshot: testDebianSnapshot,
		OpenSSLAMD64: DebianBootstrapPackage{
			Version: "3.5.9-1~deb13u2", Architecture: "amd64", Filename: "pool/main/o/openssl/openssl_3.5.9-1~deb13u2_amd64.deb", SHA256: strings.Repeat("a", 64), Size: 100,
		},
		OpenSSLARM64: DebianBootstrapPackage{
			Version: "3.5.9-1~deb13u2", Architecture: "arm64", Filename: "pool/main/o/openssl/openssl_3.5.9-1~deb13u2_arm64.deb", SHA256: strings.Repeat("b", 64), Size: 101,
		},
		CACertificates: DebianBootstrapPackage{
			Version: "20260701", Architecture: "all", Filename: "pool/main/c/ca-certificates/ca-certificates_20260701_all.deb", SHA256: strings.Repeat("c", 64), Size: 102,
		},
	}
}

func writeDebianPlan(t *testing.T, path string, pins DebianBootstrapPins) {
	t.Helper()
	data, err := json.Marshal(pins)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeDebianManifestRepo(t *testing.T, content string, mode os.FileMode) (string, string) {
	t.Helper()
	root := t.TempDir()
	manifestPath := filepath.Join(root, DebianBootstrapManifestRelPath)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return root, manifestPath
}

func testDebianBootstrapManifest() string {
	return strings.Join([]string{
		"DEBIAN_SNAPSHOT=20260518T000000Z",
		"DEBIAN_OPENSSL_AMD64_PATH=pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb",
		"DEBIAN_OPENSSL_AMD64_SHA256=" + strings.Repeat("1", 64),
		"DEBIAN_OPENSSL_ARM64_PATH=pool/main/o/openssl/openssl_3.5.5-1~deb13u1_arm64.deb",
		"DEBIAN_OPENSSL_ARM64_SHA256=" + strings.Repeat("2", 64),
		"DEBIAN_CA_CERTIFICATES_PATH=pool/main/c/ca-certificates/ca-certificates_20250419_all.deb",
		"DEBIAN_CA_CERTIFICATES_SHA256=" + strings.Repeat("6", 64),
		"",
	}, "\n")
}
