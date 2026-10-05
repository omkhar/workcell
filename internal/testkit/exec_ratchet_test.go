// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// etxtbsyRetryHelpers are the only helpers whose func-literal argument is
// exempt from the ratchet: each one guarantees an ETXTBSY retry. A near-match
// such as execRetryOn with an arbitrary predicate is counted like a raw call.
var etxtbsyRetryHelpers = map[string]bool{
	"execRetryETXTBSY":             true,
	"execRetryETXTBSYOrNestedBusy": true,
}

// systemBinaryDirs are the directories no test writes to, so a literal under
// one of them names an installed binary, never a freshly written fixture.
var systemBinaryDirs = []string{"/bin/", "/sbin/", "/usr/bin/", "/usr/sbin/"}

// systemTools are the installed tools testkit resolves on PATH. Any other
// slashless name ("fixture") can resolve to a freshly written executable, and
// so can a systemTools name once the file rewrites the process PATH, because
// exec.Command resolves the program through the test process environment.
var systemTools = map[string]bool{
	"bash": true, "chmod": true, "cp": true, "git": true, "go": true, "shasum": true, "true": true,
}

// stableProgramLiteral reports whether a string literal names a tool in
// systemTools ("git") in a file that leaves the process PATH alone, or a
// binary under systemBinaryDirs ("/bin/bash"). Any other literal, slashless
// ("fixture"), relative ("./fixture") or absolute ("/tmp/fixture.sh"), can
// still name a freshly written executable.
func stableProgramLiteral(lit *ast.BasicLit, pathRewritten bool) bool {
	if lit.Kind != token.STRING {
		return false
	}
	prog, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	if systemTools[prog] {
		return !pathRewritten
	}
	// A traversal such as "/bin/../../tmp/fixture.sh" resolves outside the
	// system directory, so only an already-clean literal can be exempt.
	if path.Clean(prog) != prog {
		return false
	}
	for _, dir := range systemBinaryDirs {
		if strings.HasPrefix(prog, dir) {
			return true
		}
	}
	return false
}

// rawExecSites counts exec.Command/CommandContext calls in src whose program
// is a path-valued expression or a non-stable literal (anything but a
// systemTools name or a systemBinaryDirs path) and that sit outside a func literal
// handed to one of etxtbsyRetryHelpers. Such a call execs a possibly freshly written fixture
// directly and can fail with ETXTBSY (golang/go#22315). The os/exec package
// is matched by its import path, so an import alias is still counted. A
// reference to exec.Command that is not a call (a saved function value) is
// counted too, because whatever it later runs is not checked here.
func rawExecSites(t *testing.T, name, src string) int {
	return rawExecSitesIn(t, name, src, false, nil)
}

// packageRawExecSites counts the raw sites of every file in sources together:
// a PATH rewrite or a saved exec.Command alias declared in one file reaches the
// others, so each file is counted with the package-wide rewrite flag and alias
// set. It returns the count per file name.
func packageRawExecSites(t *testing.T, sources map[string]string) map[string]int {
	t.Helper()
	var parsed []*ast.File
	for name, src := range sources {
		file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed = append(parsed, file)
	}
	packageAliases := packageCommandAliases(parsed)
	rewrites := packageRewritesProcessPath(parsed)
	counts := map[string]int{}
	for name, src := range sources {
		if n := rawExecSitesIn(t, name, src, rewrites, packageAliases); n > 0 {
			counts[name] = n
		}
	}
	return counts
}

// execCommandNameIn returns the function that reports "Command" or
// "CommandContext" when an expression names that os/exec function in file: a
// selector through the package name as imported there, or a bare identifier
// under a dot import.
func execCommandNameIn(file *ast.File) (func(ast.Expr) string, string) {
	execPkg := "exec"
	for _, imp := range file.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil && path == "os/exec" && imp.Name != nil {
			execPkg = imp.Name.Name
		}
	}
	return func(expr ast.Expr) string {
		name := ""
		switch fn := unparen(expr).(type) {
		case *ast.SelectorExpr:
			if pkg, _ := fn.X.(*ast.Ident); pkg != nil && pkg.Name == execPkg {
				name = fn.Sel.Name
			}
		case *ast.Ident:
			if execPkg == "." {
				name = fn.Name
			}
		}
		if name == "Command" || name == "CommandContext" {
			return name
		}
		return ""
	}, execPkg
}

