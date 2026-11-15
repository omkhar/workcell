// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

// TestCheckPinnedInputsRejectsBaseImageOnAnotherRepository requires each base
// image pin to name its one reviewed repository on Docker Hub. A pin on another
// registry or under another prefix fails even when a digest pins it.
func TestCheckPinnedInputsRejectsBaseImageOnAnotherRepository(t *testing.T) {
	t.Parallel()
	digest := "@sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, arg, image string
		validator        bool
	}{
		{"runtime base on another registry", "NODE_BASE_IMAGE", "ghcr.io/evil/node:24-trixie-slim" + digest, false},
		{"runtime base under the library prefix", "NODE_BASE_IMAGE", "docker.io/library/node:24-trixie-slim" + digest, false},
		{"runtime base from another repository", "NODE_BASE_IMAGE", "nodejs:24-trixie-slim" + digest, false},
		{"validator base on another registry", "VALIDATOR_BASE_IMAGE", "evil.example/node:24-trixie-slim" + digest, true},
		{"rust image on another registry", "RUST_TOOLCHAIN_IMAGE", "ghcr.io/evil/rust:1.99.1-slim-trixie" + digest, false},
		{"rust image with no tag", "RUST_TOOLCHAIN_IMAGE", "rust" + digest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := writePinnedInputsFixture(t)
			path := cfg.RuntimeDockerfilePath
			if tc.validator {
				path = cfg.ValidatorDockerfilePath
			}
			rewriteFile(t, path, func(content string) string {
				return replaceFirstMatch(t, content, regexp.MustCompile(`(?m)^ARG `+tc.arg+`=.*$`), "ARG "+tc.arg+"="+tc.image)
			})
			err := metadatautil.CheckPinnedInputs(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.arg) || !strings.Contains(err.Error(), "<tag>@sha256:<digest>") {
				t.Fatalf("metadatautil.CheckPinnedInputs() error = %v, want a %s repository rejection", err, tc.arg)
			}
		})
	}
}
