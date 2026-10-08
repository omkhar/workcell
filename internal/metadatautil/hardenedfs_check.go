// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	found := map[hardenedFSKey][]HardenedFSFinding{}
	for _, pkg := range hardenedFSPackages {
		err := scanHardenedGoSources(rootDir, pkg, func(rel string, content []byte) error {
			fileFindings, scanErr := HardenedFSFindings(string(content))
			if scanErr != nil {
				return fmt.Errorf("%s: %w", rel, scanErr)
			}
			for _, finding := range fileFindings {
				key := hardenedFSKey{path: rel, symbol: finding.Symbol}
				found[key] = append(found[key], finding)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	baseline, err := loadHardenedBaseline(rootDir, hardenedFSBaselinePath, false)
	if err != nil {
		return err
	}
	failures := compareHardenedBaseline(found, baseline, func(string) string {
		return "use internal/rootio, or state the reason with // " + hardenedFSExemptTag + " <reason>"
	})
	if len(failures) == 0 {
		return nil
	}
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

// compareHardenedBaseline returns one sorted failure per call that its
// baseline row does not list and per listed call the file no longer makes.
// advice names the fix for a symbol. A row lists call identities, not a count,
// so a reviewed call cannot be swapped for a new one under the same total.
func compareHardenedBaseline(found map[hardenedFSKey][]HardenedFSFinding, baseline map[hardenedFSKey][]string, advice func(symbol string) string) []string {
	var failures []string
	for key, findings := range found {
		listed := map[string]int{}
		for _, call := range baseline[key] {
			listed[call]++
		}
		var unlisted []string
		for _, finding := range findings {
			// A count row lists unnamed entries, and each one admits any call.
			for _, call := range []string{finding.Call, ""} {
				if listed[call] > 0 {
					listed[call]--
					finding.Call = ""
					break
				}
			}
			if finding.Call == "" {
				continue
			}
			unlisted = append(unlisted, fmt.Sprintf("%s:%d: %s call %s", key.path, finding.Line, key.symbol, finding.Call))
		}
		if len(unlisted) > hardenedFSMaxReported {
			unlisted = unlisted[:hardenedFSMaxReported]
		}
		if len(unlisted) > 0 {
			failures = append(failures, fmt.Sprintf("%s: %s call not in the baseline row; %s\n    %s",
				key.path, key.symbol, advice(key.symbol), strings.Join(unlisted, "\n    ")))
		}
		for call, left := range listed {
			// An unused entry is headroom that a later change could spend.
			for ; left > 0; left-- {
				failures = append(failures, fmt.Sprintf(
					"%s: stale baseline entry %q for %s; the file no longer makes that call, so remove it or lower the count", key.path, call, key.symbol))
			}
		}
	}
	for key, calls := range baseline {
		if _, ok := found[key]; !ok {
			failures = append(failures, fmt.Sprintf(
				"%s: stale baseline row for %s (%s); the file no longer makes that call, so remove the row", key.path, key.symbol, strings.Join(calls, ",")))
		}
	}
	sort.Strings(failures)
	return failures
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

// HardenedFSFinding is one reference to a raw os pathname call. Call is the
// identity a baseline row lists for it: a hash of the enclosing function name
// and the call's source text, so an edit elsewhere in the file leaves it alone.
type HardenedFSFinding struct {
	Line   int
	Column int
	Symbol string
	Call   string
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
	name, err := hardenedFSImportName(file, "os")
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, nil // the file does not import os
	}
	exempt := hardenedFSExemptLines(fileSet, file)
	findings := hardenedFindings(fileSet, file, source, func(qualifier, symbol string) string {
		if qualifier != name || !hardenedFSSymbols[symbol] {
			return ""
		}
		return name + "." + symbol
	})
	return applyHardenedFSExemptions(findings, exempt), nil
}

// hardenedFindings reports each package selector that symbolOf names, sorted
// by line. symbolOf returns the empty string for a selector that is allowed.
func hardenedFindings(fileSet *token.FileSet, file *ast.File, source string, symbolOf func(qualifier, symbol string) string) []HardenedFSFinding {
	var findings []HardenedFSFinding
	for _, decl := range file.Decls {
		scope := ""
		if function, ok := decl.(*ast.FuncDecl); ok {
			scope = function.Name.Name
			if function.Recv != nil && len(function.Recv.List) > 0 {
				scope = types.ExprString(function.Recv.List[0].Type) + "." + scope
			}
		}
		var stack []ast.Node
		ast.Inspect(decl, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, node)
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			qualifier, ok := hardenedImportQualifier(selector)
			if !ok {
				return true
			}
			symbol := symbolOf(qualifier.Name, selector.Sel.Name)
			if symbol == "" {
				return true
			}
			position := fileSet.PositionFor(selector.Pos(), false)
			findings = append(findings, HardenedFSFinding{
				Line:   position.Line,
				Column: position.Column,
				Symbol: symbol,
				Call:   hardenedCallIdentity(fileSet, source, scope, selector, stack),
			})
			return true
		})
	}
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Line < findings[j].Line })
	return findings
}