// rawExecSitesIn is rawExecSites for a file whose package may rewrite the
// process PATH or save exec.Command under an alias elsewhere: a helper in
// another file can prepend a fixture directory before this file resolves a
// slashless tool, and a call through an alias declared in another file is a
// raw site here, so both reach this file's count.
func rawExecSitesIn(t *testing.T, name, src string, packageRewritesPath bool, packageAliases map[string]bool) int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	execCommandName, execPkg := execCommandNameIn(file)
	pathRewritten := packageRewritesPath || rewritesProcessPath(file, savedSetenvAliases(file))
	aliases := commandAliases(file, execCommandName)
	for alias := range packageAliases {
		aliases[alias] = true
	}
	count := 0
	called := map[ast.Expr]bool{}
	// A file that declares its own execRetryETXTBSY shadows the package
	// helper, so its calls by that name are not trusted to retry anything.
	helpersShadowed := declaresRetryHelper(file)
	// retried holds the command expressions a retry helper's closure returns:
	// only those run under the helper's ETXTBSY retry. Any other execution
	// inside the closure runs while the closure builds its result and is raw.
	retried := map[ast.Expr]bool{}
	ast.Inspect(file, func(c ast.Node) bool {
		call, ok := c.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || !etxtbsyRetryHelpers[id.Name] || helpersShadowed {
			return true
		}
		for _, arg := range call.Args {
			closure, ok := arg.(*ast.FuncLit)
			if !ok {
				continue
			}
			if built := retriedCommand(closure, execCommandName); built != nil {
				retried[built] = true
			}
		}
		return true
	})
	ast.Inspect(file, func(c ast.Node) bool {
		call, ok := c.(*ast.CallExpr)
		if !ok {
			return true
		}
		// Every call through a saved exec.Command value is a raw site: its
		// program is never checked here.
		if id, ok := unparen(call.Fun).(*ast.Ident); ok && aliases[id.Name] {
			count++
			return true
		}
		if name := execCommandName(call.Fun); name != "" {
			called[call.Fun] = true
			called[unparen(call.Fun)] = true
			if retried[call] {
				return true
			}
			prog := 0
			if name == "CommandContext" {
				prog = 1
			}
			if len(call.Args) > prog {
				if lit, literal := call.Args[prog].(*ast.BasicLit); !literal || !stableProgramLiteral(lit, pathRewritten) {
					count++
				}
			}
		}
		return true
	})
	ast.Inspect(file, func(c ast.Node) bool {
		if expr, ok := c.(ast.Expr); ok && execCommandName(expr) != "" && !called[expr] {
			if _, selectorPart := c.(*ast.Ident); selectorPart && execPkg != "." {
				return true
			}
			count++
			return false
		}
		return true
	})
	return count
}

// retriedCommand returns the one command expression in closure that runs
// under the retry helper: the command the closure's final statement returns,
// either directly or as a variable whose last build is followed only by
// field assignments on it. Only the closure's own statements are read, so a
// nested function literal's return is never taken for this one, and a
// command the closure runs or hands elsewhere before returning is not
// retried and stays a raw site.
func retriedCommand(closure *ast.FuncLit, execCommandName func(ast.Expr) string) ast.Expr {
	statements := closure.Body.List
	if len(statements) == 0 {
		return nil
	}
	ret, ok := statements[len(statements)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil
	}
	switch returned := ret.Results[0].(type) {
	case *ast.CallExpr:
		if execCommandName(returned.Fun) != "" {
			return returned
		}
	case *ast.Ident:
		var built ast.Expr
		for _, statement := range statements[:len(statements)-1] {
			assign, ok := statement.(*ast.AssignStmt)
			if !ok {
				if built != nil {
					return nil // the command was used before the return
				}
				continue
			}
			if built != nil && !assignsFieldsOnly(assign, returned.Name) {
				return nil
			}
			for i, value := range assign.Rhs {
				if i >= len(assign.Lhs) {
					break
				}
				if id, ok := assign.Lhs[i].(*ast.Ident); ok && id.Name == returned.Name {
					if call, isCall := value.(*ast.CallExpr); isCall && execCommandName(call.Fun) != "" {
						built = call
					} else {
						built = nil
					}
				}
			}
		}
		return built
	}
	return nil
}

