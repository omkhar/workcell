// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
)

const scopeGuardMaxPatchBytes = 16 << 20

const scopeGuardMaxProblems = 50

const scopeGuardNoNewline = "\\ No newline at end of file"

const (
	scopeGuardDockerfilePath  = "runtime/container/Dockerfile"
	scopeGuardPackageJSONPath = "runtime/container/providers/package.json"
	scopeGuardCodexFixture    = "tests/fixtures/codex-subcommands.txt"
	scopeGuardLockfilePath    = "runtime/container/providers/package-lock.json"
)

const scopeGuardLockSemver = `[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.]+)?`

var (
	// Dockerfile lines may change only when they are provider version ARG
	// lines or the indented checksum assignments inside the provider RUN blocks.
	scopeGuardDockerfileLineRE = regexp.MustCompile(
		`^[-+](ARG (CLAUDE|CODEX|COPILOT)_VERSION=[A-Za-z0-9._+-]+|\s+(CLAUDE_SHA256|CODEX_SHA256|CODEX_CODE_MODE_HOST_SHA256|COPILOT_SHA256)="[0-9a-f]{64}"; \\)$`)
	// package.json may change only the pinned Gemini CLI version, because
	// npm ci installs every dependency that file declares.
	scopeGuardPackageJSONLineRE = regexp.MustCompile(`^[-+]\s+"@google/gemini-cli": "[A-Za-z0-9._+-]+",?$`)
	// The Codex fixture may change only its comment header, which carries the
	// version stamp and source tag. A bump that needs new subcommand tokens is
	// held for human review.
	scopeGuardCodexStampLineRE = regexp.MustCompile(`^[-+](# codex-version: |#.*openai/codex tag rust-v)[0-9]+\.[0-9]+\.[0-9]+(\D|$)`)
	scopeGuardSemverRE         = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)
	scopeGuardPathRE           = regexp.MustCompile(
		`^(runtime/container/providers/package(-lock)?\.json|tests/fixtures/codex-subcommands\.txt|runtime/container/control-plane-manifest\.json)$`)
	scopeGuardHeaderOnlyRE = regexp.MustCompile(
		`^(old mode|new mode|deleted file mode|rename |copy |similarity |dissimilarity )`)
	// The lockfile may change only the Gemini CLI entry (version, resolved,
	// integrity) and the root pin for it. Each changed block must follow the
	// exact context line that names its owner. Any other changed line,
	// including an added or removed package, breaks the block shape.
	scopeGuardLockEntryContext = " " + `    "node_modules/@google/gemini-cli": {`
	scopeGuardLockRootContext  = " " + `      "dependencies": {`
	scopeGuardLockVersionRE    = regexp.MustCompile(`^([-+])      "version": "(` + scopeGuardLockSemver + `)",$`)
	scopeGuardLockResolvedRE   = regexp.MustCompile(
		`^([-+])      "resolved": "https://registry\.npmjs\.org/@google/gemini-cli/-/gemini-cli-(` + scopeGuardLockSemver + `)\.tgz",$`)
	scopeGuardLockIntegrityRE = regexp.MustCompile(`^([-+])      "integrity": "sha512-[A-Za-z0-9+/]+=*",$`)
	scopeGuardLockRootDepRE   = regexp.MustCompile(`^([-+])        "@google/gemini-cli": "(` + scopeGuardLockSemver + `)"$`)
	scopeGuardHunkRE          = regexp.MustCompile(`^@@ -\d+(?:,(\d{1,6}))? \+\d+(?:,(\d{1,6}))? @@`)
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
	var seen, newFile, afterChange, hunkChanged bool
	var oldHeaders, newHeaders, hunks, remOld, remNew int
	var removedKeys, addedKeys, addedVersions []string
	var lockBlocks []scopeGuardLockBlock
	var changedBefore bool
	var prevLine string
	closeSection := func() {
		switch {
		case file == "":
		case remOld != 0 || remNew != 0:
			fail("%s: truncated hunk", file)
		case hunks == 0:
			fail("%s: incomplete patch section (no hunk)", file)
		case !hunkChanged:
			fail("%s: hunk without an added or removed line", file)
		case file == scopeGuardLockfilePath && scopeGuardLockProblem(lockBlocks) != "":
			fail("%s: %s", file, scopeGuardLockProblem(lockBlocks))
		case file == scopeGuardCodexFixture && (len(addedVersions) != 2 || addedVersions[0] != addedVersions[1]):
			fail("%s: the version stamp and source tag must both change to the same version", file)
		case (file == scopeGuardDockerfilePath || file == scopeGuardCodexFixture || file == scopeGuardPackageJSONPath) && !slices.Equal(removedKeys, addedKeys):
			// Docker and the shell use the last assignment, the updater reads the first.
			fail("%s: provider assignments must be replaced one for one, not added, removed, or reordered", file)
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
				// git rejects a marker that does not directly follow a changed line.
				if !afterChange {
					fail("%s: misplaced no-newline marker", file)
				}
				afterChange = false
				prevLine = line
				continue
			case strings.HasPrefix(line, "-"):
				remOld--
				changedBefore, afterChange, hunkChanged = afterChange, true, true
			case strings.HasPrefix(line, "+"):
				remNew--
				changedBefore, afterChange, hunkChanged = afterChange, true, true
			case strings.HasPrefix(line, " "):
				remOld--
				remNew--
				afterChange = false
			default:
				fail("%s: unrecognized hunk line %q", file, line)
				remOld, remNew = 0, 0
				continue
			}
			if remOld < 0 || remNew < 0 {
				fail("%s: hunk longer than its header", file)
				remOld, remNew = 0, 0
			}
			if file == scopeGuardLockfilePath && (line[0] == '-' || line[0] == '+') {
				if !changedBefore {
					lockBlocks = append(lockBlocks, scopeGuardLockBlock{context: prevLine})
				}
				lockBlocks[len(lockBlocks)-1].lines = append(lockBlocks[len(lockBlocks)-1].lines, line)
			}
			prevLine = line
			if (strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")) &&
				(file == scopeGuardDockerfilePath && !scopeGuardDockerfileLineRE.MatchString(line) ||
					file == scopeGuardPackageJSONPath && !scopeGuardPackageJSONLineRE.MatchString(line) ||
					file == scopeGuardCodexFixture && !scopeGuardCodexStampLineRE.MatchString(line)) {
				fail("%s: line %s", file, line)
			}
			if file == scopeGuardDockerfilePath || file == scopeGuardCodexFixture || file == scopeGuardPackageJSONPath {
				key, _, _ := strings.Cut(strings.TrimSpace(line[1:]), "=")
				if file == scopeGuardPackageJSONPath {
					key = "gemini pin" // the line regexp admits only that pin
				}
				if file == scopeGuardCodexFixture {
					key = scopeGuardSemverRE.ReplaceAllString(line[1:], "V")
					if line[0] == '+' {
						addedVersions = append(addedVersions, scopeGuardSemverRE.FindString(line))
					}
				}
				if line[0] == '-' {
					removedKeys = append(removedKeys, key)
				} else if line[0] == '+' {
					addedKeys = append(addedKeys, key)
				}
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			closeSection()
			file, newFile, hunkChanged, oldHeaders, newHeaders, hunks = "", false, false, 0, 0, 0
			removedKeys, addedKeys, addedVersions, lockBlocks, prevLine = nil, nil, nil, nil, ""
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
			if newFile || oldHeaders > 0 || newHeaders > 0 || hunks > 0 {
				fail("%s: new file mode must be the first and only mode line, before the file headers: %s", file, line)
			}
			newFile = true
			if len(fields) < 4 || fields[3] != "100644" {
				fail("%s: %s", file, line)
			}
		case strings.HasPrefix(line, "GIT binary patch") || strings.HasPrefix(line, "Binary files "):
			fail("%s: binary change", file)
		case strings.HasPrefix(line, "index "):
			if len(fields) > 3 || len(fields) < 2 || len(fields) == 3 && fields[2] != "100644" {
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
				if newFile {
					want = "--- /dev/null"
				}
			}
			if hunks > 0 {
				fail("%s: file header after a hunk: %s", file, line)
			} else if line != want {
				fail("%s: file header does not match the diff path: %s", file, line)
			}
		case strings.HasPrefix(line, "@@ "):
			m := scopeGuardHunkRE.FindStringSubmatch(line)
			if m == nil || oldHeaders != 1 || newHeaders != 1 {
				fail("%s: malformed hunk or not exactly one old and one new file header: %s", file, line)
				continue
			}
			if hunks > 0 && !hunkChanged {
				fail("%s: hunk without an added or removed line", file)
			}
			afterChange, hunkChanged, prevLine = false, false, ""
			remOld, remNew = scopeGuardHunkCount(m[1]), scopeGuardHunkCount(m[2])
			if newFile && remOld != 0 {
				fail("%s: new file hunk must have no old lines: %s", file, line)
				remOld, remNew = 0, 0
				continue
			}
			if remOld == 0 && remNew == 0 {
				fail("%s: zero-line hunk: %s", file, line)
				continue
			}
			hunks++
		case line == scopeGuardNoNewline && hunks > 0:
			if !afterChange {
				fail("%s: misplaced no-newline marker", file)
			}
			afterChange = false
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

// scopeGuardLockBlock is one run of changed lines and the line before it.
type scopeGuardLockBlock struct {
	context string
	lines   []string
}

// scopeGuardLockProblem returns "" when the changed blocks are exactly the
// Gemini CLI root pin and the Gemini CLI lockfile entry for one version bump.
func scopeGuardLockProblem(blocks []scopeGuardLockBlock) string {
	const shape = "only the Gemini CLI version, resolved, and integrity lines and the root pin may change"
	if len(blocks) != 2 || blocks[0].context != scopeGuardLockRootContext || blocks[1].context != scopeGuardLockEntryContext ||
		len(blocks[0].lines) != 2 || len(blocks[1].lines) != 6 {
		return shape
	}
	root, entry := blocks[0].lines, blocks[1].lines
	// git prints the removed lines first, then the added lines.
	var oldVer, newVer string
	for i, re := range []*regexp.Regexp{scopeGuardLockRootDepRE, scopeGuardLockRootDepRE} {
		m := re.FindStringSubmatch(root[i])
		if m == nil || m[1] != string("-+"[i]) {
			return shape
		}
		if i == 0 {
			oldVer = m[2]
		} else {
			newVer = m[2]
		}
	}
	for i, line := range entry {
		sign := string("-+"[i/3])
		ver := oldVer
		if i >= 3 {
			ver = newVer
		}
		var m []string
		switch i % 3 {
		case 0:
			m = scopeGuardLockVersionRE.FindStringSubmatch(line)
		case 1:
			m = scopeGuardLockResolvedRE.FindStringSubmatch(line)
		default:
			m = scopeGuardLockIntegrityRE.FindStringSubmatch(line)
			if m != nil {
				m = append(m, ver)
			}
		}
		if m == nil || m[1] != sign || m[2] != ver {
			return shape
		}
	}
	return ""
}

func scopeGuardHunkCount(text string) int {
	if text == "" {
		return 1
	}
	n, _ := strconv.Atoi(text) // the regexp bounds text to six digits
	return n
}

// scopeGuardSplitLF splits on LF only. bufio.ScanLines would drop a trailing
// CR that git keeps as part of the line. git rejects an unterminated last
// line, so the splitter does too.
func scopeGuardSplitLF(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return 0, nil, errors.New("final patch line has no newline")
	}
	return 0, nil, nil
}
