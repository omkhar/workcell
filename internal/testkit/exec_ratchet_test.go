// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// etxtbsyRetryHelpers are the only helpers whose func-literal argument is exempt from the ratchet: each one guarantees an ETXTBSY retry. A near-match such as execRetryOn with an arbitrary predicate is counted like a raw call.
var etxtbsyRetryHelpers = map[string]bool{
	"execRetryETXTBSY":             true,
	"execRetryETXTBSYOrNestedBusy": true,
}

// systemBinaryDirs hold installed binaries only: no test writes a fixture there.
var systemBinaryDirs = []string{"/bin/", "/sbin/", "/usr/bin/", "/usr/sbin/"}

// systemTools resolve on PATH; another slashless name, or any after a PATH rewrite, may be a fixture.
var systemTools = map[string]bool{
	"bash": true, "chmod": true, "cp": true, "git": true, "go": true, "shasum": true, "true": true,
}

// stableProgramLiteral reports a systemTools name with PATH untouched, or a binary under systemBinaryDirs.
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
	// A traversal such as "/bin/../../tmp/fixture.sh" escapes, so only a clean literal is exempt.
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

// rawExecSites counts the exec.Command calls in src whose program is not a stable literal and that no etxtbsyRetryHelpers closure returns: each can exec a fresh fixture and fail with ETXTBSY (golang/go#22315). A saved exec.Command value counts too, since what it later runs is not checked.
func rawExecSites(t *testing.T, name, src string) int {
	return rawExecSitesIn(t, name, src, false)
}

// packageRawExecSites returns raw sites per file, with PATH rewrites read package-wide.
func packageRawExecSites(t *testing.T, sources map[string]string) map[string]int {
	t.Helper()
	var parsed []*ast.File
	byName := map[string]*ast.File{}
	for name, src := range sources {
		byName[name] = parseSource(t, name, src)
		parsed = append(parsed, byName[name])
	}
	rewrites := packageRewritesProcessPath(parsed)
	counts := map[string]int{}
	for name, file := range byName {
		if n := rawExecSitesInFile(file, rewrites); n > 0 {
			counts[name] = n
		}
	}
	return counts
}

// execCommandNameIn returns, under every os/exec import name of file, a function naming exec.Command or CommandContext, and whether os/exec is dot imported.
func execCommandNameIn(file *ast.File) (func(ast.Expr) string, bool) {
	execPkgs := map[string]bool{}
	for _, imp := range file.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil && path == "os/exec" {
			name := "exec"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			execPkgs[name] = true
		}
	}
	if len(execPkgs) == 0 {
		execPkgs["exec"] = true
	}
	member := func(expr ast.Expr) string {
		switch fn := unparen(expr).(type) {
		case *ast.SelectorExpr:
			if pkg, _ := fn.X.(*ast.Ident); pkg != nil && execPkgs[pkg.Name] {
				return fn.Sel.Name
			}
		case *ast.Ident:
			if execPkgs["."] {
				return fn.Name
			}
		}
		return ""
	}
	return func(expr ast.Expr) string {
		if name := member(expr); name == "Command" || name == "CommandContext" {
			return name
		}
		return ""
	}, execPkgs["."]
}

// rawExecSitesIn is rawExecSites with a package-wide PATH rewrite.
func rawExecSitesIn(t *testing.T, name, src string, packageRewritesPath bool) int {
	t.Helper()
	return rawExecSitesInFile(parseSource(t, name, src), packageRewritesPath)
}

func parseSource(t *testing.T, name, src string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return file
}

