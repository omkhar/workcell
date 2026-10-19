// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	gooseTestArm = strings.Repeat("a", 64)
	gooseTestAmd = strings.Repeat("b", 64)
)

// gooseTestDockerfile is the goose pin shape of the runtime-uncertified build.
func gooseTestDockerfile(version, arm, amd string) string {
	return "FROM runtime-base AS uncertified-provider-builder\n" +
		"ARG GOOSE_VERSION=" + version + "\nARG GOOSE_SHA256_ARM64=" + arm + "\nARG GOOSE_SHA256_AMD64=" + amd + "\n" +
		"RUN case \"${TARGETARCH}\" in \\\n" +
		"    arm64) GOOSE_ARCH=\"aarch64-unknown-linux-musl\"; GOOSE_SHA256=\"${GOOSE_SHA256_ARM64}\" ;; \\\n" +
		"    amd64) GOOSE_ARCH=\"x86_64-unknown-linux-musl\"; GOOSE_SHA256=\"${GOOSE_SHA256_AMD64}\" ;; \\\n" +
		"  esac\n"
}

// gooseTestRelease is one GitHub release document with the two musl assets.
func gooseTestRelease(tag string, prerelease bool, published, arm, amd string) string {
	return fmt.Sprintf(`{"tag_name":%q,"prerelease":%t,"published_at":%q,"assets":[`+
		`{"name":"goose-aarch64-unknown-linux-musl.tar.gz","digest":"sha256:%s"},`+
		`{"name":"goose-x86_64-unknown-linux-musl.tar.gz","digest":"sha256:%s"}]}`, tag, prerelease, published, arm, amd)
}

func writeGooseTestDockerfile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	mustWriteText(t, path, text)
	return path
}

func TestGooseDockerfilePin(t *testing.T) {
	valid := gooseTestDockerfile("1.55.0", gooseTestArm, gooseTestAmd)
	version, checksums, err := gooseDockerfilePin(valid)
	if err != nil || version != "1.55.0" || checksums["arm64"] != gooseTestArm || checksums["amd64"] != gooseTestAmd {
		t.Fatalf("gooseDockerfilePin() = %q, %v, %v; want 1.55.0 and both checksums", version, checksums, err)
	}
	noVersion := strings.Replace(valid, "ARG GOOSE_VERSION=1.55.0\n", "", 1)
	const once = "must declare GOOSE_VERSION once, as ARG GOOSE_VERSION=VALUE"
	for name, tc := range map[string]struct {
		text, want string
	}{
		"no version":              {noVersion, once},
		"two versions":            {valid + "ARG GOOSE_VERSION=1.55.0\n", once},
		"an ENV override":         {valid + "ENV GOOSE_VERSION=1.56.0\n", once},
		"a legacy ENV":            {valid + "ENV GOOSE_SHA256_ARM64 " + gooseTestAmd + "\n", "must declare GOOSE_SHA256_ARM64 once"},
		"a two-pair ARG":          {noVersion + "ARG GOOSE_VERSION=1.55.0 X=1\n", once},
		"a quoted version":        {strings.Replace(valid, "=1.55.0", `="1.55.0"`, 1), `GOOSE_VERSION "\"1.55.0\"" is not a stable`},
		"a prerelease":            {strings.Replace(valid, "=1.55.0", "=2.0.0-rc-04-27-0", 1), `GOOSE_VERSION "2.0.0-rc-04-27-0" is not a stable`},
		"no arm64 checksum":       {strings.Replace(valid, "ARG GOOSE_SHA256_ARM64="+gooseTestArm+"\n", "", 1), "must declare GOOSE_SHA256_ARM64 once"},
		"a short amd64 sha":       {strings.Replace(valid, gooseTestAmd, gooseTestAmd[1:], 1), "GOOSE_SHA256_AMD64 for goose-x86_64-unknown-linux-musl.tar.gz is not a sha256"},
		"a comment pin":           {"# ARG GOOSE_VERSION=1.55.0\n" + noVersion, once},
		"a continued RUN pin":     {"RUN echo \\\nARG GOOSE_VERSION=1.55.0\n" + noVersion, once},
		"a heredoc pin":           {"RUN cat <<EOF\nARG GOOSE_VERSION=1.55.0\nEOF\n" + noVersion, once},
		"a dash heredoc pin":      {"RUN cat <<-'EOF'\nARG GOOSE_VERSION=1.55.0\n\tEOF\n" + noVersion, once},
		"a second heredoc pin":    {"RUN cat <<A <<B\nA\nARG GOOSE_VERSION=1.55.0\nB\n" + noVersion, once},
		"an unclosed heredoc":     {valid + "RUN cat <<EOF\nx\n", "the Dockerfile heredoc EOF is not closed"},
		"an escape directive":     {"# escape=`\n" + valid, "must not set a parser escape directive"},
		"a trailing continuation": {valid + "RUN true \\\n", "the Dockerfile ends inside a line continuation"},
	} {
		if _, _, err := gooseDockerfilePin(tc.text); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: gooseDockerfilePin() error = %v, want %q", name, err, tc.want)
		}
	}
}

