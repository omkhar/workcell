// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const goHostutilCacheFixtureMain = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "fail" {
		fmt.Fprintln(os.Stderr, "fixture failed")
		os.Exit(3)
	}
	fmt.Println("VERSION", os.Args[1:])
}
`

// TestGoHostutilCachedBinary drives scripts/lib/launcher/go-hostutil.sh
// against a fixture module: a cache hit reuses the binary, a source change
// rebuilds it, and a tampered binary or cache directory is never executed.
func TestGoHostutilCachedBinary(t *testing.T) {
	t.Parallel()

	fixture := ExecFixtureDir(t)
	rootDir := filepath.Join(fixture, "repo")
	cacheRoot := filepath.Join(fixture, "cache")
	binDir := filepath.Join(cacheRoot, "bin")
	mainPath := filepath.Join(rootDir, "cmd", "workcell-hostutil", "main.go")
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeMain := func(version string) {
		t.Helper()
		src := strings.Replace(goHostutilCacheFixtureMain, "VERSION", version, 1)
		if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeMain("v1")

	goEnv := func(name string) string {
		out, err := exec.Command("go", "env", name).Output()
		if err != nil {
			t.Fatalf("go env %s: %v", name, err)
		}
		return strings.TrimSpace(string(out))
	}
	env := map[string]string{
		"GOCACHE":                goEnv("GOCACHE"),
		"GOMODCACHE":             goEnv("GOMODCACHE"),
		"GOPATH":                 goEnv("GOPATH"),
		"WORKCELL_GO_CACHE_ROOT": cacheRoot,
	}
	libDir := filepath.Join(repoRoot(t), "scripts", "lib")
	run := func(call string) (int, string) {
		t.Helper()
		return runBashProbe(t, `set -euo pipefail
ROOT_DIR="`+rootDir+`"
REAL_HOME="`+fixture+`"
TRUSTED_HOST_PATH="${PATH}"
source "`+libDir+`/launcher/host-exec.sh"
source "`+libDir+`/go-run-env.sh"
source "`+libDir+`/launcher/go-hostutil.sh"
`+call+`
`, env)
	}
	binaries := func() []string {
		t.Helper()
		matches, err := filepath.Glob(filepath.Join(binDir, "workcell-hostutil-*"))
		if err != nil {
			t.Fatal(err)
		}
		return matches
	}
	inode := func(path string) uint64 {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Sys().(*syscall.Stat_t).Ino
	}
	expectOutput := func(call, want string) {
		t.Helper()
		code, output := run(call)
		if code != 0 || strings.TrimSpace(output) != want {
			t.Fatalf("%s: exit=%d output=%q, want exit 0 output %q", call, code, output, want)
		}
	}

	// Miss: builds one owner-only binary.
	expectOutput("go_hostutil hello", "v1 [hello]")
	first := binaries()
	if len(first) != 1 {
		t.Fatalf("cached binaries after first call = %v, want 1", first)
	}
	if info, err := os.Stat(binDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir mode = %v (err %v), want 0700", info.Mode().Perm(), err)
	}
	firstInode := inode(first[0])

	// Hit: the same file runs again without a rebuild.
	expectOutput("go_hostutil hello", "v1 [hello]")
	if got := binaries(); len(got) != 1 || got[0] != first[0] || inode(got[0]) != firstInode {
		t.Fatalf("cache hit rebuilt the binary: %v", got)
	}

	// The real child status reaches run_go_hostutil_preserve_exit callers.
	code, output := run("run_go_hostutil_preserve_exit fail")
	if code != 3 || strings.TrimSpace(output) != "fixture failed" {
		t.Fatalf("preserve_exit fail: exit=%d output=%q, want 3 and only the child's stderr", code, output)
	}

	// Key change: a source edit selects and builds a new binary.
	writeMain("v2")
	expectOutput("go_hostutil hello", "v2 [hello]")
	second := binaries()
	if len(second) != 2 {
		t.Fatalf("cached binaries after source change = %v, want 2", second)
	}
	var current string
	for _, path := range second {
		if path != first[0] {
			current = path
		}
	}

	// Tampered: a group-writable binary is replaced, not executed.
	if err := os.Chmod(current, 0o770); err != nil {
		t.Fatal(err)
	}
	tamperedInode := inode(current)
	expectOutput("go_hostutil hello", "v2 [hello]")
	if info, err := os.Lstat(current); err != nil || info.Mode().Perm() != 0o700 || inode(current) == tamperedInode {
		t.Fatalf("group-writable cached binary was not rebuilt (err %v)", err)
	}

	// Truncated: an empty file at the binary path is replaced, not executed.
	if err := os.Truncate(current, 0); err != nil {
		t.Fatal(err)
	}
	expectOutput("go_hostutil hello", "v2 [hello]")

	// Tampered: a symlink planted at the binary path is replaced, not followed.
	evil := filepath.Join(fixture, "evil.sh")
	if err := os.WriteFile(evil, []byte("#!/bin/sh\necho evil\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(evil, current); err != nil {
		t.Fatal(err)
	}
	expectOutput("go_hostutil hello", "v2 [hello]")
	if info, err := os.Lstat(current); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("symlinked cached binary was not replaced by a regular file (err %v)", err)
	}

	// Untrusted cache directory: a symlinked bin dir is refused outright.
	if err := os.Rename(binDir, binDir+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binDir+".real", binDir); err != nil {
		t.Fatal(err)
	}
	code, output = run("go_hostutil hello")
	if code == 0 || !strings.Contains(output, "Refusing untrusted Go tool cache") {
		t.Fatalf("symlinked cache dir: exit=%d output=%q, want refusal", code, output)
	}

	// Source change during the build: the binary is not stored under the old key.
	if err := os.Remove(binDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(binDir+".real", binDir); err != nil {
		t.Fatal(err)
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	racer := filepath.Join(fixture, "racer.sh")
	racerSrc := "#!/bin/bash\n\"" + realGo + "\" \"$@\"\nrc=$?\n" +
		"if [[ \"$1\" == build ]]; then echo 'package main; func main() {}' > \"" + mainPath + "\"; fi\nexit $rc\n"
	if err := os.WriteFile(racer, []byte(racerSrc), 0o700); err != nil {
		t.Fatal(err)
	}
	writeMain("v3")
	expectOutput("go_hostutil hello", "v3 [hello]")
	before := len(binaries())
	// The launcher caches the path at source time, so edit the source and
	// swap the go binary inside the call, then drop the memo.
	code, output = run(`printf 'package main\nimport "fmt"\nfunc main() { fmt.Println("v4") }\n' > "` + mainPath + `"
