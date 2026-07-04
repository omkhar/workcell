// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// exitCodeSourceFiles are the repo-relative files searched for a literal,
// word-bounded occurrence of each policy/public-contract.toml exit code.
// Mirrors the search set the G1 audit identified: the shell entrypoint, the
// four Go CLI mains (which all carry cliexit.ExitCodeError{Code: N} and/or
// die()/dieUsage() calls), and the Rust launcher's errno-to-exit-code map.
var exitCodeSourceFiles = []string{
	filepath.Join("scripts", "workcell"),
	filepath.Join("cmd", "workcell-citools", "main.go"),
	filepath.Join("cmd", "workcell-colimautil", "main.go"),
	filepath.Join("cmd", "workcell-hostutil", "main.go"),
	filepath.Join("cmd", "workcell-runtimeutil", "main.go"),
	filepath.Join("runtime", "container", "rust", "src", "bin", "common", "launcher_common.rs"),
}

type publicContract struct {
	ExitCodes           []string
	OutputLinePrefixes  []string
	SessionRecordFields []string
	SessionExportFields []string
	InjectionTables     []string
}

// CheckPublicContract validates that contractPath (policy/public-contract.toml)
// matches the actual public surface embedded in the Workcell source tree
// rooted at rootDir: the process exit-code convention, the stable
// machine-readable output-line prefixes, the durable SessionRecord /
// SessionExport JSON field sets, and the injection-policy table whitelist.
//
// Every assertion is a deterministic file read plus string/regex matching —
// no Docker, no network, no subprocess execution. Mirrors
// ValidateOperatorContract's fail-closed style: an unreadable contract or
// source file, or any mismatch between the documented and actual surface,
// returns a non-nil error naming the file and the mismatch.
//
// CLI flag/help-text drift is already covered by ValidateOperatorContract;
// this check deliberately does not duplicate that (see the G1 spec).
func CheckPublicContract(rootDir, contractPath string) error {
	contract, err := loadPublicContract(contractPath)
	if err != nil {
		return err
	}

	if err := checkExitCodes(rootDir, contractPath, contract.ExitCodes); err != nil {
		return err
	}
	if err := checkOutputLinePrefixes(rootDir, contractPath, contract.OutputLinePrefixes); err != nil {
		return err
	}
	if err := checkSessionRecordFields(rootDir, contractPath, contract.SessionRecordFields, contract.SessionExportFields); err != nil {
		return err
	}
	if err := checkInjectionTables(rootDir, contractPath, contract.InjectionTables); err != nil {
		return err
	}
	return nil
}

func loadPublicContract(contractPath string) (publicContract, error) {
	text, err := readText(contractPath)
	if err != nil {
		return publicContract{}, err
	}
	document, err := ParseTOMLSubset(text, contractPath)
	if err != nil {
		return publicContract{}, err
	}

	meta, ok := document["meta"].(map[string]any)
	if !ok {
		return publicContract{}, fmt.Errorf("%s must define a [meta] table", contractPath)
	}
	version, ok := MustString(meta["version"])
	if !ok || version != "1" {
		return publicContract{}, fmt.Errorf(`%s must set [meta] version = "1"`, contractPath)
	}

	exitCodes, err := requiredStringSliceTable(document, contractPath, "exit_codes", "codes")
	if err != nil {
		return publicContract{}, err
	}
	outputLinePrefixes, err := requiredStringSliceTable(document, contractPath, "output_lines", "prefixes")
	if err != nil {
		return publicContract{}, err
	}
	sessionRecordFields, err := requiredStringSliceTable(document, contractPath, "session_record_fields", "fields")
	if err != nil {
		return publicContract{}, err
	}
	sessionExportFields, err := requiredStringSliceTable(document, contractPath, "session_record_fields", "export_fields")
	if err != nil {
		return publicContract{}, err
	}
	injectionTables, err := requiredStringSliceTable(document, contractPath, "injection_tables", "tables")
	if err != nil {
		return publicContract{}, err
	}

	return publicContract{
		ExitCodes:           exitCodes,
		OutputLinePrefixes:  outputLinePrefixes,
		SessionRecordFields: sessionRecordFields,
		SessionExportFields: sessionExportFields,
		InjectionTables:     injectionTables,
	}, nil
}

func requiredStringSliceTable(document map[string]any, contractPath, table, key string) ([]string, error) {
	rawTable, ok := document[table].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must define a [%s] table", contractPath, table)
	}
	values, found, err := MustStringSlice(rawTable[key])
	if err != nil {
		return nil, fmt.Errorf("%s [%s] %s: %w", contractPath, table, key, err)
	}
	if !found || len(values) == 0 {
		return nil, fmt.Errorf("%s [%s] must define a non-empty %s array", contractPath, table, key)
	}
	return values, nil
}

