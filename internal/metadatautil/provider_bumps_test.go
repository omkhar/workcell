// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlanProviderBumpsSelectsNewestStableVersionsPastCooloff(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, strings.Join([]string{
		"ARG CLAUDE_VERSION=2.1.86",
		"ARG CODEX_VERSION=0.117.0-alpha.8",
		"RUN true",
	}, "\n")+"\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.34.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 72",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/codex-registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "dist-tags": {
    "latest": "0.118.0",
    "alpha": "0.119.0-alpha.19"
  },
  "time": {
    "created": "2026-01-01T00:00:00Z",
    "modified": "2026-04-08T04:37:07Z",
    "0.117.0": "2026-03-20T12:00:00Z",
    "0.118.0": "2026-03-31T17:03:18Z",
    "0.119.0-alpha.19": "2026-04-08T04:37:07Z"
  }
}`))
		case "/codex-release/rust-v0.118.0":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "tag_name": "rust-v0.118.0",
  "prerelease": false,
  "assets": [
    {
      "name": "codex-aarch64-unknown-linux-gnu.tar.gz",
      "digest": "sha256:9f9c1241d39783384313975723475020dfbe1bd7b023c22b04816168159f8fd7"
    },
    {
      "name": "codex-x86_64-unknown-linux-gnu.tar.gz",
      "digest": "sha256:526b0d64ecf3d11c89d1d476deff3002ff2c2f728ef6f8f874f8d1a9d92e6e6b"
    }
  ]
}`))
		case "/gemini-registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "dist-tags": {
    "latest": "0.36.0",
    "preview": "0.37.0-preview.2",
    "nightly": "0.36.0-nightly.20260407.1c22c5b37"
  },
  "time": {
    "created": "2026-01-01T00:00:00Z",
    "modified": "2026-04-08T00:00:00Z",
    "0.34.0": "2026-03-17T21:03:34Z",
    "0.36.0": "2026-04-01T20:23:40Z",
    "0.37.0-preview.2": "2026-04-07T19:06:31Z"
  }
}`))
		case "/claude-bucket":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.96/</Prefix></CommonPrefixes>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.94/</Prefix></CommonPrefixes>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.92/</Prefix></CommonPrefixes>
</ListBucketResult>`))
		case "/claude-release/2.1.96/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "version": "2.1.96",
  "buildDate": "2026-04-08T03:19:21Z",
  "platforms": {
    "linux-arm64": {"checksum": "too-new-arm64"},
    "linux-x64": {"checksum": "too-new-amd64"}
  }
}`))
		case "/claude-release/2.1.94/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "version": "2.1.94",
  "buildDate": "2026-04-07T20:58:22Z",
  "platforms": {
    "linux-arm64": {"checksum": "still-too-new-arm64"},
    "linux-x64": {"checksum": "still-too-new-amd64"}
  }
}`))
		case "/claude-release/2.1.92/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "version": "2.1.92",
  "buildDate": "2026-04-03T23:57:51Z",
  "platforms": {
    "linux-arm64": {"checksum": "08deb3d56477496eb92e624f492e25b123f4527dd5674f71afff58a48eccd953"},
    "linux-x64": {"checksum": "e22324514967ff2d5e9f91f0ee37e4675bf8b6dfec27fafb19cb25cc5b23fcaf"}
  }
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	now := time.Date(2026, time.April, 8, 10, 0, 0, 0, time.UTC)
	plan, err := PlanProviderBumps(policyPath, dockerfilePath, packageJSONPath, now, ProviderBumpSources{
		CodexRegistryURL:      server.URL + "/codex-registry",
		CodexReleaseAPIURLFmt: server.URL + "/codex-release/rust-v%s",
		GeminiRegistryURL:     server.URL + "/gemini-registry",
		ClaudeBucketURL:       server.URL + "/claude-bucket",
		ClaudeReleaseRootURL:  server.URL + "/claude-release",
	}, server.Client())
	if err != nil {
		t.Fatalf("PlanProviderBumps() error = %v", err)
	}

	if !plan.HasChanges {
		t.Fatal("PlanProviderBumps() should report pending provider changes")
	}
	if got := plan.Providers["codex"].TargetVersion; got != "0.118.0" {
		t.Fatalf("Codex target = %q, want 0.118.0", got)
	}
	if got := plan.Providers["claude"].TargetVersion; got != "2.1.92" {
		t.Fatalf("Claude target = %q, want 2.1.92", got)
	}
	if got := plan.Providers["gemini"].TargetVersion; got != "0.36.0" {
		t.Fatalf("Gemini target = %q, want 0.36.0", got)
	}
	if got := plan.Providers["codex"].Checksums["arm64"]; got != "9f9c1241d39783384313975723475020dfbe1bd7b023c22b04816168159f8fd7" {
		t.Fatalf("Codex arm64 checksum = %q", got)
	}
}

