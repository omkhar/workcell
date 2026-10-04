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
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	execPkg := "exec"
	for _, imp := range file.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err == nil && path == "os/exec" && imp.Name != nil {
			execPkg = imp.Name.Name
		}
	}
	// execCommandName returns "Command" or "CommandContext" when expr names
	// that os/exec function: a selector through the package name, or a bare
	// identifier under a dot import.
	execCommandName := func(expr ast.Expr) string {
		name := ""
		switch fn := expr.(type) {
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
	}
	pathRewritten := rewritesProcessPath(file)
	aliases := commandAliases(file, execCommandName)
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
			// The returned command is either the call itself or a variable
			// the closure assigned from such a call.
			returnedNames := map[string]bool{}
			ast.Inspect(closure.Body, func(r ast.Node) bool {
				if ret, ok := r.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
					switch returned := ret.Results[0].(type) {
					case *ast.CallExpr:
						if execCommandName(returned.Fun) != "" {
							retried[returned] = true
						}
					case *ast.Ident:
						returnedNames[returned.Name] = true
					}
				}
				return true
			})
			// Only the last assignment to a returned name reaches the return;
			// an earlier command assigned to the same name ran before it.
			lastBuilt := map[string]ast.Expr{}
			ast.Inspect(closure.Body, func(r ast.Node) bool {
				assign, ok := r.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, value := range assign.Rhs {
					if i >= len(assign.Lhs) {
						break
					}
					id, ok := assign.Lhs[i].(*ast.Ident)
					built, isCall := value.(*ast.CallExpr)
					if ok && isCall && returnedNames[id.Name] && execCommandName(built.Fun) != "" {
						lastBuilt[id.Name] = built
					}
				}
				return true
			})
			for _, built := range lastBuilt {
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
		if id, ok := call.Fun.(*ast.Ident); ok && aliases[id.Name] {
			count++
			return true
		}
		if name := execCommandName(call.Fun); name != "" {
			called[call.Fun] = true
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

// declaresRetryHelper reports whether a function body in the file declares a
// variable or constant named like one of etxtbsyRetryHelpers, which shadows
// the package helper for the calls that follow. A second top-level
// declaration of that name cannot compile in this package, so only local
// declarations can shadow it.
func declaresRetryHelper(file *ast.File) bool {
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(c ast.Node) bool {
			switch n := c.(type) {
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
			}
			return !found
		})
	}
	return found
}

// commandAliases lists the identifiers assigned from exec.Command or
// exec.CommandContext anywhere in the file, such as run := exec.Command, and
// every identifier assigned from one of those in turn, until no new name
// appears.
func commandAliases(file *ast.File, execCommandName func(ast.Expr) string) map[string]bool {
	aliases := map[string]bool{}
	isAlias := func(value ast.Expr) bool {
		if execCommandName(value) != "" {
			return true
		}
		id, ok := value.(*ast.Ident)
		return ok && aliases[id.Name]
	}
	record := func(names []ast.Expr, values []ast.Expr) bool {
		added := false
		for i, value := range values {
			if isAlias(value) && i < len(names) {
				if id, ok := names[i].(*ast.Ident); ok && !aliases[id.Name] {
					aliases[id.Name] = true
					added = true
				}
			}
		}
		return added
	}
	for {
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
		if !added {
			return aliases
		}
	}
}

// rewritesProcessPath reports whether the file sets the process PATH through
// t.Setenv or os.Setenv, after which a slashless program name can resolve to
// a freshly written fixture. A key that is not a string literal, such as a
// named constant, is treated as PATH, since its value is not resolved here.
func rewritesProcessPath(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(c ast.Node) bool {
		call, ok := c.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		fn, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fn.Sel.Name != "Setenv" {
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
`
	if got := rawExecSites(t, "planted.go", planted); got != 15 {
		t.Fatalf("rawExecSites = %d, want 15 (a, d, e, f, g, i, j, k, l's saved value plus two calls, m's inner call, n's saved value plus its chained call, q's first command; o builds and returns its command under the retry)", got)
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
	counts := map[string]int{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := rawExecSites(t, f, string(src)); n > 0 {
			counts["internal/testkit/"+filepath.Base(f)] = n
		}
	}
	for _, p := range execRatchetProblems(counts, readExecBaseline(t, root)) {
		t.Error(p)
	}
	if t.Failed() {
		t.Log("counts:", counts)
	}
}