// assignsFieldsOnly reports whether assign only sets fields of name, such as
// c.Dir = x, which configures the command without running it. A right-hand
// side that mentions name could run the command, so it is not a field-only
// assignment.
func assignsFieldsOnly(assign *ast.AssignStmt, name string) bool {
	for _, lhs := range assign.Lhs {
		sel, ok := lhs.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != name {
			return false
		}
	}
	mentions := false
	for _, rhs := range assign.Rhs {
		ast.Inspect(rhs, func(c ast.Node) bool {
			if id, ok := c.(*ast.Ident); ok && id.Name == name {
				mentions = true
			}
			return !mentions
		})
	}
	return !mentions
}

// declaresRetryHelper reports whether a function in the file declares a
// parameter, variable or constant named like one of etxtbsyRetryHelpers,
// which shadows the package helper for the calls that follow. A second
// top-level declaration of that name cannot compile in this package, so only
// local declarations can shadow it.
func declaresRetryHelper(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(c ast.Node) bool {
		switch n := c.(type) {
		case *ast.FuncDecl:
			if n.Recv != nil {
				for _, field := range n.Recv.List {
					for _, name := range field.Names {
						if etxtbsyRetryHelpers[name.Name] {
							found = true
						}
					}
				}
			}
		case *ast.FuncType:
			if n.Params != nil {
				for _, field := range n.Params.List {
					for _, name := range field.Names {
						if etxtbsyRetryHelpers[name.Name] {
							found = true
						}
					}
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && etxtbsyRetryHelpers[id.Name] {
					found = true
				}
			}
		case *ast.ValueSpec:
			for _, name := range n.Names {
				if etxtbsyRetryHelpers[name.Name] {
					found = true
				}
			}
		case *ast.RangeStmt:
			for _, bound := range []ast.Expr{n.Key, n.Value} {
				if id, ok := bound.(*ast.Ident); ok && etxtbsyRetryHelpers[id.Name] {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// unparen strips parentheses, so (exec.Command) and (run) are read as the
// expressions they wrap.
func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

// packageCommandAliases lists the saved exec.Command aliases of every file
// together, to a fixed point, so an alias declared from another file's alias
// is known too. Each file is read through its own os/exec import spelling.
func packageCommandAliases(files []*ast.File) map[string]bool {
	aliases := map[string]bool{}
	for {
		added := false
		for _, file := range files {
			execCommandName, _ := execCommandNameIn(file)
			added = collectCommandAliases(aliases, file, execCommandName) || added
		}
		if !added {
			return aliases
		}
	}
}

// commandAliases lists the identifiers assigned from exec.Command or
// exec.CommandContext anywhere in the file, such as run := exec.Command, and
// every identifier assigned from one of those in turn, until no new name
// appears.
func commandAliases(file *ast.File, execCommandName func(ast.Expr) string) map[string]bool {
	aliases := map[string]bool{}
	for collectCommandAliases(aliases, file, execCommandName) {
	}
	return aliases
}

// collectCommandAliases adds one pass of file's command aliases to aliases
// and reports whether any name was new.
func collectCommandAliases(aliases map[string]bool, file *ast.File, execCommandName func(ast.Expr) string) bool {
	isAlias := func(value ast.Expr) bool {
		if execCommandName(value) != "" {
			return true
		}
		id, ok := unparen(value).(*ast.Ident)
		return ok && aliases[id.Name]
	}
	record := func(names []ast.Expr, values []ast.Expr) bool {
		added := false
		for i, value := range values {
			if isAlias(value) && i < len(names) {
				if id, ok := unparen(names[i]).(*ast.Ident); ok && !aliases[id.Name] {
					aliases[id.Name] = true
					added = true
				}
			}
		}
		return added
	}
	added := false
	ast.Inspect(file, func(c ast.Node) bool {
		switch n := c.(type) {
		case *ast.AssignStmt:
			added = record(n.Lhs, n.Rhs) || added
		case *ast.ValueSpec:
			names := make([]ast.Expr, len(n.Names))
			for i, name := range n.Names {
				names[i] = name
			}
			added = record(names, n.Values) || added
		}
		return true
	})
	return added
}

// savedSetenvAliases lists the identifiers that hold a Setenv function value
// anywhere in files, such as setenv := t.Setenv or var setenv = os.Setenv,
// propagated to a fixed point so an alias assigned from another alias counts
// too. The files are read together, so a package-level alias declared in one
// file is known when another file calls it.
func savedSetenvAliases(files ...*ast.File) map[string]bool {
	savedSetenv := map[string]bool{}
	isSetenv := func(value ast.Expr) bool {
		switch v := unparen(value).(type) {
		case *ast.SelectorExpr:
			return v.Sel.Name == "Setenv"
		case *ast.Ident:
			return savedSetenv[v.Name]
		}
		return false
	}
	record := func(names []ast.Expr, values []ast.Expr) bool {
		added := false
		for i, value := range values {
			if isSetenv(value) && i < len(names) {
				if id, ok := unparen(names[i]).(*ast.Ident); ok && !savedSetenv[id.Name] {
					savedSetenv[id.Name] = true
					added = true
				}
			}
		}
		return added
	}
	for {
		added := false
		for _, file := range files {
			ast.Inspect(file, func(c ast.Node) bool {
				switch n := c.(type) {
				case *ast.AssignStmt:
					added = record(n.Lhs, n.Rhs) || added
				case *ast.ValueSpec:
					names := make([]ast.Expr, len(n.Names))
					for i, name := range n.Names {
						names[i] = name
					}
					added = record(names, n.Values) || added
				}
				return true
			})
		}
		if !added {
			return savedSetenv
		}
	}
}

// packageRewritesProcessPath reports whether any file of the package rewrites
// the process PATH, with Setenv aliases collected across all of them.
func packageRewritesProcessPath(files []*ast.File) bool {
	aliases := savedSetenvAliases(files...)
	for _, file := range files {
		if rewritesProcessPath(file, aliases) {
			return true
		}
	}
	return false
}

// rewritesProcessPath reports whether the file sets the process PATH through
// t.Setenv or os.Setenv, called directly or through one of the savedSetenv
// aliases, after which a slashless program name can resolve to a freshly
// written fixture. A key that is not a string literal, such as a named
// constant, is treated as PATH, since its value is not resolved here.
func rewritesProcessPath(file *ast.File, savedSetenv map[string]bool) bool {
	found := false
	ast.Inspect(file, func(c ast.Node) bool {
		call, ok := c.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		switch fn := unparen(call.Fun).(type) {
		case *ast.SelectorExpr:
			if fn.Sel.Name != "Setenv" {
				return true
			}
		case *ast.Ident:
			if !savedSetenv[fn.Name] {
				return true
			}
		default:
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			found = true
			return false
		}
		if name, err := strconv.Unquote(lit.Value); err == nil && name == "PATH" {
			found = true
		}
		return !found
	})
	return found
}

func readExecBaseline(t *testing.T, root string) map[string]int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "policy", "testkit-exec-baseline.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	baseline := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[2] == "" {
			t.Fatalf("baseline row %q: want FILE<TAB>COUNT<TAB>REASON", line)
		}
		n, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("baseline row %q: %v", line, err)
		}
		baseline[fields[0]] = n
	}
	return baseline
}

// execRatchetProblems lists files whose count exceeds, or is below, baseline.
func execRatchetProblems(counts, baseline map[string]int) []string {
	var problems []string
	for file, n := range counts {
		if n > baseline[file] {
			problems = append(problems, file+": "+strconv.Itoa(n)+" raw exec sites, baseline "+strconv.Itoa(baseline[file])+"; wrap in execRetryETXTBSY")
		}
	}
	for file, n := range baseline {
		if counts[file] < n {
			problems = append(problems, file+": baseline "+strconv.Itoa(n)+" is stale, now "+strconv.Itoa(counts[file])+"; lower it")
		}
	}
	sort.Strings(problems)
	return problems
}

func TestRawExecSitesCountsPathValuedExecOutsideRetryHelper(t *testing.T) {
	const planted = `package x
import "os/exec"
func a(p string) { exec.Command(p, "--x") }
func b(p string) { execRetryETXTBSY(func() *exec.Cmd { return exec.Command(p) }) }
func c()         { exec.Command("git", "init") }
func d(p string) { exec.CommandContext(nil, p) }
func e(p string) { execRetryOn(func() *exec.Cmd { return exec.Command(p) }, nil) }
func f(p string) { execRetryETXTBSYLater(func() *exec.Cmd { return exec.Command(p) }) }
func g()         { exec.Command("./fixture.sh") }
func h()         { exec.Command("/bin/bash", "-c", "true") }
func i()         { exec.Command("/tmp/workcell-fixture.sh") }
func j()         { exec.Command("fixture") }
func k()         { exec.Command("/bin/../../tmp/workcell-fixture.sh") }
func l(p string) { run := exec.Command; run(p); run(p) }
func m(p string) { execRetryETXTBSY(func() *exec.Cmd { exec.Command(p).Run(); return exec.Command("git") }) }
func n(p string) { first := exec.Command; second := first; second(p) }
func o(p string) { execRetryETXTBSY(func() *exec.Cmd { c := exec.Command(p); c.Dir = "/"; return c }) }
func q(p string) { execRetryETXTBSY(func() *exec.Cmd { c := exec.Command(p); c.Run(); c = exec.Command("git"); return c }) }
func r(p string) { execRetryETXTBSY(func() *exec.Cmd { c := exec.Command(p); c.Run(); return c }) }
func s(p string) { execRetryETXTBSY(func() *exec.Cmd { func() *exec.Cmd { return exec.Command(p) }().Run(); return exec.Command("git") }) }
func u(p string) { execRetryETXTBSY(func() *exec.Cmd { c := exec.Command(p); c.Dir = func() string { c.Run(); return "/" }(); return c }) }
func v(p string) { run := (exec.Command); run(p); (exec.Command)(p) }
func w(p string) { var run func(string, ...string) *exec.Cmd; (run) = exec.Command; run(p) }
`
	if got := rawExecSites(t, "planted.go", planted); got != 23 {
		t.Fatalf("rawExecSites = %d, want 23 (a, d, e, f, g, i, j, k, l's saved value plus two calls, m's inner call, n's saved value plus its chained call, q's first command, r's command run before its return, s's nested closure command, u's command run from a field assignment, v's parenthesized saved value plus its call and a parenthesized direct call, w's parenthesized assignment target plus its call; o builds and returns its command under the retry)", got)
	}
	// A function that declares its own helper of the same name shadows the
	// package helper, so nothing in that file is exempt.
	const shadowedHelper = `package x
import "os/exec"
func a(p string) {
	execRetryETXTBSY := func(build func() *exec.Cmd) *exec.Cmd { return build() }
	execRetryETXTBSY(func() *exec.Cmd { return exec.Command(p) })
}
`
	if got := rawExecSites(t, "shadowed_helper.go", shadowedHelper); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (a shadowed helper exempts nothing)", got)
	}
	const parameterHelper = `package x
import "os/exec"
func a(p string, execRetryETXTBSY func(func() *exec.Cmd) *exec.Cmd) { execRetryETXTBSY(func() *exec.Cmd { return exec.Command(p) }) }
`
	if got := rawExecSites(t, "parameter_helper.go", parameterHelper); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (a parameter named like the helper exempts nothing)", got)
	}
	const rangeHelper = `package x
import "os/exec"
func a(p string, helpers []func(func() *exec.Cmd) *exec.Cmd) {
	for _, execRetryETXTBSY := range helpers {
		execRetryETXTBSY(func() *exec.Cmd { return exec.Command(p) })
	}
}
`
	if got := rawExecSites(t, "range_helper.go", rangeHelper); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (a range variable named like the helper exempts nothing)", got)
	}
	const receiverHelper = `package x
import "os/exec"
type retry func(func() *exec.Cmd) *exec.Cmd
func (execRetryETXTBSY retry) run(p string) { execRetryETXTBSY(func() *exec.Cmd { return exec.Command(p) }) }
`
	if got := rawExecSites(t, "receiver_helper.go", receiverHelper); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (a receiver named like the helper exempts nothing)", got)
	}
}