func TestCheckProviderBumpPolicyRejectsPrereleaseCodexPin(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, "ARG CLAUDE_VERSION=2.1.92\nARG CODEX_VERSION=0.118.0-alpha.1\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 72",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	err := CheckProviderBumpPolicy(policyPath, dockerfilePath, packageJSONPath)
	if err == nil {
		t.Fatal("CheckProviderBumpPolicy() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "stable Codex pin") {
		t.Fatalf("CheckProviderBumpPolicy() error = %v", err)
	}
}

func TestCheckProviderBumpPolicyRejectsClaudeVersionAboveConfiguredCeiling(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, "ARG CLAUDE_VERSION=2.1.107\nARG CODEX_VERSION=0.118.0\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 72",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		`max_version = "2.1.104"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	err := CheckProviderBumpPolicy(policyPath, dockerfilePath, packageJSONPath)
	if err == nil {
		t.Fatal("CheckProviderBumpPolicy() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "requires Claude <= 2.1.104") {
		t.Fatalf("CheckProviderBumpPolicy() error = %v", err)
	}
}

func TestCheckProviderBumpPolicyAllowsApprovedClaudeVersion(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, "ARG CLAUDE_VERSION=2.1.108\nARG CODEX_VERSION=0.118.0\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 72",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		`max_version = "2.1.108"`,
		`approved_version = "2.1.108"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	if err := CheckProviderBumpPolicy(policyPath, dockerfilePath, packageJSONPath); err != nil {
		t.Fatalf("CheckProviderBumpPolicy() error = %v", err)
	}
}

func TestCheckProviderBumpPolicyRejectsClaudeApprovedVersionAboveConfiguredCeiling(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, "ARG CLAUDE_VERSION=2.1.104\nARG CODEX_VERSION=0.118.0\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 72",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		`max_version = "2.1.104"`,
		`approved_version = "2.1.108"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	err := CheckProviderBumpPolicy(policyPath, dockerfilePath, packageJSONPath)
	if err == nil {
		t.Fatal("CheckProviderBumpPolicy() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "requires provider.claude.approved_version <= 2.1.104") {
		t.Fatalf("CheckProviderBumpPolicy() error = %v", err)
	}
}

func TestCheckProviderBumpPolicyRejectsUnstableClaudePinWithCeiling(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, "ARG CLAUDE_VERSION=2.1.104-beta\nARG CODEX_VERSION=0.118.0\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 72",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		`max_version = "2.1.104"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	err := CheckProviderBumpPolicy(policyPath, dockerfilePath, packageJSONPath)
	if err == nil {
		t.Fatal("CheckProviderBumpPolicy() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "requires a stable Claude pin") {
		t.Fatalf("CheckProviderBumpPolicy() error = %v", err)
	}
}
func TestPlanProviderBumpsHonorsClaudeMaxVersion(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, strings.Join([]string{
		"ARG CLAUDE_VERSION=2.1.100",
		"ARG CODEX_VERSION=0.118.0",
		"RUN true",
	}, "\n")+"\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 12",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		`max_version = "2.1.104"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/codex-registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "dist-tags": {"latest": "0.118.0"},
  "time": {
    "created": "2026-01-01T00:00:00Z",
    "0.118.0": "2026-04-01T00:00:00Z"
  }
}`))
		case "/gemini-registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "dist-tags": {"latest": "0.36.0"},
  "time": {
    "created": "2026-01-01T00:00:00Z",
    "0.36.0": "2026-04-01T00:00:00Z"
  }
}`))
		case "/claude-bucket":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.107/</Prefix></CommonPrefixes>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.105/</Prefix></CommonPrefixes>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.104/</Prefix></CommonPrefixes>
</ListBucketResult>`))
		case "/claude-release/2.1.104/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "version": "2.1.104",
  "buildDate": "2026-04-12T01:53:39Z",
  "platforms": {
    "linux-arm64": {"checksum": "f0a79ec304334503a563c6d4618b0ea1fcbbe477a047dd3955e2078a3c5559c1"},
    "linux-x64": {"checksum": "f5fe84d4b8a5a322b83a8ae63ac117adb143d2a9a0bfd73a201a5201d6423869"}
  }
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	now := time.Date(2026, time.April, 16, 23, 0, 0, 0, time.UTC)
	plan, err := PlanProviderBumps(policyPath, dockerfilePath, packageJSONPath, now, ProviderBumpSources{
		CodexRegistryURL:      server.URL + "/codex-registry",
		CodexReleaseAPIURLFmt: server.URL + "/codex-release/rust-v%s",
		GeminiRegistryURL:     server.URL + "/gemini-registry",
		ClaudeBucketURL:       server.URL + "/claude-bucket",
		ClaudeReleaseRootURL:  server.URL + "/claude-release",
	}, server.Client())
	if err != nil {
		t.Fatalf("PlanProviderBumps() error = %v", err)
	}
	if got := plan.Providers["claude"].TargetVersion; got != "2.1.104" {
		t.Fatalf("Claude target = %q, want 2.1.104", got)
	}
	if got := plan.Providers["claude"].Checksums["arm64"]; got != "f0a79ec304334503a563c6d4618b0ea1fcbbe477a047dd3955e2078a3c5559c1" {
		t.Fatalf("Claude arm64 checksum = %q", got)
	}
}