// hardenedCallIdentity hashes the enclosing function name and the source text
// of the call that selector heads, or of selector itself when it is a value.
// The text is reprinted from the syntax tree with no positions, so a reflow
// keeps the identity while a changed literal breaks it. A line number is not
// part of it, because an unrelated edit above the call would move it.
func hardenedCallIdentity(_ *token.FileSet, _, scope string, selector *ast.SelectorExpr, stack []ast.Node) string {
	var expr ast.Node = selector
	parent := len(stack) - 2
	for parent >= 0 {
		if _, paren := stack[parent].(*ast.ParenExpr); !paren {
			break
		}
		parent--
	}
	if parent >= 0 {
		if call, ok := stack[parent].(*ast.CallExpr); ok && ast.Unparen(call.Fun) == selector {
			expr = call
		}
	}
	var text strings.Builder
	_ = printer.Fprint(&text, token.NewFileSet(), expr) // a parsed expression always prints
	sum := sha256.Sum256([]byte(scope + "\x00" + text.String()))
	return hex.EncodeToString(sum[:4])
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

// hardenedFSImportName returns the name that qualifies a call of the package
// at importPath in this file, or the empty string when the file does not
// import it.
// A dot import is refused: it removes the qualifier that the scan reads, so
// the rule would silently stop applying to the file.
func hardenedFSImportName(file *ast.File, importPath string) (string, error) {
	for _, spec := range file.Imports {
		if spec.Path == nil {
			continue
		}
		// Go accepts a raw string and an escaped string for an import path, so
		// the literal spelling is not the path. Unquote it before comparing.
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		if spec.Name == nil {
			return importPath[strings.LastIndex(importPath, "/")+1:], nil
		}
		if spec.Name.Name == "." || spec.Name.Name == "_" {
			return "", fmt.Errorf("the %s import uses the %q form; write it as a plain import or an alias so the hardened filesystem rule can read its calls", importPath, spec.Name.Name)
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

// loadHardenedBaseline reads a ratchet file. An identified file has
// PATH<TAB>SYMBOL<TAB>CALLS<TAB>REASON rows, where CALLS lists one identity per
// reviewed call. Otherwise a row is PATH<TAB>SYMBOL<TAB>COUNT, read as COUNT
// unnamed entries. The line is trimmed first, so an empty trailing reason
// leaves too few fields.
//
// ponytail: policy/hardened-fs-baseline.tsv still counts, so one call can be
// swapped for another in the same file. Give it identities in its own change.
func loadHardenedBaseline(rootDir, rel string, identified bool) (map[hardenedFSKey][]string, error) {
	content, err := rootio.ReadFileNoFollow(filepath.Join(rootDir, rel), rel, hardenedFSMaxSourceBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	want := 3
	if identified {
		want = 4
	}
	baseline := map[hardenedFSKey][]string{}
	for number, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != want {
			return nil, fmt.Errorf("%s:%d: expected %d tab-separated fields, found %d", rel, number+1, want, len(fields))
		}
		var calls []string
		if identified {
			calls = strings.Split(fields[2], ",")
			for _, call := range calls {
				if !hardenedCallIdentityPattern.MatchString(call) {
					return nil, fmt.Errorf("%s:%d: call identity must be 8 lowercase hex digits, found %q", rel, number+1, call)
				}
			}
		} else {
			count, convErr := strconv.Atoi(fields[2])
			if convErr != nil || count <= 0 {
				return nil, fmt.Errorf("%s:%d: count must be a positive integer, found %q", rel, number+1, fields[2])
			}
			calls = make([]string, count)
		}
		key := hardenedFSKey{path: fields[0], symbol: fields[1]}
		if _, dup := baseline[key]; dup {
			return nil, fmt.Errorf("%s:%d: duplicate row for %s %s", rel, number+1, fields[0], fields[1])
		}
		baseline[key] = calls
	}
	return baseline, nil
}

var hardenedCallIdentityPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)