func TestRawExecSitesCountsSystemToolsAfterProcessPathRewrite(t *testing.T) {
	const shadowed = `package x
import ("os/exec"; "testing")
func a(t *testing.T) { t.Setenv("PATH", "/tmp/fixtures"); exec.Command("git", "init") }
`
	if got := rawExecSites(t, "shadowed.go", shadowed); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a PATH rewrite)", got)
	}
	const rawShadowed = "package x\nimport (\"os/exec\"; \"testing\")\nfunc a(t *testing.T) { t.Setenv(`PATH`, \"/tmp/fixtures\"); exec.Command(\"git\") }\n"
	if got := rawExecSites(t, "raw_shadowed.go", rawShadowed); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a raw-string PATH rewrite)", got)
	}
	const constShadowed = `package x
import ("os/exec"; "testing")
const pathKey = "PATH"
func a(t *testing.T) { t.Setenv(pathKey, "/tmp/fixtures"); exec.Command("git") }
`
	if got := rawExecSites(t, "const_shadowed.go", constShadowed); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a constant-keyed PATH rewrite)", got)
	}
	const savedSetenv = `package x
import ("os/exec"; "testing")
func a(t *testing.T) { setenv := t.Setenv; setenv("PATH", "/tmp/fixtures"); exec.Command("git") }
`
	if got := rawExecSites(t, "saved_setenv.go", savedSetenv); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a PATH rewrite through a saved Setenv value)", got)
	}
	const varSetenv = `package x
import ("os/exec"; "testing")
func a(t *testing.T) { var setenv = t.Setenv; setenv("PATH", "/tmp/fixtures"); exec.Command("git") }
`
	if got := rawExecSites(t, "var_setenv.go", varSetenv); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a PATH rewrite through a var-declared Setenv value)", got)
	}
	const chainedSetenv = `package x
import ("os/exec"; "testing")
func a(t *testing.T) { first := t.Setenv; second := first; second("PATH", "/tmp/fixtures"); exec.Command("git") }
`
	if got := rawExecSites(t, "chained_setenv.go", chainedSetenv); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a PATH rewrite through a chained Setenv alias)", got)
	}
	// A Setenv alias declared in one file and called in another is a rewrite.
	decl := "package x\nimport \"os\"\nvar setenv = os.Setenv\n"
	call := "package x\nimport \"os/exec\"\nfunc a() { setenv(\"PATH\", \"/tmp/fixtures\"); exec.Command(\"git\") }\n"
	var twoFiles []*ast.File
	for name, src := range map[string]string{"decl.go": decl, "call.go": call} {
		file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		twoFiles = append(twoFiles, file)
	}
	if !packageRewritesProcessPath(twoFiles) {
		t.Fatal("a Setenv alias declared in one file and called in another was not seen as a PATH rewrite")
	}
	// A rewrite in another file of the package reaches this file's lookups.
	const plain = `package x
import "os/exec"
func a() { exec.Command("git", "init") }
`
	if got := rawExecSitesIn(t, "plain.go", plain, true, nil); got != 1 {
		t.Fatalf("rawExecSitesIn = %d, want 1 (git after a PATH rewrite elsewhere in the package)", got)
	}
	if got := rawExecSitesIn(t, "plain.go", plain, false, nil); got != 0 {
		t.Fatalf("rawExecSitesIn = %d, want 0 (no PATH rewrite anywhere)", got)
	}
	// A saved exec.Command alias declared in one file counts its calls in
	// another: the declaration counts once and each cross-file call once.
	counts := packageRawExecSites(t, map[string]string{
		"decl.go": "package x\nimport \"os/exec\"\nvar run = exec.Command\n",
		"call.go": "package x\nfunc a(p string) { run(p); run(p) }\n",
	})
	if counts["decl.go"] != 1 || counts["call.go"] != 2 {
		t.Fatalf("package counts = %v, want decl.go:1 call.go:2 (a saved alias declared in another file)", counts)
	}
	// An alias of another file's alias propagates to a fixed point.
	counts = packageRawExecSites(t, map[string]string{
		"decl.go":  "package x\nimport \"os/exec\"\nvar first = exec.Command\n",
		"chain.go": "package x\nvar second = first\nfunc a(p string) { second(p) }\n",
	})
	if counts["decl.go"] != 1 || counts["chain.go"] != 1 {
		t.Fatalf("package counts = %v, want decl.go:1 chain.go:1 (an alias of another file's alias)", counts)
	}
	const childEnvOnly = `package x
import ("os"; "os/exec")
func a() { c := exec.Command("git", "init"); c.Env = []string{"PATH=/tmp/fixtures:" + os.Getenv("PATH")} }
`
	if got := rawExecSites(t, "child.go", childEnvOnly); got != 0 {
		t.Fatalf("rawExecSites = %d, want 0 (a child PATH does not change the lookup)", got)
	}
}

