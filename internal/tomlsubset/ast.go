// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package tomlsubset

import (
	"fmt"
	"strings"
)

// Document is the parsed AST form of a TOML-subset source.
//
// Unlike Parse, which returns a nested map[string]any tree, ParseDocument
// preserves the source order of tables and the source order of pairs
// within each table.  This is the shape PR 37 (authpolicy + authresolve)
// and PR 38 (injection) will consume when they migrate off their private
// parseTOMLSubset copies: they need ordered iteration in order to apply
// per-table validation policy that depends on the order of declaration.
//
// The implicit "top level" of a document (pairs that appear before the
// first [header]) lives in TopLevel.  Each subsequent [header] starts a
// new Table appended to Tables in declaration order.
type Document struct {
	// TopLevel holds pairs that appear before any [header] line.  Order
	// of declaration is preserved.
	TopLevel Table

	// Tables holds the [header] tables in declaration order.
	Tables []Table
}

// Table is a single TOML [header] (or the implicit top-level container)
// captured with its declared name, source line, and ordered pairs.
type Table struct {
	// Name is the bracketed header name, e.g. "credentials.api" for
	// [credentials.api].  Empty for Document.TopLevel.
	Name string

	// Line is the 1-based source line number where the header appears.
	// 0 for Document.TopLevel.
	Line int

	// Pairs holds the key/value entries in declaration order.
	Pairs []Pair
}

// Pair is a single `key = value` entry.  Raw preserves the value text
// (multi-line arrays joined with `\n`) for callers that need to render
// or rehash the original source; Value is the parsed Go form produced by
// ParseValue (bool, string, int, or []any).
type Pair struct {
	// Key is the bareword left-hand side, with surrounding whitespace
	// stripped.  Never contains a dot (dotted keys are rejected).
	Key string

	// Raw is the right-hand side text, exactly as it appeared in the
	// source (with comments stripped and continuation lines joined by
	// `\n`).
	Raw string

	// Value is the parsed Go representation: bool, string, int, or
	// []any.  Use Lookup helpers in caller packages to type-assert.
	Value any

	// Line is the 1-based source line where the key appears.
	Line int
}

// LookupTable returns the first table in doc whose Name equals name, or
// nil if no such table exists.  Convenience helper for callers that walk
// the document by header.
func (doc *Document) LookupTable(name string) *Table {
	if doc == nil {
		return nil
	}
	for i := range doc.Tables {
		if doc.Tables[i].Name == name {
			return &doc.Tables[i]
		}
	}
	return nil
}

// Lookup returns the first Pair in t whose Key equals key, or nil.
func (t *Table) Lookup(key string) *Pair {
	if t == nil {
		return nil
	}
	for i := range t.Pairs {
		if t.Pairs[i].Key == key {
			return &t.Pairs[i]
		}
	}
	return nil
}

