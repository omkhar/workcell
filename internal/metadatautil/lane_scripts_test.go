// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/metadatautil"
)

// laneRoot and jobRoot are the assignments that pin the root of
// scripts/validate-repo.sh and of a scripts/ci/job-*.sh lane, and
// laneAssignedRoot is laneRoot's assignment with no readonly after it.
const (
	laneAssignedRoot = `ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"`
	laneRoot         = laneAssignedRoot + "\nreadonly ROOT_DIR"
	jobRoot          = `ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"` + "\nreadonly ROOT_DIR"
)

func TestLaneScripts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"scripts/validate-repo.sh": "#!/bin/bash\nset -euo pipefail\n" + laneRoot + "\n\"${ROOT_DIR}/scripts/a.sh\"\necho scripts/b.sh\n\"${ARTIFACT_DIR}/scripts/g.sh\"\n$ROOT_DIR/scripts/h.sh\n",
		"scripts/lib/x.sh":         "x() { printf '%s\\n' \"${ROOT_DIR:-}\"; }\n",
		"scripts/ci/job-x.sh":      "./scripts/c.sh\n",
		"scripts/ci/job-cd.sh":     "cd /tmp\nscripts/p.sh\n",
		"scripts/ci/job-src.sh":    jobRoot + "\nsource \"${ROOT_DIR}/scripts/lib/x.sh\"\nscripts/r.sh\n",
		"scripts/ci/job-bare.sh":   "gate.sh\n",
		".github/workflows/x.yml": "jobs:\n  x:\n    runs-on: ubuntu-latest\n    steps:\n      - run: ./scripts/d.sh --flag\n      - run: echo scripts/e.sh\n" +
			"      - run: ${ROOT_DIR}/scripts/v.sh\n      - run: w.sh\n        working-directory: scripts\n" +
			"      - run: scripts/j.sh\n        working-directory: other\n      - run: |\n          cd other\n          scripts/l.sh\n" +
			"  w:\n    runs-on: ubuntu-latest\n    defaults:\n      run:\n        working-directory: other\n    steps:\n      - run: scripts/k.sh\n",
		".github/workflows/z.yaml": "defaults:\n  run:\n    working-directory: sub\njobs:\n  z:\n    runs-on: ubuntu-latest\n    steps:\n      - run: ../scripts/m.sh\n" +
			"      - run: scripts/n.sh\n        working-directory: .\n      - run: /scripts/q.sh\n        working-directory: .\n",
		".github/workflows/py.yml": "jobs:\n  p:\n    runs-on: ubuntu-latest\n    steps:\n      - run: scripts/s.sh\n        shell: python\n",
		".github/workflows/env.yml": "jobs:\n  e:\n    runs-on: ubuntu-latest\n    steps:\n      - run: scripts/t.sh\n        env:\n          BASH_ENV: x\n" +
			"      - run: echo BASH_ENV=x >> \"$GITHUB_ENV\"\n      - run: scripts/u.sh\n",
		".github/workflows/notes.md": "./scripts/f.sh\n",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := metadatautil.LaneScripts(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"scripts/a.sh", "scripts/c.sh", "scripts/ci/job-bare.sh", "scripts/ci/job-cd.sh", "scripts/ci/job-src.sh", "scripts/ci/job-x.sh", "scripts/d.sh", "scripts/h.sh", "scripts/m.sh", "scripts/n.sh", "scripts/validate-repo.sh"}
	if !slices.Equal(got, want) {
		t.Fatalf("LaneScripts() = %q, want %q", got, want)
	}
	if _, err := metadatautil.LaneScripts(t.TempDir()); err == nil {
		t.Fatal("LaneScripts() on a tree with no validate-repo.sh: want an error")
	}
	outside := filepath.Join(t.TempDir(), "job-y.sh")
	if err := os.WriteFile(outside, []byte("scripts/z.sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "scripts", "ci", "job-y.sh")); err != nil {
		t.Fatal(err)
	}
	if _, err := metadatautil.LaneScripts(root); err == nil {
		t.Fatal("LaneScripts() with a symlinked lane file: want an error")
	}
}