func TestRawExecSitesResolvesOsExecImportAlias(t *testing.T) {
	const aliased = `package x
import osexec "os/exec"
func a(p string) { osexec.Command(p) }
func b(p string) { exec.Command(p) }
`
	if got := rawExecSites(t, "aliased.go", aliased); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (aliased os/exec only)", got)
	}
	// A raw-string import path spells the same package.
	const rawAliased = "package x\nimport osexec `os/exec`\nfunc a(p string) { osexec.Command(p) }\n"
	if got := rawExecSites(t, "raw_aliased.go", rawAliased); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (raw-string aliased os/exec)", got)
	}
	// A dot import drops the package qualifier entirely.
	const dotImported = `package x
import . "os/exec"
func a(p string) { Command(p) }
func b()         { Command("git") }
func c(p string) { run := Command; run(p) }
`
	if got := rawExecSites(t, "dot.go", dotImported); got != 3 {
		t.Fatalf("rawExecSites = %d, want 3 (a, and c's saved value plus its call)", got)
	}
}

func TestExecRatchetFailsOnPlantedViolation(t *testing.T) {
	if p := execRatchetProblems(map[string]int{"a_test.go": 2}, map[string]int{"a_test.go": 1}); len(p) != 1 {
		t.Fatalf("new raw exec site not reported: %v", p)
	}
	if p := execRatchetProblems(map[string]int{"new_test.go": 1}, map[string]int{}); len(p) != 1 {
		t.Fatalf("raw exec site in unbaselined file not reported: %v", p)
	}
	if p := execRatchetProblems(map[string]int{"a_test.go": 1}, map[string]int{"a_test.go": 2}); len(p) != 1 {
		t.Fatalf("stale baseline not reported: %v", p)
	}
	if p := execRatchetProblems(map[string]int{"a_test.go": 1}, map[string]int{"a_test.go": 1}); len(p) != 0 {
		t.Fatalf("matching baseline reported: %v", p)
	}
}

func TestTestkitRawExecSitesMatchBaseline(t *testing.T) {
	root := repoRoot(t)
	// Every Go file in the package, not only tests: a shared helper that execs
	// a fixture by path is a raw site too.
	files, err := filepath.Glob(filepath.Join(root, "internal", "testkit", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob testkit sources: %v (%d files)", err, len(files))
	}
	// A PATH rewrite, a Setenv alias or a saved exec.Command alias in any
	// file of the package reaches every other file.
	sources := map[string]string{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sources[f] = string(src)
	}
	counts := map[string]int{}
	for f, n := range packageRawExecSites(t, sources) {
		counts["internal/testkit/"+filepath.Base(f)] = n
	}
	for _, p := range execRatchetProblems(counts, readExecBaseline(t, root)) {
		t.Error(p)
	}
	if t.Failed() {
		t.Log("counts:", counts)
	}
}
