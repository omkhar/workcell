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

// TestGooseInstallRunRejectsEvasions runs the shared evasion corpus on the
// download and on the pin match of the goose RUN body: every row that turns
// the command into text, gates it or moves it fails the check.
func TestGooseInstallRunRejectsEvasions(t *testing.T) {
	download := `curl --ipv4 -fsSL "https://github.com/aaif-goose/goose/releases/download/v${GOOSE_VERSION}/goose-${GOOSE_ARCH}.tar.gz" --retry 8 --retry-all-errors --retry-delay 5 --connect-timeout 30 -o /tmp/goose.tar.gz`
	match := `grep -qxF -e "${GOOSE_SHA256_ARM64}  /tmp/goose.tar.gz" -e "${GOOSE_SHA256_AMD64}  /tmp/goose.tar.gz" /tmp/goose.sum`
	body := "set -eu\nread -r GOOSE_ARCH < /tmp/goose-arch\n" + download + "\nsha256sum /tmp/goose.tar.gz > /tmp/goose.sum\n" + match + "\ntar -xzf /tmp/goose.tar.gz -C /tmp ./goose\n"
	if err := metadatautil.ValidateGooseInstallRun(body); err != nil {
		t.Fatalf("the reviewed body is rejected: %v", err)
	}
	for _, anchor := range []string{download, match} {
		RequireRejectsAllEvasions(t, body, anchor, "the goose install", metadatautil.ValidateGooseInstallRun)
	}
}
