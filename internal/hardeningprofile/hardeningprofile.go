// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package hardeningprofile is the deterministic conformance check behind
// roadmap item A6 (documented syscall/filesystem hardening profile). It reads
// the reviewed policy/hardening-profile.toml and asserts every `required`
// literal is present in its section's `target` and every `forbidden` literal is
// absent, so a drift weakening the container posture (dropping --cap-drop ALL,
// adding --privileged) or silently changing the egress inventory FAILS CI.
//
// Unlike internal/workcellhardening (which hardcodes launcher literals in Go),
// A6 keeps the EXPECTED posture in a reviewed artifact and enforces
// artifact-vs-source conformance (as metadatautil does for
// github-hosted-controls.toml). It parses the TOML via internal/tomlsubset and
// iterates tables in source order, so the first-violation message is
// deterministic and diffs one-to-one against the artifact.
//
// Matching (literalPresent) is verbatim substring by default, with refinements:
//   - Comment stripping (stripBashComments): the file is scanned as CODE, not
//     comments, so the launcher's notice comment naming the --cap-add block
//     cannot satisfy the cap-add literals; applied to required and forbidden.
//   - Block scoping: a section may name a `block` (bash function); its literals
//     match only within that function (extractFunctionBlock), so a host removed
//     from one function is not masked by another. No block ⇒ whole-file scan.
//   - Capability / --security-opt normalization (capMatches, securityOptMatches):
//     equivalent Docker spellings match the same (= or whitespace separator,
//     optional CAP_ prefix, quoted array form, any case), so a forbidden form
//     cannot evade and a required form does not false-fail.
//   - Endpoint boundaries (endpointPresent): a host:port matches on host
//     boundaries, so github.com:443 is not satisfied by api.github.com:443.
//   - Exact inventory (exact_endpoints): the declared set must EQUAL the set the
//     block emits — required gives declared ⊆ source, emittedEndpoints gives
//     source ⊆ declared — baking the comm cross-check into CI.
//
// A missing/unreadable target is empty content: required literals fail,
// forbidden pass, as a fixed-string grep on a missing file would.
package hardeningprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

// profileRelPath is the repo-relative path to the reviewed hardening-profile
// policy artifact this check enforces.
const profileRelPath = "policy/hardening-profile.toml"

// Check runs the hardening-profile conformance check against the repo rooted at
// rootDir. It returns nil when every section's required literals are present and
// forbidden literals are absent in the section's target file, or an error whose
// message describes the first violated section/literal.
//
// A missing or malformed policy/hardening-profile.toml is itself a violation:
// the check cannot certify a posture it cannot read.
func Check(rootDir string) error {
	content, err := os.ReadFile(filepath.Join(rootDir, profileRelPath))
	if err != nil {
		return fmt.Errorf("hardening-profile: cannot read %s: %w", profileRelPath, err)
	}
	doc, err := tomlsubset.ParseDocument(string(content), profileRelPath)
	if err != nil {
		return fmt.Errorf("hardening-profile: cannot parse %s: %w", profileRelPath, err)
	}

	if version := doc.TopLevel.Lookup("version"); version == nil || version.Value != 1 {
		return fmt.Errorf("hardening-profile: %s must declare version = 1", profileRelPath)
	}
	if len(doc.Tables) == 0 {
		return fmt.Errorf("hardening-profile: %s declares no hardening sections", profileRelPath)
	}

	cache := make(map[string]string)
	readTarget := func(rel string) string {
		if text, ok := cache[rel]; ok {
			return text
		}
		body, readErr := os.ReadFile(filepath.Join(rootDir, rel))
		if readErr != nil {
			body = nil
		}
		text := stripBashComments(string(body))
		cache[rel] = text
		return text
	}

	for i := range doc.Tables {
		table := &doc.Tables[i]
		target, err := stringField(table, "target")
		if err != nil {
			return err
		}
		if target == "" {
			return fmt.Errorf("hardening-profile: section [%s] must declare a non-empty target", table.Name)
		}
		block, err := stringField(table, "block")
		if err != nil {
			return err
		}
		exact, err := boolField(table, "exact_endpoints")
		if err != nil {
			return err
		}
		required, err := stringArrayField(table, "required")
		if err != nil {
			return err
		}
		forbidden, err := stringArrayField(table, "forbidden")
		if err != nil {
			return err
		}
		if len(required) == 0 && len(forbidden) == 0 {
			return fmt.Errorf("hardening-profile: section [%s] declares neither required nor forbidden literals", table.Name)
		}

		// Scope the scan: a section's `block` extracts one bash function's body,
		// so a literal is satisfied only within its intended block. No block ⇒
		// whole comment-stripped file (the top-level docker-run args).
		body := readTarget(target)
		if block != "" {
			body = extractFunctionBlock(body, block)
		}
		for _, literal := range required {
			if !literalPresent(body, literal) {
				return errors.New(missingRequiredMessage(table.Name, target, block, literal))
			}
		}
		for _, literal := range forbidden {
			if literalPresent(body, literal) {
				return errors.New(presentForbiddenMessage(table.Name, target, block, literal))
			}
		}

		// Exact endpoint inventory: the required loop gave declared ⊆ source;
		// enforce source ⊆ declared so an endpoint the block emits but the
		// artifact never declared fails. Together the section EQUALS the block.
		if exact {
			if block == "" {
				return fmt.Errorf("hardening-profile: section [%s] sets exact_endpoints but declares no block", table.Name)
			}
			declared := make(map[string]bool, len(required))
			for _, e := range required {
				declared[e] = true
			}
			for _, emitted := range emittedEndpoints(body) {
				if !declared[emitted] {
					return errors.New(undeclaredEndpointMessage(table.Name, target, block, emitted))
				}
			}
		}
	}
	return nil
}

