// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const runtimeGuardPreload = "LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so"

// runtimeGuardPreloadNote is the reviewed comment that must stay attached to
// each declaration, so the reason the declaration sits where it does cannot be
// dropped without this check noticing.
const runtimeGuardPreloadNote = "# /etc/ld.so.preload loads the exec guard into every process from here on, and\n" +
	"# the guard refuses a child environment without its own preload. Declare it\n" +
	"# before the next RUN, or the build's own commands are the first thing refused.\n"

// Validate the canonical build fragments, not arbitrary Dockerfile semantics.
// Keep activation and the next child command together in the builder RUN.
func validateRuntimeBuildPreload(dockerfile, path string) error {
	blocks := []string{
		"  && printf '/usr/local/lib/libworkcell_exec_guard.so\\n' > /etc/ld.so.preload \\\n" +
			"  && export " + runtimeGuardPreload + " \\\n" +
			"  && rm -rf /tmp/workcell-rust-target /workcell-rust\n\n" +
			runtimeGuardPreloadNote +
			"ENV " + runtimeGuardPreload + "\n",
		"COPY runtime/container/control-plane-manifest.json /usr/local/libexec/workcell/control-plane-manifest.json\n\n" +
			runtimeGuardPreloadNote +
			"ENV " + runtimeGuardPreload + "\n\n" +
			"RUN mv /usr/bin/git /usr/local/libexec/workcell/real/git \\\n",
	}
	for _, block := range blocks {
		if _, _, err := requireRegex(dockerfile, "(?m)^"+regexp.QuoteMeta(block), "early runtime build preload", path); err != nil {
			return err
		}
	}
	if count := preloadAssignments(dockerfile); count != 3 {
		return fmt.Errorf("%s must keep exactly the three canonical early runtime build preload assignments, found %d", path, count)
	}
	return nil
}

// runtimeGuardVariable is the environment variable the guard is activated
// through. Every assignment of it in the build file is reviewed.
const runtimeGuardVariable = "LD_PRELOAD"

// preloadAssignments counts the assignments of the guard variable that take
// effect. It reads logical Dockerfile instructions, so a comment, a line split
// across a continuation, a key in any position, and the legacy space-separated
// ENV form are each read the way Docker reads them. ENV and export both persist
// every key they list, so every word of such an instruction is examined.
func preloadAssignments(dockerfile string) int {
	count := 0
	for _, instruction := range strings.Split(dockerfileInstructions(dockerfile), "\n") {
		fields := strings.Fields(instruction)
		if len(fields) == 0 {
			continue
		}
		if !strings.EqualFold(fields[0], "ENV") && !slices.Contains(fields, "export") {
			continue
		}
		for _, field := range fields {
			if field == runtimeGuardVariable || strings.HasPrefix(field, runtimeGuardVariable+"=") {
				count++
			}
		}
	}
	return count
}
