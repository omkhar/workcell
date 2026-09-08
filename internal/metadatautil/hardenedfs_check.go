// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
)

// CheckHardenedFS rejects a raw os file call in a trust-boundary package.
//
// internal/rootio already carries the hardened primitives: it opens through a
// verified parent directory handle, refuses to follow a symlink, and stages
// then publishes a file. Nothing forced its use, so the packages that read
// operator-controlled paths still reach for os.Open, os.Stat and os.MkdirAll
// directly. Every one of those calls follows a symlink, accepts a
// multiply-linked or non-regular file, and leaves a name-swap window between
// the check and the open. Review history holds 159 accepted findings of that
// shape before pull request 600.
//
// The check is a ratchet, not a big bang. policy/hardened-fs-baseline.tsv
// records the calls each file carries today, and a count above its baseline
// fails. New code states its case at the call instead:
//
//	data, err := os.ReadFile(path) // hardened-fs-exempt: the path is a build constant
//
// The reason is required, and the comment has to be a real comment on the
// call's own line: the scan reads the syntax tree, so the same words inside a
// string literal exempt nothing. One comment clears one call, so a line with
// two raw calls needs two lines and two reasons.
func CheckHardenedFS(rootDir string) error {
	counts := map[hardenedFSKey]int{}
	details := map[hardenedFSKey][]string{}
	scanned := 0
	for _, pkg := range hardenedFSPackages {
		err := filepath.WalkDir(filepath.Join(rootDir, pkg), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := entry.Name()
			if entry.IsDir() {
				if name == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(rootDir, path)
			if relErr != nil {
				return relErr
			}
			// Read the walked file through a verified parent handle rather
			// than by name: the entry the walk reported and the bytes a second
			// resolution returns are not the same file when the name is a
			// symlink or is swapped in between.
			content, readErr := rootio.ReadFileNoFollow(path, rel, hardenedFSMaxSourceBytes)
			if readErr != nil {
				return readErr
			}
			scanned++
			fileFindings, scanErr := HardenedFSFindings(string(content))
			if scanErr != nil {
				return fmt.Errorf("%s: %w", rel, scanErr)
			}
			for _, finding := range fileFindings {
				key := hardenedFSKey{path: filepath.ToSlash(rel), symbol: finding.Symbol}
				counts[key]++
				details[key] = append(details[key],
					fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), finding.Line, finding.Symbol))
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("scan %s for raw os file calls: %w", pkg, err)
		}
	}
	if scanned == 0 {
		return fmt.Errorf("no Go source under the trust-boundary packages; refusing a vacuous pass")
	}
	baseline, err := loadHardenedFSBaseline(filepath.Join(rootDir, hardenedFSBaselinePath))
	if err != nil {
		return err
	}
	var failures []string
	for key, count := range counts {
		allowed := baseline[key]
		if count <= allowed {
			continue
		}
		reported := details[key]
		if len(reported) > hardenedFSMaxReported {
			reported = reported[:hardenedFSMaxReported]
		}
		failures = append(failures, fmt.Sprintf(
			"%s: %d call(s) of %s, baseline allows %d; use internal/rootio, or state the reason with // %s <reason>\n    %s",
			key.path, count, key.symbol, allowed, hardenedFSExemptTag, strings.Join(reported, "\n    ")))
	}
	for key := range baseline {
		if _, ok := counts[key]; ok {
			continue
		}
		failures = append(failures, fmt.Sprintf(
			"%s: stale baseline row for %s; the file no longer makes that call, so remove the row", key.path, key.symbol))
	}
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return fmt.Errorf("hardened filesystem check failed:\n  %s", strings.Join(failures, "\n  "))
}

const (
	hardenedFSBaselinePath = "policy/hardened-fs-baseline.tsv"
	hardenedFSExemptTag    = "hardened-fs-exempt:"
	hardenedFSMaxReported  = 5

	// hardenedFSMaxSourceBytes bounds one Go source. The largest source in the
	// scanned packages is two orders of magnitude below it.
	hardenedFSMaxSourceBytes = 8 << 20
)

// hardenedFSPackages lists the packages that read or write a path an operator
// or a provider can influence. A package outside this set reads build
// constants and repository sources, where the hardened primitives buy nothing.
// internal/host holds the session, audit and launcher trees, so one entry
// covers them all. A name in this list that no directory matches fails the
// check, so the list cannot outlive its subject.
var hardenedFSPackages = []string{
	"internal/applecontainer",
	"internal/authpolicy",
	"internal/authresolve",
	"internal/host",
	"internal/injection",
	"internal/runtimeutil",
	"internal/sessionctl",
}

// hardenedFSSymbols lists the raw calls the hardened primitives replace. Each
// one resolves an operator-controlled path by name, so each one follows a
// symlink and races a rename between the check and the open.
//
// os.Lstat is deliberately absent: it does not follow the final symlink, so it
// is part of the answer rather than part of the defect.
var hardenedFSSymbols = map[string]bool{
	"Open":       true,
	"OpenFile":   true,
	"ReadFile":   true,
	"WriteFile":  true,
	"Create":     true,
	"CreateTemp": true,
	"MkdirTemp":  true,
	"Mkdir":      true,
	"MkdirAll":   true,
	"Rename":     true,
	"Stat":       true,
	"ReadDir":    true,
	"Readlink":   true,
	"Remove":     true,
	"RemoveAll":  true,
	"Chmod":      true,
	"Chown":      true,
	"Symlink":    true,
	"Link":       true,
	"Truncate":   true,
}