// emittedEndpointRe matches a bare host:port token (hostname with a dot, then
// :port) as emitted inside the `*_endpoints()` bash functions.
var emittedEndpointRe = regexp.MustCompile(`[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+:[0-9]+`)

// emittedEndpoints returns the sorted, de-duplicated host:port set a
// (comment-stripped, block-scoped) function body emits; sorting keeps the first
// violation deterministic.
func emittedEndpoints(blockBody string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, tok := range emittedEndpointRe.FindAllString(blockBody, -1) {
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	sort.Strings(out)
	return out
}

// undeclaredEndpointMessage is the violation message when a block emits an
// endpoint the artifact never declared (the inventory fell behind the source).
func undeclaredEndpointMessage(section, target, block, endpoint string) string {
	return fmt.Sprintf(
		"hardening-profile: %s emits endpoint %q not declared in %s [%s]",
		scopeLabel(target, block), endpoint, profileRelPath, section,
	)
}

// literalPresent reports whether literal is present in body: cap-add/cap-drop
// via capMatches, --security-opt via securityOptMatches, host:port via
// endpointPresent (host-boundary), everything else a verbatim substring.
func literalPresent(body, literal string) bool {
	if verb, cap, ok := parseCapLiteral(literal); ok {
		return capMatches(body, verb, cap)
	}
	if value, ok := parseSecurityOptLiteral(literal); ok {
		return securityOptMatches(body, value)
	}
	if endpointLiteralRe.MatchString(literal) {
		return endpointPresent(body, literal)
	}
	return strings.Contains(body, literal)
}

// endpointLiteralRe recognises a bare host:port endpoint literal.
var endpointLiteralRe = regexp.MustCompile(`^[A-Za-z0-9.-]+:[0-9]+$`)

// endpointPresent reports whether endpoint appears as a complete host — not
// preceded by a host char (letter/digit/dot/hyphen) nor followed by a digit —
// so github.com:443 is not masked by api.github.com:443.
func endpointPresent(body, endpoint string) bool {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9.-])` + regexp.QuoteMeta(endpoint) + `([^0-9]|$)`)
	return re.MatchString(body)
}

// scopeLabel renders the guarded source location for violation messages:
// "target block name()" when scoped, else just the target.
func scopeLabel(target, block string) string {
	if block != "" {
		return fmt.Sprintf("%s block %s()", target, block)
	}
	return target
}

// missingRequiredMessage is the violation message when a required posture
// literal is absent from its scoped source (a weakening/removal drift).
func missingRequiredMessage(section, target, block, literal string) string {
	return fmt.Sprintf(
		"hardening-profile: %s is missing required posture literal %q declared in %s [%s]",
		scopeLabel(target, block), literal, profileRelPath, section,
	)
}

// presentForbiddenMessage is the violation message when a forbidden literal is
// present in its scoped source (an unconfined/privileged drift).
func presentForbiddenMessage(section, target, block, literal string) string {
	return fmt.Sprintf(
		"hardening-profile: %s contains forbidden posture literal %q declared in %s [%s]",
		scopeLabel(target, block), literal, profileRelPath, section,
	)
}

// extractFunctionBlock returns the body of the top-level bash function name —
// from the `name()` header through the next line beginning with `}` — or "" if
// absent. Mirrors internal/workcellhardening.extractNamedFunctionBlock.
func extractFunctionBlock(text, name string) string {
	openPrefix := name + "()"
	var out []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		if !inBlock {
			if strings.HasPrefix(line, openPrefix) {
				inBlock = true
				out = append(out, line)
			}
			continue
		}
		out = append(out, line)
		if strings.HasPrefix(line, "}") {
			break
		}
	}
	return strings.Join(out, "\n")
}

// capLiteralRe recognises a `--cap-add`/`--cap-drop` literal (= or whitespace
// separator). Groups: 1=verb, 2=capability name (CAP_-prefixed or not, any case).
var capLiteralRe = regexp.MustCompile(`^--cap-(add|drop)[ =]+([A-Za-z0-9_]+)$`)

// parseCapLiteral returns the verb and canonical capability name (upper-case,
// CAP_ stripped) if literal is a capability literal, so cap_sys_admin and
// CAP_SYS_ADMIN both canonicalize to SYS_ADMIN.
func parseCapLiteral(literal string) (verb, cap string, ok bool) {
	m := capLiteralRe.FindStringSubmatch(literal)
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimPrefix(strings.ToUpper(m[2]), "CAP_"), true
}

// securityOptLiteralRe recognises a `--security-opt <value>` literal in the
// policy artifact (separator `=` or whitespace). Capture group 1 is the option
// value (e.g. seccomp=unconfined, no-new-privileges:true).
var securityOptLiteralRe = regexp.MustCompile(`^--security-opt[ =]+(.+)$`)

// parseSecurityOptLiteral reports whether literal is a `--security-opt` literal
// and, if so, returns its option value with any surrounding quotes stripped.
func parseSecurityOptLiteral(literal string) (value string, ok bool) {
	m := securityOptLiteralRe.FindStringSubmatch(literal)
	if m == nil {
		return "", false
	}
	return strings.Trim(m[1], `"'`), true
}

