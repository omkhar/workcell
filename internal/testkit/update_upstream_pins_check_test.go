// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	updaterFixtureActionlintAssetID = "424242"
	updaterFixtureCurlVersion       = "8.7.1"
	updaterFixtureRedirectURL       = "https://release-assets.githubusercontent.com/github-production-release-asset/12345678/01234567-89ab-cdef-0123-456789abcdef?sp=r&sv=fixture"
)

type updaterFixturePins struct {
	RuntimeBase                  string
	ValidatorBase                string
	GoToolchain                  string
	GoLanguage                   string
	GoAMD64SHA                   string
	GoARM64SHA                   string
	RustVersion                  string
	RuntimeRustImage             string
	RustupVersion                string
	RustupAMD64SHA               string
	RustupARM64SHA               string
	HadolintVersion              string
	HadolintAMD64SHA             string
	HadolintARM64SHA             string
	HadolintChecksums            string
	BuildkitImage                string
	BuildxVersion                string
	CosignVersion                string
	UpstreamRefreshCosignVersion string
	QEMUImage                    string
	SyftVersion                  string
	ActionlintVersion            string
	ActionlintSHA                string
	ZizmorVersion                string
	ZizmorSHA                    string
	ReleaseZizmorVersion         string
	ReleaseZizmorSHA             string
}