// checkExitCodes asserts that every documented exit code is emitted
// somewhere in exitCodeSourceFiles as a whole "word" (not a substring of a
// larger number), i.e. it is at least plausibly a real exit-code literal
// and not merely undocumented. 0/1/2 are close to trivially satisfied
// (they are common CLI usage/success/runtime-error codes emitted via
// die()/dieUsage() and "exit 0"/"exit 1"/"exit 2" throughout
// scripts/workcell); 3/124/126/127/128 each have a narrow, specific
// anchor (see the G1 audit).
func checkExitCodes(rootDir, contractPath string, codes []string) error {
	var source strings.Builder
	for _, relPath := range exitCodeSourceFiles {
		text, err := readText(filepath.Join(rootDir, relPath))
		if err != nil {
			return fmt.Errorf("%s exit_codes: %w", contractPath, err)
		}
		source.WriteString(text)
		source.WriteByte('\n')
	}
	combined := source.String()

	for _, code := range codes {
		if strings.TrimSpace(code) == "" {
			return fmt.Errorf("%s exit_codes entries may not be empty", contractPath)
		}
		pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(code) + `\b`)
		if !pattern.MatchString(combined) {
			return fmt.Errorf("%s exit code %s is not emitted anywhere in %s", contractPath, code, strings.Join(exitCodeSourceFiles, ", "))
		}
	}
	return nil
}

// checkOutputLinePrefixes asserts that every documented output-line prefix
// is emitted somewhere under internal/, cmd/, or scripts/workcell.
func checkOutputLinePrefixes(rootDir, contractPath string, prefixes []string) error {
	sources, err := outputLineSearchCorpus(rootDir)
	if err != nil {
		return fmt.Errorf("%s output_lines: %w", contractPath, err)
	}

	for _, prefix := range prefixes {
		if strings.TrimSpace(prefix) == "" {
			return fmt.Errorf("%s output_lines entries may not be empty", contractPath)
		}
		if !outputLinePrefixEmitted(sources, prefix) {
			return fmt.Errorf("%s output-line prefix %q is not emitted anywhere under internal/, cmd/, or scripts/workcell", contractPath, prefix)
		}
	}
	return nil
}

// outputLineSearchCorpus reads every file under internal/ and cmd/, plus
// scripts/workcell, into memory once so checkOutputLinePrefixes can test
// each documented prefix without re-walking the tree per prefix.
//
// cmd/ is included alongside internal/ because at least one stable output
// line ("mutation score: ") is only ever printed from
// cmd/workcell-citools/main.go, not from any internal/ package.
func outputLineSearchCorpus(rootDir string) ([]string, error) {
	internalFiles, err := walkFiles(rootDir, "internal")
	if err != nil {
		return nil, err
	}
	cmdFiles, err := walkFiles(rootDir, "cmd")
	if err != nil {
		return nil, err
	}

	relativePaths := make([]string, 0, len(internalFiles)+len(cmdFiles)+1)
	relativePaths = append(relativePaths, internalFiles...)
	relativePaths = append(relativePaths, cmdFiles...)
	relativePaths = append(relativePaths, filepath.Join("scripts", "workcell"))

	// Two classes of file are excluded from the emitter corpus because they
	// mention contract prefixes without emitting them, and would otherwise
	// let the check pass even after the real emitter is deleted — exactly the
	// drift it exists to catch:
	//   - _test.go files: not emitters, and this package's own
	//     public_contract_test.go builds mutated fixtures with deliberately
	//     bogus prefix literals for the negative controls.
	//   - this validator's own source: its doc comments necessarily quote the
	//     contract prefixes ("assurance=", "mutation score:", ...) to explain
	//     the check, so leaving it in would self-satisfy those entries.
	relativePaths = excludeNonEmitterFiles(relativePaths)

	sources := make([]string, 0, len(relativePaths))
	for _, relPath := range relativePaths {
		text, err := readText(filepath.Join(rootDir, relPath))
		if err != nil {
			return nil, err
		}
		sources = append(sources, text)
	}
	return sources, nil
}

// contractValidatorSourceFile is this validator's own source file, relative
// to the repo root. Its doc comments quote the contract prefixes to explain
// the check, so it is excluded from the emitter corpus (see
// outputLineSearchCorpus).
var contractValidatorSourceFile = filepath.Join("internal", "metadatautil", "public_contract.go")