func TestPlanProviderBumpsAllowsApprovedClaudeVersionPastCooloff(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, strings.Join([]string{
		"ARG CLAUDE_VERSION=2.1.104",
		"ARG CODEX_VERSION=0.118.0",
		"RUN true",
	}, "\n")+"\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 12",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		`max_version = "2.1.108"`,
		`approved_version = "2.1.108"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/codex-registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "dist-tags": {"latest": "0.118.0"},
  "time": {
    "created": "2026-01-01T00:00:00Z",
    "0.118.0": "2026-04-01T00:00:00Z"
  }
}`))
		case "/gemini-registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "dist-tags": {"latest": "0.36.0"},
  "time": {
    "created": "2026-01-01T00:00:00Z",
    "0.36.0": "2026-04-01T00:00:00Z"
  }
}`))
		case "/claude-bucket":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.108/</Prefix></CommonPrefixes>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.107/</Prefix></CommonPrefixes>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.104/</Prefix></CommonPrefixes>
</ListBucketResult>`))
		case "/claude-release/2.1.108/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "version": "2.1.108",
  "buildDate": "2026-04-17T22:00:00Z",
  "platforms": {
    "linux-arm64": {"checksum": "108arm64108arm64108arm64108arm64108arm64108arm64108arm64108arm64"},
    "linux-x64": {"checksum": "108amd64108amd64108amd64108amd64108amd64108amd64108amd64108amd64"}
  }
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	now := time.Date(2026, time.April, 17, 23, 0, 0, 0, time.UTC)
	plan, err := PlanProviderBumps(policyPath, dockerfilePath, packageJSONPath, now, ProviderBumpSources{
		CodexRegistryURL:      server.URL + "/codex-registry",
		CodexReleaseAPIURLFmt: server.URL + "/codex-release/rust-v%s",
		GeminiRegistryURL:     server.URL + "/gemini-registry",
		ClaudeBucketURL:       server.URL + "/claude-bucket",
		ClaudeReleaseRootURL:  server.URL + "/claude-release",
	}, server.Client())
	if err != nil {
		t.Fatalf("PlanProviderBumps() error = %v", err)
	}
	if got := plan.Providers["claude"].TargetVersion; got != "2.1.108" {
		t.Fatalf("Claude target = %q, want 2.1.108", got)
	}
	if got := plan.Providers["claude"].Checksums["arm64"]; got != "108arm64108arm64108arm64108arm64108arm64108arm64108arm64108arm64" {
		t.Fatalf("Claude arm64 checksum = %q", got)
	}
}

