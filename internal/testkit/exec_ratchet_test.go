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
	"slices"
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
	return rawExecSitesIn(t, name, src, false, nil)
}

// packageRawExecSites returns raw sites per file, with PATH rewrites and execNames read package-wide.
func packageRawExecSites(t *testing.T, sources map[string]string) map[string]int {
	t.Helper()
	var parsed []*ast.File
	byName := map[string]*ast.File{}
	for name, src := range sources {
		byName[name] = parseSource(t, name, src)
		parsed = append(parsed, byName[name])
	}
	names := packageExecNames(parsed)
	rewrites := packageRewritesProcessPath(parsed)
	counts := map[string]int{}
	for name, file := range byName {
		if n := rawExecSitesInFile(file, rewrites, &names); n > 0 {
			counts[name] = n
		}
	}
	return counts
}

// execCommandNameIn returns, under every os/exec import name of file, a function naming exec.Command or CommandContext, a predicate for the exec.Cmd type, and whether os/exec is dot imported.
func execCommandNameIn(file *ast.File) (func(ast.Expr) string, func(ast.Expr) bool, bool) {
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
	}, func(expr ast.Expr) bool { return member(expr) == "Cmd" }, execPkgs["."]
}

// execNames are a package's os/exec names: exec.Command aliases, exec.Cmd types and command holders.
type execNames struct {
	aliases, factories, holders map[any]bool
	cmdTypes                    map[string]bool
}

// packageExecNames collects the execNames of files read together.
func packageExecNames(files []*ast.File) execNames {
	aliases, factories := packageCommandAliases(files)
	return execNames{aliases, factories, commandHolders(files, aliases), cmdTypeNames(files)}
}

// rawExecSitesIn is rawExecSites with package-wide PATH and execNames; nil names reads the file alone.
func rawExecSitesIn(t *testing.T, name, src string, packageRewritesPath bool, names *execNames) int {
	t.Helper()
	return rawExecSitesInFile(parseSource(t, name, src), packageRewritesPath, names)
}

func parseSource(t *testing.T, name, src string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return file
}

