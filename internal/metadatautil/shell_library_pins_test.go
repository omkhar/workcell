// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestReviewedShellLibrariesMatchTheirFiles is the gate on the pins: a library
// edit must update its digest and its forwarder list in review, and a pinned
// library may not source, eval, rebind a name or hold a heredoc the gate does
// not read, as the reader reads each of its commands.
func TestReviewedShellLibrariesMatchTheirFiles(t *testing.T) {
	for name, want := range reviewedShellLibraries {
		data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "lib", name))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want.digest {
			t.Errorf("scripts/lib/%s digest = %s, pinned %s; review the change and update reviewedShellLibraries", name, got, want.digest)
		}
		forwarders, hidden := readLibrary(string(data))
		if hidden != "" {
			t.Errorf("scripts/lib/%s runs %q; a pinned library may not source, eval, rebind a name or hold a heredoc", name, hidden)
		}
		if !slices.Equal(forwarders, want.forwarders) {
			t.Errorf("scripts/lib/%s forwarders = %q, pinned %q", name, forwarders, want.forwarders)
		}
	}
}

// readLibrary reads a library's logical lines as the reader does and returns
// the sorted functions whose body forwards its caller's words, and the first
// line that sources, evals or rebinds a name outside such a forward, or that
// opens a heredoc.
func readLibrary(text string) (forwarders []string, hidden string) {
	var current strings.Builder
	var quotes []byte
	depth, function, definedAt := 0, "", 0
	for _, line := range strings.Split(text, "\n") {
		current.WriteString(line)
		words, heredocs, quote, rest, continues, _ := shellWords(current.String(), quotes)
		if continues {
			joined := current.String()
			current.Reset()
			current.WriteString(joined[:len(joined)-1])
			continue
		}
		if last := len(words) - 1; last >= 0 && !words[last].quoted && continuesLine(words[last].text) {
			current.WriteString(" ")
			continue
		}
		logical := current.String()
		current.Reset()
		quotes = rest
		if quote != 0 || len(heredocs) > 0 {
			return nil, logical
		}
		commands := withoutCasePatternAlternatives(splitCommands(words))
		if name := definedName(words); name != "" && function == "" {
			function, definedAt = name, depth
		}
		for _, each := range commands {
			names := commandWords(spelled(each.args))
			// A one-line body, as f() { source x; } or function f { source x; },
			// keeps its header; strip it and any opener to classify the command.
			if len(names) > 1 && names[0] == "function" {
				names = names[2:]
			}
			for len(names) > 0 && (strings.HasSuffix(names[0], "()") || slices.Contains([]string{"{", "(", "!", "if", "then", "do", "else", "elif", "while", "until", "time"}, names[0])) {
				names = names[1:]
			}
			switch {
			case function != "" && evaluates(names) && forwards(names):
				forwarders = append(forwarders, function)
			case evaluates(names) || rebindsCommand(names):
				if hidden == "" {
					hidden = logical
				}
			}
		}
		if depth += braceDepth(commands) - gluedSubshellClosers(commands, logical); function != "" && depth <= definedAt {
			function = ""
		}
	}
	slices.Sort(forwarders)
	return slices.Compact(forwarders), hidden
}

func libraryLineHidesText(line string) bool {
	_, hidden := readLibrary(line)
	return hidden != ""
}

// TestLibraryGateReadsWrappedBuiltins covers wrapped builtins and one-line
// bodies, whose command the gate must read inside the braces.
func TestLibraryGateReadsWrappedBuiltins(t *testing.T) {
	for _, line := range []string{"command source ./shadow.sh", "builtin eval 'f() { :; }'", "alias buildx_cmd=true", "  . ./x.sh",
		"f() { source ./shadow.sh; }", "f() { eval x; }", "f() { alias a=b; }", "function f { source ./shadow.sh; }"} {
		if !libraryLineHidesText(line) {
			t.Errorf("%q is not read as hiding text", line)
		}
	}
}

