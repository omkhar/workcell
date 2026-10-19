// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckPinnedInputsPreservesCompoundReadFailurePrecedence(t *testing.T) {
	cfg := writePinnedInputsFixture(t)
	if err := os.Remove(cfg.RuntimeDockerfilePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cfg.ValidatorDockerfilePath); err != nil {
		t.Fatal(err)
	}
	err := metadatautil.CheckPinnedInputs(cfg)
	want := (&os.PathError{Op: "open", Path: cfg.RuntimeDockerfilePath, Err: syscall.ENOENT}).Error()
	if err == nil || err.Error() != want {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want first read failure %q", err, want)
	}
}

func TestCheckPinnedInputsPreservesCompoundParseFailurePrecedence(t *testing.T) {
	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, cfg.ProvidersPackageJSONPath, func(string) string { return "{" })
	rewriteFile(t, cfg.ProvidersPackageLockPath, func(string) string { return "!" })
	err := metadatautil.CheckPinnedInputs(cfg)
	if err == nil || err.Error() != "unexpected end of JSON input" {
		t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want first JSON parse failure", err)
	}
}

func TestCheckPinnedInputsPreservesCompoundValidationFailurePrecedence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(testing.TB, metadatautil.PinnedInputsConfig)
		want   func(metadatautil.PinnedInputsConfig) string
	}{
		{
			name: "rust version before image",
			mutate: func(tb testing.TB, cfg metadatautil.PinnedInputsConfig) {
				rewriteFile(tb, cfg.RuntimeDockerfilePath, func(content string) string {
					content = replaceFirstMatch(tb, content, regexp.MustCompile(`(?m)^ARG RUST_VERSION=.*$`), "ARG RUST_VERSION=1.2.3")
					return replaceFirstMatch(tb, content, regexp.MustCompile(`(?m)^ARG RUST_TOOLCHAIN_IMAGE=.*$`), "ARG RUST_TOOLCHAIN_IMAGE=not-pinned")
				})
				rewriteFile(tb, cfg.ValidatorDockerfilePath, func(content string) string {
					return replaceFirstMatch(tb, content, regexp.MustCompile(`(?m)^ARG RUST_VERSION=.*$`), "ARG RUST_VERSION=9.9.9")
				})
			},
			want: func(cfg metadatautil.PinnedInputsConfig) string {
				return fmt.Sprintf("RUST_VERSION must match between %s (%q) and %s (%q)", cfg.RuntimeDockerfilePath, "1.2.3", cfg.ValidatorDockerfilePath, "9.9.9")
			},
		},
		{
			name: "actionlint version before digest",
			mutate: func(tb testing.TB, cfg metadatautil.PinnedInputsConfig) {
				securityPath := filepath.Join(cfg.WorkflowsDir, "security.yml")
				rewriteFile(tb, securityPath, func(content string) string {
					content = replaceAllMatches(tb, content, regexp.MustCompile(`ACTIONLINT_VERSION: [0-9]+\.[0-9]+\.[0-9]+`), "ACTIONLINT_VERSION: 0.0.0")
					return replaceAllMatches(tb, content, regexp.MustCompile(`ACTIONLINT_SHA256: [0-9a-f]{64}`), "ACTIONLINT_SHA256: deadbeef")
				})
			},
			want: func(metadatautil.PinnedInputsConfig) string {
				return "ACTIONLINT_VERSION must match between .github/workflows/security.yml and .github/workflows/release.yml"
			},
		},
		{
			name: "release cache before qemu and runner",
			mutate: func(tb testing.TB, cfg metadatautil.PinnedInputsConfig) {
				rewriteFile(tb, cfg.ReleaseWorkflowPath, func(content string) string {
					content = strings.ReplaceAll(content, "cache-binary: false", "cache-binary: true")
					content = strings.ReplaceAll(content, "runs-on: ubuntu-24.04-arm", "runs-on: ubuntu-latest")
					return content + "\n# docker/setup-qemu-action@compound-fault\n"
				})
			},
			want: func(metadatautil.PinnedInputsConfig) string {
				return "the publishing release workflow must not cache the Buildx binary"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := writePinnedInputsFixture(t)
			test.mutate(t, cfg)
			err := metadatautil.CheckPinnedInputs(cfg)
			if err == nil || err.Error() != test.want(cfg) {
				t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want %q", err, test.want(cfg))
			}
		})
	}
}

