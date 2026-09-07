// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"regexp"
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
	if count := len(preloadAssignment.FindAllString(dockerfile, -1)); count != 3 {
		return fmt.Errorf("%s must keep exactly the three canonical early runtime build preload assignments, found %d", path, count)
	}
	return nil
}

// preloadAssignment matches an assignment of the guard variable that takes
// effect: a Dockerfile ENV instruction or a shell export inside a RUN. It is
// anchored to the start of a line, so a comment or prose that names the
// variable assigns nothing and does not count. The variable may appear in any
// position of the instruction, because ENV and export both persist every key
// they list, and it is matched by name alone so that the legacy space-separated
// ENV form cannot smuggle in a fourth assignment either.
var preloadAssignment = regexp.MustCompile(`(?m)^[ \t]*(?:ENV|(?:&&[ \t]+)?export)[ \t]+(?:[^\n]*[ \t])?LD_PRELOAD\b`)