func TestReviewedLibrarySourceIsNotABarrier(t *testing.T) {
	const head = "#!/bin/bash\n" + canonicalRootDir + "\nX=1\n"
	for name, test := range map[string]struct {
		script string
		want   int
	}{
		"a pinned library in the prelude":      {head + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 1},
		"the dot spelling":                     {head + `. "$ROOT_DIR/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 1},
		"an unpinned library":                  {head + `source "${ROOT_DIR}/scripts/lib/other.sh"` + "\nbuildx_cmd build\n", 0},
		"a pinned library after a command":     {head + "true\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"a second ROOT_DIR assignment":         {head + "ROOT_DIR=/tmp/elsewhere\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an appended ROOT_DIR":                 {head + "ROOT_DIR+=/x\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"no canonical ROOT_DIR":                {"#!/bin/bash\nROOT_DIR=/tmp\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"a source with arguments":              {head + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh" extra` + "\nbuildx_cmd build\n", 0},
		"a pinned library at another root":     {head + `source "${HOME}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"eval still stops the reader":          {head + "eval true\nbuildx_cmd build\n", 0},
		"an indexed ROOT_DIR first":            {"#!/bin/bash\nROOT_DIR[0]=/tmp/evil\n" + canonicalRootDir + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"the canonical root after":             {"#!/bin/bash\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\n" + canonicalRootDir + "\nbuildx_cmd build\n", 0},
		"the canonical root in a comment":      {"#!/bin/bash\n# " + canonicalRootDir + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"a substituted ROOT_DIR after":         {head + `ROOT_DIR="$(echo /tmp/evil)"` + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"a read of ROOT_DIR first":             {head + "read -r ROOT_DIR\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"a RETURN trap first":                  {head + `trap 'buildx_cmd() { :; }' RETURN` + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an ERR trap first":                    {head + `trap 'echo failed >&2' ERR` + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 1},
		"a helper that exits through command":  {"stop() { command exit 0; }\nstop\nbuildx_cmd build\n", 0},
		"a helper that installs a RETURN trap": {"shadow() { trap 'buildx_cmd() { :; }' RETURN; }\nshadow\nbuildx_cmd build\n", 0},
		"a top-level builtin exit":             {"builtin exit 0\nbuildx_cmd build\n", 0},
		"a library forwarder that evals":       {head + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nrun_workcell_docker_client_command eval x\nbuildx_cmd build\n", 0},
	} {
		if got := len(ShellInvocations(test.script, "buildx_cmd")); got != test.want {
			t.Errorf("%s: %d invocations, want %d", name, got, test.want)
		}
	}
}

// TestReaderReadsBodiesAndPatterns covers what the reader must not take for a
// barrier, and what it must: a ) glued to a subshell's last word closes it;
// command -v and -V run nothing, but command "$2" does; an exit inside a || {
// … } group or an if ends nothing proved; case pattern alternatives, . among
// them, are no commands; and an expanded command word with a literal slash
// runs a file, never eval or source.
func TestReaderReadsBodiesAndPatterns(t *testing.T) {
	const die = "die() {\n  echo \"$1\" >&2\n  exit 1\n}\n"
	for name, test := range map[string]struct {
		script string
		want   int
	}{
		"a glued closer":                    {"f() {\n  (cd \"${ROOT_DIR}\" && \"${GO_BIN}\" run ./x \"$@\")\n}\nbuildx_cmd build\n", 1},
		"a closer glued to a plain word":    {"f() {\n  (cd x && go run ./y)\n}\nbuildx_cmd build\n", 1},
		"a top-level glued closer":          {"(cd x && go run ./y)\nbuildx_cmd build\n", 1},
		"a case pattern is no closer":       {"case x in\nlinux)\n  buildx_cmd build\n  ;;\nesac\n", 0},
		"a closer on a later line":          {"f() {\n  (cd x\n  go run ./y)\n}\nbuildx_cmd build\n", 0},
		"a continued closer":                {"f() {\n  (cd x &&\n  go run ./y)\n}\nbuildx_cmd build\n", 1},
		"command -v":                        {"f() {\n  command -v \"$1\" >/dev/null 2>&1 || exit 1\n}\nf docker\nbuildx_cmd build\n", 1},
		"command -pV":                       {"f() {\n  command -pV \"$1\"\n}\nf docker\nbuildx_cmd build\n", 1},
		"command runs its word":             {"f() {\n  command \"$2\"\n}\nf x eval\nbuildx_cmd build\n", 0},
		"exit inside a || group":            {"f() {\n  command -v \"$1\" >/dev/null || {\n    echo missing >&2\n    exit 1\n  }\n}\nf docker\nbuildx_cmd build\n", 1},
		"exit after the group":              {"f() {\n  true || {\n    echo x\n  }\n  exit 1\n}\nf\nbuildx_cmd build\n", 0},
		"an unconditional exit":             {"f() {\n  exit 1\n}\nf\nbuildx_cmd build\n", 0},
		"exit inside an if":                 {"f() {\n  if ! true; then\n    exit 1\n  fi\n}\nf\nbuildx_cmd build\n", 1},
		"exit as the if test":               {"f() {\n  if exit 1; then\n    :\n  fi\n}\nf\nbuildx_cmd build\n", 0},
		"a source before a pattern":         {"source ./shadow.sh; case x in '' | . | /*) : ;; esac\nbuildx_cmd build\n", 0},
		"a pattern with a dot":              {die + "g() {\n  case \"$1\" in\n    '' | . | /*)\n      die bad\n      return 1\n      ;;\n  esac\n}\ng x\nbuildx_cmd build\n", 1},
		"a top-level pattern with a dot":    {"case \"$1\" in\n  . | ..)\n    echo dot\n    ;;\nesac\nbuildx_cmd build\n", 1},
		"a real source still stops":         {"source x\nbuildx_cmd build\n", 0},
		"a quoted pattern with a parameter": {"case \"$1\" in\n  \"${HOME}\")\n    echo home\n    ;;\nesac\nbuildx_cmd build\n", 1},
		"return then exit":                  {"f() {\n  if [[ -x x ]]; then\n    return 0\n  fi\n  exit 1\n}\nf\nbuildx_cmd build\n", 1},
		"a root-relative script":            {"\"${ROOT_DIR}/scripts/x.sh\" arg\nbuildx_cmd build\n", 1},
		"the searched path itself":          {"X=1 \"${ROOT_DIR}/scripts/check.sh\"\nbuildx_cmd build\n", 1},
		"an expansion with no slash":        {"\"${cmd}\" arg\nbuildx_cmd build\n", 0},
		"a slash inside the braces":         {"\"${dir/x/y}\" arg\nbuildx_cmd build\n", 0},
		"a brace expansion":                 {"{source,./x} arg\nbuildx_cmd build\n", 0},
	} {
		if got := len(ShellInvocations(test.script, "buildx_cmd")); got != test.want {
			t.Errorf("%s: %d invocations, want %d", name, got, test.want)
		}
	}
	if got := len(ShellInvocations("X=1 \"${ROOT_DIR}/scripts/check.sh\"\n", "X=1 ${ROOT_DIR}/scripts/check.sh")); got != 1 {
		t.Errorf("a search for the path call found %d, want 1", got)
	}
}
