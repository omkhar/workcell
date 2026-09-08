// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// CheckShellPortability rejects a GNU-only or Bash-4-only construct in a
// tracked shell script.
//
// The host baseline is macOS: /bin/bash 3.2 and a BSD userland. A script that
// uses mapfile, declare -A, sha256sum, stat -c or readlink -f runs on the
// Linux validator image and fails on the operator host, and the failure only
// appears where no CI lane looks. scripts/check-doc-links.sh already states
// the contract in a header comment; nothing enforced it.
//
// The runtime tree is out of scope: it only runs inside the image. A script
// outside it that still only runs there declares that once:
//
//	# portability-exempt: linux-only (reason)
//
// A single line that the reviewer accepted carries the same tag at its end.
func CheckShellPortability(rootDir string) error {
	files, err := trackedShellScripts(rootDir)
	if err != nil {
		return err
	}
	var failures []string
	for _, rel := range files {
		if strings.HasPrefix(rel, runtimeTreePrefix) {
			// The runtime tree only ever runs inside the Debian image, where
			// bash 5 and the GNU userland are present. Its scripts are also
			// digest-bound by runtime/container/control-plane-manifest.json,
			// so a comment added to one of them is a manifest change.
			continue
		}
		content, err := os.ReadFile(filepath.Join(rootDir, rel))
		if err != nil {
			return err
		}
		failures = append(failures, ShellPortabilityFindings(string(content), rel)...)
	}
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return fmt.Errorf("shell portability check failed (%d finding(s)); the host baseline is macOS bash 3.2 with a BSD userland:\n  %s",
		len(failures), strings.Join(failures, "\n  "))
}

const (
	portabilityExemptTag = "# portability-exempt:"

	// runtimeTreePrefix holds the scripts that run inside the Linux image.
	runtimeTreePrefix = "runtime/"
)

// portabilityRule is one construct the host baseline does not carry.
type portabilityRule struct {
	name        string
	pattern     *regexp.Regexp
	replacement string
	// bashVersion marks a rule that only the shell version decides. A script
	// that re-executes itself under bash 4 has already proved the feature is
	// present, so the rule does not apply to it.
	bashVersion bool
}

// modernBashAssertion is the helper a script calls to re-execute itself under
// bash 4 or newer. scripts/lib/canonical-build-env.sh defines it.
const modernBashAssertion = "workcell_require_modern_privileged_bash"

// heredocDelimiter returns the word that ends the heredoc a line opens, or the
// empty string when the line opens none. Quotes around the word are part of
// the redirection, not of the delimiter.
func heredocDelimiter(line string) string {
	match := heredocOpener.FindStringSubmatch(line)
	if match == nil {
		return ""
	}
	return strings.Trim(match[1], `"\'`+"`")
}

var heredocOpener = regexp.MustCompile("<<-?[ \t]*([\"'`]?[A-Za-z_][A-Za-z0-9_]*[\"'`]?)")

// portabilityRules holds one row per defect the reviewer found, plus the
// constructs of the same two families that the tree does not carry yet.
var portabilityRules = []portabilityRule{
	{bashVersion: true, name: "mapfile", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*mapfile\b`), replacement: "bash 4; read the lines in a while loop"},
	{bashVersion: true, name: "readarray", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*readarray\b`), replacement: "bash 4; read the lines in a while loop"},
	{bashVersion: true, name: "wait -n", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*wait\s+-n\b`), replacement: "bash 5; wait for each recorded process identifier"},
	{bashVersion: true, name: "declare -A", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*(declare|local|typeset)\s+-A\b`), replacement: "bash 4; use a temporary file or parallel arrays"},
	{bashVersion: true, name: "case conversion expansion", pattern: regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*(\[[^]]*\])?(\^\^|,,)`), replacement: "bash 4; use tr"},
	{name: "sha256sum", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*sha256sum\b`), replacement: "GNU coreutils; use shasum -a 256"},
	{name: "stat -c", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*stat\s+(-[A-Za-z]+\s+)*-c\b`), replacement: "GNU; BSD stat uses -f"},
	{name: "date -d", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*date\s+(-[A-Za-z]+\s+)*-d\b`), replacement: "GNU; BSD date uses -v or -j -f"},
	{name: "grep -P", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*grep\s+(-[A-Za-z]+\s+)*-P\b`), replacement: "GNU; use grep -E"},
	{name: "find -printf", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*find\b[^\n]*\s-printf\b`), replacement: "GNU; use -print0 and read the names"},
	{name: "sed -i without a suffix", pattern: regexp.MustCompile(`(^|[;&|(]|\$\()\s*sed\s+(-[A-Za-z]+\s+)*-i\s`), replacement: "BSD sed requires a backup suffix, such as -i.bak"},
}

// ShellPortabilityFindings reports one finding per banned construct in script.
//
// It skips a comment line, a heredoc body, a line or a file that declares the
// exemption tag, and every bash-4 rule in a script that first re-executes
// itself under a modern bash. A heredoc body is text this script hands to
// another interpreter, most often bash 5 inside the Linux image, so its
// contents are not commands this script runs on the host.
func ShellPortabilityFindings(script, name string) []string {
	if fileExemptsPortability(script) {
		return nil
	}
	modernBash := strings.Contains(script, modernBashAssertion)
	var findings []string
	number := 0
	pending := ""
	for line := range strings.Lines(script) {
		number++
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if pending != "" {
			if strings.TrimSpace(text) == pending {
				pending = ""
			}
			continue
		}
		trimmed := strings.TrimSpace(text)
		if delimiter := heredocDelimiter(text); delimiter != "" {
			pending = delimiter
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(text, portabilityExemptTag) {
			continue
		}
		for _, rule := range portabilityRules {
			if rule.bashVersion && modernBash {
				continue
			}
			if !rule.pattern.MatchString(text) {
				continue
			}
			findings = append(findings, fmt.Sprintf("%s:%d: %s is not on the host baseline (%s)",
				name, number, rule.name, rule.replacement))
		}
	}
	return findings
}

// fileExemptsPortability reports a script that declares it only runs inside
// the Linux image. The declaration has to sit in the header, so a mention
// deeper in the script cannot exempt the whole file.
func fileExemptsPortability(script string) bool {
	lines := strings.Split(script, "\n")
	if len(lines) > portabilityHeaderLines {
		lines = lines[:portabilityHeaderLines]
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), portabilityExemptTag+" linux-only") {
			return true
		}
	}
	return false
}

const portabilityHeaderLines = 20

func trackedShellScripts(rootDir string) ([]string, error) {
	command := exec.Command("git", "-C", rootDir, "ls-files", "-z", "*.sh")
	listing, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked shell scripts: %w", err)
	}
	var files []string
	for _, path := range strings.Split(string(listing), "\x00") {
		if path != "" {
			files = append(files, path)
		}
	}
	if len(files) == 0 {
		return nil, errors.New("tracked shell script listing is empty; refusing a vacuous pass")
	}
	return files, nil
}
