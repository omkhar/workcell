// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"regexp"
	"strings"
)

const runtimeGuardPreload = "LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so"

// Validate the canonical build fragments, not arbitrary Dockerfile semantics.
// Keep activation and the next child command together in the builder RUN.
func validateRuntimeBuildPreload(dockerfile, path string) error {
	blocks := []string{
		"  && printf '/usr/local/lib/libworkcell_exec_guard.so\\n' > /etc/ld.so.preload \\\n" +
			"  && export " + runtimeGuardPreload + " \\\n" +
			"  && rm -rf /tmp/workcell-rust-target /workcell-rust\n\n" +
			"ENV " + runtimeGuardPreload + "\n",
		"COPY runtime/container/control-plane-manifest.json /usr/local/libexec/workcell/control-plane-manifest.json\n\n" +
			"ENV " + runtimeGuardPreload + "\n\n" +
			"RUN mv /usr/bin/git /usr/local/libexec/workcell/real/git \\\n",
	}
	for _, block := range blocks {
		if _, _, err := requireRegex(dockerfile, "(?m)^"+regexp.QuoteMeta(block), "early runtime build preload", path); err != nil {
			return err
		}
	}
	if strings.Count(dockerfile, "LD_PRELOAD") != 3 {
		return fmt.Errorf("%s must keep exactly the three canonical early runtime build preload assignments", path)
	}
	return nil
}
