// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
	for _, pkg := range hardenedFSPackages {
		err := scanHardenedGoSources(rootDir, pkg, func(rel string, content []byte) error {
			fileFindings, scanErr := HardenedFSFindings(string(content))
			if scanErr != nil {
				return fmt.Errorf("%s: %w", rel, scanErr)
			}
			for _, finding := range fileFindings {
				key := hardenedFSKey{path: rel, symbol: finding.Symbol}
				counts[key]++
				details[key] = append(details[key], fmt.Sprintf("%s:%d: %s", rel, finding.Line, finding.Symbol))
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	baseline, err := loadHardenedFSBaseline(filepath.Join(rootDir, hardenedFSBaselinePath))
	if err != nil {
		return err
	}
	var failures []string
	for key, count := range counts {
		allowed := baseline[key]
		switch {
		case count == allowed:
			continue
		case count < allowed:
			// A count below its row leaves the difference as unused headroom
			// that a later change can spend, which is a ratchet that does not
			// hold. Lower the row with the repair.
			failures = append(failures, fmt.Sprintf(
				"%s: %d reference(s) to %s, baseline still allows %d; lower the baseline row to %d",
				key.path, count, key.symbol, allowed, count))
		default:
			reported := details[key]
			if len(reported) > hardenedFSMaxReported {
				reported = reported[:hardenedFSMaxReported]
			}
			failures = append(failures, fmt.Sprintf(
				"%s: %d reference(s) to %s, baseline allows %d; use internal/rootio, or state the reason with // %s <reason>\n    %s",
				key.path, count, key.symbol, allowed, hardenedFSExemptTag, strings.Join(reported, "\n    ")))
		}
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

// scanHardenedGoSources hands visit each non-test Go source under pkg, named
// relative to the repository. A testdata directory is skipped.
func scanHardenedGoSources(rootDir, pkg string, visit func(rel string, content []byte) error) error {
	// A symlinked package root is not walked: a walk reports the
	// link entry and returns success, so every source below it would escape
	// the scan while the check still reported a clean result.
	info, statErr := os.Lstat(filepath.Join(rootDir, pkg)) // hardened-fs-exempt: this proves the package root is a real directory before the walk opens it
	if statErr != nil || !info.IsDir() {
		return fmt.Errorf("scan root %s is not a directory; the hardened filesystem rule cannot read it", pkg)
	}
	// Walk and read through one directory handle. A pathname walk resolves
	// the package name again for every entry, so a rename between the check
	// and the read hands the scan a different tree than the one it verified.
	// Every later open is relative to this handle.
	root, err := os.OpenRoot(filepath.Join(rootDir, pkg)) // hardened-fs-exempt: this opens the handle that every later read is relative to
	if err != nil {
		return fmt.Errorf("open the scan root %s: %w", pkg, err)
	}
	// os.OpenRoot resolves its own argument, so the handle can name a
	// directory other than the one Lstat proved. Compare the two before
	// anything is read through it.
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return fmt.Errorf("the scan root %s changed between the check and the open", pkg)
	}
	// The walk lists and reads through descriptors opened from this one. A
	// walk over root.FS() resolves each directory again by name when it lists
	// it, and os.Root follows a symlink whose target stays inside the root, so
	// a directory swapped after its parent was listed could hand the scan a
	// decoy tree under the original name.
	top, err := root.Open(".")
	closeErr := root.Close()
	if err != nil {
		return fmt.Errorf("open the scan root %s: %w", pkg, err)
	}
	if closeErr != nil {
		_ = top.Close()
		return closeErr
	}
	scanned := 0
	err = walkHardenedGoSources(top, pkg, func(rel string, content []byte) error {
		scanned++
		return visit(rel, content)
	})
	closeErr = top.Close()
	if err != nil {
		return fmt.Errorf("scan %s for raw pathname references: %w", pkg, err)
	}
	if closeErr != nil {
		return closeErr
	}
	if scanned == 0 {
		return fmt.Errorf("no Go source under the scan root %s; refusing a vacuous pass", pkg)
	}
	return nil
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
//
// HardenedFSPackages exports the list so a test builds its fixture tree from
// the same names the check reads.
var hardenedFSPackages = []string{
	"internal/applecontainer",
	"internal/authpolicy",
	"internal/authresolve",
	"internal/colimautil",
	"internal/host",
	"internal/injection",
	"internal/runtimeutil",
	"internal/publishpr",
	"internal/sessionctl",
	"internal/supportbundle",
	"internal/transcript",
}

// hardenedFSSymbols lists the raw calls the hardened primitives replace. Each
// one resolves an operator-controlled path by name, so each one follows a
// symlink and races a rename between the check and the open.
//
// os.Lstat is deliberately absent: it does not follow the final symlink, so it
// is part of the answer rather than part of the defect.
var hardenedFSSymbols = map[string]bool{
	"Open":       true,
	"OpenRoot":   true,
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
	"Chtimes":    true,
	"Symlink":    true,
	"Link":       true,
	"Truncate":   true,
}

type hardenedFSKey struct {
	path   string
	symbol string
}

// HardenedFSFinding is one reference to a raw os pathname call.
type HardenedFSFinding struct {
	Line   int
	Column int
	Symbol string
}

// HardenedFSFindings reports each raw os file call in one Go source.
//
// It reads the syntax tree rather than the text. A text scan cannot resolve an
// aliased import such as stdos "os", cannot tell an exemption comment from the
// same words inside a string literal, and cannot see through parentheses. The
// parser answers all three exactly.
//
// It counts every reference to a banned symbol, not only a direct call. A
// function value carries the same authority: open := os.Open followed by
// open(path) resolves the path by name exactly as the direct call does.
func HardenedFSFindings(source string) ([]HardenedFSFinding, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "source.go", source, parser.ParseComments)
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
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		qualifier, ok := hardenedImportQualifier(selector)
		if !ok || qualifier.Name != name || !hardenedFSSymbols[selector.Sel.Name] {
			return true
		}
		position := fileSet.PositionFor(selector.Pos(), false)
		findings = append(findings, HardenedFSFinding{
			Line:   position.Line,
			Column: position.Column,
			Symbol: name + "." + selector.Sel.Name,
		})
		return true
	})
	sort.Slice(findings, func(i, j int) bool { return findings[i].Line < findings[j].Line })
	return applyHardenedFSExemptions(findings, exempt), nil
}

// applyHardenedFSExemptions clears the reference each exemption sits beside.
//
// A comment states the reason for the reference it follows, so it clears the
// nearest reference before it on its own line and nothing else. Clearing the
// first reference on the line instead would let one reason cover a different
// call than the one it names, and a second comment on the line would fall
// back to a reference it does not name, so a line with two exemption comments
// clears nothing.
func applyHardenedFSExemptions(findings []HardenedFSFinding, exempt map[int][]int) []HardenedFSFinding {
	cleared := map[int]bool{}
	for line, columns := range exempt {
		// One line states one reason. A second comment on the same line would
		// otherwise fall back to an earlier reference that it does not name.
		if len(columns) > 1 {
			continue
		}
		for _, column := range columns {
			best, bestColumn := -1, 0
			for index, finding := range findings {
				if cleared[index] || finding.Line != line || finding.Column >= column {
					continue
				}
				if best < 0 || finding.Column > bestColumn {
					best, bestColumn = index, finding.Column
				}
			}
			if best >= 0 {
				cleared[best] = true
			}
		}
	}
	kept := findings[:0]
	for index, finding := range findings {
		if !cleared[index] {
			kept = append(kept, finding)
		}
	}
	return kept
}

// hardenedImportQualifier returns the identifier that qualifies selector when
// it can name an imported package. The parser resolves each identifier to the
// declaration in this file that it names, so a parameter or local value that
// shadows an import name resolves to that declaration and is not the package.
// An import name is never resolved: another file cannot redeclare it either,
// because a package-level name that matches an import fails to compile.
//
// ponytail: this reads the parser's legacy object resolution, which go/ast
// marks deprecated. Move to go/types if a Go release removes it.
func hardenedImportQualifier(selector *ast.SelectorExpr) (*ast.Ident, bool) {
	qualifier, ok := ast.Unparen(selector.X).(*ast.Ident)
	return qualifier, ok && qualifier.Obj == nil
}

// hardenedFSOSImportName returns the name that qualifies a call of the os
// package in this file, or the empty string when the file does not import it.
// A dot import is refused: it removes the qualifier that the scan reads, so
// the rule would silently stop applying to the file.
func hardenedFSOSImportName(file *ast.File) (string, error) {
	for _, spec := range file.Imports {
		if spec.Path == nil {
			continue
		}
		// Go accepts a raw string and an escaped string for an import path, so
		// the literal spelling is not the path. Unquote it before comparing.
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "os" {
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

// hardenedFSExemptLines returns the column of each reasoned exemption comment,
// by physical line. A //line directive renames a logical line, so two
// positions in different parts of the file can report the same logical one.
// The reason is required: a bare tag states nothing.
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

func hardenedFSExemptLines(fileSet *token.FileSet, file *ast.File) map[int][]int {
	lines := map[int][]int{}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !hardenedFSExemptionReason(comment.Text) {
				continue
			}
			position := fileSet.PositionFor(comment.Pos(), false)
			lines[position.Line] = append(lines[position.Line], position.Column)
		}
	}
	return lines
}

// walkHardenedGoSources hands visit each non-test Go source below dir, in
// name order, skipping testdata. Each child directory is opened relative to
// dir without following a symlink, and each source is read relative to dir
// with rootio's no-follow leaf read, so no name is resolved twice. A symlink
// entry fails the walk whatever its name: skipping one would hide a symlinked
// package directory, whose sources Go still builds through the link.
func walkHardenedGoSources(dir *os.File, rel string, visit func(rel string, content []byte) error) error {
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		base := entry.Name()
		name := rel + "/" + base
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; the hardened filesystem rule cannot scan behind it", name)
		}
		if entry.IsDir() {
			if base == "testdata" {
				continue
			}
			child, openErr := rootio.OpenDirectoryAtNoFollow(dir, base)
			if openErr != nil {
				return fmt.Errorf("open %s: %w", name, openErr)
			}
			walkErr := walkHardenedGoSources(child, name, visit)
			_ = child.Close()
			if walkErr != nil {
				return walkErr
			}
			continue
		}
		if !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
			continue
		}
		content, readErr := rootio.ReadFileAtNoFollow(dir, base, name, hardenedFSMaxSourceBytes)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", name, readErr)
		}
		if err := visit(name, content); err != nil {
			return err
		}
	}
	return nil
}

// HardenedFSPackages returns the trust-boundary package list.
func HardenedFSPackages() []string {
	return append([]string(nil), hardenedFSPackages...)
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