// securityOptMatches reports whether body applies `--security-opt <value>` in any
// equivalent spelling: `=` or whitespace separator (whitespace covers the
// bash-array form of flag + quoted value), optional quote, case-insensitive — so
// `--security-opt=label=disable` / `--security-opt "label=disable"` cannot evade
// and an equivalent required form still satisfies.
func securityOptMatches(body, value string) bool {
	re := regexp.MustCompile(`(?i)--security-opt[\s=]+["']?` + regexp.QuoteMeta(value) + `\b`)
	return re.MatchString(body)
}

// capMatches reports whether body grants/drops capability cap via `--cap-<verb>`
// in any equivalent spelling (= or whitespace separator, optional CAP_ prefix,
// optional quote, any case), closing the forbidden-evasion and required-false-fail
// gaps a substring match would leave. cap must be canonical (upper, no CAP_).
func capMatches(body, verb, cap string) bool {
	re := regexp.MustCompile(`(?i)--cap-` + verb + `[ =]+["']?(?:CAP_)?` + regexp.QuoteMeta(cap) + `\b`)
	return re.MatchString(body)
}

// stripBashComments removes shell comments so the scan matches code, not
// commentary (per line via stripLineComment). Quote state resets each line — a
// safe limitation here since no declared literal contains '#'.
func stripBashComments(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = stripLineComment(line)
	}
	return strings.Join(lines, "\n")
}

// stripLineComment returns line with any shell comment removed, honouring
// single/double quoting and backslash escapes so a '#' inside a string or after
// a backslash is not treated as a comment.
func stripLineComment(line string) string {
	var inSingle, inDouble, escaped bool
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case c == '\\' && inDouble:
			escaped = true
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '#' && !inSingle && !inDouble:
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				return line[:i]
			}
		}
	}
	return line
}

// stringField returns the string value of key in table, "" if the key is
// absent, or an error if the value is present but not a string.
func stringField(table *tomlsubset.Table, key string) (string, error) {
	pair := table.Lookup(key)
	if pair == nil {
		return "", nil
	}
	value, ok := pair.Value.(string)
	if !ok {
		return "", fmt.Errorf("hardening-profile: section [%s] key %q must be a string", table.Name, key)
	}
	return value, nil
}

// boolField returns the bool value of key in table, false if the key is absent,
// or an error if the value is present but not a boolean.
func boolField(table *tomlsubset.Table, key string) (bool, error) {
	pair := table.Lookup(key)
	if pair == nil {
		return false, nil
	}
	value, ok := pair.Value.(bool)
	if !ok {
		return false, fmt.Errorf("hardening-profile: section [%s] key %q must be a boolean", table.Name, key)
	}
	return value, nil
}

// stringArrayField returns the []string value of key in table, nil if the key
// is absent, or an error if the value is not an array of strings.
func stringArrayField(table *tomlsubset.Table, key string) ([]string, error) {
	pair := table.Lookup(key)
	if pair == nil {
		return nil, nil
	}
	items, ok := pair.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("hardening-profile: section [%s] key %q must be an array", table.Name, key)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("hardening-profile: section [%s] key %q must contain only strings", table.Name, key)
		}
		out = append(out, text)
	}
	return out, nil
}
