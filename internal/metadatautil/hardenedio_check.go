// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"go/parser"
	"go/token"
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
// one row per file and symbol, each with a reason. A row lists the identity of
// each call it admits, so a call it does not list fails, and so does a listed
// call the file no longer makes. There is no inline exemption: a new call
// needs a reviewed baseline entry.
func CheckHardenedIO(rootDir string) error {
	found := map[hardenedFSKey][]HardenedFSFinding{}
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
				found[key] = append(found[key], finding)
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
	failures := compareHardenedBaseline(found, baseline, func(symbol string) string {
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
	"Open":      "rootio.OpenRegularFileAtNoFollow on a parent from rootio.OpenParentDirectoryNoFollow; a directory needs rootio.OpenDirectoryAtNoFollow on that parent",
	"OpenFile":  "rootio.OpenRegularFileAtNoFollow on a parent from rootio.OpenParentDirectoryNoFollow for a read-only open; a write, create or truncate needs rootio.StageAndPublishAt or rootio.StageAndCreateAt on that parent; an append has no rootio owner yet",
	"Create":    "rootio.StageAndCreateAt on a parent from rootio.OpenParentDirectoryNoFollow",
	"Stat":      "os.Lstat, or Stat on a handle from rootio.OpenRegularFileAtNoFollow",
	"RemoveAll": "rootio.RemoveAllAtNoFollow on a parent from rootio.OpenParentDirectoryNoFollow",
	"Glob":      "ReadDir on a parent from rootio.OpenParentDirectoryNoFollow, then filepath.Match on each name",
}

// HardenedIOFindings reports each reference to a banned os or path/filepath
// symbol in one Go source. Like HardenedFSFindings it reads the syntax tree,
// so it resolves an aliased import, ignores comments and string literals, and
// counts a function value as a reference.
func HardenedIOFindings(source string) ([]HardenedFSFinding, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "source.go", source, 0)
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
	return hardenedFindings(fileSet, file, source, func(qualifier, symbol string) string {
		banned := (qualifier == osName && symbol != "Glob" && hardenedIOReplacements[symbol] != "") ||
			(qualifier == filepathName && symbol == "Glob")
		if !banned {
			return ""
		}
		return hardenedIOCanonical(qualifier, osName, symbol)
	}), nil
}

// hardenedIOCanonical names a finding by package, not by local alias, so a
// baseline row survives an import rename.
func hardenedIOCanonical(qualifier, osName, symbol string) string {
	if qualifier == osName {
		return "os." + symbol
	}
	return "filepath." + symbol
}