type updaterFixtureDebianPackage struct {
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Filename     string `json:"filename"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
}

type updaterFixtureDebianPlan struct {
	Snapshot       string                      `json:"snapshot"`
	OpenSSLAMD64   updaterFixtureDebianPackage `json:"openssl_amd64"`
	OpenSSLARM64   updaterFixtureDebianPackage `json:"openssl_arm64"`
	CACertificates updaterFixtureDebianPackage `json:"ca_certificates"`
}

type updaterFixtureManifest struct {
	Snapshot             string
	OpenSSLAMD64Path     string
	OpenSSLAMD64SHA256   string
	OpenSSLARM64Path     string
	OpenSSLARM64SHA256   string
	CACertificatesPath   string
	CACertificatesSHA256 string
}

type updaterFixtureTreeEntry struct {
	Mode            fs.FileMode
	Size            int64
	ModTimeUnixNano int64
	Digest          [sha256.Size]byte
	Link            string
}

type updaterFixtureRun struct {
	Code          int
	Output        string
	ResolutionLog string
	CIToolsLog    []string
	ProviderLog   []string
	CurlCalls     []updaterFixtureCurlCall
}

type updaterFixtureCurlCall struct {
	Kind   string
	URL    string
	Config string
	Args   string
}

type updaterFixtureOptions struct {
	Token                 string
	CurlVersion           string
	AssetID               string
	AssetAPIResponse      string
	AssetRedirectResponse string
}

func defaultUpdaterFixtureOptions() updaterFixtureOptions {
	return updaterFixtureOptions{
		CurlVersion:           updaterFixtureCurlVersion,
		AssetID:               updaterFixtureActionlintAssetID,
		AssetAPIResponse:      "redirect",
		AssetRedirectResponse: "body",
	}
}

func TestUpdateUpstreamPinsHermeticApplyAndCheck(t *testing.T) {
	t.Parallel()

	pins := readUpdaterFixturePins(t)
	targetPlan := updaterTargetDebianPlan()
	targetManifest := updaterManifestFromPlan(targetPlan)
	driftedManifest := updaterDriftedManifest(t, targetPlan.Snapshot)
	assertUpdaterManifestFieldsDiffer(t, driftedManifest, targetManifest)
	toolsRoot := t.TempDir()
	citoolsPath := buildUpdaterFixtureCITools(t, toolsRoot)
	goWrapperPath := writeUpdaterFixtureGoWrapper(t, toolsRoot)
	fixtureRoot := writeUpdaterFixture(t, driftedManifest, 0o640)

	beforeCheck := snapshotUpdaterFixtureTree(t, fixtureRoot)
	checkScratch := t.TempDir()
	checkRun := runUpdaterFixture(t, fixtureRoot, checkScratch, citoolsPath, goWrapperPath, pins, targetPlan, "--check")
	assertUpdaterFixtureRun(t, checkRun, 1, updaterFixtureSummary(pins, driftedManifest, targetManifest), targetPlan.Snapshot)
	assertUpdaterCommandCounts(t, checkRun.CIToolsLog, map[string]int{
		"hadolint-manifest-checksum": 2,
		"select-buildx-version":      1,
		"inspect-debian-bootstrap":   1,
		"apply-debian-bootstrap":     0,
		"check-pinned-inputs":        0,
	})
	if want := []string{"summary", "check"}; !reflect.DeepEqual(checkRun.ProviderLog, want) {
		t.Fatalf("provider updater command log = %q, want %q", checkRun.ProviderLog, want)
	}
	assertUpdaterScratchHasOnlyLogs(t, checkScratch)
	afterCheck := snapshotUpdaterFixtureTree(t, fixtureRoot)
	if !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatalf("update-upstream-pins.sh --check mutated its fixture tree\nbefore: %#v\nafter:  %#v", beforeCheck, afterCheck)
	}

	beforeApplyFiles := snapshotUpdaterFixtureFiles(t, fixtureRoot)
	manifestPath := filepath.Join(fixtureRoot, "runtime", "container", "debian-bootstrap.env")
	manifestInfoBefore, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	applyScratch := t.TempDir()
	applyRun := runUpdaterFixture(t, fixtureRoot, applyScratch, citoolsPath, goWrapperPath, pins, targetPlan, "--apply")
	assertUpdaterFixtureRun(t, applyRun, 0, updaterFixtureSummary(pins, driftedManifest, targetManifest), targetPlan.Snapshot)
	assertUpdaterCommandCounts(t, applyRun.CIToolsLog, map[string]int{
		"hadolint-manifest-checksum": 2,
		"select-buildx-version":      1,
		"inspect-debian-bootstrap":   1,
		"apply-debian-bootstrap":     1,
		"check-pinned-inputs":        1,
	})
	if want := []string{"summary", "check", "apply"}; !reflect.DeepEqual(applyRun.ProviderLog, want) {
		t.Fatalf("provider updater command log = %q, want %q", applyRun.ProviderLog, want)
	}
	assertUpdaterScratchHasOnlyLogs(t, applyScratch)
	manifestContent, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(manifestContent), renderUpdaterFixtureManifest(targetManifest); got != want {
		t.Fatalf("applied Debian manifest = %q, want %q", got, want)
	}
	manifestInfo, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := manifestInfo.Mode().Perm(), fs.FileMode(0o640); got != want {
		t.Fatalf("applied Debian manifest mode = %04o, want %04o", got, want)
	}
	if os.SameFile(manifestInfoBefore, manifestInfo) {
		t.Fatal("updater apply reused the original manifest file instead of atomically replacing it")
	}
	assertNoUpdaterFixtureTempResidue(t, fixtureRoot)
	assertOnlyUpdaterManifestChanged(t, beforeApplyFiles, snapshotUpdaterFixtureFiles(t, fixtureRoot))

	beforeCleanCheck := snapshotUpdaterFixtureTree(t, fixtureRoot)
	cleanScratch := t.TempDir()
	cleanRun := runUpdaterFixture(t, fixtureRoot, cleanScratch, citoolsPath, goWrapperPath, pins, targetPlan, "--check")
	assertUpdaterFixtureRun(t, cleanRun, 0, updaterFixtureSummary(pins, targetManifest, targetManifest), targetPlan.Snapshot)
	assertUpdaterCommandCounts(t, cleanRun.CIToolsLog, map[string]int{
		"hadolint-manifest-checksum": 2,
		"select-buildx-version":      1,
		"inspect-debian-bootstrap":   1,
		"apply-debian-bootstrap":     0,
		"check-pinned-inputs":        0,
	})
	assertUpdaterScratchHasOnlyLogs(t, cleanScratch)
	afterCleanCheck := snapshotUpdaterFixtureTree(t, fixtureRoot)
	if !reflect.DeepEqual(afterCleanCheck, beforeCleanCheck) {
		t.Fatalf("clean update-upstream-pins.sh --check was not byte-stable\nbefore: %#v\nafter:  %#v", beforeCleanCheck, afterCleanCheck)
	}
}

func TestUpdateUpstreamPinsCredentialedRequestsKeepTokensOffArgvAndRedirects(t *testing.T) {
	t.Parallel()

	pins := readUpdaterFixturePins(t)
	targetPlan := updaterTargetDebianPlan()
	targetManifest := updaterManifestFromPlan(targetPlan)
	driftedManifest := updaterDriftedManifest(t, targetPlan.Snapshot)
	toolsRoot := t.TempDir()
	citoolsPath := buildUpdaterFixtureCITools(t, toolsRoot)
	goWrapperPath := writeUpdaterFixtureGoWrapper(t, toolsRoot)
	fixtureRoot := writeUpdaterFixture(t, driftedManifest, 0o640)
	const token = "fixture-github-token"

	options := defaultUpdaterFixtureOptions()
	options.Token = token
	run := runUpdaterFixtureWithOptions(t, fixtureRoot, t.TempDir(), citoolsPath, goWrapperPath, pins, targetPlan, "--check", options)
	assertUpdaterFixtureRun(t, run, 1, updaterFixtureSummary(pins, driftedManifest, targetManifest), targetPlan.Snapshot)
	assertCredentialedUpdaterCurlCalls(t, run.CurlCalls, token)
}

func TestUpdateUpstreamPinsRequiresBoundedCurl(t *testing.T) {
	t.Parallel()

	pins := readUpdaterFixturePins(t)
	targetPlan := updaterTargetDebianPlan()
	targetManifest := updaterManifestFromPlan(targetPlan)
	driftedManifest := updaterDriftedManifest(t, targetPlan.Snapshot)
	toolsRoot := t.TempDir()
	citoolsPath := buildUpdaterFixtureCITools(t, toolsRoot)
	goWrapperPath := writeUpdaterFixtureGoWrapper(t, toolsRoot)
	fixtureRoot := writeUpdaterFixture(t, driftedManifest, 0o640)

	for _, tc := range []struct {
		name        string
		version     string
		wantCode    int
		wantMessage string
	}{
		{name: "minimum", version: "8.4.0", wantCode: 1, wantMessage: updaterFixtureSummary(pins, driftedManifest, targetManifest)},
		{name: "older", version: "8.3.0", wantCode: 1, wantMessage: "curl 8.4.0 or newer is required"},
		{name: "malformed", version: "not-a-version", wantCode: 1, wantMessage: "Unable to parse the curl version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := defaultUpdaterFixtureOptions()
			options.CurlVersion = tc.version
			run := runUpdaterFixtureWithOptions(t, fixtureRoot, t.TempDir(), citoolsPath, goWrapperPath, pins, targetPlan, "--check", options)
			if run.Code != tc.wantCode {
				t.Fatalf("update-upstream-pins exit code = %d, want %d\n%s", run.Code, tc.wantCode, run.Output)
			}
			if !strings.Contains(run.Output, tc.wantMessage) {
				t.Fatalf("update-upstream-pins output = %q, want text %q", run.Output, tc.wantMessage)
			}
			assertUpdaterFixtureCurlVersionProbe(t, run.CurlCalls, tc.version)
			if tc.version != "8.4.0" && updaterFixtureCurlKindCount(run.CurlCalls, "curl") != 0 {
				t.Fatalf("unsupported curl version made HTTP requests: %#v", run.CurlCalls)
			}
		})
	}
}

func TestUpdateUpstreamPinsConstrainsReleaseAssetRedirects(t *testing.T) {
	t.Parallel()

	pins := readUpdaterFixturePins(t)
	targetPlan := updaterTargetDebianPlan()
	targetManifest := updaterManifestFromPlan(targetPlan)
	driftedManifest := updaterDriftedManifest(t, targetPlan.Snapshot)
	toolsRoot := t.TempDir()
	citoolsPath := buildUpdaterFixtureCITools(t, toolsRoot)
	goWrapperPath := writeUpdaterFixtureGoWrapper(t, toolsRoot)
	fixtureRoot := writeUpdaterFixture(t, driftedManifest, 0o640)

	for _, tc := range []struct {
		name             string
		assetID          string
		apiResponse      string
		redirectResponse string
		wantMessage      string
		wantRedirects    int
	}{
		{name: "direct-body", apiResponse: "direct", wantMessage: updaterFixtureSummary(pins, driftedManifest, targetManifest)},
		{name: "wrong-status", apiResponse: "status-301", wantMessage: "Unexpected GitHub release asset response 301"},
		{name: "missing-location", apiResponse: "missing-location", wantMessage: "must include one Location header"},
		{name: "duplicate-location", apiResponse: "duplicate-location", wantMessage: "must include one Location header"},
		{name: "hostile-location", apiResponse: "hostile-location", wantMessage: "outside the allowed endpoint"},
		{name: "second-hop", apiResponse: "redirect", redirectResponse: "redirect", wantMessage: "Unexpected GitHub release asset redirect response 302", wantRedirects: 1},
		{name: "invalid-asset-id", assetID: `"invalid"`, apiResponse: "redirect", wantMessage: "Unable to resolve a numeric release asset ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := defaultUpdaterFixtureOptions()
			options.Token = "fixture-github-token"
			if tc.assetID != "" {
				options.AssetID = tc.assetID
			}
			if tc.apiResponse != "" {
				options.AssetAPIResponse = tc.apiResponse
			}
			if tc.redirectResponse != "" {
				options.AssetRedirectResponse = tc.redirectResponse
			}
			run := runUpdaterFixtureWithOptions(t, fixtureRoot, t.TempDir(), citoolsPath, goWrapperPath, pins, targetPlan, "--check", options)
			if run.Code != 1 {
				t.Fatalf("update-upstream-pins exit code = %d, want 1\n%s", run.Code, run.Output)
			}
			if !strings.Contains(run.Output, tc.wantMessage) {
				t.Fatalf("update-upstream-pins output = %q, want text %q", run.Output, tc.wantMessage)
			}
			if got := updaterFixtureCurlURLCount(run.CurlCalls, updaterFixtureRedirectURL); got != tc.wantRedirects {
				t.Fatalf("release asset redirect calls = %d, want %d; calls=%#v", got, tc.wantRedirects, run.CurlCalls)
			}
			if updaterFixtureCurlURLCount(run.CurlCalls, "https://attacker.invalid/actionlint-checksums") != 0 {
				t.Fatalf("hostile release metadata URL was requested: %#v", run.CurlCalls)
			}
		})
	}
}

func TestGitHubReleaseAssetURLValidators(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(repoRoot(t), "scripts", "update-upstream-pins.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	validators := extractShellFunction(t, string(content), "valid_github_release_asset_api_url") + "\n" +
		extractShellFunction(t, string(content), "valid_github_release_asset_redirect_url") + "\n"

	for _, tc := range []struct {
		name      string
		function  string
		url       string
		wantValid bool
	}{
		{name: "api-valid", function: "valid_github_release_asset_api_url", url: "https://api.github.com/repos/rhysd/actionlint/releases/assets/424242", wantValid: true},
		{name: "api-http", function: "valid_github_release_asset_api_url", url: "http://api.github.com/repos/rhysd/actionlint/releases/assets/424242"},
		{name: "api-userinfo", function: "valid_github_release_asset_api_url", url: "https://api.github.com@attacker.invalid/repos/rhysd/actionlint/releases/assets/424242"},
		{name: "api-port", function: "valid_github_release_asset_api_url", url: "https://api.github.com:443/repos/rhysd/actionlint/releases/assets/424242"},
		{name: "api-query", function: "valid_github_release_asset_api_url", url: "https://api.github.com/repos/rhysd/actionlint/releases/assets/424242?x=1"},
		{name: "redirect-valid", function: "valid_github_release_asset_redirect_url", url: updaterFixtureRedirectURL, wantValid: true},
		{name: "redirect-http", function: "valid_github_release_asset_redirect_url", url: strings.Replace(updaterFixtureRedirectURL, "https://", "http://", 1)},
		{name: "redirect-userinfo", function: "valid_github_release_asset_redirect_url", url: strings.Replace(updaterFixtureRedirectURL, "release-assets.githubusercontent.com", "release-assets.githubusercontent.com@attacker.invalid", 1)},
		{name: "redirect-port", function: "valid_github_release_asset_redirect_url", url: strings.Replace(updaterFixtureRedirectURL, "release-assets.githubusercontent.com", "release-assets.githubusercontent.com:443", 1)},
		{name: "redirect-host", function: "valid_github_release_asset_redirect_url", url: strings.Replace(updaterFixtureRedirectURL, "release-assets.githubusercontent.com", "objects.githubusercontent.com", 1)},
		{name: "redirect-extra-path", function: "valid_github_release_asset_redirect_url", url: strings.Replace(updaterFixtureRedirectURL, "?sp=", "/extra?sp=", 1)},
		{name: "redirect-no-query", function: "valid_github_release_asset_redirect_url", url: strings.Split(updaterFixtureRedirectURL, "?")[0]},
		{name: "redirect-fragment", function: "valid_github_release_asset_redirect_url", url: updaterFixtureRedirectURL + "#fragment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/bash", "-p", "-c", validators+tc.function+` "$1"`, "asset-url-validator", tc.url)
			err := cmd.Run()
			if tc.wantValid && err != nil {
				t.Fatalf("%s rejected %q: %v", tc.function, tc.url, err)
			}
			if !tc.wantValid && err == nil {
				t.Fatalf("%s accepted %q", tc.function, tc.url)
			}
		})
	}
}

func TestUpdateUpstreamPinsApplyFailureLeavesFixtureUnchanged(t *testing.T) {
	t.Parallel()

	pins := readUpdaterFixturePins(t)
	validPlan := updaterTargetDebianPlan()
	invalidPlan := validPlan
	invalidPlan.OpenSSLARM64.Version = "8.8.8-1~deb13u8"
	toolsRoot := t.TempDir()
	citoolsPath := buildUpdaterFixtureCITools(t, toolsRoot)
	goWrapperPath := writeUpdaterFixtureGoWrapper(t, toolsRoot)
	fixtureRoot := writeUpdaterFixture(t, updaterDriftedManifest(t, validPlan.Snapshot), 0o640)
	before := snapshotUpdaterFixtureTree(t, fixtureRoot)

	scratchRoot := t.TempDir()
	run := runUpdaterFixture(t, fixtureRoot, scratchRoot, citoolsPath, goWrapperPath, pins, invalidPlan, "--apply")
	if run.Code != 1 {
		t.Fatalf("failed apply exit code = %d, want 1\n%s", run.Code, run.Output)
	}
	if got, want := run.Output, "OpenSSL versions must agree across architectures\n"; got != want {
		t.Fatalf("failed apply output = %q, want %q", got, want)
	}
	assertUpdaterResolutionLog(t, run.ResolutionLog, invalidPlan.Snapshot)
	assertUpdaterCommandCounts(t, run.CIToolsLog, map[string]int{
		"hadolint-manifest-checksum": 2,
		"select-buildx-version":      1,
		"inspect-debian-bootstrap":   1,
		"apply-debian-bootstrap":     1,
		"check-pinned-inputs":        0,
	})
	if want := []string{"summary", "check"}; !reflect.DeepEqual(run.ProviderLog, want) {
		t.Fatalf("provider updater command log = %q, want %q", run.ProviderLog, want)
	}
	assertUpdaterScratchHasOnlyLogs(t, scratchRoot)
	assertNoUpdaterFixtureTempResidue(t, fixtureRoot)
	after := snapshotUpdaterFixtureTree(t, fixtureRoot)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("failed update-upstream-pins.sh --apply mutated its fixture tree\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestUpdaterFixtureCurlRejectsUnexpectedReleaseProbeArguments(t *testing.T) {
	t.Parallel()

	probePrefix := "set -euo pipefail\n" +
		extractShellFunction(t, updaterFixtureHarness, "unexpected_fixture") + "\n" +
		extractShellFunction(t, updaterFixtureHarness, "expect_fixture_curl_argv") + "\n" +
		extractShellFunction(t, updaterFixtureHarness, "record_fixture_http_request") + "\n" +
		extractShellFunction(t, updaterFixtureHarness, "curl") + "\n"
	endpoints := []struct {
		name string
		url  string
	}{
		{name: "base", url: "https://snapshot.debian.org/archive/debian/20260721T000000Z/dists/trixie/Release"},
		{name: "updates", url: "https://snapshot.debian.org/archive/debian/20260721T000000Z/dists/trixie-updates/Release"},
		{name: "security", url: "https://snapshot.debian.org/archive/debian-security/20260721T000000Z/dists/trixie-security/Release"},
	}
	for _, endpoint := range endpoints {
		endpoint := endpoint
		for _, malformed := range []struct {
			name string
			args string
		}{
			{name: "missing-curlrc-disable", args: "-fsSI --max-time 120 --connect-timeout 15 --max-filesize 209715200"},
			{name: "late-curlrc-disable", args: "-fsSI -q --max-time 120 --connect-timeout 15 --max-filesize 209715200"},
			{name: "wrong-method", args: "-q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200"},
			{name: "missing-size-bound", args: "-q -fsSI --max-time 120 --connect-timeout 15"},
			{name: "extra-arguments", args: "-q -fsSI --max-time 120 --connect-timeout 15 --max-filesize 209715200 --retry 1"},
		} {
			malformed := malformed
			t.Run(endpoint.name+"/"+malformed.name, func(t *testing.T) {
				t.Parallel()
				code, output := runBashProbe(t, probePrefix+"curl "+malformed.args+" "+endpoint.url+"\n", map[string]string{
					"WORKCELL_FIXTURE_UNEXPECTED_STATUS": "97",
				})
				if code != 97 || !strings.Contains(output, "unexpected fixture curl:") {
					t.Fatalf("malformed Release probe code=%d output=%q", code, output)
				}
			})
		}
	}
}

func updaterTargetDebianPlan() updaterFixtureDebianPlan {
	now := time.Now().UTC()
	snapshot := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format("20060102T150405Z")
	return updaterFixtureDebianPlan{
		Snapshot: snapshot,
		OpenSSLAMD64: updaterFixtureDebianPackage{
			Version: "9.9.9-1~deb13u9", Architecture: "amd64",
			Filename: "pool/main/o/openssl/openssl_9.9.9-1~deb13u9_amd64.deb", SHA256: strings.Repeat("a", 64), Size: 1_500_000,
		},
		OpenSSLARM64: updaterFixtureDebianPackage{
			Version: "9.9.9-1~deb13u9", Architecture: "arm64",
			Filename: "pool/main/o/openssl/openssl_9.9.9-1~deb13u9_arm64.deb", SHA256: strings.Repeat("b", 64), Size: 1_400_000,
		},
		CACertificates: updaterFixtureDebianPackage{
			Version: "20991231", Architecture: "all",
			Filename: "pool/main/c/ca-certificates/ca-certificates_20991231_all.deb", SHA256: strings.Repeat("c", 64), Size: 160_000,
		},
	}
}

func updaterDriftedManifest(t *testing.T, targetSnapshot string) updaterFixtureManifest {
	t.Helper()

	targetTime, err := time.Parse("20060102T150405Z", targetSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	return updaterFixtureManifest{
		Snapshot:             targetTime.Add(-24 * time.Hour).Format("20060102T150405Z"),
		OpenSSLAMD64Path:     "pool/main/o/openssl/openssl_1.1.1-1~deb13u1_amd64.deb",
		OpenSSLAMD64SHA256:   strings.Repeat("d", 64),
		OpenSSLARM64Path:     "pool/main/o/openssl/openssl_1.1.1-1~deb13u1_arm64.deb",
		OpenSSLARM64SHA256:   strings.Repeat("e", 64),
		CACertificatesPath:   "pool/main/c/ca-certificates/ca-certificates_20000101_all.deb",
		CACertificatesSHA256: strings.Repeat("f", 64),
	}
}

func updaterManifestFromPlan(plan updaterFixtureDebianPlan) updaterFixtureManifest {
	return updaterFixtureManifest{
		Snapshot:             plan.Snapshot,
		OpenSSLAMD64Path:     plan.OpenSSLAMD64.Filename,
		OpenSSLAMD64SHA256:   plan.OpenSSLAMD64.SHA256,
		OpenSSLARM64Path:     plan.OpenSSLARM64.Filename,
		OpenSSLARM64SHA256:   plan.OpenSSLARM64.SHA256,
		CACertificatesPath:   plan.CACertificates.Filename,
		CACertificatesSHA256: plan.CACertificates.SHA256,
	}
}

func assertUpdaterManifestFieldsDiffer(t *testing.T, current, target updaterFixtureManifest) {
	t.Helper()

	for field, pair := range map[string][2]string{
		"DEBIAN_SNAPSHOT":               {current.Snapshot, target.Snapshot},
		"DEBIAN_OPENSSL_AMD64_PATH":     {current.OpenSSLAMD64Path, target.OpenSSLAMD64Path},
		"DEBIAN_OPENSSL_AMD64_SHA256":   {current.OpenSSLAMD64SHA256, target.OpenSSLAMD64SHA256},
		"DEBIAN_OPENSSL_ARM64_PATH":     {current.OpenSSLARM64Path, target.OpenSSLARM64Path},
		"DEBIAN_OPENSSL_ARM64_SHA256":   {current.OpenSSLARM64SHA256, target.OpenSSLARM64SHA256},
		"DEBIAN_CA_CERTIFICATES_PATH":   {current.CACertificatesPath, target.CACertificatesPath},
		"DEBIAN_CA_CERTIFICATES_SHA256": {current.CACertificatesSHA256, target.CACertificatesSHA256},
	} {
		if pair[0] == pair[1] {
			t.Fatalf("fixture does not drift %s", field)
		}
	}
}

func renderUpdaterFixtureManifest(manifest updaterFixtureManifest) string {
	return fmt.Sprintf(`DEBIAN_SNAPSHOT=%s
DEBIAN_OPENSSL_AMD64_PATH=%s
DEBIAN_OPENSSL_AMD64_SHA256=%s
DEBIAN_OPENSSL_ARM64_PATH=%s
DEBIAN_OPENSSL_ARM64_SHA256=%s
DEBIAN_CA_CERTIFICATES_PATH=%s
DEBIAN_CA_CERTIFICATES_SHA256=%s
`, manifest.Snapshot,
		manifest.OpenSSLAMD64Path,
		manifest.OpenSSLAMD64SHA256,
		manifest.OpenSSLARM64Path,
		manifest.OpenSSLARM64SHA256,
		manifest.CACertificatesPath,
		manifest.CACertificatesSHA256,
	)
}

func buildUpdaterFixtureCITools(t *testing.T, toolsRoot string) string {
	t.Helper()

	path := filepath.Join(toolsRoot, "workcell-citools")
	cmd := exec.Command("go", "build", "-o", path, "./cmd/workcell-citools")
	cmd.Dir = repoRoot(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build current workcell-citools: %v\n%s", err, output)
	}
	return path
}

func writeUpdaterFixtureGoWrapper(t *testing.T, toolsRoot string) string {
	t.Helper()

	path := filepath.Join(toolsRoot, "go")
	writeUpdaterFixtureFile(t, toolsRoot, "go", `#!/bin/bash -p
set -euo pipefail
if [[ "${1:-}" != "run" || "${2:-}" != "./cmd/workcell-citools" || "$#" -lt 3 ]]; then
  printf 'unexpected fixture go wrapper: %s\n' "$*" >&2
  exit 97
fi
printf '%s\n' "$3" >>"${WORKCELL_FIXTURE_CITOOLS_LOG}"
exec "${WORKCELL_FIXTURE_CITOOLS}" "${@:3}"
`, 0o755)
	return path
}

func writeUpdaterFixture(t *testing.T, manifest updaterFixtureManifest, manifestMode fs.FileMode) string {
	t.Helper()

	root := t.TempDir()
	for _, rel := range []string{
		".github/CODEOWNERS",
		"adapters/codex/mcp/config.toml",
		"adapters/codex/requirements.toml",
		"go.mod",
		"policy/allowed-actions.toml",
		"policy/github-hosted-controls.toml",
		"policy/provider-bumps.toml",
		"policy/tool-pins.toml",
		"runtime/container/Dockerfile",
		"runtime/container/debian-bootstrap.env",
		"runtime/container/providers/package-lock.json",
		"runtime/container/providers/package.json",
		"runtime/container/rust/Cargo.toml",
		"runtime/container/rust/rust-toolchain.toml",
		"scripts/check-pinned-inputs.sh",
		"scripts/ci/build-validator-image.sh",
		"scripts/ci/job-pin-hygiene.sh",
		"scripts/ci/job-validate.sh",
		"scripts/install-dev-tools.sh",
		"scripts/lib/trusted-entrypoint.sh",
		"scripts/update-upstream-pins.sh",
		"scripts/validate-repo.sh",
		"scripts/verify-github-hosted-controls.sh",
		"tools/markdownlint/package-lock.json",
		"tools/markdownlint/package.json",
		"tools/validator/Dockerfile",
	} {
		copyUpdaterFixtureSourceFile(t, root, rel)
	}
	workflowPaths, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(workflowPaths) == 0 {
		t.Fatal("no workflow fixtures found")
	}
	for _, sourcePath := range workflowPaths {
		copyUpdaterFixtureSourceFile(t, root, filepath.ToSlash(filepath.Join(".github", "workflows", filepath.Base(sourcePath))))
	}

	writeUpdaterFixtureFile(t, root, "runtime/container/debian-bootstrap.env", renderUpdaterFixtureManifest(manifest), manifestMode)
	writeUpdaterFixtureFile(t, root, "scripts/update-provider-pins.sh", `#!/bin/bash -p
set -euo pipefail
case "${1:-}" in
  "")
    printf '%s\n' summary >>"${WORKCELL_FIXTURE_PROVIDER_LOG}"
    printf '%s\n' 'Provider pin refresh summary: fixture (up to date)'
    ;;
  --check)
    printf '%s\n' check >>"${WORKCELL_FIXTURE_PROVIDER_LOG}"
    ;;
  --apply)
    printf '%s\n' apply >>"${WORKCELL_FIXTURE_PROVIDER_LOG}"
    ;;
  *)
    printf 'unexpected fixture provider command: %s\n' "$*" >&2
    exit 97
    ;;
