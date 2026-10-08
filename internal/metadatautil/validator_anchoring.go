// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/omkhar/workcell/internal/rootio"
)

// CheckValidatorAnchoring requires each validator that anchors on the shared
// shell-invocation parser to run the shared evasion corpus. A validator that
// reads a command out of file content is bypassed by a comment, a heredoc body
// or a longer option unless a negative fixture proves otherwise, so the two
// counts must stay equal.
//
// The check counts call sites with a text scan rather than reading the syntax
// tree, the same choice the other checks in this package record: the call sites
// are few, and a scan avoids go/ast for one caller.
func CheckValidatorAnchoring(rootDir string) error {
	anchors, err := countCallSites(rootDir, "ShellInvocations", false)
	if err != nil {
		return err
	}
	corpus, err := countCallSites(rootDir, "RequireRejectsAllEvasions", true)
	if err != nil {
		return err
	}
	if anchors == 0 {
		return errors.New("no validator anchors on the shared shell-invocation parser; the anchoring check has lost its subject")
	}
	if anchors != corpus {
		return fmt.Errorf("validator anchoring parity: %d call(s) of ShellInvocations but %d run(s) of the shared evasion corpus; every anchored validator needs one RequireRejectsAllEvasions run", anchors, corpus)
	}
	return checkTextMatchBaseline(rootDir)
}

const (
	textMatchBaselinePath = "policy/validator-anchoring-baseline.tsv"
	textMatchMaxReported  = 20
)

// textMatchPackages hold the validators, and internal/testkit keeps its
// validators in test files, so test sources are scanned too. A function there
// that matches text in content is the class a comment, a heredoc or a longer
// option bypasses.
var textMatchPackages = []string{"internal/adapters", "internal/metadatautil", "internal/testkit", "internal/workcellhardening"}

// checkTextMatchBaseline is a ratchet. Every function in textMatchPackages that
// matches text must have a row in the baseline with its number of match calls
// and a reason. A new function fails, a row whose count differs fails, and a
// row whose function no longer matches text fails too, so the count only goes
// down.
func checkTextMatchBaseline(rootDir string) error {
	found := map[string]int{}
	for _, pkg := range textMatchPackages {
		// An alias declared in one file is called in another, so each package
		// directory is read as a whole.
		packages := map[string]map[string]string{}
		root, err := os.OpenRoot(filepath.Join(rootDir, pkg)) // hardened-fs-exempt: every read below is relative to this handle
		if err != nil {
			return fmt.Errorf("open validator package %s: %w", pkg, err)
		}
		scanned := 0
		err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("%s/%s is not a regular file", pkg, name)
			}
			content, err := hardenedFSReadSource(root, name, pkg+"/"+name)
			if err != nil {
				return err
			}
			scanned++
			dir := path.Join(pkg, path.Dir(name))
			if packages[dir] == nil {
				packages[dir] = map[string]string{}
			}
			packages[dir][pkg+"/"+name] = string(content)
			return nil
		})
		closeErr := root.Close()
		if err != nil {
			return fmt.Errorf("scan %s for text-matching validators: %w", pkg, err)
		}
		if closeErr != nil {
			return closeErr
		}
		if scanned == 0 {
			return fmt.Errorf("no Go source under the validator package %s; refusing a vacuous pass", pkg)
		}
		for dir, sources := range packages {
			functions, err := TextMatchingFunctions(sources)
			if err != nil {
				return err
			}
			for _, function := range functions {
				found[dir+"\t"+function]++
			}
		}
	}
	baseline, err := loadTextMatchBaseline(filepath.Join(rootDir, textMatchBaselinePath))
	if err != nil {
		return err
	}
	var failures []string
	for key, calls := range found {
		listed, ok := baseline[key]
		switch {
		case !ok:
			failures = append(failures, strings.Replace(key, "\t", ".", 1)+" matches text; parse the content it sees (ShellInvocations, a closed decoder) or add a baseline row with a reason")
		case listed != calls:
			failures = append(failures, fmt.Sprintf("%s has %d match call(s) but its baseline row says %d; parse the new match or update the row", strings.Replace(key, "\t", ".", 1), calls, listed))
		}
	}
	for key := range baseline {
		if _, ok := found[key]; !ok {
			failures = append(failures, strings.Replace(key, "\t", ".", 1)+" no longer matches text; remove its stale baseline row")
		}
	}
	if len(failures) == 0 {
		return nil
	}
	slices.Sort(failures)
	if extra := len(failures) - textMatchMaxReported; extra > 0 {
		failures = append(failures[:textMatchMaxReported], fmt.Sprintf("and %d more", extra))
	}
	return fmt.Errorf("%s ratchet failed:\n  %s", textMatchBaselinePath, strings.Join(failures, "\n  "))
}

