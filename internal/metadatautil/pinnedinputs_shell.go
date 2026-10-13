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
const canonicalHostedShellFileSHA256 = "6db26ecf583cf72e910a74cb7a6bd9382ff5da9c46e2697884e7ec7820a74098"

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