esac
`, 0o755)
	return root
}

func copyUpdaterFixtureSourceFile(t *testing.T, fixtureRoot, rel string) {
	t.Helper()

	sourcePath := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	writeUpdaterFixtureFile(t, fixtureRoot, rel, string(content), info.Mode().Perm())
}

func writeUpdaterFixtureFile(t *testing.T, root, rel, content string, mode fs.FileMode) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func runUpdaterFixture(t *testing.T, fixtureRoot, scratchRoot, citoolsPath, goWrapperPath string, pins updaterFixturePins, plan updaterFixtureDebianPlan, mode string) updaterFixtureRun {
	return runUpdaterFixtureWithOptions(t, fixtureRoot, scratchRoot, citoolsPath, goWrapperPath, pins, plan, mode, defaultUpdaterFixtureOptions())
}

func runUpdaterFixtureWithOptions(t *testing.T, fixtureRoot, scratchRoot, citoolsPath, goWrapperPath string, pins updaterFixturePins, plan updaterFixtureDebianPlan, mode string, options updaterFixtureOptions) updaterFixtureRun {
	t.Helper()

	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	runtimeTrack, runtimeDigest := splitUpdaterFixtureImage(t, pins.RuntimeBase)
	validatorTrack, validatorDigest := splitUpdaterFixtureImage(t, pins.ValidatorBase)
	rustTrack, rustDigest := splitUpdaterFixtureImage(t, pins.RuntimeRustImage)
	buildkitTrack, buildkitDigest := splitUpdaterFixtureImage(t, pins.BuildkitImage)
	qemuTrack, qemuDigest := splitUpdaterFixtureImage(t, pins.QEMUImage)
	qemuTag := strings.TrimPrefix(qemuTrack, "tonistiigi/binfmt:")
	if qemuTag == qemuTrack {
		t.Fatalf("unexpected QEMU image track %q", qemuTrack)
	}
	resolutionLogPath := filepath.Join(scratchRoot, "resolution.log")
	citoolsLogPath := filepath.Join(scratchRoot, "citools.log")
	providerLogPath := filepath.Join(scratchRoot, "provider.log")
	for _, logPath := range []string{resolutionLogPath, citoolsLogPath, providerLogPath} {
		if err := os.WriteFile(logPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	curlLogPath := filepath.Join(t.TempDir(), "curl.log")
	tokenFile := ""
	if err := os.WriteFile(curlLogPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if options.Token != "" {
		credentialDir := t.TempDir()
		tokenFile = filepath.Join(credentialDir, "github-token")
		if err := os.WriteFile(tokenFile, []byte(options.Token), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hostileHome := t.TempDir()
	const hostileCurlConfig = "output = \"/dev/null\"\n"
	writeUpdaterFixtureFile(t, hostileHome, ".curlrc", hostileCurlConfig, 0o600)
	cmd := exec.Command("/bin/bash", "-p", "-c", updaterFixtureHarness, "workcell-updater-fixture", filepath.Join(fixtureRoot, "scripts", "update-upstream-pins.sh"), mode)
	// Invoke the absolute updater path from outside the fixture repository so
	// every repo-local tool dispatch must anchor itself to ROOT_DIR.
	cmd.Dir = scratchRoot
	cmd.Env = []string{
		"ALL_PROXY=http://127.0.0.1:1",
		"BASH_ENV=",
		"DOCKER_HOST=tcp://127.0.0.1:1",
		"ENV=",
		"GOPROXY=http://127.0.0.1:1",
		"HOME=" + hostileHome,
		"HTTPS_PROXY=http://127.0.0.1:1",
		"HTTP_PROXY=http://127.0.0.1:1",
		"LANG=C",
		"LC_ALL=C",
		"NO_PROXY=",
		"PATH=/usr/bin:/bin",
		"TMPDIR=" + scratchRoot,
		"TZ=UTC",
		"WORKCELL_DEBIAN_SNAPSHOT_LOOKBACK_DAYS=0",
		"WORKCELL_FIXTURE_ACTIONLINT_SHA=" + pins.ActionlintSHA,
		"WORKCELL_FIXTURE_ACTIONLINT_ASSET_ID=" + options.AssetID,
		"WORKCELL_FIXTURE_ACTIONLINT_VERSION=" + pins.ActionlintVersion,
		"WORKCELL_FIXTURE_ASSET_API_RESPONSE=" + options.AssetAPIResponse,
		"WORKCELL_FIXTURE_ASSET_REDIRECT_URL=" + updaterFixtureRedirectURL,
		"WORKCELL_FIXTURE_ASSET_REDIRECT_RESPONSE=" + options.AssetRedirectResponse,
		"WORKCELL_FIXTURE_BUILDKIT_DIGEST=" + buildkitDigest,
		"WORKCELL_FIXTURE_BUILDKIT_TRACK=" + buildkitTrack,
		"WORKCELL_FIXTURE_BUILDX_VERSION=" + pins.BuildxVersion,
		"WORKCELL_FIXTURE_CITOOLS=" + citoolsPath,
		"WORKCELL_FIXTURE_CITOOLS_LOG=" + citoolsLogPath,
		"WORKCELL_FIXTURE_COSIGN_VERSION=" + pins.CosignVersion,
		"WORKCELL_FIXTURE_CURL_LOG=" + curlLogPath,
		"WORKCELL_FIXTURE_CURL_VERSION=" + options.CurlVersion,
		"WORKCELL_FIXTURE_GITHUB_TOKEN=" + options.Token,
		"WORKCELL_FIXTURE_REQUIRE_HOSTILE_CURLRC=1",
		"WORKCELL_FIXTURE_DEBIAN_PLAN=" + string(planJSON),
		"WORKCELL_FIXTURE_DEBIAN_SNAPSHOT=" + plan.Snapshot,
		"WORKCELL_FIXTURE_GO_AMD64_SHA=" + pins.GoAMD64SHA,
		"WORKCELL_FIXTURE_GO_ARM64_SHA=" + pins.GoARM64SHA,
		"WORKCELL_FIXTURE_GO_VERSION=" + pins.GoToolchain,
		"WORKCELL_FIXTURE_HADOLINT_AMD64_SHA=" + pins.HadolintAMD64SHA,
		"WORKCELL_FIXTURE_HADOLINT_ARM64_SHA=" + pins.HadolintARM64SHA,
		"WORKCELL_FIXTURE_HADOLINT_CHECKSUMS=" + pins.HadolintChecksums,
		"WORKCELL_FIXTURE_HADOLINT_VERSION=" + pins.HadolintVersion,
		"WORKCELL_FIXTURE_PROVIDER_LOG=" + providerLogPath,
		"WORKCELL_FIXTURE_QEMU_DIGEST=" + qemuDigest,
		"WORKCELL_FIXTURE_QEMU_TAG=" + qemuTag,
		"WORKCELL_FIXTURE_RESOLUTION_LOG=" + resolutionLogPath,
		"WORKCELL_FIXTURE_RUNTIME_BASE_DIGEST=" + runtimeDigest,
		"WORKCELL_FIXTURE_RUNTIME_BASE_TRACK=" + runtimeTrack,
		"WORKCELL_FIXTURE_RUST_IMAGE_DIGEST=" + rustDigest,
		"WORKCELL_FIXTURE_RUST_IMAGE_TRACK=" + rustTrack,
		"WORKCELL_FIXTURE_RUST_VERSION=" + pins.RustVersion,
		"WORKCELL_FIXTURE_RUSTUP_AMD64_SHA=" + pins.RustupAMD64SHA,
		"WORKCELL_FIXTURE_RUSTUP_ARM64_SHA=" + pins.RustupARM64SHA,
		"WORKCELL_FIXTURE_RUSTUP_VERSION=" + pins.RustupVersion,
		"WORKCELL_FIXTURE_SYFT_VERSION=" + pins.SyftVersion,
		"WORKCELL_FIXTURE_UNEXPECTED_STATUS=97",
		"WORKCELL_FIXTURE_VALIDATOR_BASE_DIGEST=" + validatorDigest,
		"WORKCELL_FIXTURE_VALIDATOR_BASE_TRACK=" + validatorTrack,
		"WORKCELL_FIXTURE_ZIZMOR_SHA=" + pins.ZizmorSHA,
		"WORKCELL_FIXTURE_ZIZMOR_VERSION=" + pins.ZizmorVersion,
		"WORKCELL_GO_BIN=" + goWrapperPath,
		"WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS=60",
		"WORKCELL_SANITIZED_ENTRYPOINT=1",
		"all_proxy=http://127.0.0.1:1",
		"http_proxy=http://127.0.0.1:1",
		"https_proxy=http://127.0.0.1:1",
		"no_proxy=",
	}
	if tokenFile != "" {
		cmd.Env = append(cmd.Env, "WORKCELL_GITHUB_API_TOKEN_FILE="+tokenFile)
	}
	output, runErr := cmd.CombinedOutput()
	curlConfig, err := os.ReadFile(filepath.Join(hostileHome, ".curlrc"))
	if err != nil {
		t.Fatalf("read hostile curl config after updater run: %v", err)
	}
	if string(curlConfig) != hostileCurlConfig {
		t.Fatal("updater mutated the hostile HOME/.curlrc fixture")
	}
	code := 0
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("execute update-upstream-pins.sh fixture: %v", runErr)
		}
		code = exitErr.ExitCode()
	}
	run := updaterFixtureRun{
		Code:          code,
		Output:        string(output),
		ResolutionLog: readUpdaterFixtureLog(t, resolutionLogPath),
		CIToolsLog:    splitUpdaterFixtureLog(readUpdaterFixtureLog(t, citoolsLogPath)),
		ProviderLog:   splitUpdaterFixtureLog(readUpdaterFixtureLog(t, providerLogPath)),
	}
	run.CurlCalls = readUpdaterFixtureCurlCalls(t, curlLogPath)
	return run
}

func readUpdaterFixtureLog(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture log %s: %v", path, err)
	}
	return string(content)
}

func splitUpdaterFixtureLog(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

func readUpdaterFixtureCurlCalls(t *testing.T, path string) []updaterFixtureCurlCall {
	t.Helper()

	var calls []updaterFixtureCurlCall
	for _, line := range splitUpdaterFixtureLog(readUpdaterFixtureLog(t, path)) {
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 {
			t.Fatalf("malformed fixture curl record %q", line)
		}
		calls = append(calls, updaterFixtureCurlCall{
			Kind:   fields[0],
			URL:    fields[1],
			Config: strings.ReplaceAll(fields[2], `\n`, "\n"),
			Args:   fields[3],
		})
	}
	return calls
}

func updaterFixtureCurlKindCount(calls []updaterFixtureCurlCall, kind string) int {
	count := 0
	for _, call := range calls {
		if call.Kind == kind {
			count++
		}
	}
	return count
}

func assertUpdaterFixtureCurlVersionProbe(t *testing.T, calls []updaterFixtureCurlCall, version string) {
	t.Helper()

	count := 0
	for _, call := range calls {
		if call.Kind != "curl-version" {
			continue
		}
		count++
		if call.URL != version || call.Config != "" || call.Args != "-q --version" {
			t.Fatalf("curl version probe = %#v, want version %q with -q first", call, version)
		}
	}
	if count != 1 {
		t.Fatalf("curl version probes = %d, want 1; calls=%#v", count, calls)
	}
}

func updaterFixtureCurlURLCount(calls []updaterFixtureCurlCall, url string) int {
	count := 0
	for _, call := range calls {
		if call.Kind == "curl" && call.URL == url {
			count++
		}
	}
	return count
}

func assertCredentialedUpdaterCurlCalls(t *testing.T, calls []updaterFixtureCurlCall, token string) {
	t.Helper()

	githubConfig := "header = \"Accept: application/vnd.github+json\"\nheader = \"Authorization: Bearer " + token + "\""
	assetConfig := "header = \"Authorization: Bearer " + token + "\""
	assetCalls := 0
	redirectCalls := 0
	assetURL := "https://api.github.com/repos/rhysd/actionlint/releases/assets/" + updaterFixtureActionlintAssetID
	for _, call := range calls {
		if call.Kind != "curl" {
			continue
		}
		if strings.Contains(call.Args, token) || strings.Contains(call.Args, "Authorization:") || strings.Contains(call.Args, "--oauth2-bearer") {
			t.Fatalf("credential leaked through curl argv: %#v", call)
		}
		switch {
		case call.URL == assetURL:
			assetCalls++
			if call.Config != assetConfig || !strings.Contains(call.Args, "--config -") || strings.Contains(call.Args, "-fsSL") {
				t.Fatalf("release asset curl capture = %#v, want config %q through stdin without redirects", call, assetConfig)
			}
		case call.URL == updaterFixtureRedirectURL:
			redirectCalls++
			if call.Config != "" || strings.Contains(call.Args, "--config") || strings.Contains(call.Args, "-fsSL") {
				t.Fatalf("release asset redirect curl capture = %#v, want one uncredentialed request", call)
			}
		case strings.HasPrefix(call.URL, "https://api.github.com/"):
			if call.Config != githubConfig || !strings.Contains(call.Args, "--config -") || strings.Contains(call.Args, "-fsSL") {
				t.Fatalf("GitHub API curl capture = %#v, want config %q through stdin", call, githubConfig)
			}
		}
	}
	if assetCalls != 1 || redirectCalls != 1 {
		t.Fatalf("credentialed curl captures release-asset=%d release-asset-redirect=%d, want 1, 1; calls=%#v", assetCalls, redirectCalls, calls)
	}
}

func assertUpdaterFixtureRun(t *testing.T, run updaterFixtureRun, wantCode int, wantOutput, snapshot string) {
	t.Helper()

	if run.Code != wantCode {
		t.Fatalf("update-upstream-pins exit code = %d, want %d\n%s", run.Code, wantCode, run.Output)
	}
	if run.Output != wantOutput {
		t.Fatalf("update-upstream-pins output = %q, want %q", run.Output, wantOutput)
	}
	assertUpdaterResolutionLog(t, run.ResolutionLog, snapshot)
}

func assertUpdaterResolutionLog(t *testing.T, got, snapshot string) {
	t.Helper()

	want := strings.Join([]string{
		"release https://snapshot.debian.org/archive/debian/" + snapshot + "/dists/trixie/Release",
		"release https://snapshot.debian.org/archive/debian/" + snapshot + "/dists/trixie-updates/Release",
		"release https://snapshot.debian.org/archive/debian-security/" + snapshot + "/dists/trixie-security/Release",
		"resolve " + snapshot,
		"",
	}, "\n")
	if got != want {
		t.Fatalf("Debian resolution log = %q, want %q", got, want)
	}
}

func assertUpdaterCommandCounts(t *testing.T, commands []string, expected map[string]int) {
	t.Helper()

	counts := make(map[string]int)
	for _, command := range commands {
		counts[command]++
	}
	for command, want := range expected {
		if got := counts[command]; got != want {
			t.Fatalf("workcell-citools %s count = %d, want %d; log=%q", command, got, want, commands)
		}
	}
	for command := range counts {
		switch command {
		case "extract-dockerfile-arg", "hadolint-manifest-checksum", "select-buildx-version", "inspect-debian-bootstrap", "apply-debian-bootstrap", "check-pinned-inputs":
		default:
			t.Fatalf("unexpected real workcell-citools command %q; log=%q", command, commands)
		}
	}
}

func assertUpdaterScratchHasOnlyLogs(t *testing.T, scratchRoot string) {
	t.Helper()

	entries, err := os.ReadDir(scratchRoot)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if want := []string{"citools.log", "provider.log", "resolution.log"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("updater scratch residue = %q, want only %q", names, want)
	}
}

func assertNoUpdaterFixtureTempResidue(t *testing.T, fixtureRoot string) {
	t.Helper()

	err := filepath.WalkDir(fixtureRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".debian-bootstrap-") || strings.HasPrefix(name, "workcell-upstream-refresh.") || strings.HasPrefix(name, "workcell-debian-bootstrap-plan.") {
			t.Errorf("updater left temporary fixture path %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertOnlyUpdaterManifestChanged(t *testing.T, before, after map[string]updaterFixtureTreeEntry) {
	t.Helper()

	const manifest = "runtime/container/debian-bootstrap.env"
	if len(before) != len(after) {
		t.Fatalf("successful updater changed fixture file count: before=%d after=%d", len(before), len(after))
	}
	for path, beforeEntry := range before {
		afterEntry, ok := after[path]
		if !ok {
			t.Fatalf("successful updater removed fixture file %s", path)
		}
		if path == manifest {
			if beforeEntry.Digest == afterEntry.Digest {
				t.Fatalf("successful updater did not change %s", manifest)
			}
			if beforeEntry.Mode != afterEntry.Mode {
				t.Fatalf("successful updater changed %s mode from %v to %v", manifest, beforeEntry.Mode, afterEntry.Mode)
			}
			continue
		}
		if beforeEntry != afterEntry {
			t.Fatalf("successful updater changed non-Debian fixture file %s\nbefore=%#v\nafter=%#v", path, beforeEntry, afterEntry)
		}
	}
}

func snapshotUpdaterFixtureTree(t *testing.T, root string) map[string]updaterFixtureTreeEntry {
	t.Helper()
	return snapshotUpdaterFixture(t, root, true)
}

func snapshotUpdaterFixtureFiles(t *testing.T, root string) map[string]updaterFixtureTreeEntry {
	t.Helper()
	entries := snapshotUpdaterFixture(t, root, false)
	for path, entry := range entries {
		entry.ModTimeUnixNano = 0
		entries[path] = entry
	}
	return entries
}

func snapshotUpdaterFixture(t *testing.T, root string, includeDirectories bool) map[string]updaterFixtureTreeEntry {
	t.Helper()

	entries := make(map[string]updaterFixtureTreeEntry)
	err := filepath.WalkDir(root, func(path string, dirEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root && !includeDirectories {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.IsDir() && !includeDirectories {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := updaterFixtureTreeEntry{
			Mode:            info.Mode(),
			Size:            info.Size(),
			ModTimeUnixNano: info.ModTime().UnixNano(),
		}
		switch {
		case info.Mode().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry.Digest = sha256.Sum256(content)
		case info.Mode()&os.ModeSymlink != 0:
			entry.Link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		entries[filepath.ToSlash(rel)] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func updaterFixtureSummary(pins updaterFixturePins, current, target updaterFixtureManifest) string {
	var output strings.Builder
	output.WriteString("Pinned upstream refresh summary:\n")
	writeUpdaterFixtureSummaryLine(&output, "runtime-base", pins.RuntimeBase, pins.RuntimeBase)
	writeUpdaterFixtureSummaryLine(&output, "validator-base", pins.ValidatorBase, pins.ValidatorBase)
	writeUpdaterFixtureSummaryLine(&output, "debian-snapshot", current.Snapshot, target.Snapshot)
	writeUpdaterFixtureSummaryLine(&output, "debian-openssl-amd64", current.OpenSSLAMD64Path, target.OpenSSLAMD64Path)
	writeUpdaterFixtureSummaryLine(&output, "debian-openssl-amd64-sha256", current.OpenSSLAMD64SHA256, target.OpenSSLAMD64SHA256)
	writeUpdaterFixtureSummaryLine(&output, "debian-openssl-arm64", current.OpenSSLARM64Path, target.OpenSSLARM64Path)
	writeUpdaterFixtureSummaryLine(&output, "debian-openssl-arm64-sha256", current.OpenSSLARM64SHA256, target.OpenSSLARM64SHA256)
	writeUpdaterFixtureSummaryLine(&output, "debian-ca-certificates", current.CACertificatesPath, target.CACertificatesPath)
	writeUpdaterFixtureSummaryLine(&output, "debian-ca-certificates-sha256", current.CACertificatesSHA256, target.CACertificatesSHA256)
	writeUpdaterFixtureSummaryLine(&output, "go-toolchain", pins.GoToolchain, pins.GoToolchain)
	writeUpdaterFixtureSummaryLine(&output, "go-language", pins.GoLanguage, pins.GoLanguage)
	writeUpdaterFixtureSummaryLine(&output, "rust-toolchain", pins.RustVersion, pins.RustVersion)
	writeUpdaterFixtureSummaryLine(&output, "runtime-rust-image", pins.RuntimeRustImage, pins.RuntimeRustImage)
	writeUpdaterFixtureSummaryLine(&output, "rustup", pins.RustupVersion, pins.RustupVersion)
	writeUpdaterFixtureSummaryLine(&output, "hadolint", pins.HadolintVersion, pins.HadolintVersion)
	writeUpdaterFixtureSummaryLine(&output, "buildkit-image", pins.BuildkitImage, pins.BuildkitImage)
	writeUpdaterFixtureSummaryLine(&output, "buildx-version", pins.BuildxVersion, pins.BuildxVersion)
	writeUpdaterFixtureSummaryLine(&output, "cosign-version", pins.CosignVersion, pins.CosignVersion)
	writeUpdaterFixtureSummaryLine(&output, "upstream-refresh-cosign-version", pins.UpstreamRefreshCosignVersion, pins.CosignVersion)
	writeUpdaterFixtureSummaryLine(&output, "qemu-image", pins.QEMUImage, pins.QEMUImage)
	writeUpdaterFixtureSummaryLine(&output, "syft-version", pins.SyftVersion, pins.SyftVersion)
	writeUpdaterFixtureSummaryLine(&output, "actionlint-version", pins.ActionlintVersion, pins.ActionlintVersion)
	writeUpdaterFixtureSummaryLine(&output, "zizmor-version", pins.ZizmorVersion, pins.ZizmorVersion)
	writeUpdaterFixtureSummaryLine(&output, "release-zizmor-version", pins.ReleaseZizmorVersion, pins.ZizmorVersion)
	writeUpdaterFixtureSummaryLine(&output, "release-zizmor-sha", pins.ReleaseZizmorSHA, pins.ZizmorSHA)
	output.WriteString("Provider pin refresh summary: fixture (up to date)\n")
	return output.String()
}

func writeUpdaterFixtureSummaryLine(output *strings.Builder, label, current, target string) {
	if current == target {
		fmt.Fprintf(output, "  %s: %s (up to date)\n", label, current)
		return
	}
	fmt.Fprintf(output, "  %s: %s -> %s\n", label, current, target)
}

func readUpdaterFixturePins(t *testing.T) updaterFixturePins {
	t.Helper()

	root := repoRoot(t)
	runtimeDockerfile := readUpdaterFixtureSource(t, filepath.Join(root, "runtime", "container", "Dockerfile"))
	validatorDockerfile := readUpdaterFixtureSource(t, filepath.Join(root, "tools", "validator", "Dockerfile"))
	goMod := readUpdaterFixtureSource(t, filepath.Join(root, "go.mod"))
	ciWorkflow := readUpdaterFixtureSource(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	releaseWorkflow := readUpdaterFixtureSource(t, filepath.Join(root, ".github", "workflows", "release.yml"))
	securityWorkflow := readUpdaterFixtureSource(t, filepath.Join(root, ".github", "workflows", "security.yml"))
	upstreamWorkflow := readUpdaterFixtureSource(t, filepath.Join(root, ".github", "workflows", "upstream-refresh.yml"))
	hadolintAMD64SHA := updaterFixtureUniqueValue(t, validatorDockerfile, "ARG HADOLINT_LINUX_X86_64_SHA256=")
	hadolintARM64SHA := updaterFixtureUniqueValue(t, validatorDockerfile, "ARG HADOLINT_LINUX_ARM64_SHA256=")

	return updaterFixturePins{
		RuntimeBase:      updaterFixtureUniqueValue(t, runtimeDockerfile, "ARG NODE_BASE_IMAGE="),
		ValidatorBase:    updaterFixtureUniqueValue(t, validatorDockerfile, "ARG VALIDATOR_BASE_IMAGE="),
		GoToolchain:      updaterFixtureUniqueValue(t, goMod, "toolchain go"),
		GoLanguage:       updaterFixtureUniqueValue(t, goMod, "go "),
		GoAMD64SHA:       updaterFixtureUniqueValue(t, validatorDockerfile, "ARG GO_LINUX_X86_64_SHA256="),
		GoARM64SHA:       updaterFixtureUniqueValue(t, validatorDockerfile, "ARG GO_LINUX_ARM64_SHA256="),
		RustVersion:      updaterFixtureUniqueValue(t, runtimeDockerfile, "ARG RUST_VERSION="),
		RuntimeRustImage: updaterFixtureUniqueValue(t, runtimeDockerfile, "ARG RUST_TOOLCHAIN_IMAGE="),
		RustupVersion:    updaterFixtureUniqueValue(t, validatorDockerfile, "ARG RUSTUP_VERSION="),
		RustupAMD64SHA:   updaterFixtureUniqueValue(t, validatorDockerfile, "ARG RUSTUP_INIT_LINUX_X86_64_SHA256="),
		RustupARM64SHA:   updaterFixtureUniqueValue(t, validatorDockerfile, "ARG RUSTUP_INIT_LINUX_ARM64_SHA256="),
		HadolintVersion:  updaterFixtureUniqueValue(t, validatorDockerfile, "ARG HADOLINT_VERSION="),
		HadolintAMD64SHA: hadolintAMD64SHA,
		HadolintARM64SHA: hadolintARM64SHA,
		HadolintChecksums: fmt.Sprintf(
			"%s  *hadolint-linux-x86_64\n%s  *hadolint-linux-arm64\n",
			hadolintAMD64SHA,
			hadolintARM64SHA,
		),
		BuildkitImage:                updaterFixtureUniqueValue(t, ciWorkflow, "  WORKCELL_BUILDKIT_IMAGE: "),
		BuildxVersion:                updaterFixtureUniqueValue(t, ciWorkflow, "  WORKCELL_BUILDX_VERSION: "),
		CosignVersion:                updaterFixtureUniqueValue(t, ciWorkflow, "  WORKCELL_COSIGN_VERSION: "),
		UpstreamRefreshCosignVersion: updaterFixtureUniqueValue(t, upstreamWorkflow, "  WORKCELL_COSIGN_VERSION: "),
		QEMUImage:                    updaterFixtureUniqueValue(t, ciWorkflow, "  WORKCELL_QEMU_IMAGE: "),
		SyftVersion:                  updaterFixtureUniqueValue(t, releaseWorkflow, "  WORKCELL_SYFT_VERSION: "),
		ActionlintVersion:            updaterFixtureUniformValue(t, securityWorkflow, "          ACTIONLINT_VERSION: "),
		ActionlintSHA:                updaterFixtureUniformValue(t, securityWorkflow, "          ACTIONLINT_SHA256: "),
		ZizmorVersion:                updaterFixtureUniformValue(t, securityWorkflow, "          ZIZMOR_VERSION: "),
		ZizmorSHA:                    updaterFixtureUniformValue(t, securityWorkflow, "          ZIZMOR_SHA256: "),
		ReleaseZizmorVersion:         updaterFixtureUniformValue(t, releaseWorkflow, "          ZIZMOR_VERSION: "),
		ReleaseZizmorSHA:             updaterFixtureUniformValue(t, releaseWorkflow, "          ZIZMOR_SHA256: "),
	}
}

func readUpdaterFixtureSource(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func updaterFixtureUniqueValue(t *testing.T, content, prefix string) string {
	t.Helper()

	values := updaterFixtureValues(content, prefix)
	if len(values) != 1 {
		t.Fatalf("fixture prefix %q matched %d lines, want 1", prefix, len(values))
	}
	return values[0]
}

func updaterFixtureUniformValue(t *testing.T, content, prefix string) string {
	t.Helper()

	values := updaterFixtureValues(content, prefix)
	if len(values) == 0 {
		t.Fatalf("fixture prefix %q matched no lines", prefix)
	}
	for _, value := range values[1:] {
		if value != values[0] {
			t.Fatalf("fixture prefix %q has non-uniform values %q", prefix, values)
		}
	}
	return values[0]
}

func updaterFixtureValues(content, prefix string) []string {
	var values []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, prefix) {
			values = append(values, strings.TrimSpace(strings.TrimPrefix(line, prefix)))
		}
	}
	return values
}

func splitUpdaterFixtureImage(t *testing.T, image string) (string, string) {
	t.Helper()

	track, digest, ok := strings.Cut(image, "@sha256:")
	if !ok || track == "" || len(digest) != 64 {
		t.Fatalf("fixture image is not pinned by SHA256: %q", image)
	}
	return track, digest
}

const updaterFixtureHarness = `set -euo pipefail