// loadTextMatchBaseline reads PACKAGE<TAB>FUNCTION<TAB>CALLS<TAB>REASON rows.
// A row with no reason, a count that is not positive, or a repeated row, is an
// error.
func loadTextMatchBaseline(baselinePath string) (map[string]int, error) {
	content, err := rootio.ReadFileNoFollow(baselinePath, textMatchBaselinePath, hardenedFSMaxSourceBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", textMatchBaselinePath, err)
	}
	baseline := map[string]int{}
	for number, line := range strings.Split(string(content), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || strings.TrimSpace(fields[3]) == "" {
			return nil, fmt.Errorf("%s:%d: expected PACKAGE<TAB>FUNCTION<TAB>CALLS<TAB>REASON", textMatchBaselinePath, number+1)
		}
		calls, err := strconv.Atoi(fields[2])
		if err != nil || calls < 1 {
			return nil, fmt.Errorf("%s:%d: CALLS must be a positive count", textMatchBaselinePath, number+1)
		}
		key := fields[0] + "\t" + fields[1]
		if _, ok := baseline[key]; ok {
			return nil, fmt.Errorf("%s:%d: repeated row for %s.%s", textMatchBaselinePath, number+1, fields[0], fields[1])
		}
		baseline[key] = calls
	}
	return baseline, nil
}

// TextMatchingFunctions returns each function in the sources of one package,
// keyed by file name, as Name or Type.Name, that matches text with strings or
// bytes Contains, HasPrefix or Index, or with regexp, and each package-level
// variable whose initializer does. A name
// appears once per match call, so a new match in a listed function changes
// its count. A package is known by its import path, so an alias or a dot
// import does not hide it, and a call through a function value bound to a
// match, as contains := strings.Contains binds one, is a match too. It does not
// ask where the text came from. Content read inside the match call, or read in
// one function and matched in a helper, is the same bypassable class, and only
// counting every match never misses one. A call whose arguments are all
// literals, such as regexp.MustCompile of a constant, or that searches an error
// message, sees no content and is skipped.
//
// ponytail: counting every match lists matches on decoded fields and names as
// well, so the baseline is longer than the debt; that is the price of a scan
// that cannot miss.
func TextMatchingFunctions(sources map[string]string) ([]string, error) {
	fileSet := token.NewFileSet()
	var files []*ast.File
	var imports []map[string]string
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		file, err := parser.ParseFile(fileSet, name, sources[name], parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		fileImports, err := importNames(file)
		if err != nil {
			return nil, err
		}
		files, imports = append(files, file), append(imports, fileImports)
	}
	aliases := matchAliases(files, imports)
	var names []string
	for index, file := range files {
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				if typed.Body != nil {
					for range contentMatches(typed.Body, imports[index], aliases) {
						names = append(names, receiverPrefix(typed)+typed.Name.Name)
					}
				}
			case *ast.GenDecl:
				for _, spec := range typed.Specs {
					if value, ok := spec.(*ast.ValueSpec); ok {
						for range contentMatches(value, imports[index], aliases) {
							names = append(names, value.Names[0].Name)
						}
					}
				}
			}
		}
	}
	return names, nil
}

// importNames maps each local package name in file to the package it imports.
// A dot import is under "." and its path, and its functions have bare names.
func importNames(file *ast.File) (map[string]string, error) {
	imports := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		local := path.Base(importPath)
		if spec.Name != nil {
			local = spec.Name.Name
		}
		if local == "." {
			local += importPath
		}
		imports[local] = path.Base(importPath)
	}
	return imports, nil
}