// rawExecSitesInFile counts file, the parse names was built from, since local holders key by its objects.
func rawExecSitesInFile(file *ast.File, packageRewritesPath bool, names *execNames) int {
	execCommandName, isCmdType, dotImported := execCommandNameIn(file)
	pathRewritten := packageRewritesPath || rewritesProcessPath(file, savedSetenvAliases(file))
	if names == nil {
		own := packageExecNames([]*ast.File{file})
		names = &own
	}
	aliases := names.aliases
	isCommand, _ := commandValueIn(file, execCommandName, isCmdType, names.holders)
	count := 0
	called := map[ast.Expr]bool{}
	// A file that declares its own execRetryETXTBSY shadows the helper, so those calls are not trusted.
	helpersShadowed := declaresRetryHelper(file)
	// retried holds the command expressions a retry helper's closure returns: only those run under the helper's ETXTBSY retry. Any other execution inside the closure runs while the closure builds its result and is raw.
	retried := map[ast.Expr]bool{}
	isCmdLit := func(lit *ast.CompositeLit) bool {
		id, ok := lit.Type.(*ast.Ident)
		return isCmdType(lit.Type) || ok && names.cmdTypes[id.Name]
	}
	// build returns the counted node for an exec.Command call or a Cmd literal, direct or by &.
	build := func(expr ast.Expr) ast.Expr {
		if u, ok := unparen(expr).(*ast.UnaryExpr); ok && u.Op == token.AND {
			expr = u.X
		}
		switch v := unparen(expr).(type) {
		case *ast.CallExpr:
			if execCommandName(v.Fun) != "" {
				return v
			}
		case *ast.CompositeLit:
			if isCmdLit(v) {
				return v
			}
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
		var call *ast.CallExpr
		switch n := c.(type) {
		case *ast.AssignStmt:
			// A command whose Path field is reassigned runs a program that exec.Command never saw, so each such assignment is a raw site. A Path field of a value that holds no command starts nothing.
			for _, lhs := range n.Lhs {
				if sel, ok := unparen(lhs).(*ast.SelectorExpr); ok && sel.Sel.Name == "Path" && isCommand(sel.X) {
					count++
				}
			}
			return true
		case *ast.CompositeLit:
			// A Cmd literal names its program in Path, not through exec.Command.
			if isCmdLit(n) && !retried[n] {
				count++
			}
			return true
		case *ast.CallExpr:
			call = n
		default:
			return true
		}
		// Every call through a saved exec.Command value, or one a factory returns, is a raw site.
		fun, calls := unparen(call.Fun), aliases
		if inner, ok := fun.(*ast.CallExpr); ok {
			fun, calls = factoryName(inner), names.factories
		}
		if id, ok := fun.(*ast.Ident); ok && (calls[id.Name] || calls[id.Obj]) {
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

// factoryName returns a factory call's callee, reading a method such as m.factory() by name.
func factoryName(call *ast.CallExpr) ast.Expr {
	if sel, ok := unparen(call.Fun).(*ast.SelectorExpr); ok {
		return sel.Sel
	}
	return unparen(call.Fun)
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

// packageCommandAliases lists, to a fixed point, exec.Command aliases and factories that return one.
func packageCommandAliases(files []*ast.File) (map[any]bool, map[any]bool) {
	aliases, factories, params := map[any]bool{}, map[any]bool{}, funcParams(files)
	for {
		added := false
		for _, file := range files {
			execCommandName, _, _ := execCommandNameIn(file)
			added = collectCommandAliases(aliases, factories, params, file, execCommandName) || added
		}
		if !added {
			return aliases, factories
		}
	}
}

// funcParams lists by name the parameters of each function, method and named func literal; Go names all of a list's parameters or none.
func funcParams(files []*ast.File) map[string][][]*ast.Ident {
	params := map[string][][]*ast.Ident{}
	add := func(name, value ast.Expr) {
		id, named := name.(*ast.Ident)
		if lit, ok := value.(*ast.FuncLit); ok && named {
			var names []*ast.Ident
			for _, field := range lit.Type.Params.List {
				names = append(names, field.Names...)
			}
			params[id.Name] = append(params[id.Name], names)
		}
	}
	for _, file := range files {
		ast.Inspect(file, func(c ast.Node) bool {
			switch n := c.(type) {
			case *ast.FuncDecl:
				add(n.Name, &ast.FuncLit{Type: n.Type})
			case *ast.AssignStmt:
				for i := range min(len(n.Lhs), len(n.Rhs)) {
					add(n.Lhs[i], n.Rhs[i])
				}
			case *ast.ValueSpec:
				for i := range min(len(n.Names), len(n.Values)) {
					add(n.Names[i], n.Values[i])
				}
			}
			return true
		})
	}
	return params
}

// collectCommandAliases adds one pass of file's aliases and factories, including a parameter that receives one, and reports whether any was new.
func collectCommandAliases(aliases, factories map[any]bool, params map[string][][]*ast.Ident, file *ast.File, execCommandName func(ast.Expr) string) bool {
	added := false
	add := func(set map[any]bool, name any) {
		if !set[name] {
			set[name], added = true, true
		}
	}
	isAlias := func(value ast.Expr) bool {
		switch v := unparen(value).(type) {
		case *ast.Ident:
			return aliases[v.Name] || aliases[v.Obj] || execCommandName(v) != ""
		case *ast.CallExpr: // a factory's result
			id, ok := factoryName(v).(*ast.Ident)
			return ok && factories[id.Name]
		}
		return execCommandName(value) != ""
	}
	returnsAlias := func(body *ast.BlockStmt) bool {
		found := false
		ast.Inspect(body, func(c ast.Node) bool {
			if ret, ok := c.(*ast.ReturnStmt); ok {
				for _, result := range ret.Results {
					found = found || isAlias(result)
				}
			}
			return !found
		})
		return found
	}
	record := func(names []ast.Expr, values []ast.Expr) {
		if len(values) == 1 && len(names) > 1 { // any result of a tuple call may be the alias
			values = slices.Repeat(values, len(names))
		}
		for i := 0; i < len(values) && i < len(names); i++ {
			id, ok := unparen(names[i]).(*ast.Ident)
			lit, isLit := unparen(values[i]).(*ast.FuncLit)
			from, isIdent := unparen(values[i]).(*ast.Ident)
			switch {
			case !ok:
			case isAlias(values[i]):
				add(aliases, id.Name)
			case isLit && returnsAlias(lit.Body), isIdent && factories[from.Name]:
				add(factories, id.Name)
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
		case *ast.FuncDecl:
			if n.Body != nil && returnsAlias(n.Body) {
				add(factories, n.Name.Name)
			}
		case *ast.CallExpr: // a parameter given a command constructor is an alias in its callee's scope
			if callee, ok := factoryName(n).(*ast.Ident); ok {
				for _, names := range params[callee.Name] {
					for i, name := range names {
						if i < len(n.Args) && name.Obj != nil && isAlias(n.Args[i]) {
							add(aliases, name.Obj)
						}
					}
				}
			}
		}
		return true
	})
	return added
}

// cmdTypeNames lists, to a fixed point, types declared from exec.Cmd or from one of those.
func cmdTypeNames(files []*ast.File) map[string]bool {
	cmdTypes := map[string]bool{}
	for added := true; added; {
		added = false
		for _, file := range files {
			_, isCmdType, _ := execCommandNameIn(file)
			ast.Inspect(file, func(c ast.Node) bool {
				if spec, ok := c.(*ast.TypeSpec); ok && !cmdTypes[spec.Name.Name] {
					if id, ok := spec.Type.(*ast.Ident); isCmdType(spec.Type) || ok && cmdTypes[id.Name] {
						cmdTypes[spec.Name.Name] = true
						added = true
					}
				}
				return true
			})
		}
	}
	return cmdTypes
}

// commandValueIn returns predicates for an expression that yields a command (an exec.Command or holder call, a holder, or new or a literal of a Cmd type) and for a type that mentions exec.Cmd or a holder outside its parameters.
func commandValueIn(file *ast.File, execCommandName func(ast.Expr) string, isCmdType func(ast.Expr) bool, holders map[any]bool) (func(ast.Expr) bool, func(ast.Node) bool) {
	var mentionsCmd func(ast.Node) bool
	mentionsCmd = func(typ ast.Node) bool {
		found := false
		ast.Inspect(typ, func(c ast.Node) bool {
			switch n := c.(type) {
			case *ast.FuncType:
				found = found || n.Results != nil && mentionsCmd(n.Results)
				return false
			case *ast.Ident:
				found = found || holders[holderKey(file, n)] || isCmdType(n)
			case *ast.SelectorExpr:
				found = found || isCmdType(n)
			}
			return !found
		})
		return found
	}
	var isCommand func(ast.Expr) bool
	isCommand = func(expr ast.Expr) bool {
		switch v := unparen(expr).(type) {
		case *ast.UnaryExpr:
			return v.Op == token.AND && isCommand(v.X)
		case *ast.StarExpr:
			return isCommand(v.X)
		case *ast.IndexExpr:
			return isCommand(v.X)
		case *ast.Ident:
			return holders[holderKey(file, v)]
		case *ast.SelectorExpr:
			return holders[v.Sel.Name]
		case *ast.CompositeLit:
			return v.Type != nil && mentionsCmd(v.Type)
		case *ast.FuncLit:
			return mentionsCmd(v.Type)
		case *ast.CallExpr:
			if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "new" && len(v.Args) == 1 {
				return mentionsCmd(v.Args[0])
			}
			return execCommandName(v.Fun) != "" || isCommand(v.Fun)
		}
		return false
	}
	return isCommand, mentionsCmd
}

// holderKey keys a name declared inside a function by the parser's object for its declaration, so it holds a command only in its own scope, and any other name (package level, from another file) by the name itself.
func holderKey(file *ast.File, id *ast.Ident) any {
	if id.Obj != nil && file.Scope.Lookup(id.Name) != id.Obj {
		return id.Obj
	}
	return id.Name
}

// commandHolders lists, to a fixed point across files, the names that can hold a command: aliases, names declared with a type that mentions exec.Cmd, and names assigned or ranged from a command value. Struct fields, read through selectors, match by name alone, which only makes the count stricter.
func commandHolders(files []*ast.File, aliases map[any]bool) map[any]bool {
	holders := map[any]bool{}
	for name := range aliases {
		holders[name] = true
	}
	for added := true; added; {
		added = false
		for _, file := range files {
			execCommandName, isCmdType, _ := execCommandNameIn(file)
			isCommand, mentionsCmd := commandValueIn(file, execCommandName, isCmdType, holders)
			markKey := func(key any) {
				if !holders[key] {
					holders[key], added = true, true
				}
			}
			mark := func(exprs ...ast.Expr) {
				for _, expr := range exprs {
					if id, ok := unparen(expr).(*ast.Ident); ok && id.Name != "_" {
						markKey(holderKey(file, id))
					}
				}
			}
			assign := func(names, values []ast.Expr) {
				if len(values) == 1 && len(names) > 1 && isCommand(values[0]) {
					mark(names...)
				}
				for i, value := range values {
					if i < len(names) && isCommand(value) {
						mark(names[i])
					}
				}
			}
			ast.Inspect(file, func(c ast.Node) bool {
				switch n := c.(type) {
				case *ast.AssignStmt:
					assign(n.Lhs, n.Rhs)
				case *ast.ValueSpec:
					names := make([]ast.Expr, len(n.Names))
					for i, name := range n.Names {
						names[i] = name
					}
					if n.Type != nil && mentionsCmd(n.Type) {
						mark(names...)
					}
					assign(names, n.Values)
				case *ast.StructType:
					for _, field := range n.Fields.List {
						for _, name := range field.Names {
							if mentionsCmd(field.Type) {
								markKey(name.Name)
							}
						}
					}
				case *ast.Field:
					if mentionsCmd(n.Type) {
						for _, name := range n.Names {
							mark(name)
						}
					}
				case *ast.FuncDecl:
					if n.Type.Results != nil && mentionsCmd(n.Type.Results) {
						mark(n.Name)
					}
				case *ast.TypeSpec:
					if mentionsCmd(n.Type) {
						mark(n.Name)
					}
				case *ast.RangeStmt:
					if isCommand(n.X) {
						mark(n.Key, n.Value)
					}
				}
				return true
			})
		}
	}
	return holders
}

// envWriters can rewrite PATH, called through any qualifier or bare, as under a dot import of os.
var envWriters = map[string]bool{"Setenv": true, "Putenv": true}

// savedSetenvAliases lists, to a fixed point, names holding a Setenv value, such as setenv := t.Setenv.
func savedSetenvAliases(files ...*ast.File) map[string]bool {
	savedSetenv := map[string]bool{}
	isSetenv := func(value ast.Expr) bool {
		switch v := unparen(value).(type) {
		case *ast.SelectorExpr:
			return envWriters[v.Sel.Name]
		case *ast.Ident:
			return savedSetenv[v.Name] || envWriters[v.Name]
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

// packageRewritesProcessPath reports whether any file rewrites PATH, with Setenv aliases package-wide.
func packageRewritesProcessPath(files []*ast.File) bool {
	aliases := savedSetenvAliases(files...)
	for _, file := range files {
		if rewritesProcessPath(file, aliases) {
			return true
		}
	}
	return false
}

// rewritesProcessPath reports a process PATH write through an envWriter or alias; any non-literal key counts.
func rewritesProcessPath(file *ast.File, savedSetenv map[string]bool) bool {
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
			if !savedSetenv[fn.Name] && !envWriters[fn.Name] {
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
func x(p string) { c := exec.Command("/bin/true"); c.Path = p; c.Run() }
func y(p string) { (&exec.Cmd{Path: p}).Run() }
func z(p string) { type command = exec.Cmd; type named exec.Cmd; (&command{Path: p}).Run(); _ = named{Path: p} }
type request struct{ Path string }
func aa(p string) { var r request; r.Path = p }
func ab(p string) { var c exec.Cmd; c.Path = p; c.Run() }
func ac(c *exec.Cmd, p string) { c.Path = p; c.Run() }
type suite struct{ cmd *exec.Cmd }
func ad(s suite, p string) { s.cmd.Path = p; s.cmd.Run() }
func ae(p string) { c := new(exec.Cmd); c.Path = p; c.Run() }
func factory() func(string, ...string) *exec.Cmd { return exec.Command }
func af(p string) { launch := factory(); launch(p); mk := func() func(string, ...string) *exec.Cmd { return launch }; mk()(p) }
`
	if got := rawExecSites(t, "planted.go", planted); got != 34 {
		t.Fatalf("rawExecSites = %d, want 34 (every planted func but b, c, h, o and aa counts; see each line)", got)
	}
	const notRaw = `package x
import "os/exec"
type request struct{ Path string }
func a(request *request, value string) { request.Path = value }
func b(p string) { execRetryETXTBSY(func() *exec.Cmd { return &exec.Cmd{Path: p} }) }
func c(p string) { execRetryETXTBSY(func() *exec.Cmd { c := &exec.Cmd{Path: p}; c.Dir = "/"; return c }) }
`
	if got := rawExecSites(t, "not_raw.go", notRaw); got != 0 {
		t.Fatalf("rawExecSites = %d, want 0 (a Path assignment on a value that holds no command, and Cmd literals a retry helper returns)", got)
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
	const varSetenv = `package x
import ("os/exec"; "testing")
func a(t *testing.T) { var setenv = t.Setenv; setenv("PATH", "/tmp/fixtures"); exec.Command("git") }
`
	if got := rawExecSites(t, "var_setenv.go", varSetenv); got != 1 {
		t.Fatalf("rawExecSites = %d, want 1 (git after a PATH rewrite through a var-declared Setenv value)", got)
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
		twoFiles = append(twoFiles, parseSource(t, name, src))
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
	// An alias of another file's alias propagates to a fixed point, and a constructor passed to a function, method or func value counts per call through the parameter; a stable closure counts nothing.
	counts := packageRawExecSites(t, map[string]string{
		"decl.go":   "package x\nimport \"os/exec\"\nvar first = exec.Command\n",
		"chain.go":  "package x\nvar second = first\nfunc a(p string) { second(p) }\n",
		"use.go":    "package x\nimport \"os/exec\"\nfunc b(p string, h helper) { invoke(exec.Command, p); h.once(exec.Command); fv := func(run func(string, ...string) *exec.Cmd) { run(p) }; fv(exec.Command); stable(func(string, ...string) *exec.Cmd { return exec.Command(\"git\") }) }\n",
		"helper.go": "package x\nimport \"os/exec\"\ntype helper struct{}\nfunc invoke(run func(string, ...string) *exec.Cmd, p string) { run(p).Run(); run(p).Run() }\nfunc (helper) once(run func(string, ...string) *exec.Cmd) { run(\"x\") }\nfunc stable(run func(string, ...string) *exec.Cmd) { run(\"x\").Run() }\n",
	})
	if counts["decl.go"] != 1 || counts["chain.go"] != 1 || counts["use.go"] != 4 || counts["helper.go"] != 3 {
		t.Fatalf("package counts = %v, want decl.go:1 chain.go:1 use.go:4 helper.go:3 (an alias of another file's alias; use.go's three references, fv's call, invoke's two calls, once's call; stable counts nothing)", counts)
	}
	// An exec.Cmd type alias and a Cmd variable from one file count their literal and Path write in another.
	counts = packageRawExecSites(t, map[string]string{
		"decl.go": "package x\nimport \"os/exec\"\ntype command = exec.Cmd\nvar shared *exec.Cmd\n",
		"use.go":  "package x\nfunc a(p string) { (&command{Path: p}).Run(); shared.Path = p }\n",
	})
	if counts["decl.go"] != 0 || counts["use.go"] != 2 {
		t.Fatalf("package counts = %v, want use.go:2 (a Cmd type alias and a Cmd variable declared in another file)", counts)
	}
	// A holder resolves in its own scope, not as c in another file or block.
	counts = packageRawExecSites(t, map[string]string{
		"cmd.go":   "package x\nimport \"os/exec\"\nfunc a(c *exec.Cmd, p string) { c.Path = p; { c := request{}; c.Path = p } }\n",
		"other.go": "package x\ntype request struct{ Path string }\nfunc b(c request, p string) { c.Path = p }\n",
	})
	if counts["cmd.go"] != 1 || counts["other.go"] != 0 {
		t.Fatalf("package counts = %v, want cmd.go:1 (an unrelated c in another file or an inner scope)", counts)
	}
	// Method factories count like function factories; a returned Cmd counts where it is built.
	const methodFactory = `package x
import "os/exec"
type maker struct{}
func (maker) factory() func(string, ...string) *exec.Cmd { return exec.Command }
func (maker) build(p string) *exec.Cmd { return exec.Command(p) }
func (maker) pair() (int, func(string, ...string) *exec.Cmd) { return 0, exec.Command }
func a(m maker, p string) { m.factory()(p); launch := m.factory(); launch(p); m.build(p).Run(); _, run := m.pair(); run(p) }
`
	if got := rawExecSites(t, "method_factory.go", methodFactory); got != 6 {
		t.Fatalf("rawExecSites = %d, want 6 (factory's saved value, calls through m.factory() directly and saved, build's command, and pair's saved value plus a call through its second result)", got)
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
	// A PATH rewrite, Setenv alias or exec.Command alias in any file reaches every other file.
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
