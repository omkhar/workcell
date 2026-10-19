// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

// TestGooseDockerfilePinRejectsTextEvasions runs the shared evasion corpus on
// the goose version pin: every row that turns the ARG instruction into other
// text fails the reader.
func TestGooseDockerfilePinRejectsTextEvasions(t *testing.T) {
	sum := strings.Repeat("a", 64)
	dockerfile := "FROM runtime-base AS uncertified-provider-builder\n" +
		"ARG GOOSE_VERSION=1.55.0\nARG GOOSE_SHA256_ARM64=" + sum + "\nARG GOOSE_SHA256_AMD64=" + sum + "\n"
	// No row is accepted: a shell form that still runs the anchor is not a
	// Dockerfile instruction, so the build refuses it too.
	requireEvasions(t, dockerfile, "ARG GOOSE_VERSION=1.55.0", "the goose pin", map[string]bool{}, func(text string) error {
		_, _, err := metadatautil.GooseDockerfilePin(text)
		return err
	})
}