// excludeNonEmitterFiles drops every "_test.go" path and this validator's own
// source file — both mention contract prefixes without emitting them.
func excludeNonEmitterFiles(paths []string) []string {
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || path == contractValidatorSourceFile {
			continue
		}
		filtered = append(filtered, path)
	}
	return filtered
}

// outputLinePrefixEmitted reports whether prefix is emitted at a key
// boundary (the start of the source, or immediately after a non-identifier
// byte such as a quote, space, or newline) somewhere in sources, OR — for
// the shellproto emitter, which builds "key=value\n" lines from a bare Go
// string key rather than a literal "key=" substring (see
// internal/shellproto.WriteField) — as a quoted key literal in a file that
// also references the shellproto package. Without this second form, a
// prefix such as "publish_pr_url=" (internal/publishpr/publish_pr_cli.go's
// shellproto.Field{Key: "publish_pr_url"}) would be invisible even though
// it is exactly what shellproto guarantees gets printed.
//
// The match requires the prefix to be immediately preceded by a quote
// (a `"` or `'`), i.e. to begin a Go/shell format-string literal such as
// `fmt.Sprintf("assurance=%s", …)` or `printf 'record_digest=%q '`. This
// anchors the check to actual emitters and rejects two false-positive
// classes a plain substring or word-boundary match would accept:
//   - longer keys that merely end in the prefix ("current_assurance=",
//     "cache_assurance="), whose quote sits before the longer key; and
//   - non-emitting references such as shell variable assignments
//     (`local record_digest=…`) and sed patterns (`.*record_digest=`),
//     whose prefix is preceded by whitespace or a metacharacter.
// So deleting the real emitter — while leaving those references behind —
// correctly fails the check. The quoted-key shellproto form below carries
// its own leading-quote anchor.
func outputLinePrefixEmitted(sources []string, prefix string) bool {
	quoted := regexp.MustCompile(`["']` + regexp.QuoteMeta(prefix))
	for _, source := range sources {
		if quoted.MatchString(source) {
			return true
		}
	}
	if key, ok := strings.CutSuffix(prefix, "="); ok {
		quotedKey := `"` + key + `"`
		for _, source := range sources {
			if strings.Contains(source, "shellproto") && strings.Contains(source, quotedKey) {
				return true
			}
		}
	}
	return false
}

// structJSONFieldPattern extracts the tag name out of a `json:"name"` or
// `json:"name,omitempty"` struct tag.
var structJSONFieldPattern = regexp.MustCompile(`json:"([a-zA-Z0-9_]+)(?:,[^"]*)?"`)

// checkSessionRecordFields asserts that [session_record_fields].fields and
// .export_fields exactly match the json tags of SessionRecord and
// SessionExport in internal/host/sessions/sessions.go.
func checkSessionRecordFields(rootDir, contractPath string, wantRecordFields, wantExportFields []string) error {
	sessionsPath := filepath.Join(rootDir, "internal", "host", "sessions", "sessions.go")
	source, err := readText(sessionsPath)
	if err != nil {
		return fmt.Errorf("%s session_record_fields: %w", contractPath, err)
	}

	recordFields, err := structJSONFields(source, "SessionRecord", sessionsPath)
	if err != nil {
		return fmt.Errorf("%s session_record_fields: %w", contractPath, err)
	}
	if err := assertSetsEqual(contractPath, "session_record_fields.fields", "the json tags of SessionRecord in "+sessionsPath, recordFields, wantRecordFields); err != nil {
		return err
	}

	exportFields, err := structJSONFields(source, "SessionExport", sessionsPath)
	if err != nil {
		return fmt.Errorf("%s session_record_fields: %w", contractPath, err)
	}
	if err := assertSetsEqual(contractPath, "session_record_fields.export_fields", "the json tags of SessionExport in "+sessionsPath, exportFields, wantExportFields); err != nil {
		return err
	}
	return nil
}

// structJSONFields extracts the json tag names of the exported struct
// named structName from source, scoped to that struct's `{ ... }` block:
// from the `type <structName> struct {` marker up to the next line whose
// first character is the closing brace. sessions.go's structs are flat
// (no nested struct literals with their own top-level-column braces), so
// this simple scan is sufficient and avoids pulling in go/ast for one
// call site.
func structJSONFields(source, structName, path string) ([]string, error) {
	marker := "type " + structName + " struct {"
	start := strings.Index(source, marker)
	if start == -1 {
		return nil, fmt.Errorf("%s: struct %s not found", path, structName)
	}
	body := source[start+len(marker):]
	end := strings.Index(body, "\n}")
	if end == -1 {
		return nil, fmt.Errorf("%s: struct %s has no closing brace", path, structName)
	}
	body = body[:end]

	matches := structJSONFieldPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("%s: struct %s has no json tags", path, structName)
	}
	fields := make([]string, 0, len(matches))
	for _, match := range matches {
		fields = append(fields, match[1])
	}
	return fields, nil
}

