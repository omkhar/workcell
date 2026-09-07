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

// internal/testkit holds this clearing prologue to the top of each script; the
// PATH pin is required on the line where it ends.
const runtimeStartupPin = "unset BASH_ENV ENV\n" +
	"[[ -z \"${BASH_ENV+set}${ENV+set}\" ]] || {\n" +
	"  echo 'Workcell refuses a pinned BASH_ENV or ENV startup file.' >&2\n" +
	"  exit 2\n}\n"

func TestRuntimePathPrologues(t *testing.T) {
	executable := runtimePathPin + "set -euo pipefail\n"
	library := "if [[ \"${PATH}\" != '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' ]]; then\n" +
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
			_, tail, found := strings.Cut(string(content), runtimeStartupPin)
			body, ok := strings.CutPrefix(tail, prefix)
			if !found || !ok {
				t.Fatal("missing ordered trusted PATH prologue")
			}
			if runtimePathMutation(body) {
				t.Fatal("shell PATH mutation follows the trusted prologue")
			}
		})
	}
}

// runtimePathMutation reports direct shell mutations of PATH: an assignment at
// the start of a command, after a declaration builtin, or an unset naming PATH.
// A command argument such as env PATH=value assigns no shell variable. The scan
// is over-eager, so a missed mutation cannot pass as a parse subtlety.
var runtimePathMutationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)(^|[;&|(){}]|\b(then|else|elif|do|export|declare|local|typeset|readonly)\b)[ \t]*PATH=`),
	regexp.MustCompile(`(?m)\bunset\b[^;&|\n]*\bPATH\b`),
}

func runtimePathMutation(body string) bool {
	// Comments first: joining one into the next line hides what follows it.
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
	// Over-eager by design, recorded so it stays a reviewed property: a # inside
	// a quoted word is not a comment, and a scan that stripped from the first #
	// would delete the assignment behind it.
	for _, body := range []string{"true # export PATH=/tmp", "printf 'then PATH=/tmp'"} {
		if !runtimePathMutation(body) {
			t.Errorf("over-eager classification changed: %s", body)
		}
	}
}