unexpected_fixture() {
  printf 'unexpected fixture %s: %s\n' "$1" "$2" >&2
  return "${WORKCELL_FIXTURE_UNEXPECTED_STATUS}"
}

date() {
  if [[ "$#" -eq 4 && "$1" == "-u" && "$2" == "-d" && "$3" == "1970-01-01 UTC" && "$4" == "+%Y%m%dT000000Z" ]]; then
    return 0
  fi
  if [[ "$#" -eq 2 && "$1" == "-u" && "$2" == "+%Y%m%dT000000Z" ]]; then
    printf '%s\n' "${WORKCELL_FIXTURE_DEBIAN_SNAPSHOT}"
    return 0
  fi
  unexpected_fixture date "$*"
}

expect_fixture_curl_argv() {
  local label="$1"
  shift
  local -a actual=()
  local -a expected=()
  local index=0
  while [[ "$#" -gt 0 && "$1" != "--" ]]; do
    actual+=("$1")
    shift
  done
  if [[ "$#" -eq 0 ]]; then
    unexpected_fixture curl "${label}: missing expected-argv delimiter"
    return
  fi
  shift
  expected=("$@")
  if [[ "${WORKCELL_FIXTURE_GITHUB_TOKEN:-}" != "" && "${label}" == https://api.github.com/* ]]; then
    local -a credentialed=()
    for ((index = 0; index < ${#expected[@]}; index++)); do
      if [[ "${expected[index]}" == "-H" && "${expected[index + 1]:-}" == "Accept: application/vnd.github+json" ]]; then
        ((index++))
        continue
      fi
      if [[ "${expected[index]}" == "-fsSL" ]]; then
        credentialed+=(-fsS)
        continue
      fi
      if [[ "${expected[index]}" == "${label}" ]]; then
        credentialed+=(--config -)
      fi
      credentialed+=("${expected[index]}")
    done
    expected=("${credentialed[@]}")
  fi
  if [[ "${#actual[@]}" -ne "${#expected[@]}" ]]; then
    unexpected_fixture curl "${label}: argv=${actual[*]}"
    return
  fi
  for ((index = 0; index < ${#expected[@]}; index++)); do
    if [[ "${actual[index]}" != "${expected[index]}" ]]; then
      unexpected_fixture curl "${label}: argv=${actual[*]}"
      return
    fi
  done
}

record_fixture_http_request() {
  [[ -n "${WORKCELL_FIXTURE_CURL_LOG:-}" ]] || return 0
  local escaped_config="${3//$'\n'/\\n}"
  printf '%s\t%s\t%s\t%s\n' "$1" "$2" "${escaped_config}" "$4" >>"${WORKCELL_FIXTURE_CURL_LOG}"
}

expect_fixture_authenticated_asset_argv() {
  local url="$1"
  shift
  if [[ "${WORKCELL_FIXTURE_GITHUB_TOKEN:-}" != "" ]]; then
    if [[ "$#" -ne 21 || "$1" != "-q" || "$2" != "-fsS" || "$3" != "--max-time" || "$4" != "60" || "$5" != "--connect-timeout" || "$6" != "15" || "$7" != "--max-filesize" || "$8" != "65536" || "$9" != "--proto" || "${10}" != "=https" || "${11}" != "-D" || -z "${12}" || "${13}" != "-o" || -z "${14}" || "${15}" != "-w" || "${16}" != "%{http_code}" || "${17}" != "-H" || "${18}" != "Accept: application/octet-stream" || "${19}" != "--config" || "${20}" != "-" || "${21}" != "${url}" ]]; then
      unexpected_fixture curl "${url}: argv=$*"
      return
    fi
    printf '%s\n%s\n' "${12}" "${14}"
    return
  fi
  if [[ "$#" -ne 19 || "$1" != "-q" || "$2" != "-fsS" || "$3" != "--max-time" || "$4" != "60" || "$5" != "--connect-timeout" || "$6" != "15" || "$7" != "--max-filesize" || "$8" != "65536" || "$9" != "--proto" || "${10}" != "=https" || "${11}" != "-D" || -z "${12}" || "${13}" != "-o" || -z "${14}" || "${15}" != "-w" || "${16}" != "%{http_code}" || "${17}" != "-H" || "${18}" != "Accept: application/octet-stream" || "${19}" != "${url}" ]]; then
    unexpected_fixture curl "${url}: argv=$*"
    return
  fi
  printf '%s\n%s\n' "${12}" "${14}"
}

expect_fixture_asset_redirect_argv() {
  local url="$1"
  shift
  if [[ "$#" -ne 17 || "$1" != "-q" || "$2" != "-fsS" || "$3" != "--max-time" || "$4" != "60" || "$5" != "--connect-timeout" || "$6" != "15" || "$7" != "--max-filesize" || "$8" != "65536" || "$9" != "--proto" || "${10}" != "=https" || "${11}" != "-D" || -z "${12}" || "${13}" != "-o" || -z "${14}" || "${15}" != "-w" || "${16}" != "%{http_code}" || "${17}" != "${url}" ]]; then
    unexpected_fixture curl "${url}: argv=$*"
    return
  fi
  printf '%s\n%s\n' "${12}" "${14}"
}

curl() {
  if [[ "$#" -eq 0 ]]; then
    unexpected_fixture curl '<missing-argv>'
    return
  fi
  if [[ "${WORKCELL_FIXTURE_REQUIRE_HOSTILE_CURLRC:-0}" == "1" ]]; then
    if [[ ! -f "${HOME}/.curlrc" ]]; then
      unexpected_fixture curl 'hostile HOME/.curlrc fixture is missing'
      return
    fi
    if [[ "$1" != "-q" ]]; then
      unexpected_fixture curl 'ambient HOME/.curlrc was not disabled by the first option'
      return
    fi
  fi
  if [[ "$#" -eq 2 && "$1" == "-q" && "$2" == "--version" ]]; then
    record_fixture_http_request curl-version "${WORKCELL_FIXTURE_CURL_VERSION}" "" "$*"
    printf 'curl %s (fixture) libcurl/%s\n' "${WORKCELL_FIXTURE_CURL_VERSION}" "${WORKCELL_FIXTURE_CURL_VERSION}"
    return
  fi
  local url="${!#}"
  local fixture_curl_config=""
  local index=0
  for ((index = 1; index <= $#; index++)); do
    if [[ "${!index}" != "--config" ]]; then
      continue
    fi
    local next_index=$((index + 1))
    local config_source="${!next_index}"
    if [[ "${config_source}" != "-" ]]; then
      unexpected_fixture curl "${url}: unexpected curl config source ${config_source:-<missing>}"
      return
    fi
    fixture_curl_config="$(cat)"
    break
  done
  if [[ "${WORKCELL_FIXTURE_GITHUB_TOKEN:-}" != "" && "${url}" == https://api.github.com/repos/rhysd/actionlint/releases/assets/* ]]; then
    local expected_asset_config="header = \"Authorization: Bearer ${WORKCELL_FIXTURE_GITHUB_TOKEN}\""
    if [[ "${fixture_curl_config}" != "${expected_asset_config}" ]]; then
      unexpected_fixture curl "${url}: credential config=${fixture_curl_config}"
      return
    fi
  elif [[ "${WORKCELL_FIXTURE_GITHUB_TOKEN:-}" != "" && "${url}" == https://api.github.com/* ]]; then
    local expected_config="header = \"Accept: application/vnd.github+json\"
header = \"Authorization: Bearer ${WORKCELL_FIXTURE_GITHUB_TOKEN}\""
    if [[ "${fixture_curl_config}" != "${expected_config}" ]]; then
      unexpected_fixture curl "${url}: credential config=${fixture_curl_config}"
      return
    fi
  fi
  record_fixture_http_request curl "${url}" "${fixture_curl_config}" "$*"
  case "${url}" in
    'https://go.dev/dl/?mode=json')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 "${url}" || return
      printf '[{"stable":true,"version":"go%s","files":[{"os":"linux","arch":"amd64","kind":"archive","sha256":"%s"},{"os":"linux","arch":"arm64","kind":"archive","sha256":"%s"}]}]\n' \
        "${WORKCELL_FIXTURE_GO_VERSION}" "${WORKCELL_FIXTURE_GO_AMD64_SHA}" "${WORKCELL_FIXTURE_GO_ARM64_SHA}"
      ;;
    'https://static.rust-lang.org/dist/channel-rust-stable.toml')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 "${url}" || return
      printf '[pkg.rust]\nversion = "%s (fixture)"\n' "${WORKCELL_FIXTURE_RUST_VERSION}"
      ;;
    'https://static.rust-lang.org/rustup/release-stable.toml')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 "${url}" || return
      printf "version = '%s'\n" "${WORKCELL_FIXTURE_RUSTUP_VERSION}"
      ;;
    */x86_64-unknown-linux-gnu/rustup-init.sha256)
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 60 --connect-timeout 15 --max-filesize 65536 "${url}" || return
      printf '%s  rustup-init\n' "${WORKCELL_FIXTURE_RUSTUP_AMD64_SHA}"
      ;;
    */aarch64-unknown-linux-gnu/rustup-init.sha256)
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 60 --connect-timeout 15 --max-filesize 65536 "${url}" || return
      printf '%s  rustup-init\n' "${WORKCELL_FIXTURE_RUSTUP_ARM64_SHA}"
      ;;
    'https://api.github.com/repos/hadolint/hadolint/releases/latest')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 -H 'Accept: application/vnd.github+json' "${url}" || return
      printf '{"tag_name":"%s","assets":[{"name":"checksums.sha256","browser_download_url":"https://fixture.invalid/hadolint-checksums.sha256"}]}\n' "${WORKCELL_FIXTURE_HADOLINT_VERSION}"
      ;;
    'https://fixture.invalid/hadolint-checksums.sha256')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 60 --connect-timeout 15 --max-filesize 65536 "${url}" || return
      printf '%s' "${WORKCELL_FIXTURE_HADOLINT_CHECKSUMS}"
      ;;
    'https://api.github.com/repos/docker/buildx/releases/latest')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 -H 'Accept: application/vnd.github+json' "${url}" || return
      printf '{"tag_name":"%s","assets":[]}\n' "${WORKCELL_FIXTURE_BUILDX_VERSION}"
      ;;
    'https://api.github.com/repos/docker/actions-toolkit/contents/.github/buildx-releases.json?ref=main')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 -H 'Accept: application/vnd.github+json' "${url}" || return
      local catalog
      catalog="$(printf '{"%s":{"tag_name":"%s"}}' "${WORKCELL_FIXTURE_BUILDX_VERSION}" "${WORKCELL_FIXTURE_BUILDX_VERSION}" | base64 | tr -d '\n')"
      printf '{"encoding":"base64","content":"%s"}\n' "${catalog}"
      ;;
    'https://api.github.com/repos/sigstore/cosign/releases/latest')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 -H 'Accept: application/vnd.github+json' "${url}" || return
      printf '{"tag_name":"%s","assets":[]}\n' "${WORKCELL_FIXTURE_COSIGN_VERSION}"
      ;;
    'https://api.github.com/repos/anchore/syft/releases/latest')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 -H 'Accept: application/vnd.github+json' "${url}" || return
      printf '{"tag_name":"%s","assets":[]}\n' "${WORKCELL_FIXTURE_SYFT_VERSION}"
      ;;
    'https://api.github.com/repos/rhysd/actionlint/releases/latest')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 -H 'Accept: application/vnd.github+json' "${url}" || return
      printf '{"tag_name":"v%s","assets":[{"id":%s,"name":"actionlint_%s_checksums.txt","url":"https://attacker.invalid/actionlint-checksums"}]}\n' \
        "${WORKCELL_FIXTURE_ACTIONLINT_VERSION}" "${WORKCELL_FIXTURE_ACTIONLINT_ASSET_ID}" "${WORKCELL_FIXTURE_ACTIONLINT_VERSION}"
      ;;
    https://api.github.com/repos/rhysd/actionlint/releases/assets/*)
      if [[ "${url}" != "https://api.github.com/repos/rhysd/actionlint/releases/assets/${WORKCELL_FIXTURE_ACTIONLINT_ASSET_ID}" ]]; then
        unexpected_fixture curl "${url}: unexpected release asset ID"
        return
      fi
      local asset_paths
      asset_paths="$(expect_fixture_authenticated_asset_argv "${url}" "$@")" || return
      if [[ "${WORKCELL_FIXTURE_GITHUB_TOKEN:-}" != "" && "${fixture_curl_config}" != "header = \"Authorization: Bearer ${WORKCELL_FIXTURE_GITHUB_TOKEN}\"" ]]; then
        unexpected_fixture curl "${url}: release asset credential config=${fixture_curl_config}"
        return
      fi
      if [[ "${WORKCELL_FIXTURE_GITHUB_TOKEN:-}" == "" && "${fixture_curl_config}" != "" ]]; then
        unexpected_fixture curl "${url}: unexpected release asset credential config=${fixture_curl_config}"
        return
      fi
      local headers_file="${asset_paths%%$'\n'*}"
      local body_file="${asset_paths#*$'\n'}"
      case "${WORKCELL_FIXTURE_ASSET_API_RESPONSE}" in
        direct)
          : >"${headers_file}"
          printf '%s  actionlint_%s_linux_amd64.tar.gz\n' "${WORKCELL_FIXTURE_ACTIONLINT_SHA}" "${WORKCELL_FIXTURE_ACTIONLINT_VERSION}" >"${body_file}"
          printf '200'
          ;;
        redirect)
          printf 'Location: %s\r\n' "${WORKCELL_FIXTURE_ASSET_REDIRECT_URL}" >"${headers_file}"
          : >"${body_file}"
          printf '302'
          ;;
        status-301)
          printf 'Location: %s\r\n' "${WORKCELL_FIXTURE_ASSET_REDIRECT_URL}" >"${headers_file}"
          : >"${body_file}"
          printf '301'
          ;;
        missing-location)
          : >"${headers_file}"
          : >"${body_file}"
          printf '302'
          ;;
        duplicate-location)
          printf 'Location: %s\r\nLocation: %s\r\n' "${WORKCELL_FIXTURE_ASSET_REDIRECT_URL}" "${WORKCELL_FIXTURE_ASSET_REDIRECT_URL}" >"${headers_file}"
          : >"${body_file}"
          printf '302'
          ;;
        hostile-location)
          printf 'Location: https://attacker.invalid/actionlint-checksums\r\n' >"${headers_file}"
          : >"${body_file}"
          printf '302'
          ;;
        *)
          unexpected_fixture curl "${url}: asset API response=${WORKCELL_FIXTURE_ASSET_API_RESPONSE}"
          ;;
      esac
      ;;
    "${WORKCELL_FIXTURE_ASSET_REDIRECT_URL:-https://release-assets.githubusercontent.com/github-production-release-asset/12345678/01234567-89ab-cdef-0123-456789abcdef?sp=r&sv=fixture}")
      local redirect_paths
      redirect_paths="$(expect_fixture_asset_redirect_argv "${url}" "$@")" || return
      if [[ "${fixture_curl_config}" != "" ]]; then
        unexpected_fixture curl "${url}: redirect retained credential config=${fixture_curl_config}"
        return
      fi
      local redirect_headers_file="${redirect_paths%%$'\n'*}"
      local redirect_body_file="${redirect_paths#*$'\n'}"
      case "${WORKCELL_FIXTURE_ASSET_REDIRECT_RESPONSE}" in
        body)
          : >"${redirect_headers_file}"
          printf '%s  actionlint_%s_linux_amd64.tar.gz\n' "${WORKCELL_FIXTURE_ACTIONLINT_SHA}" "${WORKCELL_FIXTURE_ACTIONLINT_VERSION}" >"${redirect_body_file}"
          printf '200'
          ;;
        redirect)
          printf 'Location: %s\r\n' "${WORKCELL_FIXTURE_ASSET_REDIRECT_URL}" >"${redirect_headers_file}"
          : >"${redirect_body_file}"
          printf '302'
          ;;
        *)
          unexpected_fixture curl "${url}: asset redirect response=${WORKCELL_FIXTURE_ASSET_REDIRECT_RESPONSE}"
          ;;
      esac
      ;;
    'https://api.github.com/repos/zizmorcore/zizmor/releases/latest')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 -H 'Accept: application/vnd.github+json' "${url}" || return
      printf '{"tag_name":"v%s","assets":[{"name":"zizmor-x86_64-unknown-linux-gnu.tar.gz","browser_download_url":"https://fixture.invalid/zizmor.tar.gz"}]}\n' "${WORKCELL_FIXTURE_ZIZMOR_VERSION}"
      ;;
    'https://fixture.invalid/zizmor.tar.gz')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 60 --connect-timeout 15 --max-filesize 209715200 "${url}" || return
      printf 'fixture-zizmor-archive'
      ;;
    'https://hub.docker.com/v2/repositories/tonistiigi/binfmt/tags?page_size=100')
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSL --max-time 120 --connect-timeout 15 --max-filesize 209715200 "${url}" || return
      printf '{"results":[{"name":"%s"}]}\n' "${WORKCELL_FIXTURE_QEMU_TAG}"
      ;;
    https://snapshot.debian.org/archive/debian/*/dists/trixie/Release|\
    https://snapshot.debian.org/archive/debian/*/dists/trixie-updates/Release|\
    https://snapshot.debian.org/archive/debian-security/*/dists/trixie-security/Release)
      expect_fixture_curl_argv "${url}" "$@" -- -q -fsSI --max-time 120 --connect-timeout 15 --max-filesize 209715200 "${url}" || return
      printf 'release %s\n' "${url}" >>"${WORKCELL_FIXTURE_RESOLUTION_LOG}"
      ;;
    *)
      unexpected_fixture curl "${url:-<missing-url>}"
      ;;
  esac
}

