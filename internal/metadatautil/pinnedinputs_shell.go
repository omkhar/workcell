// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// canonicalHostedShellFileSHA256 pins the exact reviewed bytes of
// scripts/verify-github-hosted-controls.sh. Any edit to the script changes
// the reviewed hosted-controls command graph: re-review the script and
// update this digest in the same change. The raw-byte digest subsumes the
// former mvdan.cc/sh AST audit (command allowlist, call counts, and
// canonical function bodies): every structural drift those checks caught
// also changes the file bytes.
const canonicalHostedShellFileSHA256 = "3da21bd8d6d0b333a01bcfbef9a4afd72977c199b7648b02dd81fb5ef0dbee32"

var errHostedShellRouting = errors.New("scripts/verify-github-hosted-controls.sh must use the exact reviewed command graph and versioned github_api wrapper")

func validateCanonicalHostedControlsScript(script string) error {
	if !strings.HasPrefix(script, "#!/bin/bash -p\n") {
		return errors.New("scripts/verify-github-hosted-controls.sh must use the exact privileged Bash shebang")
	}
	digest := sha256.Sum256([]byte(script))
	if fmt.Sprintf("%x", digest) != canonicalHostedShellFileSHA256 {
		return fmt.Errorf("%w: unexpected shell structure", errHostedShellRouting)
	}
	return nil
}