// TestPlanGooseRelease plans the goose pin from one page of a stub release
// list: a prerelease, a release in the cool-off and a release without an
// amd64 asset are skipped, and the next release is the target.
func TestPlanGooseRelease(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/releases" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("[" + strings.Join([]string{
			gooseTestRelease("v2.0.0-rc-1", true, "2026-10-01T00:00:00Z", gooseTestArm, gooseTestAmd),
			gooseTestRelease("v1.57.0", false, "2026-10-17T00:00:00Z", gooseTestArm, gooseTestAmd),
			strings.Replace(gooseTestRelease("v1.56.0", false, "2026-10-10T00:00:00Z", gooseTestArm, gooseTestAmd), "x86_64", "x86-64", 1),
			gooseTestRelease("v1.55.1", false, "2026-10-09T00:00:00Z", strings.Repeat("c", 64), strings.Repeat("d", 64)),
			gooseTestRelease("v1.55.0", false, "2026-10-01T00:00:00Z", gooseTestArm, gooseTestAmd),
		}, ",") + "]"))
	}))
	defer server.Close()
	dockerfilePath := writeGooseTestDockerfile(t, gooseTestDockerfile("1.55.0", gooseTestArm, gooseTestAmd))
	cutoff := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)

	selection, err := selectGooseStable(dockerfilePath, cutoff, server.URL+"/releases", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Changed || selection.TargetVersion != "1.55.1" || selection.Checksums["arm64"] != strings.Repeat("c", 64) || selection.Checksums["amd64"] != strings.Repeat("d", 64) {
		t.Fatalf("goose selection = %#v, want 1.55.1 and its digests", selection)
	}
	var reasons []string
	for _, skipped := range selection.SkippedReleases {
		reasons = append(reasons, skipped.Version+": "+skipped.Reason)
	}
	if got := strings.Join(reasons, "\n"); !strings.Contains(got, "1.57.0: within configured cool-off window") ||
		!strings.Contains(got, "1.56.0: missing supported Linux release assets") {
		t.Fatalf("goose skipped releases = %s, want the cool-off and the missing-asset skips", got)
	}
	if requests != 1 {
		t.Fatalf("goose plan made %d release list requests, want 1", requests)
	}
}