docker() {
  local ref="${!#}"
  local digest=""
  if [[ "${1:-}" != "buildx" || "${2:-}" != "imagetools" || "${3:-}" != "inspect" || "$#" -ne 4 ]]; then
    unexpected_fixture docker "$*"
    return
  fi
  if [[ "${ref}" == "${WORKCELL_FIXTURE_RUNTIME_BASE_TRACK}" ]]; then
    digest="${WORKCELL_FIXTURE_RUNTIME_BASE_DIGEST}"
  elif [[ "${ref}" == "${WORKCELL_FIXTURE_VALIDATOR_BASE_TRACK}" ]]; then
    digest="${WORKCELL_FIXTURE_VALIDATOR_BASE_DIGEST}"
  elif [[ "${ref}" == "${WORKCELL_FIXTURE_RUST_IMAGE_TRACK}" ]]; then
    digest="${WORKCELL_FIXTURE_RUST_IMAGE_DIGEST}"
  elif [[ "${ref}" == "${WORKCELL_FIXTURE_BUILDKIT_TRACK}" ]]; then
    digest="${WORKCELL_FIXTURE_BUILDKIT_DIGEST}"
  elif [[ "${ref}" == "tonistiigi/binfmt:${WORKCELL_FIXTURE_QEMU_TAG}" ]]; then
    digest="${WORKCELL_FIXTURE_QEMU_DIGEST}"
  else
    unexpected_fixture docker "${ref}"
    return
  fi
  printf 'Name: %s\nDigest: sha256:%s\n' "${ref}" "${digest}"
}

go() {
  local command="${3:-}"
  if [[ "${1:-}" != "run" || "${2:-}" != "./cmd/workcell-citools" || "$#" -lt 3 ]]; then
    unexpected_fixture go "$*"
    return
  fi
  if [[ "${command}" == "resolve-debian-bootstrap" ]]; then
    [[ "$#" -eq 4 && "$4" == "${WORKCELL_FIXTURE_DEBIAN_SNAPSHOT}" ]] || {
      unexpected_fixture go "$*"
      return
    }
    printf 'resolve %s\n' "$4" >>"${WORKCELL_FIXTURE_RESOLUTION_LOG}"
    printf '%s\n' "${WORKCELL_FIXTURE_DEBIAN_PLAN}"
    return
  fi
  printf '%s\n' "${command}" >>"${WORKCELL_FIXTURE_CITOOLS_LOG}"
  "${WORKCELL_FIXTURE_CITOOLS}" "${@:3}"
}

shasum() {
  if [[ "$*" != "-a 256" ]]; then
    unexpected_fixture shasum "$*"
    return
  fi
  while IFS= read -r _; do :; done
  printf '%s  -\n' "${WORKCELL_FIXTURE_ZIZMOR_SHA}"
}

source "$1" "$2"
`
