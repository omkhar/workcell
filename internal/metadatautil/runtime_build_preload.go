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
// Fragments match instructions only: a comment beside one changes nothing.
func validateRuntimeBuildPreload(dockerfile, path string) error {
	instructions := dockerfileInstructions(dockerfile)
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
		if _, _, err := requireRegex(instructions, "(?m)^"+regexp.QuoteMeta(block), "early runtime build preload", path); err != nil {
			return err
		}
	}
	if count := preloadAssignments(instructions); count != 3 {
		return fmt.Errorf("%s must keep exactly the three canonical early runtime build preload assignments, found %d", path, count)
	}
	return nil
}

// preloadAssignments counts the guard assignments that take effect, joining
// continuations first. An ENV persists every key it lists, including the legacy
// space-separated form; inside a RUN only an export-anchored assignment counts.
func preloadAssignments(instructions string) int {
	const guard = "LD_PRELOAD"
	count := 0
	for _, instruction := range strings.Split(strings.ReplaceAll(instructions, "\\\n", " "), "\n") {
		fields := strings.Fields(instruction)
		if len(fields) == 0 {
			continue
		}
		env := strings.EqualFold(fields[0], "ENV")
		exported := false
		for _, field := range fields[1:] {
			switch {
			case env && (field == guard || strings.HasPrefix(field, guard+"=")):
				count++
			case exported && strings.HasPrefix(field, guard+"="):
				count++
			}
			exported = field == "export" || (exported && strings.Contains(field, "="))
		}
	}
	return count
}