// matchAliases returns each name in the package bound to a match function
// value, by := , = or var, as in var has = strings.HasPrefix. It repeats
// until no name is new, so an alias of an alias in another file counts too.
// ponytail: a name is an alias package-wide, scope ignored, so a shadowing
// local of the same name over-counts; that only lists more.
func matchAliases(files []*ast.File, imports []map[string]string) map[string]bool {
	aliases := map[string]bool{}
	for added := true; added; {
		added = false
		bind := func(name ast.Expr, value ast.Expr, imports map[string]string) {
			if ident, ok := ast.Unparen(name).(*ast.Ident); ok && ident.Name != "_" &&
				!aliases[ident.Name] && namesMatch(value, imports, aliases) {
				aliases[ident.Name], added = true, true
			}
		}
		for index, file := range files {
			ast.Inspect(file, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.AssignStmt:
					for at := range min(len(typed.Lhs), len(typed.Rhs)) {
						bind(typed.Lhs[at], typed.Rhs[at], imports[index])
					}
				case *ast.ValueSpec:
					for at := range min(len(typed.Names), len(typed.Values)) {
						bind(typed.Names[at], typed.Values[at], imports[index])
					}
				}
				return true
			})
		}
	}
	return aliases
}

// contentMatches returns the number of text matches in node that can see
// content.
func contentMatches(node ast.Node, imports map[string]string, aliases map[string]bool) int {
	count := 0
	ast.Inspect(node, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && !seesNoContent(call) && namesMatch(call.Fun, imports, aliases) {
			count++
		}
		return true
	})
	return count
}

// namesMatch reports whether a callee or a function value names a text match:
// a package function, a method, or a name in aliases.
func namesMatch(callee ast.Expr, imports map[string]string, aliases map[string]bool) bool {
	if ident, ok := ast.Unparen(callee).(*ast.Ident); ok && aliases[ident.Name] {
		return true
	}
	pkg, name := calleeName(callee)
	if imported, ok := imports[pkg]; ok {
		pkg = imported
	}
	matched := matchesText(pkg, name)
	for local, imported := range imports {
		matched = matched || pkg == "" && local[0] == '.' && matchesText(imported, name)
	}
	return matched
}

// seesNoContent reports whether a match call searches an error message, as a
// test assertion does, or has only literal arguments.
func seesNoContent(call *ast.CallExpr) bool {
	if len(call.Args) > 0 {
		if subject, ok := call.Args[0].(*ast.CallExpr); ok {
			if _, name := calleeName(subject.Fun); name == "Error" && len(subject.Args) == 0 {
				return true
			}
		}
	}
	for _, argument := range call.Args {
		if _, literal := argument.(*ast.BasicLit); !literal {
			return false
		}
	}
	return true
}

// calleeName returns the package or receiver identifier and the name a callee
// names, such as strings and Contains, or "" and the name of a plain call.
func calleeName(callee ast.Expr) (string, string) {
	switch callee := ast.Unparen(callee).(type) {
	case *ast.Ident:
		return "", callee.Name
	case *ast.SelectorExpr:
		if ident, ok := callee.X.(*ast.Ident); ok {
			return ident.Name, callee.Sel.Name
		}
		return "", callee.Sel.Name
	}
	return "", ""
}

// matchesText reports whether a call matches text: a strings or bytes search,
// any regexp function, or a method that only a compiled regexp has.
func matchesText(pkg, name string) bool {
	switch pkg {
	case "strings", "bytes":
		return strings.HasPrefix(name, "Contains") || name == "HasPrefix" ||
			strings.HasPrefix(name, "Index") || strings.HasPrefix(name, "LastIndex")
	case "regexp":
		return true
	case "filepath", "path", "slices", "maps":
		return false
	}
	return strings.HasPrefix(name, "Match") || strings.HasPrefix(name, "Find") || strings.HasPrefix(name, "ReplaceAllString")
}

// receiverPrefix returns "Type." for a method and "" for a function.
func receiverPrefix(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return ""
	}
	expression := function.Recv.List[0].Type
	for {
		switch typed := expression.(type) {
		case *ast.StarExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		case *ast.Ident:
			return typed.Name + "."
		default:
			return ""
		}
	}
}

