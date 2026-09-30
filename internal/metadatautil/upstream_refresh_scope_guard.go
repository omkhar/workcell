// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
)

const scopeGuardMaxPatchBytes = 16 << 20

const scopeGuardDockerfilePath = "runtime/container/Dockerfile"

var (
	// Dockerfile lines may change only when they are provider version ARG
	// lines or the indented checksum assignments inside the provider RUN blocks.
	scopeGuardDockerfileLineRE = regexp.MustCompile(
		`^[-+](ARG (CLAUDE|CODEX|COPILOT|GEMINI)_[A-Z0-9_]*(VERSION|SHA256)=[A-Za-z0-9._+:@/-]*|\s+(CLAUDE|CODEX|COPILOT)_(CODE_MODE_HOST_)?(SHA256)="[0-9a-f]{64}"; \\)$`)
	scopeGuardPathRE = regexp.MustCompile(
		`^(runtime/container/providers[^/]*/package(-lock)?\.json|tests/fixtures/flags/[^/]+|tests/fixtures/codex-subcommands\.txt|runtime/container/control-plane-manifest\.json)$`)
	scopeGuardHeaderOnlyRE = regexp.MustCompile(
		`^(old mode|new mode|deleted file mode|rename |copy |similarity |dissimilarity )`)
)

// CheckUpstreamRefreshScope reads one git patch without following symlinks
// and returns an error that lists every change outside the agent-bump surface.
// A rejected patch is still a valid PR. A human must review it.
func CheckUpstreamRefreshScope(patchPath string) error {
	data, err := rootio.ReadFileNoFollow(patchPath, "upstream-refresh patch", scopeGuardMaxPatchBytes)
	if err != nil {
		return err
	}
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, "out of scope: "+fmt.Sprintf(format, args...))
	}
	var file string
	var seen, inHunk bool
	var oldHeaders, newHeaders int
	// A section without a hunk is truncated or empty. Fail closed.
	closeSection := func() {
		if file != "" && !inHunk {
			fail("%s: incomplete patch section (no hunk)", file)
		}
	}
	for _, line := range strings.Split(string(bytes.TrimSuffix(data, []byte("\n"))), "\n") {
		fields := strings.Fields(line)
		switch {
		case strings.HasPrefix(line, "diff --git "):
			closeSection()
			inHunk, file, oldHeaders, newHeaders = false, "", 0, 0
			if len(fields) != 4 || !strings.HasPrefix(fields[2], "a/") || !strings.HasPrefix(fields[3], "b/") || fields[2][2:] != fields[3][2:] {
				fail("unsupported diff header (rename, copy, or unusual path): %s", line)
				continue
			}
			file, seen = fields[2][2:], true
			if file != scopeGuardDockerfilePath && !scopeGuardPathRE.MatchString(file) {
				fail("path %s", file)
			}
		case !inHunk && scopeGuardHeaderOnlyRE.MatchString(line):
			fail("%s: %s", file, line)
		case !inHunk && strings.HasPrefix(line, "new file mode "):
			if len(fields) < 4 || fields[3] != "100644" {
				fail("%s: %s", file, line)
			}
		case !inHunk && (strings.HasPrefix(line, "GIT binary patch") || strings.HasPrefix(line, "Binary files ")):
			fail("%s: binary change", file)
		case !inHunk && strings.HasPrefix(line, "index "):
			if len(fields) == 3 && fields[2] != "100644" {
				fail("%s: %s", file, line)
			}
		case !inHunk && strings.HasPrefix(line, "--- "):
			// git apply takes target paths from these headers, not from the diff line.
			oldHeaders++
			if line != "--- a/"+file && line != "--- /dev/null" {
				fail("%s: file header does not match the diff path: %s", file, line)
			}
		case !inHunk && strings.HasPrefix(line, "+++ "):
			newHeaders++
			if line != "+++ b/"+file {
				fail("%s: file header does not match the diff path: %s", file, line)
			}
		case strings.HasPrefix(line, "@@ "):
			if !inHunk && (oldHeaders != 1 || newHeaders != 1) {
				fail("%s: need exactly one old and one new file header before the first hunk", file)
			}
			inHunk = true
		case inHunk && file == scopeGuardDockerfilePath && (strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")) && !scopeGuardDockerfileLineRE.MatchString(line):
			fail("%s: line %s", file, line)
		}
	}
	closeSection()
	if !seen {
		fail("empty patch")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}
