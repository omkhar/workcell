// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"errors"
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
		// A readonly root holds: bash refuses every later write to it, so a
		// computed name needs no reading. Without readonly it may be moved.
		{"root not readonly", laneAssignedRoot + "\n" + gate, "", "unwired"},
		{"root made readonly by declare -r", laneAssignedRoot + "\ndeclare -r ROOT_DIR\n" + gate, "", "wired"},
		{"readonly root after another line", laneAssignedRoot + "\n:\nreadonly ROOT_DIR\n" + gate, "", "unwired"},
		{"arithmetic computed name without readonly", laneAssignedRoot + "\nv=ROOT_; [[ 1 -eq ${v}DIR=1 ]]\n" + gate, "", "unwired"},
		{"arithmetic computed name with readonly", laneRoot + "\nv=ROOT_; [[ 1 -eq ${v}DIR=1 ]]; (( ${v}DIR=1 )); a[${v}DIR=1]=x\n" + gate, "", "wired"},
		{"wait -p computed name with readonly", laneRoot + "\nv=ROOT_; sleep 0 & wait -n -p \"${v}DIR\"\n" + gate, "", "wired"},
		{"library rebinds a positional with readonly", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\nclaim x OUT\n" + gate, "claim() {\n  local v=\"$2\"\n  v=${n}DIR\n  shift\n  read -r v <<<\"$v\"\n  printf -v \"${v}\" /tmp\n}\n", "wired"},
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
		{"computed name", laneRoot + "\nv=ROOT; export \"${v}_DIR=/tmp\"\n" + gate, "", "wired"},
		{"computed printf -v name", laneRoot + "\nprintf -v \"$1\" /tmp\n" + gate, "", "wired"},
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
		{"set prelude that defines pwd", "set -e; pwd() { echo /tmp; }\n" + laneRoot + "\n" + gate, "", "unwired"},
		{"set prelude that opens a function", "set -e; f() {\n" + laneRoot + "\n}\n" + gate, "", "unwired"},
		{"root name continued across lines", laneRoot + "\nROOT\\\n_DIR=/tmp\n" + gate, "", "unwired"},
		{"root name by brace expansion", laneRoot + "\nexport ROOT{_,}DIR=/tmp\n" + gate, "", "unwired"},
		{"root name by brace sequence", laneRoot + "\nexport ROO{T..T}_DIR=/tmp\n" + gate, "", "unwired"},
		{"quoted braces keep the root", laneRoot + "\necho 'ROOT{_,}' a{b,c}\n" + gate, "", "wired"},
		{"computed nameref", laneRoot + "\ndeclare -n ref=\"${n}DIR\"; ref=/tmp\n" + gate, "", "wired"},
		{"nameref with no target", laneRoot + "\ndeclare -n ref\nref=\"${n}DIR\"\nref=/tmp\n" + gate, "", "wired"},
		{"library computed printf -v name", laneRoot + "\nsource \"${ROOT_DIR}/scripts/lib/l.sh\"\n" + gate, "printf -v \"${v}_DIR\" /tmp\n", "wired"},
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
	RequireRejectsTextEvasions(t, head+anchor+"\n", anchor, "wiring", validate)
}