// countCallSites returns the number of calls of needle under internal/. It
// counts every occurrence on a line, not one per line, so a second call added
// to an existing line still needs its own corpus run.
// It reads test sources when inTests is set and non-test sources otherwise, it
// removes comments and string literals first so that text about a call is not
// counted as one, and it reads a declaration line from its body onwards so that
// a function is never counted as its own caller while a call written in a
// one-line body still is.
func countCallSites(rootDir, needle string, inTests bool) (int, error) {
	count := 0
	root := filepath.Join(rootDir, "internal")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") != inTests {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for line := range strings.Lines(dropCommentsAndLiterals(string(content))) {
			count += countCalls(afterDeclaration(line), needle)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan %s for %s: %w", root, needle, err)
	}
	return count, nil
}

// dropCommentsAndLiterals blanks the comments and string literals of Go source
// and keeps every newline, so a line number and the code on it survive. A
// comment or a literal that names a call is text about the call, not the call,
// and counting it would let a mention satisfy the parity gate or break it.
//
// A byte scan is enough for this one caller, the same choice this package
// records for structJSONFields, and it avoids go/ast.
func dropCommentsAndLiterals(source string) string {
	var out strings.Builder
	out.Grow(len(source))
	// state is 0 in code, or the byte that ends the current span.
	var state byte
	var lineComment bool
	for index := 0; index < len(source); index++ {
		character := source[index]
		switch {
		case character == '\n':
			// A block comment and a raw literal run past a newline, so their
			// state survives it. An interpreted literal cannot, so a newline
			// inside one means the scan has lost its place; resync there.
			lineComment = false
			if state == '"' || state == '\'' {
				state = 0
			}
			out.WriteByte(character)
		case lineComment:
			out.WriteByte(' ')
		case state == '*':
			if character == '*' && index+1 < len(source) && source[index+1] == '/' {
				index++
				state = 0
				out.WriteString("  ")
				continue
			}
			out.WriteByte(' ')
		case state != 0:
			// A backslash escapes the next byte in an interpreted literal. A
			// raw literal has no escapes, so only " and ' take this branch.
			if character == '\\' && state != '`' && index+1 < len(source) {
				index++
				out.WriteString("  ")
				continue
			}
			if character == state {
				state = 0
			}
			out.WriteByte(' ')
		case character == '/' && index+1 < len(source) && source[index+1] == '/':
			lineComment = true
			out.WriteString("  ")
			index++
		case character == '/' && index+1 < len(source) && source[index+1] == '*':
			state = '*'
			out.WriteString("  ")
			index++
		case character == '"' || character == '\'' || character == '`':
			state = character
			out.WriteByte(' ')
		default:
			out.WriteByte(character)
		}
	}
	return out.String()
}

// afterDeclaration returns the part of a line that can hold a call. On a
// declaration line that is the body after the opening brace, so that
// func ShellInvocations(...) is not a call of itself while a one-line body such
// as func validate() { ShellInvocations(...) } still is. Every other line is
// returned whole.
func afterDeclaration(line string) string {
	if !strings.HasPrefix(line, "func ") {
		return line
	}
	_, body, found := strings.Cut(line, "{")
	if !found {
		return ""
	}
	return body
}

// countCalls returns how many times text calls the function named name. The
// match must be the whole identifier, so neither cachedShellInvocations nor a
// name that begins with a letter outside ASCII is read as the call, and the
// argument list must follow it. A comment between the callee and that list is
// blanked to spaces by then, so the spaces are skipped before it is required.
func countCalls(text, name string) int {
	count, offset := 0, 0
	for {
		at := strings.Index(text[offset:], name)
		if at < 0 {
			return count
		}
		at += offset
		offset = at + len(name)
		previous, _ := utf8.DecodeLastRuneInString(text[:at])
		if (at == 0 || !isIdentifierRune(previous)) &&
			strings.HasPrefix(strings.TrimLeft(text[offset:], " \t"), "(") {
			count++
		}
	}
}

// isIdentifierRune reports whether the rune can continue a Go identifier. Go
// takes any Unicode letter or digit, so an ASCII test reads a name such as
// 偽RequireRejectsAllEvasions as a call of the one it ends with.
func isIdentifierRune(character rune) bool {
	return character == '_' || unicode.IsLetter(character) || unicode.IsDigit(character)
}