// checkInjectionTables asserts that [injection_tables].tables exactly
// matches the injection-policy top-level table whitelist accepted by
// internal/injection/render_policy_load.go: the `name != "documents" &&
// name != "ssh" && name != "credentials"` guard in documentToInjectionMap
// (single-bracket tables) and the `tableName != "copies"` guard in
// extractCopiesBlocks (the one supported [[array-of-table]]). Both guards
// reject every name outside their listed set, so the literals extracted
// from each `!=` chain are the complete accepted whitelist, not merely an
// example subset.
func checkInjectionTables(rootDir, contractPath string, tables []string) error {
	renderPolicyPath := filepath.Join(rootDir, "internal", "injection", "render_policy_load.go")
	source, err := readText(renderPolicyPath)
	if err != nil {
		return fmt.Errorf("%s injection_tables: %w", contractPath, err)
	}

	singleBracketTables, err := functionScopedMatches(source, "documentToInjectionMap", `name != "([a-zA-Z0-9_]+)"`, renderPolicyPath)
	if err != nil {
		return fmt.Errorf("%s injection_tables: %w", contractPath, err)
	}
	arrayTables, err := functionScopedMatches(source, "extractCopiesBlocks", `tableName != "([a-zA-Z0-9_]+)"`, renderPolicyPath)
	if err != nil {
		return fmt.Errorf("%s injection_tables: %w", contractPath, err)
	}

	codeTables := append(append([]string{}, singleBracketTables...), arrayTables...)
	return assertSetsEqual(contractPath, "injection_tables.tables", "the accepted table names in "+renderPolicyPath, codeTables, tables)
}

// functionScopedMatches runs pattern over the body of the top-level
// function funcName in source (from its `func funcName(` line up to the
// next line consisting solely of `}`), returning each match's first
// capture group in first-seen order with duplicates removed.
func functionScopedMatches(source, funcName, pattern, path string) ([]string, error) {
	marker := "func " + funcName + "("
	start := strings.Index(source, marker)
	if start == -1 {
		return nil, fmt.Errorf("%s: function %s not found", path, funcName)
	}
	rest := source[start:]
	end := strings.Index(rest, "\n}\n")
	if end == -1 {
		return nil, fmt.Errorf("%s: function %s has no closing brace", path, funcName)
	}
	body := rest[:end]

	re := regexp.MustCompile(pattern)
	matches := re.FindAllStringSubmatch(body, -1)
	seen := map[string]struct{}{}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if _, ok := seen[match[1]]; ok {
			continue
		}
		seen[match[1]] = struct{}{}
		names = append(names, match[1])
	}
	return names, nil
}

// assertSetsEqual reports an error naming both any code-but-not-doc
// ("orphan") and doc-but-not-code ("stale") entries when codeValues and
// docValues differ as sets.
func assertSetsEqual(contractPath, contractField, sourceDescription string, codeValues, docValues []string) error {
	codeSet := map[string]struct{}{}
	for _, v := range codeValues {
		codeSet[v] = struct{}{}
	}
	docSet := map[string]struct{}{}
	for _, v := range docValues {
		docSet[v] = struct{}{}
	}

	var orphans, stale []string
	for v := range codeSet {
		if _, ok := docSet[v]; !ok {
			orphans = append(orphans, v)
		}
	}
	for v := range docSet {
		if _, ok := codeSet[v]; !ok {
			stale = append(stale, v)
		}
	}
	if len(orphans) == 0 && len(stale) == 0 {
		return nil
	}
	slices.Sort(orphans)
	slices.Sort(stale)

	var msg strings.Builder
	fmt.Fprintf(&msg, "%s %s must match %s", contractPath, contractField, sourceDescription)
	if len(orphans) > 0 {
		fmt.Fprintf(&msg, "; in code but missing from contract: %s", strings.Join(orphans, ", "))
	}
	if len(stale) > 0 {
		fmt.Fprintf(&msg, "; in contract but not in code: %s", strings.Join(stale, ", "))
	}
	return errors.New(msg.String())
}
