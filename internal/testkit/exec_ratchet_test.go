// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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

// stableProgramLiteral reports whether a string literal names a tool resolved
// on PATH ("git") or a binary under systemBinaryDirs ("/bin/bash"). Any other
// path literal, relative ("./fixture") or absolute ("/tmp/fixture.sh"), can
// still name a freshly written script.
func stableProgramLiteral(lit *ast.BasicLit) bool {
	if lit.Kind != token.STRING {
		return false
	}
	prog, err := strconv.Unquote(lit.Value)
	if err != nil {
		return false
	}
	if !strings.Contains(prog, "/") {
		return true
	}
	for _, dir := range systemBinaryDirs {
		if strings.HasPrefix(prog, dir) {
			return true
		}
	}
	return false
}

// rawExecSites counts exec.Command/CommandContext calls in src whose program
// is a path-valued expression or a relative path literal (not a stable
// literal such as "git" or "/bin/bash") and that sit outside a func literal
// handed to one of etxtbsyRetryHelpers. Such a call execs a possibly freshly written fixture
// directly and can fail with ETXTBSY (golang/go#22315). The os/exec package
// is matched by its import path, so an import alias is still counted.
func rawExecSites(t *testing.T, name, src string) int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	execPkg := "exec"
	for _, imp := range file.Imports {
		if imp.Path.Value == `"os/exec"` && imp.Name != nil {
			execPkg = imp.Name.Name
		}
	}
	count := 0
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
			case *ast.SelectorExpr:
				pkg, _ := fn.X.(*ast.Ident)
				if pkg != nil && pkg.Name == execPkg && (fn.Sel.Name == "Command" || fn.Sel.Name == "CommandContext") && !wrapped {
					prog := 0
					if fn.Sel.Name == "CommandContext" {
						prog = 1
					}
					if len(call.Args) > prog {
						if lit, literal := call.Args[prog].(*ast.BasicLit); !literal || !stableProgramLiteral(lit) {
							count++
						}
					}
				}
			}
			return true
		})
	}
	walk(file, false)
	return count
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
`
	if got := rawExecSites(t, "planted.go", planted); got != 6 {
		t.Fatalf("rawExecSites = %d, want 6 (a, d, e, f, g, i)", got)
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