// rawExecSitesInFile counts the raw exec sites of one parsed file.
func rawExecSitesInFile(file *ast.File, packageRewritesPath bool) int {
	execCommandName, dotImported := execCommandNameIn(file)
	pathRewritten := packageRewritesPath || rewritesProcessPath(file)
	count := 0
	called := map[ast.Expr]bool{}
	// A file that declares its own execRetryETXTBSY shadows the helper, so those calls are not trusted.
	helpersShadowed := declaresRetryHelper(file)
	// retried holds the command expressions a retry helper's closure returns: only those run under the helper's ETXTBSY retry. Any other execution inside the closure runs while the closure builds its result and is raw.
	retried := map[ast.Expr]bool{}
	// build returns the counted node for an exec.Command call.
	build := func(expr ast.Expr) ast.Expr {
		if v, ok := unparen(expr).(*ast.CallExpr); ok && execCommandName(v.Fun) != "" {
			return v
		}
		return nil
	}
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
			if built := retriedCommand(closure, build); built != nil {
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
			if _, selectorPart := c.(*ast.Ident); selectorPart && !dotImported {
				return true
			}
			count++
			return false
		}
		return true
	})
	return count
}

// retriedCommand returns the command build the closure's final statement returns, directly or through a variable followed only by field assignments. A command run or handed elsewhere first stays a raw site.
func retriedCommand(closure *ast.FuncLit, build func(ast.Expr) ast.Expr) ast.Expr {
	statements := closure.Body.List
	if len(statements) == 0 {
		return nil
	}
	ret, ok := statements[len(statements)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil
	}
	if built := build(ret.Results[0]); built != nil {
		return built
	}
	returned, ok := ret.Results[0].(*ast.Ident)
	if !ok {
		return nil
	}
	var built ast.Expr
	for _, statement := range statements[:len(statements)-1] {
		assign, ok := statement.(*ast.AssignStmt)
		if !ok {
			if built != nil || usesEventualCommand(statement, returned.Name) {
				return nil // the command was used before the return, or a deferred closure can use it
			}
			continue
		}
		if built != nil && !assignsFieldsOnly(assign, returned.Name) {
			return nil
		}
		if built == nil && mentionsName(assign.Rhs, returned.Name) {
			return nil // a closure saved before the build can run the eventual command
		}
		for i, value := range assign.Rhs {
			if i >= len(assign.Lhs) {
				break
			}
			if id, ok := assign.Lhs[i].(*ast.Ident); ok && id.Name == returned.Name {
				built = build(value)
			}
		}
	}
	return built
}

// assignsFieldsOnly reports whether assign only sets fields of name, such as c.Dir = x, without reading name.
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
	return !mentionsName(assign.Rhs, name)
}

// usesEventualCommand reports whether a statement before the command build can use the command later, such as a defer or go statement that mentions name. A declaration of name without a value is only the holder.
func usesEventualCommand(statement ast.Stmt, name string) bool {
	if decl, ok := statement.(*ast.DeclStmt); ok {
		gen, ok := decl.Decl.(*ast.GenDecl)
		if !ok {
			return true
		}
		for _, spec := range gen.Specs {
			if value, ok := spec.(*ast.ValueSpec); ok && mentionsName(value.Values, name) {
				return true
			}
		}
		return false
	}
	return mentionsStmt(statement, name)
}

// mentionsStmt reports whether statement mentions the identifier name.
func mentionsStmt(statement ast.Stmt, name string) bool {
	mentions := false
	ast.Inspect(statement, func(c ast.Node) bool {
		if id, ok := c.(*ast.Ident); ok && id.Name == name {
			mentions = true
		}
		return !mentions
	})
	return mentions
}

// mentionsName reports whether any expression mentions the identifier name.
func mentionsName(exprs []ast.Expr, name string) bool {
	for _, expr := range exprs {
		mentions := false
		ast.Inspect(expr, func(c ast.Node) bool {
			if id, ok := c.(*ast.Ident); ok && id.Name == name {
				mentions = true
			}
			return !mentions
		})
		if mentions {
			return true
		}
	}
	return false
}