type hardenedFSKey struct {
	path   string
	symbol string
}

// HardenedFSFinding is one raw os file call.
type HardenedFSFinding struct {
	Line   int
	Symbol string
}

// HardenedFSFindings reports each raw os file call in one Go source.
//
// It reads the syntax tree rather than the text. A text scan cannot tell a
// call from a mention of one, cannot resolve an aliased import such as
// stdos "os", and cannot tell an exemption comment from the same words inside
// a string literal. The parser answers all three exactly.
func HardenedFSFindings(source string) ([]HardenedFSFinding, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "source.go", source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse the Go source: %w", err)
	}
	name, err := hardenedFSOSImportName(file)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, nil // the file does not import os
	}
	exempt := hardenedFSExemptLines(fileSet, file)
	var findings []HardenedFSFinding
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if !ok {
			return true
		}
		qualifier, ok := ast.Unparen(selector.X).(*ast.Ident)
		if !ok || qualifier.Name != name || !hardenedFSSymbols[selector.Sel.Name] {
			return true
		}
		findings = append(findings, HardenedFSFinding{
			Line:   fileSet.Position(call.Pos()).Line,
			Symbol: name + "." + selector.Sel.Name,
		})
		return true
	})
	sort.Slice(findings, func(i, j int) bool { return findings[i].Line < findings[j].Line })
	return applyHardenedFSExemptions(findings, exempt), nil
}

// applyHardenedFSExemptions clears one call per exempted line.
//
// A comment states the reason for the call it sits beside, and one comment
// cannot state the reason for two. A line that carries an exemption and more
// than one call keeps every call after the first, so the author splits the
// line and states a reason for each.
func applyHardenedFSExemptions(findings []HardenedFSFinding, exempt map[int]bool) []HardenedFSFinding {
	used := map[int]bool{}
	kept := findings[:0]
	for _, finding := range findings {
		if exempt[finding.Line] && !used[finding.Line] {
			used[finding.Line] = true
			continue
		}
		kept = append(kept, finding)
	}
	return kept
}

// hardenedFSOSImportName returns the name that qualifies a call of the os
// package in this file, or the empty string when the file does not import it.
// A dot import is refused: it removes the qualifier that the scan reads, so
// the rule would silently stop applying to the file.
func hardenedFSOSImportName(file *ast.File) (string, error) {
	for _, spec := range file.Imports {
		if spec.Path == nil || spec.Path.Value != `"os"` {
			continue
		}
		if spec.Name == nil {
			return "os", nil
		}
		if spec.Name.Name == "." || spec.Name.Name == "_" {
			return "", fmt.Errorf("the os import uses the %q form; write it as a plain import or an alias so the hardened filesystem rule can read its calls", spec.Name.Name)
		}
		return spec.Name.Name, nil
	}
	return "", nil
}

// hardenedFSExemptLines returns the lines that carry a reasoned exemption
// comment. The reason is required: a bare tag states nothing.
// hardenedFSExemptionReason reports a comment whose body opens with the tag
// and then states a reason. The delimiters are removed first: a block comment
// carries its closing "*/" in the text, and that is not a reason.
func hardenedFSExemptionReason(text string) bool {
	body := strings.TrimPrefix(text, "//")
	if trimmed, found := strings.CutPrefix(text, "/*"); found {
		body = strings.TrimSuffix(trimmed, "*/")
	}
	reason, found := strings.CutPrefix(strings.TrimSpace(body), hardenedFSExemptTag)
	return found && strings.TrimSpace(reason) != ""
}

func hardenedFSExemptLines(fileSet *token.FileSet, file *ast.File) map[int]bool {
	lines := map[int]bool{}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !hardenedFSExemptionReason(comment.Text) {
				continue
			}
			lines[fileSet.Position(comment.Pos()).Line] = true
		}
	}
	return lines
}

func loadHardenedFSBaseline(path string) (map[hardenedFSKey]int, error) {
	content, err := rootio.ReadFileNoFollow(path, hardenedFSBaselinePath, hardenedFSMaxSourceBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", hardenedFSBaselinePath, err)
	}
	baseline := map[hardenedFSKey]int{}
	for number, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s:%d: expected three tab-separated fields, found %d", hardenedFSBaselinePath, number+1, len(fields))
		}
		count, convErr := strconv.Atoi(fields[2])
		if convErr != nil || count <= 0 {
			return nil, fmt.Errorf("%s:%d: count must be a positive integer, found %q", hardenedFSBaselinePath, number+1, fields[2])
		}
		baseline[hardenedFSKey{path: fields[0], symbol: fields[1]}] = count
	}
	return baseline, nil
}