HOST_GO_BIN="` + racer + `"; GO_HOSTUTIL_BIN=""; go_hostutil hello`)
	if code == 0 || !strings.Contains(output, "changed during the build") || len(binaries()) != before {
		t.Fatalf("racing source change: exit=%d output=%q binaries=%v, want refusal and no new binary", code, output, binaries())
	}

	// A failed chmod of the cache directory is an error, not a pass: callers run
	// go_tool_bin in an || list, where errexit is off.
	code, output = run(`chmod() { return 1; }; GO_HOSTUTIL_BIN=""; go_tool_bin workcell-hostutil || { echo refused; exit 7; }`)
	if code != 7 || strings.TrimSpace(output) != "refused" {
		t.Fatalf("failing chmod: exit=%d output=%q, want the refusal path", code, output)
	}

	// A group-writable cache root lets another account swap bin/: refused.
	// Sourcing the launcher resets the root mode, so loosen it inside the call.
	code, output = run(`chmod 0770 "${WORKCELL_GO_CACHE_ROOT}"; GO_HOSTUTIL_BIN=""; go_tool_bin workcell-hostutil || { echo refused; exit 7; }`)
	if code != 7 || !strings.Contains(output, "Refusing untrusted Go tool cache") {
		t.Fatalf("group-writable cache root: exit=%d output=%q, want refusal", code, output)
	}

	// A failing permission lookup is not a pass.
	code, output = run(`find() { return 1; }; go_tool_path_private "${WORKCELL_GO_CACHE_ROOT}" && echo private; exit 0`)
	if code != 0 || strings.Contains(output, "private") {
		t.Fatalf("failing find: exit=%d output=%q, want the path treated as not private", code, output)
	}

	// A failed temp-binary chmod removes the temp file and fails. A new source
	// forces a build; the cache root must be owner-only again.
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	before = len(binaries())
	code, output = run(`printf 'package main\nimport "fmt"\nfunc main() { fmt.Println("v5") }\n' > "` + mainPath + `"
chmod() { [[ "$2" == *"/.workcell-hostutil."* ]] && return 1; command chmod "$@"; }
GO_HOSTUTIL_BIN=""; go_tool_bin workcell-hostutil || { echo refused; exit 7; }`)
	leftovers, _ := filepath.Glob(filepath.Join(binDir, ".workcell-hostutil.*"))
	if code != 7 || len(binaries()) != before || len(leftovers) != 0 {
		t.Fatalf("failing temp chmod: exit=%d output=%q binaries=%v leftovers=%v, want refusal, no new binary, no temp file", code, output, binaries(), leftovers)
	}

	// A failing second build-ID lookup (nonzero after printing the right ID)
	// is not a pass.
	statusGo := filepath.Join(fixture, "statusgo.sh")
	statusSrc := "#!/bin/bash\n\"" + realGo + "\" \"$@\"\nrc=$?\n" +
		"if [[ \"$1\" == build ]]; then : > \"" + fixture + "/built\"; fi\n" +
		"if [[ \"$1\" == list && -e \"" + fixture + "/built\" ]]; then exit 1; fi\nexit $rc\n"
	if err := os.WriteFile(statusGo, []byte(statusSrc), 0o700); err != nil {
		t.Fatal(err)
	}
	expectOutput("go_hostutil hello", "v5")
	before = len(binaries())
	code, output = run(`printf 'package main\nimport "fmt"\nfunc main() { fmt.Println("v6") }\n' > "` + mainPath + `"
HOST_GO_BIN="` + statusGo + `"; GO_HOSTUTIL_BIN=""; go_tool_bin workcell-hostutil || { echo refused; exit 7; }`)
	if code != 7 || len(binaries()) != before {
		t.Fatalf("failing second lookup: exit=%d output=%q binaries=%v, want refusal and no new binary", code, output, binaries())
	}

	// A failed pre-rename sync removes the temp file and publishes nothing.
	code, output = run(`printf 'package main\nimport "fmt"\nfunc main() { fmt.Println("v7") }\n' > "` + mainPath + `"
sync() { return 1; }
GO_HOSTUTIL_BIN=""; go_tool_bin workcell-hostutil || { echo refused; exit 7; }`)
	leftovers, _ = filepath.Glob(filepath.Join(binDir, ".workcell-hostutil.*"))
	if code != 7 || len(binaries()) != before+1 || len(leftovers) != 0 {
		t.Fatalf("failing sync: exit=%d output=%q binaries=%v leftovers=%v, want refusal, no new binary, no temp file", code, output, binaries(), leftovers)
	}
}
