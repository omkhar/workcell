// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
)

const scopeGuardMaxPatchBytes = 16 << 20

const scopeGuardMaxProblems = 50

const scopeGuardNoNewline = "\\ No newline at end of file"

const scopeGuardDockerfilePath = "runtime/container/Dockerfile"

var (
	// Dockerfile lines may change only when they are provider version ARG
	// lines or the indented checksum assignments inside the provider RUN blocks.
	scopeGuardDockerfileLineRE = regexp.MustCompile(
		`^[-+](ARG (CLAUDE|CODEX|COPILOT)_VERSION=[A-Za-z0-9._+-]+|\s+(CLAUDE|CODEX|COPILOT)_(CODE_MODE_HOST_)?(SHA256)="[0-9a-f]{64}"; \\)$`)
	scopeGuardPathRE = regexp.MustCompile(
		`^(runtime/container/providers/package(-lock)?\.json|tests/fixtures/flags/[^/]+|tests/fixtures/codex-subcommands\.txt|runtime/container/control-plane-manifest\.json)$`)
	scopeGuardHeaderOnlyRE = regexp.MustCompile(
		`^(old mode|new mode|deleted file mode|rename |copy |similarity |dissimilarity )`)
	scopeGuardHunkRE = regexp.MustCompile(`^@@ -\d+(?:,(\d{1,6}))? \+\d+(?:,(\d{1,6}))? @@`)
)

// CheckUpstreamRefreshScope reads one git patch without following symlinks
// and returns an error that lists every change outside the agent-bump surface.
// The parser follows hunk line counts and fails closed on any line it does not
// recognize, so git apply cannot see a target that the guard did not check.
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
	var seen bool
	var oldHeaders, newHeaders, hunks, remOld, remNew int
	closeSection := func() {
		switch {
		case file == "":
		case remOld != 0 || remNew != 0:
			fail("%s: truncated hunk", file)
		case hunks == 0:
			fail("%s: incomplete patch section (no hunk)", file)
		}
	}
	lines := bufio.NewScanner(bytes.NewReader(data))
	lines.Split(scopeGuardSplitLF)
	for lines.Scan() {
		line := lines.Text()
		if len(problems) >= scopeGuardMaxProblems {
			problems = append(problems, "out of scope: further problems omitted")
			break
		}
		fields := strings.Fields(line)
		if remOld > 0 || remNew > 0 {
			switch {
			case line == scopeGuardNoNewline:
			case strings.HasPrefix(line, "-"):
				remOld--
			case strings.HasPrefix(line, "+"):
				remNew--
			case strings.HasPrefix(line, " "):
				remOld--
				remNew--
			default:
				fail("%s: unrecognized hunk line %q", file, line)
				remOld, remNew = 0, 0
				continue
			}
			if remOld < 0 || remNew < 0 {
				fail("%s: hunk longer than its header", file)
				remOld, remNew = 0, 0
			}
			if file == scopeGuardDockerfilePath && (strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")) && !scopeGuardDockerfileLineRE.MatchString(line) {
				fail("%s: line %s", file, line)
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			closeSection()
			file, oldHeaders, newHeaders, hunks = "", 0, 0, 0
			if len(fields) != 4 || !strings.HasPrefix(fields[2], "a/") || !strings.HasPrefix(fields[3], "b/") || fields[2][2:] != fields[3][2:] {
				fail("unsupported diff header (rename, copy, or unusual path): %s", line)
				continue
			}
			file, seen = fields[2][2:], true
			if file != scopeGuardDockerfilePath && !scopeGuardPathRE.MatchString(file) {
				fail("path %s", file)
			}
		case file == "":
			fail("line outside a checked diff section: %q", line)
		case scopeGuardHeaderOnlyRE.MatchString(line):
			fail("%s: %s", file, line)
		case strings.HasPrefix(line, "new file mode "):
			if len(fields) < 4 || fields[3] != "100644" {
				fail("%s: %s", file, line)
			}
		case strings.HasPrefix(line, "GIT binary patch") || strings.HasPrefix(line, "Binary files "):
			fail("%s: binary change", file)
		case strings.HasPrefix(line, "index "):
			if len(fields) == 3 && fields[2] != "100644" {
				fail("%s: %s", file, line)
			}
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			// git apply takes target paths from these headers, not from the diff line.
			want := "--- a/" + file
			if line[0] == '+' {
				want = "+++ b/" + file
				newHeaders++
			} else {
				oldHeaders++
			}
			if hunks > 0 {
				fail("%s: file header after a hunk: %s", file, line)
			} else if line != want && line != "--- /dev/null" {
				fail("%s: file header does not match the diff path: %s", file, line)
			}
		case strings.HasPrefix(line, "@@ "):
			m := scopeGuardHunkRE.FindStringSubmatch(line)
			if m == nil || oldHeaders != 1 || newHeaders != 1 {
				fail("%s: malformed hunk or not exactly one old and one new file header: %s", file, line)
				continue
			}
			remOld, remNew = scopeGuardHunkCount(m[1]), scopeGuardHunkCount(m[2])
			if remOld == 0 && remNew == 0 {
				fail("%s: zero-line hunk: %s", file, line)
				continue
			}
			hunks++
		case line == scopeGuardNoNewline && hunks > 0:
		default:
			fail("%s: unrecognized patch line %q", file, line)
		}
	}
	if err := lines.Err(); err != nil {
		fail("unreadable patch: %v", err)
	}
	if len(problems) < scopeGuardMaxProblems {
		closeSection()
	}
	if !seen {
		fail("empty patch")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func scopeGuardHunkCount(text string) int {
	if text == "" {
		return 1
	}
	n, _ := strconv.Atoi(text) // the regexp bounds text to six digits
	return n
}

// scopeGuardSplitLF splits on LF only. bufio.ScanLines would drop a trailing
// CR that git keeps as part of the line.
func scopeGuardSplitLF(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