func TestApplyProviderBumpPlanRejectsUnstableClaudeTargetVersion(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")
	planPath := filepath.Join(root, "plan.json")

	mustWriteText(t, dockerfilePath, "ARG CLAUDE_VERSION=2.1.104\nARG CODEX_VERSION=0.118.0\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 12",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		`max_version = "2.1.104"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	plan := ProviderBumpPlan{
		Providers: map[string]ProviderBumpSelection{
			"claude": {
				TargetVersion: "2.1.104-beta",
				Checksums: map[string]string{
					"arm64": "99376866bf7ec367142d3be548c17184a79f30a97318441ee9a00f78e51246e7",
					"amd64": "5d4df970040b0f83aac434ae540b409126a4778a379e8c9b4c793560e3bfa060",
				},
			},
			"codex": {
				TargetVersion: "0.118.0",
			},
			"gemini": {
				TargetVersion: "0.36.0",
			},
		},
	}
	content, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	err = ApplyProviderBumpPlan(planPath, policyPath, dockerfilePath, packageJSONPath)
	if err == nil {
		t.Fatal("ApplyProviderBumpPlan() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "contains a non-stable Claude target version") {
		t.Fatalf("ApplyProviderBumpPlan() error = %v", err)
	}
}
func TestPlanProviderBumpsRespectsCurrentRegistryLatestTrack(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")

	mustWriteText(t, dockerfilePath, "ARG CLAUDE_VERSION=2.1.92\nARG CODEX_VERSION=0.118.0\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 72",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	var codexReleaseRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/codex-registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "dist-tags": {
    "latest": "0.118.0",
    "alpha": "0.119.0-alpha.19"
  },
  "time": {
    "created": "2026-01-01T00:00:00Z",
    "modified": "2026-04-08T04:37:07Z",
    "0.118.0": "2026-03-31T17:03:18Z",
  "0.119.0": "2026-04-02T17:03:18Z",
  "0.119.0-alpha.19": "2026-04-08T04:37:07Z"
  }
}`))
		case "/codex-release/rust-v0.118.0":
			codexReleaseRequests.Add(1)
			http.Error(w, "unexpected Codex release metadata request for unchanged stable pin", http.StatusInternalServerError)
		case "/gemini-registry":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "dist-tags": {"latest": "0.36.0"},
  "time": {
    "created": "2026-01-01T00:00:00Z",
    "0.36.0": "2026-04-01T20:23:40Z"
  }
}`))
		case "/claude-bucket":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <CommonPrefixes><Prefix>claude-code-releases/2.1.92/</Prefix></CommonPrefixes>
</ListBucketResult>`))
		case "/claude-release/2.1.92/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "version": "2.1.92",
  "buildDate": "2026-04-03T23:57:51Z",
  "platforms": {
    "linux-arm64": {"checksum": "08deb3d56477496eb92e624f492e25b123f4527dd5674f71afff58a48eccd953"},
    "linux-x64": {"checksum": "e22324514967ff2d5e9f91f0ee37e4675bf8b6dfec27fafb19cb25cc5b23fcaf"}
  }
}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	now := time.Date(2026, time.April, 8, 10, 0, 0, 0, time.UTC)
	plan, err := PlanProviderBumps(policyPath, dockerfilePath, packageJSONPath, now, ProviderBumpSources{
		CodexRegistryURL:      server.URL + "/codex-registry",
		CodexReleaseAPIURLFmt: server.URL + "/codex-release/rust-v%s",
		GeminiRegistryURL:     server.URL + "/gemini-registry",
		ClaudeBucketURL:       server.URL + "/claude-bucket",
		ClaudeReleaseRootURL:  server.URL + "/claude-release",
	}, server.Client())
	if err != nil {
		t.Fatalf("PlanProviderBumps() error = %v", err)
	}

	if plan.Providers["codex"].TargetVersion != "0.118.0" {
		t.Fatalf("Codex target = %q, want 0.118.0", plan.Providers["codex"].TargetVersion)
	}
	if plan.Providers["codex"].Changed {
		t.Fatal("Codex plan should not propose a version newer than dist-tags.latest")
	}
	if got := codexReleaseRequests.Load(); got != 0 {
		t.Fatalf("Codex release API was queried %d time(s) for an unchanged stable pin", got)
	}
}

func TestFetchJSONAddsUserAgentHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != providerBumpUserAgent {
			t.Fatalf("User-Agent = %q, want %q", got, providerBumpUserAgent)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	var decoded map[string]bool
	if err := fetchJSON(server.Client(), server.URL, &decoded); err != nil {
		t.Fatalf("fetchJSON() error = %v", err)
	}
	if !decoded["ok"] {
		t.Fatalf("fetchJSON() decoded = %#v", decoded)
	}
}

func TestApplyProviderBumpPlanRewritesPinnedVersions(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")
	planPath := filepath.Join(root, "plan.json")

	mustWriteText(t, dockerfilePath, strings.Join([]string{
		"ARG CLAUDE_VERSION=2.1.86",
		"ARG CODEX_VERSION=0.117.0-alpha.8",
		"RUN case \"${TARGET_ARCH}\" in \\",
		"  arm64) \\",
		"    CLAUDE_PLATFORM=\"linux-arm64\"; \\",
		"    CLAUDE_SHA256=\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"; \\",
		"    ;;",
		"  amd64) \\",
		"    CLAUDE_PLATFORM=\"linux-x64\"; \\",
		"    CLAUDE_SHA256=\"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\"; \\",
		"    ;;",
		"esac",
		"RUN case \"${TARGET_ARCH}\" in \\",
		"  arm64) \\",
		"    CODEX_ARCH=\"aarch64-unknown-linux-gnu\"; \\",
		"    CODEX_SHA256=\"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc\"; \\",
		"    ;;",
		"  amd64) \\",
		"    CODEX_ARCH=\"x86_64-unknown-linux-gnu\"; \\",
		"    CODEX_SHA256=\"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd\"; \\",
		"    ;;",
		"esac",
	}, "\n")+"\n")
	mustWriteText(t, packageJSONPath, "{\n  \"dependencies\": {\n    \"@google/gemini-cli\": \"0.34.0\"\n  }\n}\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 72",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	plan := ProviderBumpPlan{
		Providers: map[string]ProviderBumpSelection{
			"claude": {
				TargetVersion: "2.1.92",
				Checksums: map[string]string{
					"arm64": "08deb3d56477496eb92e624f492e25b123f4527dd5674f71afff58a48eccd953",
					"amd64": "e22324514967ff2d5e9f91f0ee37e4675bf8b6dfec27fafb19cb25cc5b23fcaf",
				},
			},
			"codex": {
				TargetVersion: "0.118.0",
				Checksums: map[string]string{
					"arm64": "9f9c1241d39783384313975723475020dfbe1bd7b023c22b04816168159f8fd7",
					"amd64": "526b0d64ecf3d11c89d1d476deff3002ff2c2f728ef6f8f874f8d1a9d92e6e6b",
				},
			},
			"gemini": {
				TargetVersion: "0.36.0",
			},
		},
	}
	content, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyProviderBumpPlan(planPath, policyPath, dockerfilePath, packageJSONPath); err != nil {
		t.Fatalf("ApplyProviderBumpPlan() error = %v", err)
	}

	updatedDockerfile, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedDockerfile), "ARG CLAUDE_VERSION=2.1.92") {
		t.Fatalf("Dockerfile was not updated with Claude target version:\n%s", updatedDockerfile)
	}
	if !strings.Contains(string(updatedDockerfile), "ARG CODEX_VERSION=0.118.0") {
		t.Fatalf("Dockerfile was not updated with Codex target version:\n%s", updatedDockerfile)
	}
	if !strings.Contains(string(updatedDockerfile), `CLAUDE_SHA256="08deb3d56477496eb92e624f492e25b123f4527dd5674f71afff58a48eccd953"`) {
		t.Fatalf("Dockerfile was not updated with Claude arm64 checksum:\n%s", updatedDockerfile)
	}
	if !strings.Contains(string(updatedDockerfile), `CODEX_SHA256="526b0d64ecf3d11c89d1d476deff3002ff2c2f728ef6f8f874f8d1a9d92e6e6b"`) {
		t.Fatalf("Dockerfile was not updated with Codex amd64 checksum:\n%s", updatedDockerfile)
	}

	updatedPackageJSON, err := os.ReadFile(packageJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedPackageJSON), `"@google/gemini-cli": "0.36.0"`) {
		t.Fatalf("package.json was not updated with Gemini target version:\n%s", updatedPackageJSON)
	}
}

