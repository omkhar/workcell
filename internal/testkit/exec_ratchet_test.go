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

// rawExecSites counts exec.Command/CommandContext calls in src whose program
// is a path-valued expression (not a string literal such as "git" or
// "/bin/bash") and that sit outside a func literal handed to an execRetry*
// helper. Such a call execs a possibly freshly written fixture directly and
// can fail with ETXTBSY (golang/go#22315).
func rawExecSites(t *testing.T, name, src string) int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
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
				if strings.HasPrefix(fn.Name, "execRetry") {
					for _, arg := range call.Args {
						walk(arg, true)
					}
					return false
				}
			case *ast.SelectorExpr:
				pkg, _ := fn.X.(*ast.Ident)
				if pkg != nil && pkg.Name == "exec" && (fn.Sel.Name == "Command" || fn.Sel.Name == "CommandContext") && !wrapped {
					prog := 0
					if fn.Sel.Name == "CommandContext" {
						prog = 1
					}
					if len(call.Args) > prog {
						if _, literal := call.Args[prog].(*ast.BasicLit); !literal {
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
`
	if got := rawExecSites(t, "planted.go", planted); got != 2 {
		t.Fatalf("rawExecSites = %d, want 2 (a and d)", got)
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
	files, err := filepath.Glob(filepath.Join(root, "internal", "testkit", "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob testkit tests: %v (%d files)", err, len(files))
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