// declaresRetryHelper reports whether a local declaration in the file shadows an etxtbsyRetryHelpers name.
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
		case *ast.TypeSpec:
			if etxtbsyRetryHelpers[n.Name.Name] {
				found = true
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

// unparen strips parentheses, so (exec.Command) and (run) read as what they wrap.
func unparen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

// envWriters can rewrite PATH, called through any qualifier or bare, as under a dot import of os.
var envWriters = map[string]bool{"Setenv": true, "Putenv": true}

// packageRewritesProcessPath reports whether any file rewrites PATH.
func packageRewritesProcessPath(files []*ast.File) bool {
	for _, file := range files {
		if rewritesProcessPath(file) {
			return true
		}
	}
	return false
}

// rewritesProcessPath reports a process PATH write through an envWriter; any non-literal key counts.
func rewritesProcessPath(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(c ast.Node) bool {
		call, ok := c.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		switch fn := unparen(call.Fun).(type) {
		case *ast.SelectorExpr:
			if !envWriters[fn.Sel.Name] {
				return true
			}
		case *ast.Ident:
			if !envWriters[fn.Name] {
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

const execBaselinePath = "policy/testkit-exec-baseline.tsv"

func parseExecBaseline(t *testing.T, data string) map[string]int {
	t.Helper()
	baseline := map[string]int{}
	for _, line := range strings.Split(data, "\n") {
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

// mergeBaseExecBaseline reads the baseline at HEAD's merge base with execBaselineBaseRef, or nil if absent.
func mergeBaseExecBaseline(t *testing.T, root string) map[string]int {
	t.Helper()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return string(out)
	}
	base, err := execBaselineBaseRef(root)
	if err != nil {
		t.Fatalf("probe origin/main: %v", err)
	}
	mergeBase := strings.TrimSpace(git("merge-base", base, "HEAD"))
	if git("ls-tree", "--name-only", mergeBase, "--", execBaselinePath) == "" {
		return nil
	}
	return parseExecBaseline(t, git("show", mergeBase+":"+execBaselinePath))
}

// execBaselineBaseRef returns origin/main, or main only when origin/main is missing; any other probe failure is an error, so the check fails closed.
func execBaselineBaseRef(root string) (string, error) {
	err := exec.Command("git", "-C", root, "show-ref", "--exists", "refs/remotes/origin/main").Run()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
		return "refs/heads/main", nil
	}
	return "refs/remotes/origin/main", err
}

// execBaselineRaises lists baseline rows above their merge-base row. A raise would admit a new raw site, so the editable file alone cannot hold the ratchet.
func execBaselineRaises(baseline, base map[string]int) []string {
	var problems []string
	for file, n := range baseline {
		if n > base[file] {
			problems = append(problems, file+": baseline "+strconv.Itoa(n)+" is above "+strconv.Itoa(base[file])+" at the merge base; wrap the new site in execRetryETXTBSY")
		}
	}
	sort.Strings(problems)
	return problems
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
func x(p string) { execRetryETXTBSY(func() *exec.Cmd { var c *exec.Cmd; defer func() { _ = c.Run() }(); c = exec.Command(p); return c }) }
func y(p string) { execRetryETXTBSY(func() *exec.Cmd { var c *exec.Cmd; run := func() { _ = c.Run() }; c = exec.Command(p); run(); return c }) }
func z(p string) { execRetryETXTBSY(func() *exec.Cmd { var c *exec.Cmd; c = exec.Command(p); return c }) }
`
	if got := rawExecSites(t, "planted.go", planted); got != 20 {
		t.Fatalf("rawExecSites = %d, want 20 (every planted func but b, c, h, o and z counts, and v twice; a saved exec.Command value counts where it is saved)", got)
	}
	// A function that declares its own same-name helper shadows it, so nothing in that file is exempt.
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
	const typeHelper = `package x
import "os/exec"
func a(p string) {
	type execRetryETXTBSY func() *exec.Cmd
	execRetryETXTBSY(func() *exec.Cmd { return exec.Command(p) })()
}
`
	if got := rawExecSites(t, "type_helper.go", typeHelper); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (a local type named like the helper exempts nothing)", got)
	}
}

func TestRawExecSitesCountsSystemToolsAfterProcessPathRewrite(t *testing.T) {
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
	const putenv = `package x
import ("os"; "os/exec")
func a() { os.Putenv("PATH", "/tmp/fixtures"); exec.Command("git") }
`
	if got := rawExecSites(t, "putenv.go", putenv); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a PATH rewrite through os.Putenv)", got)
	}
	const dotSetenv = `package x
import (. "os"; "os/exec")
func a() { Setenv("PATH", "/tmp/fixtures"); exec.Command("git") }
`
	if got := rawExecSites(t, "dot_setenv.go", dotSetenv); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a PATH rewrite through a dot-imported Setenv)", got)
	}
	// A rewrite in another file of the package reaches this file's lookups.
	const plain = `package x
import "os/exec"
func a() { exec.Command("git", "init") }
`
	if got := rawExecSitesIn(t, "plain.go", plain, true); got != 1 {
		t.Fatalf("rawExecSitesIn = %d, want 1 (git after a PATH rewrite elsewhere in the package)", got)
	}
	if got := rawExecSitesIn(t, "plain.go", plain, false); got != 0 {
		t.Fatalf("rawExecSitesIn = %d, want 0 (no PATH rewrite anywhere)", got)
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
	// Two imports of os/exec under different names both qualify the package.
	const twiceAliased = `package x
import (a "os/exec"; b "os/exec")
func x(p string) { _ = b.Command("/bin/true"); a.Command(p).Run() }
`
	if got := rawExecSites(t, "twice_aliased.go", twiceAliased); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (a call through the first of two os/exec aliases)", got)
	}
	// A dot import drops the package qualifier entirely.
	const dotImported = `package x
import . "os/exec"
func a(p string) { Command(p) }
func b()         { Command("git") }
func c(p string) { run := Command; run(p) }
`
	if got := rawExecSites(t, "dot.go", dotImported); got != 2 {
		t.Fatalf("rawExecSites = %d, want 2 (a, and c's saved value)", got)
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
	if p := execBaselineRaises(map[string]int{"a_test.go": 2, "new_test.go": 1, "b_test.go": 1}, map[string]int{"a_test.go": 1, "b_test.go": 2}); len(p) != 2 {
		t.Fatalf("baseline raised above the merge base not reported twice: %v", p)
	}
}

func TestExecBaselineBaseRefFailsClosed(t *testing.T) {
	root := t.TempDir()
	ref := filepath.Join(root, ".git", "refs", "remotes", "origin", "main")
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil || os.MkdirAll(filepath.Dir(ref), 0o755) != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if base, err := execBaselineBaseRef(root); err != nil || base != "refs/heads/main" {
		t.Fatalf("missing origin/main = %q, %v; want refs/heads/main", base, err)
	}
	if err := os.WriteFile(ref, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := execBaselineBaseRef(root); err == nil {
		t.Fatal("corrupt origin/main probed without an error; want the check to fail closed")
	}
}

func TestTestkitRawExecSitesMatchBaseline(t *testing.T) {
	root := repoRoot(t)
	// Every Go file in the package: a shared helper that execs a fixture by path is a raw site too.
	files, err := filepath.Glob(filepath.Join(root, "internal", "testkit", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob testkit sources: %v (%d files)", err, len(files))
	}
	// A PATH rewrite in any file reaches every other file.
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
	data, err := os.ReadFile(filepath.Join(root, execBaselinePath))
	if err != nil {
		t.Fatal(err)
	}
	baseline := parseExecBaseline(t, string(data))
	problems := execRatchetProblems(counts, baseline)
	if base := mergeBaseExecBaseline(t, root); base != nil {
		problems = append(problems, execBaselineRaises(baseline, base)...)
	}
	for _, p := range problems {
		t.Error(p)
	}
	if t.Failed() {
		t.Log("counts:", counts)
	}
}
