// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package workcellhardening

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const runtimePathPin = "readonly PATH='/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'\nexport PATH\n"

// The BASH_ENV/ENV clearing prologue is checked in internal/testkit; it is
// repeated here so the PATH pin is held to the line right after it.
const runtimeStartupPin = "#!/usr/bin/env -S BASH_ENV= ENV= bash\n" +
	"# The shebang clears BASH_ENV/ENV only when the kernel applies it; these scripts\n" +
	"# also run as plain `/bin/bash <script>`, so clear them for every child bash.\n" +
	"# A startup file that already ran can pin either one readonly, which makes the\n" +
	"# unset fail while errexit is still off, so refuse to run while one survives.\n" +
	"unset BASH_ENV ENV\n" +
	"[[ -z \"${BASH_ENV+set}${ENV+set}\" ]] || {\n" +
	"  echo 'Workcell refuses a pinned BASH_ENV or ENV startup file.' >&2\n" +
	"  exit 2\n" +
	"}\n"

func TestRuntimePathPrologues(t *testing.T) {
	executable := runtimeStartupPin + runtimePathPin + "set -euo pipefail\n"
	library := runtimeStartupPin +
		"if [[ \"${PATH}\" != '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' ]]; then\n" +
		"  PATH='/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'\n" +
		"fi\nreadonly PATH\nexport PATH\n"
	for name, prefix := range map[string]string{
		"entrypoint.sh":          executable,
		"provider-wrapper.sh":    executable,
		"development-wrapper.sh": executable,
		"home-control-plane.sh":  library,
		"runtime-user.sh":        library,
	} {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("..", "..", "runtime", "container", name))
			if err != nil {
				t.Fatal(err)
			}
			body, ok := strings.CutPrefix(string(content), prefix)
			if !ok {
				t.Fatal("missing ordered trusted PATH prologue")
			}
			if runtimePathMutation(body) {
				t.Fatal("shell PATH mutation follows the trusted prologue")
			}
		})
	}
}

// runtimePathMutation reports direct shell mutations of PATH, not the meaning
// of arbitrary shell programs: an assignment at the start of a command, after
// a declaration builtin, or an unset naming PATH. Command arguments such as
// env PATH=value do not assign the shell variable and are not mutations.
// The scan is deliberately over-eager on anything that reads as an
// assignment, so a missed mutation cannot pass as a parse subtlety.
var runtimePathMutationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)(^|[;&|(){}]|\b(then|else|elif|do|export|declare|local|typeset|readonly)\b)[ \t]*PATH=`),
	regexp.MustCompile(`(?m)\bunset\b[^;&|\n]*\bPATH\b`),
}

func runtimePathMutation(body string) bool {
	// Comments first, then continuations: joining a comment into the next
	// physical line would hide the assignment that follows it.
	var code strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		code.WriteString(line)
		code.WriteByte('\n')
	}
	joined := strings.ReplaceAll(code.String(), "\\\n", " ")
	for _, pattern := range runtimePathMutationPatterns {
		if pattern.MatchString(joined) {
			return true
		}
	}
	return false
}

func TestRuntimePathMutationSourceClassification(t *testing.T) {
	for _, body := range []string{
		"PATH=/tmp", "export PATH=/tmp", "declare PATH=/tmp", "unset PATH", "unset -v PATH",
		"if true; then PATH=/tmp; fi", "readonly PATH=/tmp", "true && PATH=/tmp",
	} {
		if !runtimePathMutation(body) {
			t.Errorf("direct mutation not detected: %s", body)
		}
	}
	for _, body := range []string{
		"env PATH=/bin tool", "printf '%s' \"$PATH\"", "# PATH=/tmp", "HOME=/tmp",
		"MYPATH=/tmp", "  # unset PATH", "unset GIT_EXEC_PATH",
		"/usr/bin/env -i \\\n  PATH=/bin LC_ALL=C \\\n  tool",
	} {
		if runtimePathMutation(body) {
			t.Errorf("non-mutation rejected: %s", body)
		}
	}
	// Deliberately over-eager, recorded so it stays a reviewed property. A #
	// inside a quoted word is not a comment, so a scan that stripped from the
	// first # would delete the assignment behind it: a false report costs one
	// edit, a missed mutation costs the runtime PATH.
	for _, body := range []string{"true # export PATH=/tmp", "printf 'then PATH=/tmp'"} {
		if !runtimePathMutation(body) {
			t.Errorf("over-eager classification changed: %s", body)
		}
	}
}