// ParseDocument parses content as Workcell's strict TOML subset and
// returns an ordered AST.  sourcePath is used only for diagnostic
// line:column prefixes in error messages.
//
// Rejected constructs (strict subset):
//   - array-of-tables ([[table]])
//   - dotted keys (a.b.c = 1) at the pair level
//   - duplicate keys within a table
//   - duplicate [headers]
//   - multi-line basic strings ("""...""" or ”'...”')
//   - inline tables ({a=1, b=2})
//   - TOML datetime literals (1979-05-27T07:32:00Z and friends)
//
// Dotted *table* headers ([a.b]) are tolerated because Workcell already
// uses them for the `credentials.<name>` family; they're recorded as the
// raw Name and the caller decides whether to accept them.
func ParseDocument(content, sourcePath string) (*Document, error) {
	doc := &Document{}
	current := &doc.TopLevel
	seenTables := map[string]struct{}{}
	seenKeys := map[*Table]map[string]struct{}{
		&doc.TopLevel: {},
	}

	lines := strings.Split(content, "\n")
	for idx := 0; idx < len(lines); idx++ {
		rawLine := lines[idx]
		lineNo := idx + 1
		line := StripComment(rawLine)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[[") {
			return nil, fmt.Errorf("%s:%d: array-of-table headers ([[...]]) are not supported in the TOML subset", sourcePath, lineNo)
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name == "" {
				return nil, fmt.Errorf("%s:%d: empty table name", sourcePath, lineNo)
			}
			if _, exists := seenTables[name]; exists {
				return nil, fmt.Errorf("%s:%d: duplicate table [%s]", sourcePath, lineNo, name)
			}
			seenTables[name] = struct{}{}
			doc.Tables = append(doc.Tables, Table{Name: name, Line: lineNo})
			current = &doc.Tables[len(doc.Tables)-1]
			seenKeys[current] = map[string]struct{}{}
			continue
		}

		if !strings.Contains(line, "=") {
			return nil, fmt.Errorf("%s:%d: expected key = value", sourcePath, lineNo)
		}

		key, valueText, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", sourcePath, lineNo)
		}
		if strings.Contains(key, ".") {
			return nil, fmt.Errorf("%s:%d: dotted TOML keys are not supported; use explicit [table] headers instead", sourcePath, lineNo)
		}
		if _, exists := seenKeys[current][key]; exists {
			return nil, fmt.Errorf("%s:%d: duplicate key: %s", sourcePath, lineNo, key)
		}

		trimmedValue := strings.TrimSpace(valueText)
		if err := rejectUnsupportedValue(trimmedValue, sourcePath, lineNo); err != nil {
			return nil, err
		}

		// Multi-line array continuation: join subsequent lines until the
		// `[ ... ]` bracket depth balances.
		if strings.HasPrefix(trimmedValue, "[") && !ArrayClosed(valueText) {
			for {
				idx++
				if idx >= len(lines) {
					return nil, fmt.Errorf("%s:%d: unterminated TOML array", sourcePath, lineNo)
				}
				nextLine := StripComment(lines[idx])
				if nextLine == "" {
					continue
				}
				valueText += "\n" + nextLine
				if ArrayClosed(valueText) {
					break
				}
			}
		}

		parsed, err := ParseValue(valueText, fmt.Sprintf("%s:%d", sourcePath, lineNo))
		if err != nil {
			return nil, err
		}

		current.Pairs = append(current.Pairs, Pair{
			Key:   key,
			Raw:   valueText,
			Value: parsed,
			Line:  lineNo,
		})
		seenKeys[current][key] = struct{}{}
	}

	return doc, nil
}

// rejectUnsupportedValue rejects TOML constructs that Parse/ParseValue
// don't recognize but that would otherwise produce a confusing error
// downstream — multi-line strings, inline tables, and datetime literals.
// Strings, integers, booleans, and arrays fall through to ParseValue.
func rejectUnsupportedValue(value, sourcePath string, lineNo int) error {
	if strings.HasPrefix(value, "\"\"\"") || strings.HasPrefix(value, "'''") {
		return fmt.Errorf("%s:%d: multi-line strings are not supported in the TOML subset", sourcePath, lineNo)
	}
	if strings.HasPrefix(value, "{") {
		return fmt.Errorf("%s:%d: inline tables are not supported in the TOML subset", sourcePath, lineNo)
	}
	if looksLikeDatetime(value) {
		return fmt.Errorf("%s:%d: TOML datetimes are not supported in the TOML subset", sourcePath, lineNo)
	}
	return nil
}

// looksLikeDatetime reports whether v has the shape of a TOML date,
// time, or datetime literal.  Workcell's subset rejects all of these
// because policy files only store strings, ints, bools, and arrays.
func looksLikeDatetime(v string) bool {
	// Quoted strings, arrays, and tables are handled elsewhere.
	if v == "" {
		return false
	}
	if v[0] == '"' || v[0] == '\'' || v[0] == '[' || v[0] == '{' {
		return false
	}
	// Date: YYYY-MM-DD  (so at least 10 chars with '-' at positions 4 and 7).
	if len(v) >= 10 && v[4] == '-' && v[7] == '-' && isASCIIDigits(v[:4]) && isASCIIDigits(v[5:7]) && isASCIIDigits(v[8:10]) {
		return true
	}
	// Local time: HH:MM:SS  (at least 8 chars with ':' at positions 2 and 5).
	if len(v) >= 8 && v[2] == ':' && v[5] == ':' && isASCIIDigits(v[:2]) && isASCIIDigits(v[3:5]) && isASCIIDigits(v[6:8]) {
		return true
	}
	return false
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