// The uncertified provider lockfile gets the Tier 1 lockfile rules: an exact
// root pin, and an npm registry tarball with integrity for every entry.
func TestCheckPinnedInputsValidatesUncertifiedProviderLock(t *testing.T) {
	for _, tc := range []struct {
		name, old, new, want string
	}{
		{
			name: "source",
			old:  "https://registry.npmjs.org/opencode-linux-arm64/",
			new:  "https://mirror.example/opencode-linux-arm64/",
			want: "runtime/container/providers-uncertified/package-lock.json: provider lockfile entry uses an unexpected source (node_modules/opencode-linux-arm64)",
		},
		{
			name: "root pin",
			old:  `"opencode-ai": "1.`,
			new:  `"opencode-ai": "^1.`,
			want: "runtime/container/providers-uncertified/package-lock.json root dependencies do not match package.json",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := writePinnedInputsFixture(t)
			lock := filepath.Join(filepath.Dir(filepath.Dir(cfg.ProvidersPackageLockPath)), "providers-uncertified", "package-lock.json")
			rewriteFile(t, lock, func(content string) string {
				if !strings.Contains(content, tc.old) {
					t.Fatalf("fixture lockfile has no %q", tc.old)
				}
				return strings.Replace(content, tc.old, tc.new, 1)
			})
			err := metadatautil.CheckPinnedInputs(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want %q", err, tc.want)
			}
		})
	}
}

// The goose pin of the runtime-uncertified build keeps its release URL, its
// sha256 check, one stable version, one sha256 ARG per architecture, and the
// case arm that selects the musl archive and the sha256 ARG of each
// architecture.
func TestCheckPinnedInputsValidatesGoosePin(t *testing.T) {
	for _, tc := range []struct {
		name, old, new, want string
	}{
		{
			name: "download host",
			old:  "https://github.com/aaif-goose/goose/releases/download/",
			new:  "https://mirror.example/goose/releases/download/",
			want: "goose release download URL",
		},
		{
			name: "checksum check",
			old:  `echo "${GOOSE_SHA256}  /tmp/goose.tar.gz" | sha256sum -c -`,
			new:  `echo "${GOOSE_SHA256}  /tmp/goose.tar.gz"`,
			want: "goose archive checksum verification",
		},
		{
			name: "version",
			old:  "ARG GOOSE_VERSION=",
			new:  "ARG GOOSE_VERSION=v",
			want: `GOOSE_VERSION "v1.55.0" is not a stable X.Y.Z version`,
		},
		{
			name: "architecture",
			old:  `GOOSE_ARCH="x86_64-unknown-linux-musl"`,
			new:  `GOOSE_ARCH="x86_64-unknown-linux-gnu"`,
			want: "goose amd64 checksum selection",
		},
		{
			name: "checksum selection",
			old:  `GOOSE_SHA256="${GOOSE_SHA256_ARM64}"`,
			new:  `GOOSE_SHA256="${GOOSE_SHA256_AMD64}"`,
			want: "goose arm64 checksum selection",
		},
		{
			name: "checksum pin",
			old:  "ARG GOOSE_SHA256_AMD64=",
			new:  "ENV GOOSE_SHA256_AMD64=",
			want: "must declare GOOSE_SHA256_AMD64 once",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := writePinnedInputsFixture(t)
			rewriteFile(t, cfg.RuntimeDockerfilePath, func(content string) string {
				if !strings.Contains(content, tc.old) {
					t.Fatalf("fixture Dockerfile has no %q", tc.old)
				}
				return strings.Replace(content, tc.old, tc.new, 1)
			})
			err := metadatautil.CheckPinnedInputs(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want %q", err, tc.want)
			}
		})
	}
}
