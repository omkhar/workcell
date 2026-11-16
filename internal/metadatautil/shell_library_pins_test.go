// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want.digest {
			t.Errorf("%s digest = %s, pinned %s; review the change and update reviewedShellLibraries", name, got, want.digest)
		}
		forwarders, hidden := readLibrary(string(data))
		if hidden != "" {
			t.Errorf("%s runs %q; a pinned library may not source, eval, rebind a name or hold a heredoc", name, hidden)
		}
		if !slices.Equal(forwarders, want.forwarders) {
			t.Errorf("%s forwarders = %q, pinned %q", name, forwarders, want.forwarders)
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
	// entryGuard is the check that entry scripts run before they load a
	// helper: an assignment from a substitution, then a refusal that prints to
	// standard error and exits, so it cannot move ROOT_DIR or change the
	// library it then sources.
	const entryGuard = `workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/x.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
`
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
		"a pinned launcher library":            {head + `source "${ROOT_DIR}/scripts/lib/launcher/host-exec.sh"` + "\nbuildx_cmd build\n", 1},
		"a pinned container library":           {head + `source "${ROOT_DIR}/runtime/container/assurance.sh"` + "\nbuildx_cmd build\n", 1},
		"a launcher library in another place":  {head + `source "${ROOT_DIR}/scripts/lib/host-exec.sh"` + "\nbuildx_cmd build\n", 0},
		"a launcher library through a parent":  {head + `source "${ROOT_DIR}/scripts/lib/launcher/../launcher/host-exec.sh"` + "\nbuildx_cmd build\n", 0},
		"an unpinned launcher library":         {head + `source "${ROOT_DIR}/scripts/lib/launcher/egress-endpoints.sh"` + "\nbuildx_cmd build\n", 0},
		"the entry link guard first":           {head + entryGuard + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 1},
		"the guard's root":                     {"#!/bin/bash\n" + entryGuard + entryRootDir + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 1},
		"the guard's root without the guard":   {"#!/bin/bash\n" + entryRootDir + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"the guard's root moved after it":      {"#!/bin/bash\n" + entryGuard + "workcell_entry_root=/tmp/evil\n" + entryRootDir + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"the guard's root read after it":       {"#!/bin/bash\n" + entryGuard + "read -r workcell_entry_root\n" + entryRootDir + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"the guard's root read in between":     {"#!/bin/bash\n" + entryGuard + `echo "${workcell_entry_root}" >&2` + "\n" + entryRootDir + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 1},
		"the guard's root defaulted after it":  {"#!/bin/bash\n" + entryGuard + `echo "${workcell_entry_root:=/tmp/evil}" >&2` + "\n" + entryRootDir + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an echo into the library first":       {head + `echo x >"${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an echo to stderr and a file first":   {head + "echo x >&2 >/tmp/f\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an echo that dups another descriptor": {head + "echo x 3>&2\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an echo with a glued redirection":     {head + "echo x>/tmp/f >&2\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an exit with a redirection first":     {head + "[[ -n x ]] || exit 2 >/tmp/f\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an exit in the prelude":               {head + "[[ -n x ]] || exit 2\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 1},
		"an echo that runs a command first":    {head + "echo $(cp /tmp/evil lib.sh) >&2\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an echo with a backtick first":        {head + "echo `cp /tmp/evil lib.sh` >&2\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an exit that runs a command first":    {head + "[[ -n x ]] || exit $(cp /tmp/evil lib.sh)\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an echo of a stand-in substitution":   {head + "echo \"$_\" >&2\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"an echo to stdout first":              {head + "echo x\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
		"a printf first":                       {head + "printf -v ROOT_DIR /tmp/evil >&2\n" + `source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"` + "\nbuildx_cmd build\n", 0},
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

// launcherLibrariesNotPinned are the launcher libraries that the gate cannot
// pin yet: go-run-env.sh runs resolved tool paths as expanded command words,
// and egress-endpoints.sh still holds a line the gate reads as hidden.
var launcherLibrariesNotPinned = []string{"scripts/lib/go-run-env.sh", "scripts/lib/launcher/egress-endpoints.sh"}

// TestLauncherSourcesOnlyPinnedLibraries reads every command that
// scripts/workcell may run. Each source or . names one fixed repository
// library under ROOT_DIR, and that library is pinned or named as not pinned
// yet. A source of a state file or a profile could define or redefine any
// launcher function, a reviewed egress function among them.
func TestLauncherSourcesOnlyPinnedLibraries(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "workcell"))
	if err != nil {
		t.Fatal(err)
	}
	if failures := launcherSourceFailures(string(data)); len(failures) > 0 {
		t.Errorf("scripts/workcell:\n%s", strings.Join(failures, "\n"))
	}
	for name, script := range map[string]string{
		"a state file":         "f() {\n  source \"${state_file}\"\n}\nf\n",
		"a profile":            ". \"${PROFILE_ENV}\"\n",
		"a wrapped source":     "builtin source /tmp/x.sh\n",
		"an unpinned library":  "source \"${ROOT_DIR}/scripts/lib/other.sh\"\n",
		"a library with words": "source \"${ROOT_DIR}/scripts/lib/shellproto.sh\" x\n",
	} {
		if len(launcherSourceFailures(script)) == 0 {
			t.Errorf("%s: no failure", name)
		}
	}
}

// TestLauncherSetsNoReturnTrap covers scripts/workcell: a RETURN trap runs its
// text when a function returns, so it could redefine the command that the
// caller runs next, after the library pins. No function sets one.
func TestLauncherSetsNoReturnTrap(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "workcell"))
	if err != nil {
		t.Fatal(err)
	}
	if failures := returnTrapFailures(string(data)); len(failures) > 0 {
		t.Errorf("scripts/workcell:\n%s", strings.Join(failures, "\n"))
	}
	// A library that the launcher sources runs in the launcher's shell, so a
	// RETURN trap there fires on the launcher's own function returns too.
	libraries := launcherLibrarySource.FindAllStringSubmatch(string(data), -1)
	if len(libraries) < 8 {
		t.Fatalf("found %d libraries that scripts/workcell sources; the source lines changed form", len(libraries))
	}
	for _, library := range libraries {
		text, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(library[1])))
		if err != nil {
			t.Fatal(err)
		}
		if failures := returnTrapFailures(string(text)); len(failures) > 0 {
			t.Errorf("%s:\n%s", library[1], strings.Join(failures, "\n"))
		}
	}
	for name, script := range map[string]string{
		"a RETURN trap in a function": "f() {\n  trap 'rm -f x' RETURN\n}\nf\n",
		"a lower-case signal name":    "trap 'rm -f x' return\n",
		"RETURN among other signals":  "trap 'rm -f x' EXIT RETURN\n",
	} {
		if len(returnTrapFailures(script)) == 0 {
			t.Errorf("%s: no failure", name)
		}
	}
	if failures := returnTrapFailures("trap 'rm -f x' EXIT\ntrap - RETURN\n"); len(failures) > 0 {
		t.Errorf("an EXIT trap and a reset: %v", failures)
	}
}

// launcherLibrarySource matches each top-level source of a repository library
// in scripts/workcell.
var launcherLibrarySource = regexp.MustCompile(`(?m)^source "\$\{ROOT_DIR\}/([^"$]+)"$`)

func returnTrapFailures(script string) []string {
	var failures []string
	for _, words := range EveryShellCommand(script) {
		if len(words) < 3 || words[0] != "trap" || words[1] == "-" {
			continue
		}
		if slices.ContainsFunc(words[2:], func(signal string) bool { return strings.EqualFold(signal, "RETURN") }) {
			failures = append(failures, fmt.Sprintf("%q: remove the file on each path out instead", words))
		}
	}
	return failures
}

func launcherSourceFailures(script string) []string {
	var failures []string
	for _, words := range EveryShellCommand(script) {
		if len(words) == 0 || (words[0] != "source" && words[0] != ".") {
			continue
		}
		library, fixed := "", false
		if len(words) == 2 {
			library, fixed = strings.CutPrefix(words[1], "${ROOT_DIR}/")
		}
		_, pinned := reviewedShellLibraries[library]
		if !fixed || pinned == slices.Contains(launcherLibrariesNotPinned, library) {
			failures = append(failures, fmt.Sprintf("%q: pin the library, or name it as not pinned yet", words))
		}
	}
	return failures
}