// A ${ROOT_DIR}/ path names the repository script only while the lane pins
// ROOT_DIR to the repository, and a function named after a shell, a wrapper or
// a path makes bash call the function instead of the script.
func TestLaneScriptsReadsTheRootAndShadowingFunctions(t *testing.T) {
	t.Parallel()
	const gate = "\"${ROOT_DIR}/scripts/gate.sh\"\n"
	cases := []struct {
		name, lane, library, want string // want: wired, unwired, or an error substring
	}{
		// A readonly root holds: bash refuses every later write to it.
		// Without readonly it may be moved. A computed name fails closed
		// all the same, since it may name BASH_CMDS, PATH or ENV.
		{"root not readonly", laneAssignedRoot + "\n" + gate, "", "unwired"},
		{"root made readonly by declare -r", laneAssignedRoot + "\ndeclare -r ROOT_DIR\n" + gate, "", "wired"},
		{"readonly root after another line", laneAssignedRoot + "\n:\nreadonly ROOT_DIR\n" + gate, "", "unwired"},
		{"arithmetic computed name without readonly", laneAssignedRoot + "\nv=ROOT_; [[ 1 -eq ${v}DIR=1 ]]\n" + gate, "", "assigns in arithmetic"},
		{"arithmetic computed name in a test", laneRoot + "\nv=BASH_; [[ 1 -eq ${v}ENV=1 ]]\n" + gate, "", "assigns in arithmetic"},
		{"arithmetic computed name", laneRoot + "\nv=BASH_; (( ${v}ENV=1 ))\n" + gate, "", "assigns in arithmetic"},
		{"arithmetic computed name in a subscript", laneRoot + "\nv=BASH_; a[${v}ENV=1]=x\n" + gate, "", "assigns in arithmetic"},
		{"wait -p computed name", laneRoot + "\nv=BASH_; sleep 0 & wait -n -p \"${v}ENV\"\n" + gate, "", "name this reader cannot spell"},
		{"library rebinds a positional", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\nclaim x OUT\n" + gate, "claim() {\n  local v=\"$2\"\n  v=${n}DIR\n  printf -v \"${v}\" /tmp\n}\n", "name this reader cannot spell"},
		{"library shifts before a positional", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\nclaim x OUT\n" + gate, "claim() {\n  shift\n  local v=\"$2\"\n  printf -v \"${v}\" /tmp\n}\n", "name this reader cannot spell"},
		{"library assigns a caller's name", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\nclaim x OUT\n" + gate, "claim() {\n  local v=\"$2\"\n  printf -v \"${v}\" /tmp\n}\n", "wired"},
		{"library assigns BASH_CMDS by a caller's name after a redirection", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\nclaim 2>/dev/null x 'BASH_CMDS[bash]'\n" + gate, "claim() {\n  local v=\"$2\"\n  printf -v \"${v}\" /bin/true\n}\n", "lane wiring is unreadable"},
		{"library assigns BASH_CMDS by a caller's name", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\nclaim x 'BASH_CMDS[bash]'\n" + gate, "claim() {\n  local v=\"$2\"\n  printf -v \"${v}\" /bin/true\n}\n", "lane wiring is unreadable"},
		{"canonical root", laneRoot + "\n" + gate, "", "wired"},
		{"no root assignment", gate, "", "unwired"},
		{"root of another depth", jobRoot + "\n" + gate, "", "unwired"},
		{"root assigned after a command", "cd /\n" + laneRoot + "\n" + gate, "", "unwired"},
		{"root inside a branch", "if false; then\n" + laneRoot + "\nfi\n" + gate, "", "unwired"},
		{"root moved after the prelude", laneRoot + "\nROOT_DIR=/tmp\n" + gate, "", "unwired"},
		{"root exported", laneRoot + "\nexport ROOT_DIR=/tmp\n" + gate, "", "unwired"},
		{"root read", laneRoot + "\nread -r ROOT_DIR </dev/null\n" + gate, "", "unwired"},
		{"root assigned by an expansion", laneRoot + "\n: \"${ROOT_DIR:=/tmp}\"\n" + gate, "", "unwired"},
		{"root name split by quotes", laneRoot + "\nexport \"ROOT_\"\"DIR=/tmp\"\n" + gate, "", "unwired"},
		{"computed name", laneRoot + "\nv=ROOT; export \"${v}_DIR=/tmp\"\n" + gate, "", "name this reader cannot spell"},
		{"computed printf -v name", laneRoot + "\nprintf -v \"$1\" /tmp\n" + gate, "", "name this reader cannot spell"},
		{"eval of computed text", laneRoot + "\neval \"$1\"\n" + gate, "", "lane wiring is unreadable"},
		{"reading expansions keep the root", laneRoot + "\nlocal x=\"$1\"\necho \"${ROOT_DIR:-}\" \"${ROOT_DIR%/}\"\n" + gate, "", "wired"},
		{"library moves the root", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, "ROOT_DIR=/tmp\n", "lane wiring is unreadable"},
		{"library reads the root", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, "f() { echo \"${ROOT_DIR}\"; }\n", "wired"},
		{"missing library", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/none.sh\"\n" + gate, "", "lane wiring is unreadable"},
		{"bash function", laneRoot + "\nbash() { :; }\nbash scripts/gate.sh\n", "", "lane wiring is unreadable"},
		{"function keyword", laneRoot + "\nfunction BASH { :; }\nBASH scripts/gate.sh\n", "", "lane wiring is unreadable"},
		{"env function", laneRoot + "\nenv() { :; }\nenv bash scripts/gate.sh\n", "", "lane wiring is unreadable"},
		{"path-named function", laneRoot + "\n./scripts/gate.sh() { :; }\n./scripts/gate.sh\n", "", "lane wiring is unreadable"},
		{"nested shadowing function", laneRoot + "\nf() {\n  sh() { :; }\n}\nsh scripts/gate.sh\n", "", "lane wiring is unreadable"},
		{"library shadowing function", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\nbash scripts/gate.sh\n", "bash() { :; }\n", "lane wiring is unreadable"},
		{"hash -p rebinds a shell", laneRoot + "\nhash -p /bin/true bash\nbash \"${ROOT_DIR}/scripts/gate.sh\"\n", "", "lane wiring is unreadable"},
		{"missing library outside scripts", laneRoot + "\nsource \"${ROOT_DIR}/tools/l.sh\"\n" + gate, "", "lane wiring is unreadable"},
		{"other function", laneRoot + "\nrun() { \"$@\"; }\nbash \"${ROOT_DIR}/scripts/gate.sh\"\n", "", "wired"},
		{"set prelude with a comment", "set -o pipefail # strict\n" + laneRoot + "\n" + gate, "", "wired"},
		{"set prelude that moves", "set -e; cd /tmp\n" + laneRoot + "\n" + gate, "", "unwired"},
		{"set prelude that defines pwd", "set -e; pwd() { echo /tmp; }\n" + laneRoot + "\n" + gate, "", "instead of the builtin this reader reads"},
		{"set prelude that opens a function", "set -e; f() {\n" + laneRoot + "\n}\n" + gate, "", "unwired"},
		{"root name continued across lines", laneRoot + "\nROOT\\\n_DIR=/tmp\n" + gate, "", "unwired"},
		{"root name by brace expansion", laneRoot + "\nexport ROOT{_,}DIR=/tmp\n" + gate, "", "declares a name this reader cannot spell"},
		{"root name by brace sequence", laneRoot + "\nexport ROO{T..T}_DIR=/tmp\n" + gate, "", "declares a name this reader cannot spell"},
		{"quoted braces keep the root", laneRoot + "\necho 'ROOT{_,}' a{b,c}\n" + gate, "", "wired"},
		{"computed nameref", laneRoot + "\ndeclare -n ref=\"${n}DIR\"; ref=/tmp\n" + gate, "", "name this reader cannot spell"},
		{"nameref with no target", laneRoot + "\ndeclare -n ref\nref=\"${n}DIR\"\nref=/tmp\n" + gate, "", "name this reader cannot spell"},
		{"library computed printf -v name", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, "printf -v \"${v}_DIR\" /tmp\n", "name this reader cannot spell"},
		{"library alias", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, "alias bash=true\n", "lane wiring is unreadable"},
		{"library alias in a function", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, "f() {\n  alias bash=true\n}\n", "lane wiring is unreadable"},
		{"library source of a computed path", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, "source \"$(dirname \"${BASH_SOURCE[0]}\")/m.sh\"\n", "lane wiring is unreadable"},
		{"bash function with a subshell body", laneRoot + "\nbash() ( : )\nbash scripts/gate.sh\n", "", "lane wiring is unreadable"},
		{"bash function spelled name ( )", laneRoot + "\nbash ( ) {\n  :\n}\nbash scripts/gate.sh\n", "", "lane wiring is unreadable"},
		{"bash function inside an if", laneRoot + "\nif true; then bash() { :; }; fi\nbash scripts/gate.sh\n", "", "lane wiring is unreadable"},
		{"bash function an eval defines", laneRoot + "\neval 'bash() { :; }'\nbash scripts/gate.sh\n", "", "lane wiring is unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			files := map[string]string{"scripts/validate-repo.sh": "#!/bin/bash\nset -euo pipefail\n" + tc.lane, "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": "name: w\n"}
			if tc.library != "" {
				files["scripts/lib/l.sh"] = tc.library
			}
			for rel, body := range files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := metadatautil.LaneScripts(root)
			switch tc.want {
			case "wired", "unwired":
				if err != nil {
					t.Fatal(err)
				}
				if wired := slices.Contains(got, "scripts/gate.sh"); wired != (tc.want == "wired") {
					t.Fatalf("LaneScripts() = %q, want scripts/gate.sh %s", got, tc.want)
				}
			default:
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("LaneScripts() = %q, %v, want an error with %q", got, err, tc.want)
				}
			}
		})
	}
}

// A library is found from the words of every command, as bash reads them, so
// each spelling of a source is read; a builtin in front of a command, a hash or
// alias table write, text an eval computes and a trap or eval function each
// fail closed, since they may rebind what the lane runs.
func TestLaneScriptsReadsEverySourceAndRebinding(t *testing.T) {
	t.Parallel()
	const (
		gate     = "bash \"${ROOT_DIR}/scripts/gate.sh\"\n"
		shadow   = "bash() { :; }\n"
		rebinds  = "lane wiring is unreadable"
		library  = "scripts/lib/l.sh"
		second   = "scripts/lib/m.sh"
		relative = "bash scripts/gate.sh\n"
	)
	cases := []struct {
		name, lane string
		files      map[string]string
		want       string // wired, or an error substring
	}{
		{"plain source of a shadowing library", "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: shadow}, rebinds},
		{"quoted source word", "\"source\" \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: shadow}, rebinds},
		{"escaped source word", "\\source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: shadow}, rebinds},
		{"quoted dot", "\".\" \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: shadow}, rebinds},
		{"source continued on the next line", "source \\\n  \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: shadow}, rebinds},
		{"quoted root then the path", "source \"${ROOT_DIR}\"/scripts/lib/l.sh\n" + gate, map[string]string{library: shadow}, rebinds},
		{"unbraced quoted root", "source \"$ROOT_DIR\"/scripts/lib/l.sh\n" + gate, map[string]string{library: shadow}, rebinds},
		{"command builtin source", "command builtin source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: shadow}, rebinds},
		{"source in a called function", "f() {\n  source \"${ROOT_DIR}/scripts/lib/l.sh\"\n}\nf\n" + gate, map[string]string{library: shadow}, rebinds},
		{"library sources a second by a quoted word", "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: "\"source\" \"${ROOT_DIR}/scripts/lib/m.sh\"\n", second: shadow}, rebinds},
		{"library function sources a second", "source \"${ROOT_DIR}/scripts/lib/l.sh\"\nf\n" + gate, map[string]string{library: "f() { builtin . \"${ROOT_DIR}/scripts/lib/m.sh\"; }\n", second: shadow}, rebinds},
		{"two clean libraries", "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: "\"source\" \"${ROOT_DIR}/scripts/lib/m.sh\"\n", second: "f() { :; }\n"}, "wired"},
		{"lane builtin hash -p", "builtin hash -p /bin/true bash\n" + gate, nil, rebinds},
		{"lane builtin alias", "shopt -s expand_aliases\nbuiltin alias bash=true\n" + gate, nil, rebinds},
		{"lane function runs builtin hash -p", "f() { builtin hash -p /bin/true bash; }\nf\n" + gate, nil, rebinds},
		{"library builtin hash -p", "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: "builtin hash -p /bin/true bash\n"}, rebinds},
		{"library function runs builtin alias", "source \"${ROOT_DIR}/scripts/lib/l.sh\"\nf\n" + gate, map[string]string{library: "f() { command alias bash=true; }\n"}, rebinds},
		{"BASH_CMDS element", "BASH_CMDS[bash]=/bin/true\n" + gate, nil, rebinds},
		{"BASH_CMDS declared", "declare -A BASH_CMDS=([bash]=/bin/true)\n" + gate, nil, rebinds},
		{"BASH_CMDS named by quotes", "declare \"BASH_\"\"CMDS[bash]=/bin/true\"\n" + gate, nil, rebinds},
		{"BASH_ALIASES element", "shopt -s expand_aliases\nBASH_ALIASES[bash]=true\n" + gate, nil, rebinds},
		{"library BASH_CMDS", "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: "BASH_CMDS[bash]=/bin/true\n"}, rebinds},
		{"eval of computed text defines a function", "x='bash() { :; }'\neval \"$x\"\n" + relative, nil, rebinds},
		{"library eval of computed text", "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, map[string]string{library: "f() { eval \"$1\"; }\n"}, rebinds},
		{"mapfile callback", "mapfile -C 'source' -c 1 x <<<''\n" + gate, nil, rebinds},
		{"trap function", "trap() { :; }\ntrap 'bash \"${ROOT_DIR}/scripts/gate.sh\"' EXIT\n", nil, rebinds},
		{"eval function", "eval() { :; }\neval " + relative, nil, rebinds},
		{"letter sequence spells BASH_CMDS", "declare -A BASH_{C..C}MDS=([bash]=/bin/true)\n" + gate, nil, rebinds},
		{"number sequence keeps the root", "for i in {1..300}; do :; done\n" + gate, nil, "wired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			files := map[string]string{"scripts/validate-repo.sh": "#!/bin/bash\nset -euo pipefail\n" + laneRoot + "\n" + tc.lane, "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": "name: w\n"}
			maps.Copy(files, tc.files)
			writeLaneTree(t, root, files)
			got, err := metadatautil.LaneScripts(root)
			if tc.want == "wired" {
				if err != nil || !slices.Contains(got, "scripts/gate.sh") {
					t.Fatalf("LaneScripts() = %q, %v, want scripts/gate.sh wired", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LaneScripts() = %q, %v, want an error with %q", got, err, tc.want)
			}
		})
	}
}

// Text built to make the reader's work grow faster than its size, as a long
// run of brace groups or of nested braces does, is read in bounded time.
func TestLaneScriptsBoundsPathologicalText(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"brace groups":        "echo " + strings.Repeat("{a,b}", 40000) + "\n",
		"nested braces":       "echo " + strings.Repeat("{", 100000) + "x" + strings.Repeat("}", 100000) + "\n",
		"many nested words":   strings.Repeat("echo {{{{{{{{{{x,y}}}}}}}}}}\n", 20000),
		"grouped short words": strings.Repeat("echo {a,b}{c,d}{e,f}{g,h}{i,j}{k,l}{m,n}{o,p}{q,r}\n", 5000),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeLaneTree(t, root, map[string]string{"scripts/validate-repo.sh": "#!/bin/bash\n" + laneRoot + "\n" + text, "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": "name: w\n"})
			done := make(chan error, 1)
			go func() { _, err := metadatautil.LaneScripts(root); done <- err }()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("LaneScripts() did not finish within 20s")
			}
		})
	}
}

// Where this reader cannot model what a lane or a library runs it fails
// closed, with an error that names the construct: deep or unread eval and trap
// text, a quoted eval, a definition in a trap action, a program variable or a
// forwarder that may run a builtin, a child shell that reads its own ROOT_DIR,
// a computed or startup write, and builtins that change what bash runs.
func TestLaneScriptsFailsClosedOnUnmodeledText(t *testing.T) {
	t.Parallel()
	const (
		gate    = "bash \"${ROOT_DIR}/scripts/gate.sh\"\n"
		source  = "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n"
		library = "scripts/lib/l.sh"
		shadows = "defines a function named"
		program = "is not a program this reader can name"
		named   = "name this reader cannot spell"
		through = "a builtin or a function this reader does not read through"
		builtin = "a builtin this reader does not model"
		spelled = "an expansion spells"
		split   = "a command word bash may split"
		// inArithmetic is the error for an operand bash evaluates as an
		// expression, as x='PATH=1'; (( x )) assigns PATH.
		inArithmetic = "in arithmetic"
	)
	forwarder := map[string]string{library: "run() {\n  (\n    cd / &&\n      \"$@\"\n  )\n}\n"}
	cases := []struct {
		name, lane string
		files      map[string]string
		want       string // wired, unwired, or an error substring
	}{
		{"chained command eval", strings.Repeat("command eval ", 32) + "true\n" + gate, nil, "nests eval, trap or child shell text"},
		{"eval within a command eval", "command eval command eval 'bash() { :; }'\n" + gate, nil, shadows},
		{"eval after then", "if true; then eval 'bash() { :; }'; fi\n" + gate, nil, shadows},
		{"escaped eval", "\\eval 'bash() { :; }'\n" + gate, nil, shadows},
		{"quoted eval", "\"eval\" 'bash() { :; }'\n" + gate, nil, shadows},
		{"eval after command -p", "command -p eval 'bash() { :; }'\n" + gate, nil, shadows},
		{"debug trap defines a function", "trap 'bash() { :; }' DEBUG\n" + gate, nil, shadows},
		{"builtin trap -- defines a function", "builtin trap -- 'bash() { :; }' ERR\n" + gate, nil, shadows},
		{"library trap defines a function", source + gate, map[string]string{library: "trap 'bash() { :; }' DEBUG\n"}, shadows},
		{"trap action of an expansion", "trap \"$1\" DEBUG\n" + gate, nil, "a trap on a signal other than EXIT"},
		{"exit trap action of an expansion", "trap 'rm -f \"${tmp}\"' EXIT\n" + gate, nil, "wired"},
		{"library program variable holds hash", source + gate, map[string]string{library: "c=hash\n\"$c\" -p /bin/true bash\n"}, program},
		{"library program variable holds source", source + gate, map[string]string{library: "f() {\n  local x=source\n  \"$x\" /tmp/e.sh\n}\n"}, program},
		{"lane program variable holds hash", "c=hash; \"$c\" -p /bin/true bash\n" + gate, nil, program},
		{"unquoted program variable", "c=hash; $c -p /bin/true bash\n" + gate, nil, split},
		{"program variable built by +=", "c=ha; c+=sh; \"$c\" -p /bin/true bash\n" + gate, nil, program},
		{"program variable appended to its inherited value", "c+=cat; \"$c\" /dev/null\n" + gate, nil, program},
		{"program variable from a substitution", "c=\"$(printf hash)\"; \"$c\" -p /bin/true bash\n" + gate, nil, program},
		{"program variable read", "read -r c <<<hash; \"$c\" -p /bin/true bash\n" + gate, nil, program},
		{"program variable from a positional", "c=\"$1\"; \"$c\" -p /bin/true bash\n" + gate, nil, program},
		{"program variable holds a name", "c=make; \"${c}\" all\n" + gate, nil, "wired"},
		{"program default is a builtin", "\"${X:-eval}\" x\n" + gate, nil, program},
		{"program variable holds a function", "f() { :; }\nX=f\n\"${X}\" x\n" + gate, nil, program},
		{"program variable from a for loop", "for X in /usr/bin/true alias; do \"${X}\" bash=true; done\n" + gate, nil, program},
		{"program variable holds a path", "X=/usr/bin/true\n\"${X}\"\n" + gate, nil, "wired"},
		{"library forwarder runs hash -p", source + "run hash -p /bin/true bash\n" + gate, forwarder, through},
		{"library forwarder runs eval", source + "run eval 'bash() { :; }'\n" + gate, forwarder, through},
		{"library forwarder runs trap", source + "run trap 'bash() { :; }' DEBUG\n" + gate, forwarder, through},
		{"library forwarder runs a function", source + "f() { :; }\nrun f\n" + gate, forwarder, through},
		{"library forwarder shifts", source + "run x hash -p /bin/true bash\n" + gate, map[string]string{library: "run() {\n  shift\n  \"$@\"\n}\n"}, "cannot spell"},
		{"library forwarder sets its parameters", source + "run x hash -p /bin/true bash\n" + gate, map[string]string{library: "run() {\n  set -- \"${@:2}\"\n  \"$@\"\n}\n"}, spelled},
		{"library forwarder sets them by builtin", source + "run x\n" + gate, map[string]string{library: "run() {\n  builtin set -- hash -p /bin/true bash\n  \"$@\"\n}\n"}, "cannot spell"},
		{"library forwarder shifts after", source + "run x\n" + gate, map[string]string{library: "run() {\n  while :; do\n    \"$@\"\n    shift\n  done\n}\n"}, "cannot spell"},
		{"library forwarder unquoted", source + "run x\n" + gate, map[string]string{library: "run() {\n  $@\n}\n"}, split},
		{"library runs a local positional", source + "run hash -p /bin/true bash\n" + gate, map[string]string{library: "run() {\n  local c=\"$1\"\n  shift\n  \"$c\" \"$@\"\n}\n"}, program},
		{"library forwarder runs alias", source + "run alias bash=true\n" + gate, forwarder, "alias"},
		{"library forwarder runs a computed name", source + "run \"$1\" x\n" + gate, forwarder, "cannot spell"},
		{"library forwarder runs a program", source + "HOST_BIN=docker\nrun make\nrun \"${HOST_BIN:-docker}\" ps\n" + gate, forwarder, "wired"},
		{"library forwarder runs an inherited program", source + "run \"${HOST_BIN:-docker}\" ps\n" + gate, forwarder, "never assigns"},
		{"library forwards through itself", source + strings.Repeat("run ", 9) + "true\n" + gate, forwarder, through},
		{"lane forwarder", "run() { \"$@\"; }\nrun hash -p /bin/true bash\n" + gate, nil, "cannot spell"},
		{"child shell reads the root from a heredoc", "bash <<'EOS'\n\"${ROOT_DIR}/scripts/gate.sh\"\nEOS\n", nil, "child shell"},
		{"child shell -s reads the root", "bash -s <<<'\"${ROOT_DIR}/scripts/gate.sh\"'\n", nil, "child shell"},
		{"child shell -c reads the root", "bash -c '\"${ROOT_DIR}/scripts/gate.sh\"'\n", nil, "child shell"},
		{"child shell reads the root from /dev/stdin", "bash /dev/stdin <<'EOS'\n\"${ROOT_DIR}/scripts/gate.sh\"\nEOS\n", nil, "child shell"},
		{"enable -f", "enable -f /tmp/x.so bash\n" + gate, nil, builtin},
		{"enable -n", "enable -n hash\n" + gate, nil, builtin},
		{"complete -C", "complete -C 'bash() { :; }' x\n" + gate, nil, builtin},
		{"compgen -C", "compgen -C 'bash() { :; }' x\n" + gate, nil, "compgen -F or -C"},
		{"compgen -G", "compgen -G \"${ROOT_DIR}/*\" >/dev/null\n" + gate, nil, "wired"},
		{"fc runs history", "set -o history\nhistory -s 'bash() { :; }'\nfc -s bash\n" + gate, nil, "history"},
		{"fc alone", "fc -s bash\n" + gate, nil, builtin},
		{"set an expanded option", "set -$x\n" + gate, nil, spelled},
		{"set -o an expanded option", "set -o $o\n" + gate, nil, spelled},
		{"set parameters from an array", "set \"${a[@]}\"\n" + gate, nil, spelled},
		{"shopt an expanded option", "shopt -s $v\n" + gate, nil, spelled},
		{"read an expanded option", "read $o x </dev/null\n" + gate, nil, named},
		{"read an option an expansion ends", "read -r$o x </dev/null\n" + gate, nil, spelled},
		{"export a braced PATH", "export P{A,X}TH=/tmp\n" + gate, nil, "declares a name this reader cannot spell"},
		{"export a braced BASH_ENV", "export B{ASH_,X}ENV=/tmp/e.sh\n" + gate, nil, "declares a name this reader cannot spell"},
		{"PS4", "PS4='$(bash() { :; })'\nset -x\n" + gate, nil, "assigns PS4"},
		{"library PS4", source + gate, map[string]string{library: "f() { local PS4=x; }\n"}, "assigns PS4"},
		{"BASHOPTS", "BASHOPTS=extdebug bash \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "assigns BASHOPTS"},
		{"arithmetic assigns PATH with spaces", "(( PATH = 1 ))\n" + gate, nil, "assigns in arithmetic"},
		{"process substitution", "read -r x < <(printf '%s\\n' \"${y}\")\n" + gate, nil, "wired"},
		{"process substitution reads a computed name", "diff <(read -r \"$v\" </dev/null) /dev/null\n" + gate, nil, named},
		{"history -s", "history -s 'bash() { :; }'\n" + gate, nil, "history"},
		{"set -n", "set -n\n" + gate, nil, "set -n"},
		{"set -o noexec", "set -o noexec\n" + gate, nil, "set -n"},
		{"set -t", "set -t\n:\n" + gate, nil, "set -t"},
		{"set -o onecmd", "set -o onecmd\n:\n" + gate, nil, "set -t"},
		{"shopt -so noexec", "shopt -so noexec\n" + gate, nil, "shopt -so"},
		{"shopt -s -o noexec", "shopt -s -o noexec\n" + gate, nil, "shopt -so"},
		{"shopt -o -s noexec", "shopt -o -s noexec\n" + gate, nil, "shopt -so"},
		{"shopt -so onecmd", "shopt -so onecmd\n:\n" + gate, nil, "shopt -so"},
		{"child shell -o noexec", "bash -o noexec \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell -eo noexec", "bash -eo noexec \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell -ex -o noexec", "bash -ex -o noexec \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell --norc -o noexec", "bash --norc -o noexec \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"exec child shell -o noexec", "exec bash -o noexec \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell -D", "bash -D \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell --dump-strings", "bash --dump-strings \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell --version", "bash --version \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell --help", "bash --help \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"env child shell --version", "env bash --version \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell -O an unknown option", "bash -O extdebug \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"interactive child shell", "bash -i \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"login child shell", "bash -l \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"restricted child shell", "bash -r \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"restricted child shell by its long option", "bash --restricted \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"restricted child shell in an option cluster", "bash -er \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "unwired"},
		{"child shell with options that run it", "bash --norc --noprofile -euo pipefail -O extglob -x \"${ROOT_DIR}/scripts/gate.sh\"\n", nil, "wired"},
		{"read a brace group of names", "read -r P{A,X}TH <<<'/tmp /tmp'\n" + gate, nil, named},
		{"printf -v a brace group of names", "printf -v P{A,X}TH /tmp\n" + gate, nil, named},
		{"read a glob name", "read -r P?TH </dev/null\n" + gate, nil, named},
		{"printf -v a bracket glob name", "printf -v P[A]TH /tmp\n" + gate, nil, named},
		{"mapfile a brace group of names", "mapfile -t P{A,X}TH </dev/null\n" + gate, nil, named},
		{"getopts a glob name", "getopts a P?TH\n" + gate, nil, named},
		{"inherited program variable", "\"${TOOL}\" -p /bin/true bash\n" + gate, nil, "never assigns"},
		{"inherited program variable with a default", "\"${TOOL:-make}\" -p /bin/true bash\n" + gate, nil, "never assigns"},
		{"coproc named for a startup variable", "coproc PATH { cat; }\n" + gate, nil, "unwired"},
		{"prompt expansion assigns a computed name", "a=BASH_; x=\"\\${${a}CMDS[bash]:=/bin/true}\"; : \"${x@P}\"\n" + gate, nil, "@P operator"},
		{"inert expansion operator", "x=y; echo \"${x@Q}\"\n" + gate, nil, "wired"},
		{"quoted arithmetic beside a hash rebind", "hash -p /bin/true bash \"$((0))\"\n" + gate, nil, "binds a name to a path"},
		{"arithmetic evaluates a variable's value", "x='PATH=1'; (( x ))\n" + gate, nil, inArithmetic},
		{"arithmetic evaluates a computed value", "a=PA; x=\"${a}TH=1\"; (( x ))\n" + gate, nil, inArithmetic},
		{"arithmetic evaluates a hash table entry", "a=BASH_; x=\"${a}CMDS[bash]=2\"; (( x ))\n" + gate, nil, inArithmetic},
		{"arithmetic expansion evaluates a variable's value", "x='PATH=1'; : $(( x ))\n" + gate, nil, inArithmetic},
		{"integer test evaluates a variable's value", "x='PATH=1'; [[ x -eq 1 ]]\n" + gate, nil, inArithmetic},
		{"integer test after && evaluates a variable's value", "x='PATH=1'; [[ -n y && \"$x\" -eq 1 ]]\n" + gate, nil, inArithmetic},
		{"subscript evaluates a variable's value", "x='PATH=1'; z[x]=1\n" + gate, nil, inArithmetic},
		{"substring offset evaluates a variable's value", "x='PATH=1'; y=abc; echo \"${y:x}\"\n" + gate, nil, inArithmetic},
		{"substring offset assigns", "y=abc; echo ${y:PATH=1}\n" + gate, nil, inArithmetic},
		{"while(( )) evaluates a variable's value", "x='PATH=1'; while((x)); do break; done\n" + gate, nil, inArithmetic},
		{"arithmetic evaluates an inherited value", "(( x ))\n" + gate, nil, inArithmetic},
		{"arithmetic evaluates a value past an expansion", "y='PATH=1'; x=$(( 1 ))+y; (( x ))\n" + gate, nil, inArithmetic},
		{"library arithmetic evaluates a variable's value", source + gate, map[string]string{library: "x='PATH=1'\n: $((x))\n"}, inArithmetic},
		{"integer declaration", "declare -i n; n='PATH=1'\n" + gate, nil, "integer variable"},
		{"arithmetic evaluates a positional parameter", "(( $1 ))\n" + gate, nil, inArithmetic},
		{"let evaluates a variable's value", "x='PATH=1'; let x\n" + gate, nil, inArithmetic},
		{"$[ ] evaluates a variable's value", "x='PATH=1'; : $[ x ]\n" + gate, nil, inArithmetic},
		{"for (( )) evaluates a variable's value", "x='PATH=1'; for (( i = x; i < 1; i++ )); do :; done\n" + gate, nil, inArithmetic},
		{"unset subscript evaluates a variable's value", "z=(1 2); x='PATH=1'; unset 'z[x]'\n" + gate, nil, inArithmetic},
		{"test -v subscript evaluates a variable's value", "x='PATH=1'; [[ -v z[x] ]]\n" + gate, nil, inArithmetic},
		{"substring length evaluates a variable's value", "x='PATH=1'; y=abc; echo \"${y:0:x}\"\n" + gate, nil, inArithmetic},
		{"local integer declaration", "f() { local -i n; n='PATH=1'; }\nf\n" + gate, nil, "integer variable"},
		{"arithmetic of numbers", "n=0; n=$((n + 1)); m=\"${n}\"; (( m < 3 )); [[ \"${n}\" -gt 0 ]]; a[n]=1; y=abc\nk=$(( n + 1 ))\necho \"${a[n]} ${y:k} ${#y} $(( ${#y} + $# ))\"\n" + gate, nil, "wired"},
		{"associative array subscript", "declare -A m=()\nread -r x </dev/null || true\nm[\"${x}\"]=1\necho \"${m[${x}]:-}\"\n" + gate, nil, inArithmetic},
		{"associative array unset by a computed name", "declare -A m\nn=m\nunset \"$n\"\nx='PATH=1'\nm[x]=1\n" + gate, nil, inArithmetic},
		{"associative array declared after &&", "false && declare -A m\nx='PATH=1'\nm[x]=1\n" + gate, nil, inArithmetic},
		{"associative array declared in a pipeline", "declare -A m | true\nx='PATH=1'\nm[x]=1\n" + gate, nil, inArithmetic},
		{"heredoc arithmetic evaluates a variable's value", "a=PA; x=\"${a}TH=1\"\ncat <<EOF >/dev/null\n$(( x ))\nEOF\n" + gate, nil, inArithmetic},
		{"heredoc subscript evaluates a variable's value", "a=PA; x=\"${a}TH=1\"\ncat <<EOF >/dev/null\n${z[x]-}\nEOF\n" + gate, nil, inArithmetic},
		{"[ -v subscript evaluates a variable's value", "x='PATH=1'; [ -v 'z[x]' ]\n" + gate, nil, inArithmetic},
		{"test builtin -v subscript evaluates a variable's value", "x='PATH=1'; test -v 'z[x]'\n" + gate, nil, inArithmetic},
		{"indirect expansion of a built element name", "l='['; r=']'; a=PA; x=\"z${l}${a}TH=1${r}\"; : \"${!x-}\"\n" + gate, nil, "expands ${!x}"},
		{"[[ -v of a built element name", "l='['; r=']'; a=PA; x=\"z${l}${a}TH=1${r}\"; [[ -v $x ]]\n" + gate, nil, "names a variable by"},
		{"[ -v of a built element name", "l='['; r=']'; a=PA; x=\"z${l}${a}TH=1${r}\"; [ -v \"$x\" ]\n" + gate, nil, "names a variable by"},
		{"unset of a built element name", "l='['; r=']'; a=PA; x=\"z${l}${a}TH=1${r}\"; unset \"$x\"\n" + gate, nil, "names a variable by"},
		{"unset of a listing that holds PATH", "for name in \"${!P@}\"; do unset \"${name}\"; done\n" + gate, nil, "names a variable by"},
		{"unset of a list that holds PATH", "for name in CC PATH; do unset \"${name}\"; done\n" + gate, nil, "names a variable by"},
		{"indirect expansion of a positional parameter", ": \"${!1-}\"\n" + gate, nil, "whose name a parameter or an expansion computes"},
		{"unset of a listed name before its loop", "unset \"${name}\"\nfor name in CC; do :; done\n" + gate, nil, "names a variable by"},
		{"arithmetic after the reader loses track of nesting", "coproc { :; }\nn=0\n(( n ))\n" + gate, nil, inArithmetic},
		{"indirect expansion and unset of a prefix listing", "for name in \"${!GO@}\" CC; do : \"${!name-}\"; unset \"${name}\"; done\n" + gate, nil, "wired"},
		{"arithmetic before the first assignment", "(( n )) || true; n=0\n" + gate, nil, inArithmetic},
		{"arithmetic after a conditional assignment", "false && n=0; (( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment in a branch", "if false; then n=0; fi; (( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment in another case branch", "case x in\na)\nn=0\n;;\n*)\n(( n ))\n;;\nesac\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment in a substitution", ": \"$(n=0)\"; (( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment in the other branch", "if false; then n=0; else (( n )); fi\n" + gate, nil, inArithmetic},
		{"arithmetic after a conditional source", "false && source \"${ROOT_DIR}/scripts/lib/l.sh\"\n(( n ))\n" + gate, map[string]string{library: "n=0\n"}, inArithmetic},
		{"arithmetic after a library assigns a number", source + "(( n ))\n" + gate, map[string]string{library: "n=0\n"}, "wired"},
		{"arithmetic after an assignment in a child shell", "bash -c 'n=0'\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment in a trap action", "trap 'n=0' EXIT\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a prefix assignment before a program", "n=0 true\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a prefix assignment before a special builtin", "n=0 :\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a prefix assignment before a function", "f() { :; }\nn=0 f\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a prefix assignment before command", "n=0 command true\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a prefix assignment before builtin", "n=0 builtin true\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a prefix assignment before env", "n=0 env true\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a prefix assignment before eval", "n=0 eval true\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a background assignment", "n=0 &\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment piped into a command", "n=0 | true\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment a pipe feeds", "true | n=0\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment a pipe feeds on the next line", "true |\n  n=0\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after a coproc assignment", "coproc n=0\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an eval in a subshell", "( eval n=0 )\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an eval after ||", "true || eval n=0\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after declare with no value", "declare n\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after typeset with no value", "typeset n\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after export with no value", "export n\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after readonly with no value", "readonly n\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after declare -g with no value", "declare -g n\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after declare -n", "declare -n n=m\n(( n ))\n" + gate, nil, named},
		{"arithmetic after an assignment after || on the line before", "true ||\n  n=0\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic of a variable bash sets after a prefix assignment", "a=PA\nx86_64=\"${a}TH=1\"\nHOSTTYPE=0 true\n(( HOSTTYPE )) || true\n" + gate, nil, inArithmetic},
		{"localvar_inherit", "shopt -s localvar_inherit\nf() { local n; (( n )); }\nn=0\nf\n" + gate, nil, "localvar_inherit"},
		{"localvar_inherit spelled by a brace expansion", "shopt -s localvar_{inherit,unset}\n" + gate, nil, "localvar_inherit"},
		{"arithmetic after declare with a value", "declare n=0\nexport m=1\n(( n + m ))\n" + gate, nil, "wired"},
		{"arithmetic in a for loop over numbers", "for n in 1 2; do (( n )); done\n" + gate, nil, "wired"},
		{"arithmetic after an assignment an eval makes", "eval 'n=0'\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment in an uncalled function", "f() { n=0; }\n(( n ))\n" + gate, nil, inArithmetic},
		{"arithmetic after an assignment in the same branch", "if true; then n=0; (( n )); fi\nf() { local m=0; (( m )); }\nf\n" + gate, nil, "wired"},
		{"program variable before its assignment", "\"${TOOL}\" -p /bin/true bash; TOOL=make\n" + gate, nil, "read before the lane assigns it"},
		{"prompt expansion of a nested subscript", "a=BASH_; v[0]=\"\\${${a}CMDS[bash]:=/bin/true}\"; i=0; : \"${v[${i}]@P}\"\n" + gate, nil, "@P operator"},
		{"library prompt expansion of a nested subscript", source + gate, map[string]string{library: "a=BASH_; v[0]=\"\\${${a}CMDS[bash]:=/bin/true}\"; i=0; : \"${v[${i}]@P}\"\n"}, "@P operator"},
		{"associative array declared in a function", "f() {\ndeclare -A m\n}\nx='PATH=1'\nm[x]=1\n" + gate, nil, inArithmetic},
		{"associative array declared in a loop", "for _ in 1; do\ndeclare -A m\ndone\nx='PATH=1'\nm[x]=1\n" + gate, nil, inArithmetic},
		{"associative array declared after a use", "x='PATH=1'\nm[x]=1\ndeclare -A m 2>/dev/null || true\n" + gate, nil, inArithmetic},
		{"associative array used in a library first", source + "declare -A m\n" + gate, map[string]string{library: "x='PATH=1'\nm[x]=1\n"}, inArithmetic},
		{"arithmetic evaluates a variable only an argument assigns", "echo x=1\n(( x ))\n" + gate, nil, inArithmetic},
		{"arithmetic evaluates a copy of a value", "y='PATH=1'; x=\"${y}\"; (( x ))\n" + gate, nil, inArithmetic},
		{"associative array unset", "declare -A m\nunset m\nx='PATH=1'\nm[x]=1\n" + gate, nil, inArithmetic},
		{"arithmetic past the reader's bound", "echo \"" + strings.Repeat("${a:", 4000) + "0" + strings.Repeat("}", 4000) + "\"\n" + gate, nil, "nests more expansions"},
		{"shopt -s extdebug", "shopt -s extdebug\ntrap false DEBUG\n" + gate, nil, "extdebug"},
		{"library heredoc whose delimiter this reader cannot end", source + gate, map[string]string{library: ": <<$'\\x45OF'\nx\nEOF\nbash() { :; }\n"}, "heredoc"},
		{"eval heredoc whose delimiter this reader cannot end", "eval ': <<\"EO\\\nF\"\nx\nEOF\nbash() { :; }'\n" + gate, nil, "more text than this reader parses"},
		{"lane heredoc whose delimiter this reader cannot end", ": <<$'\\x45OF'\nx\nEOF\nbash() { :; }\n" + gate, nil, "heredoc"},
		{"library sets PATH", source + gate, map[string]string{library: "PATH=/tmp/x:$PATH\n"}, "assigns PATH"},
		{"library passes PATH to a program env runs by its path", source + gate, map[string]string{library: "lock() {\n  /usr/bin/env -i PATH=/usr/bin:/bin \\\n    /usr/bin/perl lock.pl \"$@\"\n}\n"}, "wired"},
		{"library passes PATH to a program env looks up", source + gate, map[string]string{library: "/usr/bin/env -i PATH=/tmp/x perl lock.pl\n"}, "assigns PATH"},
		{"library passes PATH to a shell env runs by its path", source + gate, map[string]string{library: "env PATH=/tmp/x /bin/bash -c :\n"}, "assigns PATH"},
		{"library sets PATH before env", source + gate, map[string]string{library: "PATH=/tmp/x /usr/bin/env /usr/bin/perl lock.pl\n"}, "assigns PATH"},
		{"library exports BASH_ENV", source + gate, map[string]string{library: "f() { export BASH_ENV=/tmp/e.sh; }\n"}, "assigns BASH_ENV"},
		{"lane sets PATH", "PATH=/tmp/x:$PATH\n" + gate, nil, "unwired"},
		{"lane sets PATH for one command", "PATH=/tmp/x " + gate, nil, "unwired"},
		{"lane runs env PATH=", "env PATH=/tmp/x " + gate, nil, "unwired"},
		{"lane reads ENV", "read -r ENV </dev/null\n" + gate, nil, "unwired"},
		{"computed printf -v into BASH_CMDS", "v=BASH_; printf -v \"${v}CMDS[bash]\" /bin/true\n" + gate, nil, named},
		{"computed declare", "declare \"${v}=x\"\n" + gate, nil, named},
		{"computed read", "read -r \"${v}\" </dev/null\n" + gate, nil, named},
		{"computed local", "f() { local \"${v}\"=x; }\nf\n" + gate, nil, named},
		{"nameref", "declare -n ref=PATH\n" + gate, nil, named},
		{"indirect default", "x=PATH; : \"${!x:=/tmp}\"\n" + gate, nil, named},
		{"indirect default in an assignment", "x=PATH; y=${!x:=/tmp}\n" + gate, nil, named},
		{"library assigns a caller's names", source + "claim x OUT RES\n" + gate, map[string]string{library: "claim() {\n  local out=\"$2\"\n  local res=\"$3\"\n  printf -v \"${res}\" '%s' x\n  printf -v \"${out}\" '%s' y\n}\n"}, "wired"},
		{"library assigns PATH by a caller's name", source + "claim x PATH\n" + gate, map[string]string{library: "claim() {\n  local out=\"$2\"\n  printf -v \"${out}\" '%s' /tmp\n}\n"}, "names \"PATH\""},
		{"library assigns BASHOPTS by a caller's name", source + "claim x BASHOPTS\n" + gate, map[string]string{library: "claim() {\n  local out=\"$2\"\n  printf -v \"${out}\" '%s' extdebug\n}\n"}, "names \"BASHOPTS\""},
		{"library assigns BASH_COMPAT by a caller's name", source + "claim x BASH_COMPAT\n" + gate, map[string]string{library: "claim() {\n  local out=\"$2\"\n  printf -v \"${out}\" '%s' 31\n}\n"}, "names \"BASH_COMPAT\""},
		{"library assigns BASH_ALIASES by a caller's name", source + "claim x 'BASH_ALIASES[bash]'\n" + gate, map[string]string{library: "claim() {\n  local out=\"$2\"\n  printf -v \"${out}\" '%s' true\n}\n"}, "lane wiring is unreadable"},
		{"library output after a redirection in arithmetic", source + "OUT=0\nclaim 2>/dev/null x OUT\n(( OUT ))\n" + gate, map[string]string{library: "claim() {\n  local out=\"$2\"\n  printf -v \"${out}\" '%s' y\n}\n"}, inArithmetic},
		{"library output program variable", source + "claim x OUT\n\"$OUT\" -p /bin/true bash\n" + gate, map[string]string{library: "claim() {\n  local out=\"$2\"\n  printf -v \"${out}\" '%s' hash\n}\n"}, program},
		{"library parameter written by a callee", source + "claim x OUT\n" + gate, map[string]string{library: "claim() {\n  local out=\"$2\"\n  g\n  printf -v \"${out}\" '%s' /tmp\n}\ng() { out=y; }\n"}, named},
		{"root length keeps the root", "echo \"${#ROOT_DIR}\"\n" + gate, nil, "wired"},
		{"long word without a group keeps the root", "echo " + strings.Repeat("x", 5000) + "\n" + gate, nil, "wired"},
		{"long library substitution", source + gate, map[string]string{library: "x=\"$(printf %s " + strings.Repeat("y", 5000) + ")\"\n"}, "wired"},
		{"long word with a group", "echo {a,b}" + strings.Repeat("x", 5000) + "\n" + gate, nil, "brace expansion is too large"},
		{"groups that make more words than the bound", "echo " + strings.Repeat("{a,b}", 9) + "\n" + gate, nil, "brace expansion is too large"},
		{"many groups in a short word", "echo " + strings.Repeat("{a,b}", 800) + "\n" + gate, nil, "brace expansion is too large"},
		{"a long quoted run of continued lines in a library", source + gate, map[string]string{library: "x=\"" + strings.Repeat("a \\\n", 20000) + "b\"\n"}, "than this reader parses"},
		{"hash table in a process substitution", "cat <(hash -p /bin/true bash; bash \"${ROOT_DIR}/scripts/gate.sh\")\n", nil, "unreadable"},
		{"a long quoted run of continued lines", "echo \"" + strings.Repeat("a \\\n", 20000) + "b\"\n" + gate, nil, "than this reader parses"},
		{"deeply nested library definitions", source + gate, map[string]string{library: strings.Repeat("f() {\n", 5000) + strings.Repeat("}\n", 5000)}, "nests function definitions deeper"},
		{"a long run of continued lines", "echo " + strings.Repeat("a \\\n", 20000) + "b\n" + gate, nil, "wired"},
		{"eval after a redirection", ">/dev/null eval 'bash() { :; }'\n" + gate, nil, shadows},
		{"eval after a redirection and its target", "> /dev/null 2>&1 eval 'bash() { :; }'\n" + gate, nil, shadows},
		{"trap after a named descriptor", "{fd}</dev/null trap 'bash() { :; }' DEBUG\n" + gate, nil, shadows},
		// Only the first eval of a command is read: reading each eval of
		// eight chained ones reads the long tail 2^8 times, past the bound.
		{"eight chained command evals", strings.Repeat("command eval ", 8) + "true " + strings.Repeat("x", 4000) + "\n" + gate, nil, "wired"},
		{"calls that fan out past the reader's bound", fanOut(20) + gate, nil, "past the shell reader's work bound"},
		// Twenty nested definitions are shallow, but each nested body is read
		// again on its own, so a wide innermost body runs past the bound.
		{"shallow nested library definitions over a wide body", source + gate, map[string]string{library: strings.Repeat("f() {\n", 20) + "echo " + strings.Repeat("x", 100000) + "\n" + strings.Repeat("}\n", 20)}, "nests function definitions deeper"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			files := map[string]string{"scripts/validate-repo.sh": "#!/bin/bash\nset -euo pipefail\n" + laneRoot + "\n" + tc.lane, "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": "name: w\n"}
			maps.Copy(files, tc.files)
			writeLaneTree(t, root, files)
			type result struct {
				got []string
				err error
			}
			done := make(chan result, 1)
			go func() { got, err := metadatautil.LaneScripts(root); done <- result{got, err} }()
			var got []string
			var err error
			select {
			case each := <-done:
				got, err = each.got, each.err
			case <-time.After(20 * time.Second):
				t.Fatal("LaneScripts() did not finish within 20s")
			}
			switch tc.want {
			case "wired", "unwired":
				if err != nil {
					t.Fatal(err)
				}
				if wired := slices.Contains(got, "scripts/gate.sh"); wired != (tc.want == "wired") {
					t.Fatalf("LaneScripts() = %q, want scripts/gate.sh %s", got, tc.want)
				}
			default:
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("LaneScripts() = %q, %v, want an error with %q", got, err, tc.want)
				}
			}
		})
	}
}

// A function's local holds a number by the function's own writes and every
// write outside a function that declares it local; a test of digits that
// skips the rest of a loop body or returns proves a local holds digits until
// a write to it; and an eval whose expansions are such numbers is read as the
// text it runs. Anything else fails closed.
func TestLaneScriptsReadsLocalNumbersGuardsAndEvals(t *testing.T) {
	t.Parallel()
	const (
		take     = "take() {\n  local fd=20\n  while [[ -e \"/dev/fd/${fd}\" ]]; do\n    fd=$((fd + 1))\n    [[ \"${fd}\" -lt 100 ]] || return 71\n  done\n  eval \"exec ${fd}<\\\"\\${target}\\\"\" || return 71\n}\n"
		release  = "release() {\n  local fd=\"\"\n  for fd in ${FDS}; do\n    [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n"
		loop     = "release() {\n  local fd=\"\"\n  for fd in ${FDS}; do\n"
		unread   = "lane wiring is unreadable"
		spelling = "evals text this reader cannot spell"
		number   = "cannot show is a number"
		program  = "is not a program this reader can name"
	)
	var guards strings.Builder
	for at := range 9 {
		fmt.Fprintf(&guards, "  local v%d=\"$1\"\n  [[ \"${v%d}\" =~ ^[0-9]+$ ]] || return\n", at, at)
	}
	nineGuards := guards.String()
	eightGuards := nineGuards[strings.Index(nineGuards, "  local v1="):]
	cases := []struct{ name, library, want string }{
		{"the cache lock's take and release", take + release, "wired"},
		{"the cache lock's take", take, "wired"},
		{"the cache lock's release", release, "wired"},
		{"a guard that returns", "close() {\n  local n=\"$1\"\n  [[ $n =~ ^[0-9]+$ ]] || return 1\n  eval \"exec $n<&-\"\n}\n", "wired"},
		{"a local number beside another function's string", take + "other() {\n  local fd=x\n}\n", "wired"},
		{"a local program variable beside another function's", "f() {\n  local c=\"\"\n  for c in /opt/x/bash /usr/local/bin/bash; do\n    \"${c}\" -p -c :\n  done\n}\ng() {\n  local c=\"$1\"\n  [[ -n \"${c}\" ]]\n}\n", "wired"},
		{"a local program variable a callee writes", "f() {\n  local c=/bin/true\n  h x\n  \"${c}\" x\n}\nh() {\n  c=\"$1\"\n}\n", program},
		{"a program variable a caller holds as a local", "c=/bin/true\nf() {\n  local c=hash\n  g\n}\ng() {\n  local x=1\n  \"${c}\" -p /bin/true bash\n}\n", program},
		{"a program variable run before its local", "c=/bin/true\nf() {\n  \"${c}\" -p /bin/true bash\n  local c=/bin/true\n  \"${c}\" x\n}\ng() {\n  local c=hash\n  f\n}\n", program},
		{"a program variable in a function another defines", "c=/bin/true\ng() {\n  local c=/bin/true\n  \"${c}\" -p /bin/true bash\n  h() {\n    \"${c}\" -p /bin/true bash\n  }\n}\nk() {\n  local c=hash\n  h\n}\n", program},
		{"a local program variable in a body opened as f(){", "f(){\n  local c=\"\"\n  for c in /opt/x/bash /usr/local/bin/bash; do\n    \"${c}\" -p -c :\n  done\n}\ng() {\n  local c=\"$1\"\n  [[ -n \"${c}\" ]]\n}\n", "wired"},
		{"a local program variable in a trap", "c=/bin/true\nf() {\n  local c=/bin/true\n  trap '\"${c}\" x' EXIT\n}\ng() {\n  local c=hash\n}\n", program},
		{"a local program variable also run where it is no local", "c=/bin/true\nf() {\n  local c=/bin/true\n  \"${c}\" x\n}\ng() {\n  local c=hash\n  f\n  h\n}\nh() {\n  \"${c}\" -p /bin/true bash\n}\n", program},
		{"a redirection exec runs", "f() {\n  exec 3<\"${target}\"\n}\n", "wired"},
		{"a redirection exec runs, with its target apart", "f() {\n  exec 3< \"${target}\"\n}\n", "wired"},
		{"a guard that continues on the next line", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] ||\n      continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", "wired"},
		{"a guard that only goes on", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] || true\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a guard read after its loop", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n  done\n  eval \"exec ${fd}<&-\"\n}\n", spelling},
		{"an eval whose quoted text starts like a redirection", "f() {\n  local n=1\n  eval \"2>/dev/null hash -p /bin/true bash ${n}\"\n}\n", spelling},
		{"a library output after a redirection with a quoted target", "claim() {\n  local out=\"$2\"\n  printf -v \"${out}\" '%s' y\n}\nOUT=0\n3<\"${f}\" claim x OUT\n(( OUT ))\n", number},
		{"a spelled eval that evaluates arithmetic", "x='PATH=1'\nf() {\n  local n=1\n  eval \"(( x )); : ${n}\"\n}\n", "in arithmetic"},
		{"a local named _", "f() {\n  local _=1\n  eval \"exec ${_}<&-\"\n}\n", spelling},
		{"a local declared in a branch a callee writes", "f() {\n  if [[ -n x ]]; then\n    local fd\n  fi\n  fd=x\n}\ng() {\n  local fd=20\n  f\n  eval \"exec ${fd}<&-\"\n}\n", spelling},
		{"a local declared after a condition a callee writes", "f() {\n  [[ -n x ]] && local fd\n  fd=x\n}\ng() {\n  local fd=20\n  f\n  eval \"exec ${fd}<&-\"\n}\n", spelling},
		{"a callee writes a name before it declares it local", "f() {\n  fd=x\n  local fd=1\n}\ng() {\n  local fd=20\n  f\n  eval \"exec ${fd}<&-\"\n}\n", spelling},
		{"nine guards, the first of which lapses", "f() {\n" + nineGuards + "  eval \"exec ${v0}<&-\"\n}\n", "limit of 8 guards"},
		{"eight guards", "f() {\n" + eightGuards + "  eval \"exec ${v1}<&-\"\n}\n", "wired"},
		{"an eval with a redirection", "f() {\n  local n=1\n  eval \": ${n}\" 2>/dev/null\n}\n", spelling},
		{"an eval of a backquoted substitution", "f() {\n  local n=1\n  eval \"exec ${n}<`echo f`\"\n}\n", spelling},
		{"an eval of a backquoted substitution it runs", "f() {\n  local n=1\n  eval \"exec ${n}<\\`hash -p /bin/true bash\\`\"\n}\n", "binds a name to a path"},
		{"a guard after a blank line and ||", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] ||\n\n      continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", "wired"},
		{"a guard ended by a semicolon", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]];\n    true || continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"an eval of a special parameter", "f() {\n  local n=1\n  eval \"exec ${n}<$_\"\n}\n", spelling},
		{"an eval of the last argument", "_=1\neval \"exec ${_}<&-\"\n", spelling},
		{"a guard that tests for a pattern", loop + "    [[ \"${fd}\" != ^[0-9]+$ ]] || continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a guard on an inherited name", "f() {\n  [[ \"${FD}\" =~ ^[0-9]+$ ]] || return\n  eval \"exec ${FD}<&-\"\n}\n", spelling},
		{"a regex anchored only at the start", loop + "    [[ \"${fd}\" =~ ^[0-9]+ ]] || continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a regex anchored only at the end", loop + "    [[ \"${fd}\" =~ [0-9]+$ ]] || continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a quoted regex", loop + "    [[ \"${fd}\" =~ \"^[0-9]+$\" ]] || continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a guard then and", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] && continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a guard after a condition", loop + "    true && [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a guard in one branch only", loop + "    if [[ -n x ]]; then\n      [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    fi\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a guard that continues outside a loop", "close() {\n  local n=\"$1\"\n  [[ $n =~ ^[0-9]+$ ]] || continue\n  eval \"exec $n<&-\"\n}\n", spelling},
		{"a guard that returns inside a loop", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] || return\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a reassignment after the guard", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    fd=\"${OTHER}\"\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a guard on a name that is not local", "release() {\n  for fd in ${FDS}; do\n    [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    eval \"exec ${fd}<&-\"\n  done\n}\n", spelling},
		{"a guarded local a called function writes", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    reset\n    eval \"exec ${fd}<&-\"\n  done\n}\nreset() {\n  fd=x\n}\n", spelling},
		{"a local number a called function writes", "f() {\n  local fd=20\n  reset\n  eval \"exec ${fd}<&-\"\n}\nreset() {\n  fd=x\n}\n", spelling},
		{"a guard read in a function the loop defines", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    g() {\n      eval \"exec ${fd}<&-\"\n    }\n  done\n}\n", spelling},
		{"a guard read in a trap that runs later", loop + "    [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    trap 'eval \"exec ${fd}<&-\"' EXIT\n  done\n}\n", spelling},
		{"an eval mixing a number with a string", "f() {\n  local fd=20\n  eval \"exec ${fd}<${target}\"\n}\n", spelling},
		{"an eval of an expansion with a default", "f() {\n  local fd=20\n  eval \"exec ${fd:-3}<&-\"\n}\n", spelling},
		{"an eval of a substitution", "f() {\n  local fd=20\n  eval \"exec $(echo ${fd})<&-\"\n}\n", spelling},
		{"one proven eval and the same text unproven", "f() {\n  local fd=20\n  eval \"exec ${fd}<&-\"\n}\ng() {\n  eval \"exec ${fd}<&-\"\n}\nfd=x\n", spelling},
		{"one unproven eval and the same text proven", "a() {\n  local fd=x\n  eval \"exec ${fd}<&-\"\n}\nb() {\n  local fd=20\n  eval \"exec ${fd}<&-\"\n}\n", spelling},
		{"an eval before its function declares the name local", "fd=1\ng() {\n  local fd=x\n  f\n}\nf() {\n  eval \"exec ${fd}<&-\"\n  local fd=2\n}\n", spelling},
		{"an eval of a name a caller holds as a local", "fd=1\ng() {\n  local fd=x\n  f\n}\nf() {\n  eval \"exec ${fd}<&-\"\n}\n", spelling},
		{"arithmetic of a name a caller holds as a local", "n=1\ng() {\n  local n='PATH=1'\n  f\n}\nf() {\n  (( n ))\n}\n", "in arithmetic"},
		{"arithmetic of a local that holds a string", "f() {\n  local fd=x\n  echo $((fd))\n}\n", number},
		{"a local read before its declaration", "fd=x\nf() {\n  echo $((fd))\n  local fd=1\n}\n", number},
		{"a local declared in its own value", "fd=x\nf() {\n  local fd=$((fd))\n}\n", number},
		{"a local declared in one branch", "f() {\n  if [[ -n x ]]; then\n    local fd=1\n  fi\n  echo $((fd))\n}\ng() {\n  local fd=x\n  f\n}\n", number},
		{"a local declared with an option", "f() {\n  local -g fd\n  fd=x\n}\ng() {\n  local fd=20\n  f\n  eval \"exec ${fd}<&-\"\n}\n", spelling},
		{"a local declared after a condition", "f() {\n  [[ -n x ]] && local fd=1\n  echo $((fd))\n}\ng() {\n  local fd=x\n  f\n}\n", number},
		{"a spelled eval that rebinds a program", "f() {\n  local n=1\n  eval \"hash -p /bin/true bash ${n}\"\n}\n", "binds a name to a path"},
		{"a spelled eval that defines a function", "f() {\n  local n=1\n  eval \"bash() { :; }; : ${n}\"\n}\n", "defines a function named"},
		{"a spelled eval whose number may be empty", "f() {\n  local n=1\n  eval \"${n}hash -p /bin/true bash\"\n}\n", "binds a name to a path"},
		{"an eval of a local number with no number written", "f() {\n  local fd=\"${X}\"\n  eval \"exec ${fd}<&-\"\n}\n", spelling},
	}
	// Each row below is a lane that bash runs without ever reaching the gate,
	// with the library it sources, when the reader would call it wired.
	const (
		x       = `'0>&0; bash() { :; } #'`
		guarded = `f() { local fd; fd="$1"; [[ $fd =~ ^[0-9]+$ ]] || return; eval "exec $fd<&-"; }; `
		looped  = `f() { local fd=""; for fd in "$@"; do [[ $fd =~ ^[0-9]+$ ]] || continue; eval "exec $fd<&-"; done; }; `
		shadow  = "instead of the builtin this reader reads"
		source  = `source "${ROOT_DIR}/scripts/lib/l.sh"; `
	)
	lanes := []struct{ name, lane, library, want string }{
		{"a function named return", `return() { :; }; ` + guarded + `f ` + x, "", shadow},
		{"a function named return by the function keyword", `function return { :; }; ` + guarded + `f ` + x, "", shadow},
		{"a function named return defined after its caller", guarded + `return() { :; }; f ` + x, "", shadow},
		{"a function named return an eval defines", `eval 'return() { :; }'; ` + guarded + `f ` + x, "", shadow},
		{"a function named return a library defines", source + guarded + `f ` + x, "return() { :; }\n", shadow},
		{"a function named continue", `continue() { :; }; ` + looped + `f ` + x, "", shadow},
		{"a function named set", `set() { exit 0; }; set -e`, "", shadow},
		{"an eval by the same text proven elsewhere, in a case branch on its header's line", `f() { local fd=7; eval "exec $fd<&-"; }; f; g() { local fd=` + x + `; case x in x) eval "exec $fd<&-" ;; esac; }; g`, "", spelling},
		{"an eval by the same text proven elsewhere, redirected after", `f() { local fd=7; eval "exec $fd<&-"; }; f; g() { local fd=` + x + `; eval "exec $fd<&-" 2>/dev/null; }; g`, "", spelling},
		{"the same eval twice, both proven", `f() { local fd=7; eval "exec $fd<&-"; }; g() { local fd=8; eval "exec $fd<&-"; }; f; g`, "", "wired"},
		{"a callee unsets a local, baring its caller's", `outer() { local fd=` + x + `; f; }; f() { local fd=7; h; eval "exec $fd<&-"; }; h() { unset fd; }; outer`, "", spelling},
		{"a callee unsets a guarded local", `outer() { local fd=` + x + `; f 7; }; ` + `f() { local fd; fd="$1"; [[ $fd =~ ^[0-9]+$ ]] || return; h; eval "exec $fd<&-"; }; h() { unset fd; }; outer`, "", spelling},
		{"a library callee unsets a local", source + `outer() { local fd=` + x + `; f; }; f() { local fd=7; h; eval "exec $fd<&-"; }; outer`, "h() { unset fd; }\n", spelling},
		{"a callee unsets a local, baring the inherited value", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { unset fd; }; f`, "", spelling},
		{"a callee unsets a local by -v", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { unset -v fd; }; f`, "", spelling},
		{"a callee unsets a local by an element", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { unset 'fd[0]'; }; f`, "", spelling},
		{"a callee unsets an exported local", `export fd=` + x + `; f() { local fd=7; h; eval "exec $fd<&-"; }; h() { unset fd; }; f`, "", spelling},
		{"a callee unsets a local by a name it lists", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { local name; for name in fd; do unset "${name}"; done; }; f`, "", spelling},
		{"a callee unsets a local by a prefix it lists", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { local name; for name in "${!f@}"; do unset "${name}"; done; }; f`, "", spelling},
		{"a callee unsets its own local", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { local fd; unset fd; }; f`, "", "wired"},
		{"a callee unsets a function", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { unset -f fd; }; f`, "", "wired"},
		{"a callee unsets names the eval does not read", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { local name; for name in CC "${!GO@}"; do unset "${name}"; done; }; f`, "", "wired"},
		{"a library unsets BASH_ENV", source, "unset BASH_ENV ENV\n", "wired"},
		{"a lane unsets BASH_ENV", `unset BASH_ENV ENV`, "", "wired"},
		{"a local declared with a redirection apart", `f() { local fd=7 2> "${log}"; eval "exec $fd<&-"; }; f`, "", "wired"},
		{"a callee unsets a program variable by a prefix it lists", `f() { local p=go; h; "$p" x; }; h() { local name; for name in "${!p@}"; do unset "${name}"; done; }; f`, "", program},
		{"a library unsets PATH", source, "unset PATH\n", "assigns PATH"},
		{"a callee unsets a local program variable", `f() { local p=go; h; "$p" 'bash() { :; }'; }; h() { unset p; }; f`, "", program},
		{"a loop value quoted like a redirection", `fd=7; for fd in ` + x + `; do :; done; eval "exec $fd<&-"`, "", spelling},
		{"a callee's loop over a local", `f() { local fd=7; h; eval "exec $fd<&-"; }; h() { for fd in ` + x + `; do :; done; }; f`, "", spelling},
		{"a loop over a guarded local", `f() { local fd; fd="$1"; [[ $fd =~ ^[0-9]+$ ]] || return; for fd in ` + x + `; do :; done; eval "exec $fd<&-"; }; f 7`, "", spelling},
		{"a select loop value quoted like a redirection", `fd=7; select fd in ` + x + `; do break; done <<<1; eval "exec $fd<&-"`, "", spelling},
		{"a guard that runs a quoted return", `f() { local fd; fd="$1"; [[ $fd =~ ^[0-9]+$ ]] || "return"; eval "exec $fd<&-"; }; f 7`, "", spelling},
		{"a guard that returns two statuses", `f() { local fd; fd="$1"; [[ $fd =~ ^[0-9]+$ ]] || return 1 2; eval "exec $fd<&-"; }; f 7`, "", spelling},
		{"a guard that returns an expanded status", `f() { local fd; fd="$1"; [[ $fd =~ ^[0-9]+$ ]] || return $x; eval "exec $fd<&-"; }; f 7`, "", spelling},
	}
	for _, form := range []string{`"eval"`, `\eval`, `e""val`, `2>/dev/null eval`, `command eval`, `builtin eval`, `Y=1 eval`} {
		lanes = append(lanes, struct{ name, lane, library, want string }{"an eval by the same text proven elsewhere, as " + form, `f() { local fd=7; eval "exec $fd<&-"; }; f; g() { local fd=` + x + `; ` + form + ` "exec $fd<&-"; }; g`, "", spelling})
	}
	for _, name := range strings.Fields("return continue break local unset set declare typeset read printf export readonly shift exit builtin command") {
		lanes = append(lanes, struct{ name, lane, library, want string }{"a bare function named " + name, name + `() { :; }`, "", "defines a function named"})
	}
	const ends, builtin = "may stop or end the lane's shell", "a builtin this reader does not model"
	for _, row := range [][2]string{ // a command that may end the lane, also in an unquoted heredoc's substitution, and what may not
		{"kill -9 $$", ends}, {"command kill -9 $$", ends}, {"builtin kill -STOP $$", ends}, {"env kill -9 $$", ends}, {"/bin/kill -9 $$", ends},
		{"trap 'exit 0' TERM; kill $$", "a trap on a signal other than EXIT"}, {"eval 'kill -9 $$'", ends}, {"f() { kill -9 $$; }; f", ends}, {"trap 'kill -9 0' DEBUG", "a trap on a signal other than EXIT"},
		{"logout", ends}, {"suspend -f", ends}, {"killall -STOP bash", ends}, {"pkill -P $$", ends}, {"k=/usr/bin/pkill; \"${k}\" bash", program}, {"cat <<EOF\n$(kill -9 $$)\nEOF", ends},
		{"cat <<EOF\n`kill -9 0`\nEOF", ends}, {"cat <<-EOF\n\t${x:-$(kill -9 $$)}\n\tEOF", ends}, {"cat <<EOF\n$(alias a=b)\nEOF", builtin},
		{"x='kill -9 $$'\ncat <<EOF\n$(eval \"$x\")\nEOF", spelling}, {"cat <<'EOF'\n$(kill -9 $$)\nEOF", "wired"},
		{"cat <<\"EOF\"\n`kill -9 $$`\nEOF", "wired"}, {"cat <<EOF\n\\$(kill -9 $$)\nEOF", "wired"}, {"cat <<EOF\n$(date)\nEOF", "wired"},
	} {
		lanes = append(lanes, struct{ name, lane, library, want string }{fmt.Sprintf("a lane that runs %q", row[0]), row[0], "", row[1]})
	}
	const y, shadowed, ended, exported = "${fd:=0>&0; bash() ( : ) #}", `"${p}" 'bash() ( : )'`, "ends the lane's shell before a later command", "exports a function"
	const lastpipe, inEval, setO = "as shopt -s lastpipe does", "defines a function in text an eval or a trap runs", "sets a set -o option"
	for _, form := range []string{": <<EOF\nY\nEOF", "read -r _ <<EOF\nY\nEOF", "{ :; } <<EOF\nY\nEOF", "while false; do :; done <<EOF\nY\nEOF", "h <<EOF\nY\nEOF",
		": <<-EOF\n\tY\n\tEOF", ": <<<\"Y\"", "exec 3<<<\"Y\"", "(( 1 )) <<<\"Y\"", ": >\"Y\"", ": 2>\"${fd=0>&0; bash() ( : ) #}\""} {
		lane := "h() { :; }\nf() {\nlocal fd=\n" + strings.ReplaceAll(form, "Y", y) + "\neval \"exec $fd<&-\"\n}\nf"
		lanes = append(lanes, struct{ name, lane, library, want string }{fmt.Sprintf("a default assigned in %q", form), lane, "", spelling})
	}
	for _, row := range [][2]string{ // a default assigned where bash expands, an export of a function, an exit before the gate, and what stays wired
		{"fd=\n: <<<\"" + y + "\"\neval \"exec $fd<&-\"", spelling}, {"p=\n: <<<\"${p:=eval}\"\n" + shadowed, program}, {"p=\n: 2>\"${p:=eval}\"\n" + shadowed, program},
		{"p=\nh() { :; } 2>\"${p:=eval}\"; h\n" + shadowed, program}, {"p=\n: <<EOF\n${p:=eval}\nEOF\n" + shadowed, program},
		{"f() {\nlocal p=\n: <<<\"${p:=eval}\"\n" + shadowed + "\n}\nf", program}, {"p=\n: <<<\"${p:=source}\"\n\"${p}\" /dev/null", program},
		{"g() { local fd=7; eval \"exec $fd<&-\"; }; g; g", "wired"}, {"g() { local fd=7; eval \"exec $fd<&-\"; }\ng\ng", "wired"},
		{"g() { local fd=7; eval \"exec $fd<&-\"; }; h() { g; }; k() { g; }; h; k", "wired"},
		{"g() { local fd=\"$1\"; [[ \"${fd}\" =~ ^[0-9]+$ ]] || return; eval \"exec ${fd}<&-\"; }; g 7; g 8", "wired"},
		{"touch() { :; }; export -f touch", exported}, {"touch() { :; }; declare -fx touch", exported}, {"touch() { :; }; typeset -f -x touch", exported},
		{"touch() { :; }; export touch", exported}, {"set -a", "as set -a does"}, {"set -o allexport", "as set -a does"}, {"set +o pipefail", "wired"},
		{"exit 0", ended}, {"exec true", ended}, {"{ exit 0; }", ended}, {"f() { exit 0; }; f", ended},
		{"f() { g; }; g() { command exit 0; }; f", ended}, {"eval 'exit 0'", ended}, {"f() { local s=0; eval \"exit $s\"; }; f", ended}, {"source \"${ROOT_DIR}/scripts/lib/l.sh\"\nexit 0", ended},
		{"trap 'exit 0' EXIT; false", "EXIT trap"}, {"f() { exit 256; }; trap f EXIT; false", "EXIT trap"}, {"trap 'exit 1' EXIT", "wired"},
		{"trap 'exit' EXIT", "wired"}, {"f() { local s=$?; return \"${s}\"; }; trap f EXIT", "wired"}, {"true || { echo x; exit 1; }", "wired"}, {"die() { echo x; exit 1; }; true || die", "wired"},
		{"f() { if true; then return; fi; exit 1; }; f", "wired"}, {"( exit 0 )", "wired"}, {"x=$(exit 0)", "wired"}, {"exec 3>&-", "wired"},
		{"trap '' INT", "wired"}, {"trap - INT", "wired"}, {"trap -- 'exit 0' EXIT; false", "EXIT trap"}, {"f() { if true; then exec true; fi; }; false && f() { :; }; trap f EXIT; false", "EXIT trap"},
		{"export -f bash", exported}, {"touch() { :; }; declare -f touch >/dev/null", "wired"}, {"declare -x V=1", "wired"}, {"f() { local -x V=1; }; f", "wired"},
		{"x=PATH\n: <<<\"${!x:=/tmp}\"", "whose name this reader cannot spell"}, {"true | exit 0", "wired"}, {"f() { true || return 0; exit 0; echo x; }; f", ended},
		{"f() { echo \"$(return 0)\"; exit 0; }; f", ended}, {"f() { x=$(return 0); exit 0; }; f", ended}, {"f() { (return 0); exit 0; }; f", ended},
		{"f() { return 0 | true; exit 0; }; f", ended}, {"f() { return 0 & exit 0; }; f", ended}, {"f() { { return 0; } | true; exit 0; }; f", ended},
		{"f() { { return 0; } & exit 0; }; f", ended}, {"f() { if true; then return 0; else :; fi | cat; exit 0; }; f", ended},
		{"f() { true | while true; do return 0; done; exit 0; }; f", ended}, {"f() { while true; do return 0; done | cat; exit 0; }; f", ended},
		{"f() { coproc { return 0; }; exit 0; }; f", ended}, {"coproc { :; }; exit 0", ended}, {"f() { coproc { :; }; true || exit 1; }; f", "wired"},
		{"f() { [ -n \"${x:-}\" ] || return 0; exit 1; }; f", "wired"}, {"f() { true && { return 0; }; exit 1; }; f", "wired"},
		{"f() { for i in 1; do return 0; done; exit 0; }; f", ended}, {"f() { if true; then return 0; else echo x | cat; fi; exit 1; }; f", "wired"},
		{"f() {\n  {\n    return 0\n  } | true\n  exit 0\n}\nf", ended}, {"f() {\n  (\n    return 0\n  )\n  exit 0\n}\nf", ended},
		{"f() { coproc return 0; exit 0; }; f", ended}, {"f() { echo \"coproc\"; true || return 0; exit 1; }; f", "wired"},
		{"coproc { :; }\ng() { true || return 0; exit 0; }\ng", ended}, {"coproc { :; }\n{ exit 0; }", ended},
		{"trap 'exec true' EXIT", "EXIT trap"}, {"trap 'exec env true' EXIT", "EXIT trap"}, {"f() { exec true; }; trap f EXIT", "EXIT trap"},
		{"trap \"eval 'exec true'\" EXIT", "EXIT trap"}, {"f() { trap 'exec true' EXIT; }; f", "EXIT trap"}, {"trap 'exec true' 0", "EXIT trap"},
		{"trap 'exec true' exit", "EXIT trap"}, {"trap 'rm -f x; exit' EXIT; false", "EXIT trap"}, {"trap 'source \"${ROOT_DIR}/scripts/lib/l.sh\"' EXIT; false", "EXIT trap"},
		{"trap 'rm -f x' EXIT", "wired"}, {"f() { local s=$?; rm -f x; return \"$s\"; }; trap f EXIT", "wired"},
		{"eval 'exec true'", ended}, {"eval \"exec true\"", ended}, {"eval exec true", ended}, {"eval 'exec env true'", ended},
		{"eval 'command exec true'", ended}, {"eval 'exec -a x true'", ended}, {"f() { eval 'exec true'; }; f", ended},
		{"g() { exit 0; }; eval g", ended}, {"g() { exit 0; }; eval 'g; :'", ended}, {"eval 'exec 3>&-'", "wired"},
		{"shopt -s lastpipe; : | exit 0", lastpipe}, {"shopt -s lastpipe; : | exec true", lastpipe}, {"shopt -s lastpipe; g() { exit 0; }; : | g", lastpipe},
		{"shopt -s lastpipe; : | eval 'exit 0'", lastpipe}, {"eval 'shopt -s lastpipe'; : | exit 0", lastpipe}, {"shopt -s lastpipe extglob; : | exit 0", lastpipe},
		{"shopt -s extglob", "wired"},
		{"p=eval; eval 'g() { \"$p\" \"bash() ( : )\"; }'; g", inEval}, {"eval 'g() { local fd=\"$1\"; eval \"exec $fd<&-\"; }'; g '0>&0; bash() ( : ) #'", inEval},
		{"fd='0>&0; bash() ( : ) #'; f() { local fd='0>&0; bash() ( : ) #'; eval 'g() { eval \"exec $fd<&-\"; }'; g; }; f", inEval},
		{"eval 'g() { exit 0; }'; g", inEval}, {"eval 'g() { exec true; }'; g", inEval}, {"eval 'g() { shopt -so allexport; }'; g; touch() { :; }", inEval},
		{"trap 'g() { :; }' EXIT", inEval},
		{"shopt -so allexport; touch() { :; }", setO}, {"shopt -s -o allexport; touch() { :; }", setO}, {"shopt -os allexport; touch() { :; }", setO},
		{"f() { shopt -so allexport; }; f; touch() { :; }", setO}, {"shopt -uo allexport", setO},
		{"g() { exit 0; }; if g; then :; fi", ended}, {"g() { exit 0; }; while g; do :; done", ended}, {"g() { :; }; if g; then :; fi", "wired"},
		{"case x in *) exit 0 ;; esac", ended}, {"case x in x) exit 0 ;; esac", ended}, {"case x in (x) exit 0;; esac", ended}, {"case x in x | y) exit 0;; esac", ended},
		{"case x in x) : ;; *) exit 0;; esac", ended}, {"case x in\n  x | y)\n    exit 0\n    ;;\nesac", ended}, {"case x in\n  *)\n    exit 0\n    ;;\nesac", ended},
		{"f() { case x in *) exit 0;; esac; }; f", ended}, {"f() { case x in x | y) exit 0;; esac; }; f", ended}, {"f() {\n  case x in\n    *)\n      exit 0\n      ;;\n  esac\n}\nf", ended},
		{"for i in 1; do exit 0; done", ended}, {"while :; do exit 0; done", ended}, {"until false; do exit 0; done", ended}, {"select x in a; do exit 0; done", ended},
		{"f() { if false; then return; fi; exit 0; }; f", ended}, {"f() { for i in; do return; done; exit 0; }; f", ended}, {"f() { while false; do return; done; exit 0; }; f", ended},
		{"case \"$1\" in\n  -h | --help)\n    exit 0\n    ;;\nesac", "wired"}, {"case \"${1}\" in -h|--help) exit 0;; esac", "wired"},
		{"case \"$1\" in\n  --profile | --help)\n    exit 0\n    ;;\nesac", ended}, {"case \"${1:-}\" in\n  -h | --help)\n    exit 0\n    ;;\nesac", ended},
		{"case \"$1\" in\n  -h | --help)\n    exit 0\n    ;;\n  *) exit 0 ;;\nesac", ended}, {"case \"$2\" in --help) exit 0;; esac", ended},
		{"f() { case \"$1\" in -h) exit 0;; esac; }; f x", ended}, {"case x in\n  x)\n    exit 1\n    ;;\nesac", "wired"},
		{"case x in a | b | x) exit 0;; esac", ended}, {"case x in\n  a) : ;;\n  x | y)\n    exit 0\n    ;;\nesac", ended},
		{"f() {\n  case \"$1\" in\n    -h | --help)\n      exit 0\n      ;;\n  esac\n}\nf -h", ended},
		{"[ -n \"${x:-}\" ] || { echo x; exit 0; }", "wired"}, {"eval ': ; exec true'", ended}, {"g() { for i in 1; do exit 0; done; }; eval g", ended},
		{"g() { exit 1; }; eval g", ended}, {"exec 3<<<\"x\"", "wired"},
		{"f() { false && return 0; exit 0; }; f", ended}, {"f() {\n  for i in; do\n    return\n  done\n  exit 0\n}\nf", ended},
		{"f() {\n" + nineGuards + "  v0=\"$1\"\n  eval \"exec ${v0}<&-\"\n}\nf 1", spelling + "$"},
		{"f() {\n" + nineGuards + "  g\n}\ng() {\n  eval \"exec ${v0}<&-\"\n}\nf 1", spelling + "$"},
		{"f() {\n" + nineGuards + "}\nf 1\ng() {\n  eval \"exec ${v0}<&-\"\n}\ng", spelling + "$"},
	} {
		lanes = append(lanes, struct{ name, lane, library, want string }{fmt.Sprintf("a lane that runs %q", row[0]), row[0], "", row[1]})
	}
	cases = append(cases, struct{ name, library, want string }{"a library that runs kill", "kill -9 $$\n", ends},
		struct{ name, library, want string }{"a library that exits", "exit 0\n", ended},
		struct{ name, library, want string }{"a library that sets an EXIT trap that execs", "trap 'exec true' EXIT\n", "EXIT trap"},
		struct{ name, library, want string }{"a library that evals an exec", "eval 'exec true'\n", ended},
		struct{ name, library, want string }{"a library that sets lastpipe", "shopt -s lastpipe\n", lastpipe},
		struct{ name, library, want string }{"a library that defines a function in eval text", "eval 'g() { exit 0; }'\n", inEval},
		struct{ name, library, want string }{"a library whose case branch exits", "case x in *) exit 0;; esac\n", ended},
		struct{ name, library, want string }{"a library whose help case exits", "case \"$1\" in -h|--help) exit 0;; esac\n", ended})
	for _, tc := range cases {
		lanes = append(lanes, struct{ name, lane, library, want string }{tc.name, `source "${ROOT_DIR}/scripts/lib/l.sh"`, tc.library, tc.want})
	}
	for _, tc := range lanes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeLaneTree(t, root, map[string]string{"scripts/validate-repo.sh": "#!/bin/bash\nset -euo pipefail\n" + laneRoot + "\n" + tc.lane + "\nbash \"${ROOT_DIR}/scripts/gate.sh\"\n",
				"scripts/lib/l.sh": tc.library, "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": "name: w\n"})
			got, err := metadatautil.LaneScripts(root)
			if tc.want == "wired" {
				if err != nil || !slices.Contains(got, "scripts/gate.sh") {
					t.Fatalf("LaneScripts() = %q, %v, want scripts/gate.sh wired", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), unread) || !strings.Contains(err.Error()+"$", tc.want) { // a want that ends in $ ends the error
				t.Fatalf("LaneScripts() = %q, %v, want an error with %q", got, err, tc.want)
			}
		})
	}
}

// TestLaneScriptsWorkGrowsLinearly times LaneScripts on lanes of each shape
// at two sizes, the second four times the first, and fails when the larger
// takes more than eight times as long, so a reader that is quadratic or worse
// on a shape fails, whether it reads the text or fails closed on it. Each size
// is timed three times and the fastest kept, and the first size grows until it
// takes long enough to time. A shape fails only when three attempts in a row
// exceed the ratio.
func TestLaneScriptsWorkGrowsLinearly(t *testing.T) {
	shapes := map[string]func(n int) (lane, library string){
		"command eval":       func(n int) (string, string) { return strings.Repeat("command eval ", n) + "true\n", "" },
		"then eval":          func(n int) (string, string) { return strings.Repeat("if true; then eval ", n) + "true\n", "" },
		"trap in trap":       func(n int) (string, string) { return strings.Repeat("trap ", n) + "true DEBUG\n", "" },
		"nested definitions": func(n int) (string, string) { return strings.Repeat("f() {\n", n) + strings.Repeat("}\n", n), "" },
		"nested library definitions": func(n int) (string, string) {
			return "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n", strings.Repeat("f() {\n", n) + strings.Repeat("}\n", n)
		},
		"continued lines": func(n int) (string, string) { return "echo " + strings.Repeat("a \\\n", n) + "b\n", "" },
		"quoted continued lines": func(n int) (string, string) {
			return "echo \"" + strings.Repeat("a \\\n", n) + "b\"\n", ""
		},
		"forwarded calls": func(n int) (string, string) {
			return "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + strings.Repeat("run make\n", n), "run() { \"$@\"; }\n"
		},
		"function calls": func(n int) (string, string) { return "f() { g; g; }\ng() { :; }\n" + strings.Repeat("f\n", n), "" },
		"brace words":    func(n int) (string, string) { return strings.Repeat("echo {a,b}{c,d}{e,f}\n", n), "" },
		"nested offsets": func(n int) (string, string) {
			return "echo \"" + strings.Repeat("${a:", n) + "0" + strings.Repeat("}", n) + "\"\n", ""
		},
		"nested arithmetic": func(n int) (string, string) {
			return "echo " + strings.Repeat("$((", n) + "1" + strings.Repeat("))", n) + "\n", ""
		},
		"arithmetic words": func(n int) (string, string) { return "(( " + strings.Repeat("1 + ", n) + "1 ))\n", "" },
		"guarded evals": func(n int) (string, string) {
			return "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n", "f() {\n  local fd=\"\"\n  for fd in ${FDS}; do\n" + strings.Repeat("    [[ \"${fd}\" =~ ^[0-9]+$ ]] || continue\n    eval \"exec ${fd}<&-\"\n", n) + "  done\n}\n"
		},
		"many locals": func(n int) (string, string) {
			var locals, reads strings.Builder
			for at := range n {
				fmt.Fprintf(&locals, "  local v%d=1\n", at)
				fmt.Fprintf(&reads, "  echo $((v%d))\n", at)
			}
			return "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n", "f() {\n" + locals.String() + reads.String() + "}\n"
		},
		"many guards": func(n int) (string, string) {
			var locals, guards strings.Builder
			for at := range n {
				fmt.Fprintf(&locals, "  local v%d=\"$1\"\n", at)
				fmt.Fprintf(&guards, "  [[ \"${v%d}\" =~ ^[0-9]+$ ]] || return\n", at)
			}
			return "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n", "f() {\n" + locals.String() + guards.String() + strings.Repeat(fmt.Sprintf("  eval \"exec ${v%d}<&-\"\n", n-1), n) + "}\n"
		},
	}
	for name, shape := range shapes {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			measure := func(n int) time.Duration {
				lane, library := shape(n)
				writeLaneTree(t, root, map[string]string{"scripts/validate-repo.sh": "#!/bin/bash\n" + laneRoot + "\n" + lane, "scripts/lib/l.sh": library, "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": "name: w\n"})
				best := time.Duration(1 << 62)
				for range 3 {
					start := time.Now()
					_, _ = metadatautil.LaneScripts(root)
					best = min(best, time.Since(start))
				}
				return best
			}
			n := 64
			for ; measure(n) < 20*time.Millisecond && len(func() string { lane, library := shape(4 * n); return lane + library }()) < 1<<19; n *= 2 {
			}
			// A busy machine can slow one size more than the other, so a
			// shape fails only when three attempts in a row exceed the ratio.
			for range 3 {
				small, large := measure(n), measure(4*n)
				if large <= 8*small {
					return
				}
				t.Logf("LaneScripts() took %v at %d and %v at %d", small, n, large, 4*n)
			}
			t.Fatalf("LaneScripts() took more than eight times as long for four times the text in three attempts")
		})
	}
}

// fanOut returns n functions, each of which calls the next twice, and a call
// of the first, so reading every call reads the last body 2^n times.
func fanOut(n int) string {
	var text strings.Builder
	for at := range n {
		fmt.Fprintf(&text, "f%d() { f%d; f%d; }\n", at, at+1, at+1)
	}
	return text.String() + "f0\n"
}

// writeLaneTree writes each file under root, keyed by its repository path.
func writeLaneTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLaneScriptsRejectsEvasions runs the shared corpus against a gate a workflow step runs. A row that turns the command into text must unwire the gate, and a gated row must keep it wired.
func TestLaneScriptsRejectsEvasions(t *testing.T) {
	t.Parallel()
	const head = "name: w\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - name: s\n        run: |\n          true\n"
	const anchor = "          ./scripts/gate.sh"
	validate := func(workflow string) error {
		root := t.TempDir()
		for rel, body := range map[string]string{"scripts/validate-repo.sh": "true\n", "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": workflow} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := metadatautil.LaneScripts(root)
		if err != nil {
			return err
		}
		if !slices.Contains(got, "scripts/gate.sh") {
			return errors.New("scripts/gate.sh has no lane wiring")
		}
		return nil
	}
	if err := validate(head + anchor + "\n"); err != nil {
		t.Fatalf("LaneScripts() on the wired step: %v", err)
	}
	// An unquoted heredoc joins a line that ends in \ to the next, and turns \\
	// into one backslash that the child bash then joins the same way, so
	// neither runs the gate.
	for body, wired := range map[string]bool{"x=\\\\": false, "x=\\": false, "x=y": true} {
		heredoc := "name: w\njobs:\n  j:\n    runs-on: ubuntu-latest\n    steps:\n      - run: |\n          bash <<EOF\n          " + body + "\n          scripts/gate.sh\n          EOF\n"
		if err := validate(heredoc); (err == nil) != wired {
			t.Errorf("LaneScripts() on a heredoc step with %q: %v, want wired %v", body, err, wired)
		}
	}
	// An exit or an exec before the gate ends the lane, so the reader refuses it.
	RequireRejectsTextEvasions(t, head+anchor+"\n", anchor, "wiring", validate, "exit before the command", "exec before the command")
}
