// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// CheckHardenedIO rejects a symlink-following path call anywhere in the Go
// tree outside internal/rootio.
//
// CheckHardenedFS covers the trust-boundary packages with a wide symbol list.
// This check covers every non-test source under cmd/ and internal/ with the
// narrow list that review history shows agents reach for first. Each call
// resolves a path by name, so it follows a symlink and races a rename.
//
// policy/hardened-io-baseline.tsv records the calls the tree carries today,
// one row per file and symbol, each with a reason. A count that does not match
// its row fails, so the count can only go down. There is no inline exemption:
// a new call needs a reviewed baseline row.
func CheckHardenedIO(rootDir string) error {
	counts := map[hardenedFSKey]int{}
	details := map[hardenedFSKey][]string{}
	for _, tree := range []string{"cmd", "internal"} {
		err := scanHardenedGoSources(rootDir, tree, func(rel string, content []byte) error {
			if strings.HasPrefix(rel, hardenedIOAllowedTree) {
				return nil
			}
			findings, err := HardenedIOFindings(string(content))
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			for _, finding := range findings {
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
	baseline, err := loadHardenedBaseline(rootDir, hardenedIOBaselinePath, true)
	if err != nil {
		return err
	}
	failures := compareHardenedBaseline(counts, details, baseline, func(symbol string) string {
		_, name, _ := strings.Cut(symbol, ".")
		return "use " + hardenedIOReplacements[name] + ", or add a row with a reason to " + hardenedIOBaselinePath
	})
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("hardened I/O check failed:\n  %s", strings.Join(failures, "\n  "))
}

const (
	hardenedIOBaselinePath = "policy/hardened-io-baseline.tsv"
	hardenedIOAllowedTree  = "internal/rootio/"
)

// hardenedIOReplacements maps each banned symbol to the internal/rootio form
// that replaces it. The keys are the banned set: os.Glob does not exist, so
// one map serves both packages.
var hardenedIOReplacements = map[string]string{
	"ReadFile":  "rootio.ReadFileNoFollow",
	"WriteFile": "rootio.WriteFileAtomicAtNoFollow on a parent from rootio.OpenParentDirectoryNoFollow",
	"Open":      "rootio.OpenParentDirectoryNoFollow with rootio.OpenRegularFileAtNoFollow",
	"OpenFile":  "rootio.OpenParentDirectoryNoFollow with rootio.OpenRegularFileAtNoFollow",
	"Create":    "rootio.StageAndCreateAt on a parent from rootio.OpenParentDirectoryNoFollow",
	"Stat":      "os.Lstat, or Stat on a handle from rootio.OpenRegularFileAtNoFollow",
	"RemoveAll": "os.Root.RemoveAll on a root bound to the parent from rootio.OpenParentDirectoryNoFollow",
	"Glob":      "ReadDir on a parent from rootio.OpenParentDirectoryNoFollow, then filepath.Match on each name",
}

// HardenedIOFindings reports each reference to a banned os or path/filepath
// symbol in one Go source. Like HardenedFSFindings it reads the syntax tree,
// so it resolves an aliased import, ignores comments and string literals, and
// counts a function value as a reference.
func HardenedIOFindings(source string) ([]HardenedFSFinding, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "source.go", source, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse the Go source: %w", err)
	}
	osName, err := hardenedFSImportName(file, "os")
	if err != nil {
		return nil, err
	}
	filepathName, err := hardenedFSImportName(file, "path/filepath")
	if err != nil {
		return nil, err
	}
	var findings []HardenedFSFinding
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		qualifier, ok := ast.Unparen(selector.X).(*ast.Ident)
		if !ok {
			return true
		}
		symbol := selector.Sel.Name
		banned := (qualifier.Name == osName && symbol != "Glob" && hardenedIOReplacements[symbol] != "") ||
			(qualifier.Name == filepathName && symbol == "Glob")
		if !banned {
			return true
		}
		position := fileSet.PositionFor(selector.Pos(), false)
		findings = append(findings, HardenedFSFinding{
			Line:   position.Line,
			Column: position.Column,
			Symbol: hardenedIOCanonical(qualifier.Name, osName, symbol),
		})
		return true
	})
	sort.Slice(findings, func(i, j int) bool { return findings[i].Line < findings[j].Line })
	return findings, nil
}

// hardenedIOCanonical names a finding by package, not by local alias, so a
// baseline row survives an import rename.
func hardenedIOCanonical(qualifier, osName, symbol string) string {
	if qualifier == osName {
		return "os." + symbol
	}
	return "filepath." + symbol
}
