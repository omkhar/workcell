// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package gitconfigblocklist re-implements the git-config blocklist
// parity invariant that previously lived inline in
// scripts/verify-invariants.sh (the final check before the
// "Workcell invariant verification passed." banner).
//
// The invariant pins policy/git-config-blocklist.toml to its three
// enforcement points so that adding (or removing) a blocked git-config
// key or pattern requires editing the TOML and all three enforcers in a
// single PR:
//
//   - scripts/workcell                      (host launcher)
//   - runtime/container/bin/git             (in-container git wrapper)
//   - runtime/container/rust/src/gitpolicy.rs (LD_PRELOAD exec guard)
//
// The check reads the TOML `keys = [ ... ]` array and the
// `[[prefix_suffix_patterns]]` tables and asserts every key (as a fixed
// substring) and every prefix/suffix substring appears in each
// enforcer.  This is a behaviour-preserving migration of the shell
// block: the extraction, substring semantics (grep -Fq --), exit codes,
// and stderr messages match the original awk+grep implementation.
package gitconfigblocklist

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// tomlRelPath is the repo-relative path to the canonical blocklist.  It
// is emitted verbatim in failure messages (the shell hard-coded the
// same repo-relative string) even though the file is read via an
// absolute path derived from rootDir.
const tomlRelPath = "policy/git-config-blocklist.toml"

// enforcerRelPaths are the three enforcement points, in the same order
// the shell iterated them.  Failure messages name them by their
// absolute ${ROOT_DIR}/... path, matching the shell's ${enforcer}.
var enforcerRelPaths = []string{
	"scripts/workcell",
	"runtime/container/bin/git",
	"runtime/container/rust/src/gitpolicy.rs",
}

// Check runs the git-config blocklist parity invariant against the repo
// rooted at rootDir.  It returns nil when every key and pattern is
// present in every enforcer, and an error whose message matches the
// shell's stderr output otherwise.  A nil return corresponds to the
// shell's exit 0; a non-nil error corresponds to exit 1.
func Check(rootDir string) error {
	// The shell reads the TOML through awk in a process substitution; a
	// missing or unreadable file yields no awk output, i.e. an empty key
	// list, which surfaces below as the "no [keys] entries" failure.  We
	// preserve that behaviour by treating a read error as empty content.
	content, err := os.ReadFile(filepath.Join(rootDir, tomlRelPath))
	if err != nil {
		content = nil
	}
	text := string(content)

	keys := parseKeys(text)
	if len(keys) == 0 {
		return fmt.Errorf("%s had no [keys] entries; parity check needs at least one", tomlRelPath)
	}
	patterns := parsePrefixSuffixPatterns(text)

	type enforcer struct {
		path string
		body string
	}
	enforcers := make([]enforcer, 0, len(enforcerRelPaths))
	for _, rel := range enforcerRelPaths {
		path := filepath.Join(rootDir, rel)
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("git-config blocklist enforcer missing or unreadable: %s", path)
		}
		enforcers = append(enforcers, enforcer{path: path, body: string(body)})
		// The shell checks all keys for an enforcer immediately after its
		// readability check, before moving to the next enforcer.
		for _, key := range keys {
			if !strings.Contains(string(body), key) {
				return fmt.Errorf(
					"git-config blocklist key '%s' from %s is missing in %s\n"+
						"Add the same key to that enforcer or remove it from the TOML.",
					key, tomlRelPath, path)
			}
		}
	}

	for _, pat := range patterns {
		for _, e := range enforcers {
			if !strings.Contains(e.body, pat.prefix) || !strings.Contains(e.body, pat.suffix) {
				return fmt.Errorf(
					"git-config blocklist prefix+suffix pattern '%s*%s' missing in %s",
					pat.prefix, pat.suffix, e.path)
			}
		}
	}

	return nil
}

var (
	keysStartRe   = regexp.MustCompile(`^keys[[:space:]]*=[[:space:]]*\[`)
	keyLeadRe     = regexp.MustCompile(`^[[:space:]]*"`)
	keyTrailRe    = regexp.MustCompile(`",?[[:space:]]*$`)
	psStartRe     = regexp.MustCompile(`^\[\[prefix_suffix_patterns\]\]`)
	psPrefixRe    = regexp.MustCompile(`^prefix[[:space:]]*=[[:space:]]*"`)
	psSuffixRe    = regexp.MustCompile(`^suffix[[:space:]]*=[[:space:]]*"`)
	psTrailRe     = regexp.MustCompile(`"[[:space:]]*$`)
	doubleTableRe = regexp.MustCompile(`^\[\[`)
	blankLineRe   = regexp.MustCompile(`^[[:space:]]*$`)
)

// parseKeys extracts the `keys = [ ... ]` array entries, faithfully
// mirroring the shell's awk state machine: a line matching
// `^keys\s*=\s*\[` opens the array, a line beginning with `]` closes it,
// and each intervening line beginning (after optional whitespace) with a
// double-quote yields one key with its leading `\s*"` and trailing
// `",?\s*$` stripped.
func parseKeys(content string) []string {
	var keys []string
	inKeys := false
	for _, line := range strings.Split(content, "\n") {
		if keysStartRe.MatchString(line) {
			inKeys = true
			continue
		}
		if inKeys && strings.HasPrefix(line, "]") {
			inKeys = false
			continue
		}
		if inKeys && keyLeadRe.MatchString(line) {
			key := keyLeadRe.ReplaceAllString(line, "")
			key = keyTrailRe.ReplaceAllString(key, "")
			keys = append(keys, key)
		}
	}
	return keys
}

// prefixSuffixPattern is one extracted [[prefix_suffix_patterns]] entry.
type prefixSuffixPattern struct {
	prefix string
	suffix string
}

// parsePrefixSuffixPatterns extracts [[prefix_suffix_patterns]] blocks,
// mirroring the shell's awk state machine exactly: a
// `[[prefix_suffix_patterns]]` line opens a block and resets the pending
// prefix/suffix; `prefix = "..."` / `suffix = "..."` lines set them; and
// reaching any `[[` table header or a blank line flushes a complete
// (both non-empty) pair.  A trailing complete block at EOF is also
// flushed.  Only pairs where both prefix and suffix are non-empty are
// emitted.
func parsePrefixSuffixPatterns(content string) []prefixSuffixPattern {
	var out []prefixSuffixPattern
	inBlock := false
	prefix, suffix := "", ""
	for _, line := range strings.Split(content, "\n") {
		if psStartRe.MatchString(line) {
			inBlock = true
			prefix, suffix = "", ""
			continue
		}
		if inBlock && psPrefixRe.MatchString(line) {
			prefix = psTrailRe.ReplaceAllString(psPrefixRe.ReplaceAllString(line, ""), "")
			continue
		}
		if inBlock && psSuffixRe.MatchString(line) {
			suffix = psTrailRe.ReplaceAllString(psSuffixRe.ReplaceAllString(line, ""), "")
			continue
		}
		if inBlock && (doubleTableRe.MatchString(line) || blankLineRe.MatchString(line)) {
			if prefix != "" && suffix != "" {
				out = append(out, prefixSuffixPattern{prefix: prefix, suffix: suffix})
			}
			// A prefix_suffix header would have been consumed by the first
			// rule above (with its own reset); any other `[[` header closes
			// the block, matching the awk `in_block = (...) ? 1 : 0`.
			inBlock = psStartRe.MatchString(line)
			prefix, suffix = "", ""
		}
	}
	if inBlock && prefix != "" && suffix != "" {
		out = append(out, prefixSuffixPattern{prefix: prefix, suffix: suffix})
	}
	return out
}