func TestApplyProviderBumpPlanRejectsClaudeTargetAboveConfiguredCeiling(t *testing.T) {
	root := t.TempDir()
	dockerfilePath := filepath.Join(root, "Dockerfile")
	packageJSONPath := filepath.Join(root, "package.json")
	policyPath := filepath.Join(root, "provider-bumps.toml")
	planPath := filepath.Join(root, "plan.json")

	mustWriteText(t, dockerfilePath, "ARG CLAUDE_VERSION=2.1.104\nARG CODEX_VERSION=0.118.0\n")
	mustWriteText(t, packageJSONPath, `{"dependencies":{"@google/gemini-cli":"0.36.0"}}`+"\n")
	mustWriteText(t, policyPath, strings.Join([]string{
		"version = 1",
		"cooloff_hours = 12",
		"",
		"[provider.codex]",
		`channel = "stable"`,
		"",
		"[provider.claude]",
		`channel = "stable"`,
		`max_version = "2.1.104"`,
		"",
		"[provider.gemini]",
		`channel = "stable"`,
	}, "\n")+"\n")

	plan := ProviderBumpPlan{
		Providers: map[string]ProviderBumpSelection{
			"claude": {
				TargetVersion: "2.1.111",
				Checksums: map[string]string{
					"arm64": "99376866bf7ec367142d3be548c17184a79f30a97318441ee9a00f78e51246e7",
					"amd64": "5d4df970040b0f83aac434ae540b409126a4778a379e8c9b4c793560e3bfa060",
				},
			},
			"codex": {
				TargetVersion: "0.118.0",
			},
			"gemini": {
				TargetVersion: "0.36.0",
			},
		},
	}
	content, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	err = ApplyProviderBumpPlan(planPath, policyPath, dockerfilePath, packageJSONPath)
	if err == nil {
		t.Fatal("ApplyProviderBumpPlan() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "requires Claude <= 2.1.104") {
		t.Fatalf("ApplyProviderBumpPlan() error = %v", err)
	}
}

func mustWriteText(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