func TestApplyUncertifiedReleaseBumpPlan(t *testing.T) {
	newArm, newAmd := strings.Repeat("c", 64), strings.Repeat("d", 64)
	cases := map[string]struct {
		releases map[string]ProviderBumpSelection
		before   string // the Dockerfile before apply, if not the 1.55.0 pin
		want     string // the Dockerfile after apply, or the error text
		wantErr  bool
	}{
		"a changed pin": {
			releases: map[string]ProviderBumpSelection{"goose": {TargetVersion: "1.55.1", Changed: true, Checksums: map[string]string{"arm64": newArm, "amd64": newAmd}}},
			want:     gooseTestDockerfile("1.55.1", newArm, newAmd),
		},
		"an unchanged pin": {
			releases: map[string]ProviderBumpSelection{"goose": {TargetVersion: "1.55.0"}},
			want:     gooseTestDockerfile("1.55.0", gooseTestArm, gooseTestAmd),
		},
		"no release plan": {want: gooseTestDockerfile("1.55.0", gooseTestArm, gooseTestAmd)},
		"an unknown release": {
			releases: map[string]ProviderBumpSelection{"other": {TargetVersion: "1.0.0", Changed: true}},
			want:     "plans the other release, which no Dockerfile pin owns", wantErr: true,
		},
		"a prerelease target": {
			releases: map[string]ProviderBumpSelection{"goose": {TargetVersion: "2.0.0-rc-1", Changed: true, Checksums: map[string]string{"arm64": newArm, "amd64": newAmd}}},
			want:     `the goose target version "2.0.0-rc-1" is not a stable X.Y.Z version`, wantErr: true,
		},
		"a pin the rewrite misses": {
			releases: map[string]ProviderBumpSelection{"goose": {TargetVersion: "1.55.1", Changed: true, Checksums: map[string]string{"arm64": newArm, "amd64": newAmd}}},
			// A trailing blank keeps the instruction valid, but the line
			// rewrite no longer matches it.
			before: strings.Replace(gooseTestDockerfile("1.55.0", gooseTestArm, gooseTestAmd), "=1.55.0\n", "=1.55.0 \n", 1),
			want:   "the goose pin did not rewrite to 1.55.1", wantErr: true,
		},
		"a missing checksum": {
			releases: map[string]ProviderBumpSelection{"goose": {TargetVersion: "1.55.1", Changed: true, Checksums: map[string]string{"arm64": newArm}}},
			want:     "the goose target version 1.55.1 needs a valid amd64 sha256 checksum", wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dockerfilePath := filepath.Join(dir, "Dockerfile")
			before := gooseTestDockerfile("1.55.0", gooseTestArm, gooseTestAmd)
			if tc.before != "" {
				before = tc.before
			}
			mustWriteText(t, dockerfilePath, before)
			content, err := json.Marshal(ProviderBumpPlan{UncertifiedReleases: tc.releases})
			if err != nil {
				t.Fatal(err)
			}
			planPath := filepath.Join(dir, "plan.json")
			mustWriteText(t, planPath, string(content))

			err = ApplyUncertifiedReleaseBumpPlan(planPath, dockerfilePath)
			got, _ := os.ReadFile(dockerfilePath)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), tc.want) || string(got) != before {
					t.Fatalf("ApplyUncertifiedReleaseBumpPlan() = %v, want %q and an unchanged Dockerfile", err, tc.want)
				}
				return
			}
			if err != nil || string(got) != tc.want {
				t.Fatalf("ApplyUncertifiedReleaseBumpPlan() = %v, Dockerfile:\n%s\nwant:\n%s", err, got, tc.want)
			}
		})
	}
}

func TestVerifyGooseReleasePin(t *testing.T) {
	cases := map[string]struct {
		release string
		want    string
	}{
		"a matching release": {gooseTestRelease("v1.55.0", false, "2026-10-01T00:00:00Z", gooseTestArm, gooseTestAmd), ""},
		"another tag":        {gooseTestRelease("v1.55.1", false, "2026-10-01T00:00:00Z", gooseTestArm, gooseTestAmd), `the goose release metadata names "v1.55.1", want v1.55.0`},
		"a prerelease":       {gooseTestRelease("v1.55.0", true, "2026-10-01T00:00:00Z", gooseTestArm, gooseTestAmd), "goose v1.55.0 is a prerelease"},
		"a missing asset":    {strings.Replace(gooseTestRelease("v1.55.0", false, "2026-10-01T00:00:00Z", gooseTestArm, gooseTestAmd), "aarch64", "arm64", 1), "goose v1.55.0 release: release asset not found: goose-aarch64-unknown-linux-musl.tar.gz"},
		"a different digest": {gooseTestRelease("v1.55.0", false, "2026-10-01T00:00:00Z", gooseTestArm, strings.Repeat("e", 64)), "the pinned amd64 goose sha256 " + gooseTestAmd + " differs from the v1.55.0 release digest " + strings.Repeat("e", 64)},
		"a malformed digest": {gooseTestRelease("v1.55.0", false, "2026-10-01T00:00:00Z", "zz", gooseTestAmd), "release asset goose-aarch64-unknown-linux-musl.tar.gz is missing a valid sha256 digest"},
		"an unpublished pin": {"", "unexpected status 404"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/tags/v1.55.0" || tc.release == "" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(tc.release))
			}))
			defer server.Close()
			dockerfilePath := writeGooseTestDockerfile(t, gooseTestDockerfile("1.55.0", gooseTestArm, gooseTestAmd))

			err := VerifyGooseReleasePin(dockerfilePath, server.URL+"/tags/v%s", server.Client())
			if tc.want == "" {
				if err != nil {
					t.Fatalf("VerifyGooseReleasePin() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("VerifyGooseReleasePin() = %v, want %q", err, tc.want)
			}
		})
	}
}
