// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
