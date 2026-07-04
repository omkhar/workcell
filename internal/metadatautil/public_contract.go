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
	ExitCodes               []string
	OutputLinePrefixes      []string
	SessionRecordFields     []string
	SessionExportFields     []string
	InjectionTables         []string
	InjectionScalarRootKeys []string
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
	if err := checkInjectionTables(rootDir, contractPath, contract.InjectionTables, contract.InjectionScalarRootKeys); err != nil {
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
	injectionScalarRootKeys, err := requiredStringSliceTable(document, contractPath, "injection_tables", "scalar_root_keys")
	if err != nil {
		return publicContract{}, err
	}

	return publicContract{
		ExitCodes:               exitCodes,
		OutputLinePrefixes:      outputLinePrefixes,
		SessionRecordFields:     sessionRecordFields,
		SessionExportFields:     sessionExportFields,
		InjectionTables:         injectionTables,
		InjectionScalarRootKeys: injectionScalarRootKeys,
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

// blockCommentPattern matches Go/Rust `/* … */` block comments.
var blockCommentPattern = regexp.MustCompile(`(?s)/\*.*?\*/`)

// stripComments removes comment prose from source so an explanatory comment
// mentioning an exit form (`// … exit 2 …`) or a contract prefix cannot be
// mistaken for a real emitter/exit site. It drops Go/Rust `/* */` and `//`
// comments and shell full-line `#` comments (leaving inline `#` — e.g.
// `${#arr[@]}` — and shebangs intact). It is a lexical approximation: a
// string literal that itself contains `//` is truncated, which is harmless
// because such strings never carry the exit constructs or quoted
// format-string prefixes the scans look for.
func stripComments(source string) string {
	source = blockCommentPattern.ReplaceAllString(source, " ")
	var b strings.Builder
	for _, line := range strings.Split(source, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		if trimmed := strings.TrimLeft(line, " \t"); strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#!") {
			line = ""
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// checkExitCodes asserts that every documented exit code is emitted at an
// actual exit site in exitCodeSourceFiles — an `os.Exit(N)`, a
// cliexit.ExitCodeError{Code: N}, a `return N`, a shell `exit N`, or the
// Rust launcher's bare status literals — rather than merely appearing as a
// standalone number. A plain word-boundary match on the digit would leave a
// documented code satisfied by unrelated arity/index constants even after
// the only real producer is removed.
func checkExitCodes(rootDir, contractPath string, codes []string) error {
	var source strings.Builder
	for _, relPath := range exitCodeSourceFiles {
		text, err := readText(filepath.Join(rootDir, relPath))
		if err != nil {
			return fmt.Errorf("%s exit_codes: %w", contractPath, err)
		}
		source.WriteString(stripComments(text))
		source.WriteByte('\n')
	}
	combined := source.String()

	for _, code := range codes {
		if strings.TrimSpace(code) == "" {
			return fmt.Errorf("%s exit_codes entries may not be empty", contractPath)
		}
		if !exitCodeEmitted(combined, code) {
			return fmt.Errorf("%s exit code %s is not emitted at any exit site in %s", contractPath, code, strings.Join(exitCodeSourceFiles, ", "))
		}
	}
	return nil
}

// exitCodeEmitted reports whether code appears at a real exit construct in
// combined. The generic forms cover the Go and shell exit paths; the
// launcher-literal forms cover the Rust launcher's bare status expressions
// (errno branch results `{ 126 }` / `{ 127 }`, the `128 + WTERMSIG` signal
// base) and the named colima-timeout constant `= 124`.
func exitCodeEmitted(combined, code string) bool {
	n := regexp.QuoteMeta(code)
	patterns := []string{
		`Code:\s*` + n + `\b`, // cliexit.ExitCodeError{Code: N}
		`os\.Exit\(` + n + `\)`,
		`\bexit\s+` + n + `\b`,   // shell exit N
		`\breturn\s+` + n + `\b`, // Go / Rust return N
	}
	switch code {
	case "124":
		patterns = append(patterns, `=\s*124\b`) // const ColimaTimeoutExitCode = 124
	case "126", "127":
		patterns = append(patterns, `\{\s*`+n+`\s*\}`) // errno branch result
	case "128":
		patterns = append(patterns, `\b128\s*\+`) // 128 + WTERMSIG signal base
	}
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(combined) {
			return true
		}
	}
	return false
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
		// Strip comments so a prefix mentioned only in a comment (rather than
		// a real emitter string) cannot satisfy the check.
		sources = append(sources, stripComments(text))
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
//
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
		// A shellproto emitter builds "key=value\n" from a Field literal
		// `shellproto.Field{Key: "key", …}` rather than a "key=" string
		// literal. Anchor to that construction (a `Key: "key"` in a
		// shellproto-importing file) rather than any occurrence of the bare
		// quoted key, so an unrelated `missing := "workspace"` error string
		// does not count as an emitter.
		fieldKey := regexp.MustCompile(`Key:\s*"` + regexp.QuoteMeta(key) + `"`)
		for _, source := range sources {
			if strings.Contains(source, "shellproto") && fieldKey.MatchString(source) {
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

// checkInjectionTables asserts that the injection-policy contract matches the
// authoritative root-key gate `allowedRootPolicyKeys` in
// internal/injection/render_injection_bundle.go — the map validated (via
// validateAllowedKeys) before any table parsing runs, so it is the first and
// definitive gate on which top-level keys a policy may carry. The contract's
// [injection_tables].tables (documents/ssh/credentials/copies) plus its
// declared scalar_root_keys (version/includes) must set-equal that map's
// keys; dropping a key from the gate then fails this check (a later
// `name != …` chain scrape would miss that, since the gate rejects first).
func checkInjectionTables(rootDir, contractPath string, tables, scalarRootKeys []string) error {
	bundlePath := filepath.Join(rootDir, "internal", "injection", "render_injection_bundle.go")
	source, err := readText(bundlePath)
	if err != nil {
		return fmt.Errorf("%s injection_tables: %w", contractPath, err)
	}

	// 1. The full root-key set (tables + scalars) must equal the authoritative
	//    allowedRootPolicyKeys gate, so no accepted root key is dropped.
	gateKeys, err := mapStringSetKeys(source, "allowedRootPolicyKeys", bundlePath)
	if err != nil {
		return fmt.Errorf("%s injection_tables: %w", contractPath, err)
	}
	contractKeys := append(append([]string{}, tables...), scalarRootKeys...)
	if err := assertSetsEqual(contractPath, "injection_tables.tables + scalar_root_keys", "the allowedRootPolicyKeys gate in "+bundlePath, gateKeys, contractKeys); err != nil {
		return err
	}

	// 2. Separately, [injection_tables].tables must equal the actual accepted
	//    TABLE names — the `name != …` guard in documentToInjectionMap (single-
	//    bracket tables) and the `tableName != …` guard in extractCopiesBlocks
	//    (the one array-of-tables) — so moving a table into scalar_root_keys (or
	//    vice versa) fails even though the flattened union would still match.
	renderPolicyPath := filepath.Join(rootDir, "internal", "injection", "render_policy_load.go")
	policySource, err := readText(renderPolicyPath)
	if err != nil {
		return fmt.Errorf("%s injection_tables: %w", contractPath, err)
	}
	singleBracketTables, err := functionScopedMatches(policySource, "documentToInjectionMap", `name != "([a-zA-Z0-9_]+)"`, renderPolicyPath)
	if err != nil {
		return fmt.Errorf("%s injection_tables: %w", contractPath, err)
	}
	arrayTables, err := functionScopedMatches(policySource, "extractCopiesBlocks", `tableName != "([a-zA-Z0-9_]+)"`, renderPolicyPath)
	if err != nil {
		return fmt.Errorf("%s injection_tables: %w", contractPath, err)
	}
	codeTables := append(append([]string{}, singleBracketTables...), arrayTables...)
	return assertSetsEqual(contractPath, "injection_tables.tables", "the accepted table names in "+renderPolicyPath, codeTables, tables)
}

// functionScopedMatches runs pattern over the body of the top-level function
// funcName in source (from its `func funcName(` line up to the next line that
// is solely `}`), returning each match's first capture group with duplicates
// removed.
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

// mapStringSetKeys extracts the string keys of a `varName = map[string]struct{}{
// "k": {}, … }` literal in source, scoped to that variable's block.
func mapStringSetKeys(source, varName, path string) ([]string, error) {
	marker := varName + " = map[string]struct{}{"
	start := strings.Index(source, marker)
	if start == -1 {
		return nil, fmt.Errorf("%s: map %s not found", path, varName)
	}
	// The marker ends at the map's opening brace; brace-count to its match so
	// the `{}` set values (each self-balancing) do not terminate the scan early.
	bodyStart := start + len(marker)
	depth := 1
	i := bodyStart
	for ; i < len(source) && depth > 0; i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("%s: map %s has no closing brace", path, varName)
	}
	body := source[bodyStart : i-1]

	matches := regexp.MustCompile(`"([a-zA-Z0-9_]+)":`).FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("%s: map %s has no keys", path, varName)
	}
	keys := make([]string, 0, len(matches))
	for _, match := range matches {
		keys = append(keys, match[1])
	}
	return keys, nil
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
