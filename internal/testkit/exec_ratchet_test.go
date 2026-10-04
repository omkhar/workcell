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
	isExecCommand := func(fn *ast.SelectorExpr) bool {
		pkg, _ := fn.X.(*ast.Ident)
		return pkg != nil && pkg.Name == execPkg && (fn.Sel.Name == "Command" || fn.Sel.Name == "CommandContext")
	}
	pathRewritten := rewritesProcessPath(file)
	aliases := commandAliases(file, isExecCommand)
	count := 0
	called := map[*ast.SelectorExpr]bool{}
	var walk func(n ast.Node, wrapped bool)
	walk = func(n ast.Node, wrapped bool) {
		ast.Inspect(n, func(c ast.Node) bool {
			call, ok := c.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if etxtbsyRetryHelpers[fn.Name] {
					for _, arg := range call.Args {
						walk(arg, true)
					}
					return false
				}
				// Every call through a saved exec.Command value is a raw site:
				// its program is never checked here.
				if aliases[fn.Name] {
					count++
				}
			case *ast.SelectorExpr:
				if isExecCommand(fn) {
					called[fn] = true
					if wrapped {
						break
					}
					prog := 0
					if fn.Sel.Name == "CommandContext" {
						prog = 1
					}
					if len(call.Args) > prog {
						if lit, literal := call.Args[prog].(*ast.BasicLit); !literal || !stableProgramLiteral(lit, pathRewritten) {
							count++
						}
					}
				}
			}
			return true
		})
	}
	walk(file, false)
	ast.Inspect(file, func(c ast.Node) bool {
		if sel, ok := c.(*ast.SelectorExpr); ok && isExecCommand(sel) && !called[sel] {
			count++
		}
		return true
	})
	return count
}

// commandAliases lists the identifiers assigned from exec.Command or
// exec.CommandContext anywhere in the file, such as run := exec.Command.
func commandAliases(file *ast.File, isExecCommand func(*ast.SelectorExpr) bool) map[string]bool {
	aliases := map[string]bool{}
	record := func(names []ast.Expr, values []ast.Expr) {
		for i, value := range values {
			if sel, ok := value.(*ast.SelectorExpr); ok && isExecCommand(sel) && i < len(names) {
				if id, ok := names[i].(*ast.Ident); ok {
					aliases[id.Name] = true
				}
			}
		}
	}
	ast.Inspect(file, func(c ast.Node) bool {
		switch n := c.(type) {
		case *ast.AssignStmt:
			record(n.Lhs, n.Rhs)
		case *ast.ValueSpec:
			names := make([]ast.Expr, len(n.Names))
			for i, name := range n.Names {
				names[i] = name
			}
			record(names, n.Values)
		}
		return true
	})
	return aliases
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
`
	if got := rawExecSites(t, "planted.go", planted); got != 11 {
		t.Fatalf("rawExecSites = %d, want 11 (a, d, e, f, g, i, j, k, and l's saved value plus its two calls)", got)
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
