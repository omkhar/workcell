// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckShellPortabilityAcceptsThisRepository(t *testing.T) {
	if err := metadatautil.CheckShellPortability(filepath.Join("..", "..")); err != nil {
		t.Fatalf("CheckShellPortability() error = %v", err)
	}
}

// Each row carries a construct the reviewer flagged, or one of the same two
// families. Every "flag" row was measured on macOS 26 with the system path
// /usr/bin:/bin and the system /bin/bash 3.2: the command or the builtin
// fails there.
func TestShellPortabilityFindings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		flag   bool
	}{
		{"mapfile", "mapfile -t lines <input\n", true},
		{"readarray", "readarray -t lines <input\n", true},
		{"wait -n", "wait -n\n", true},
		{"declare -A", "declare -A table=()\n", true},
		{"local -A", "  local -A table=()\n", true},
		{"upper-case expansion", `printf '%s' "${name^^}"` + "\n", true},
		{"lower-case expansion", `printf '%s' "${name,,}"` + "\n", true},
		{"sha256sum", `digest="$(sha256sum "${path}")"` + "\n", true},
		{"stat -c", "stat -c '%a' \"${path}\"\n", true},
		{"date -d", "date -u -d '-1 day' +%Y\n", true},
		{"grep -P", "grep -P 'x' file\n", true},
		{"find -printf", "find . -type f -printf '%p'\n", true},
		{"sed -i without a suffix", "sed -i 's/a/b/' file\n", true},
		{"pipeline position", "cat file | mapfile -t lines\n", true},

		{"shasum is the portable form", `digest="$(shasum -a 256 "${path}")"` + "\n", false},
		{"stat -f is the BSD form", "stat -f '%Lp' \"${path}\"\n", false},
		{"sed -i with a suffix", "sed -i.bak 's/a/b/' file\n", false},
		{"a comment names the construct", "# do not call sha256sum here\n", false},
		{"an argument is not a command", "require_tool sha256sum\n", false},
		{"a longer name is not the command", "workcell_sha256sum file\n", false},
		{"the line declares the exemption", "stat -c '%a' f # portability-exempt: the BSD form is tried first\n", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			findings := metadatautil.ShellPortabilityFindings(testCase.script, "fixture.sh")
			if (len(findings) > 0) != testCase.flag {
				t.Fatalf("ShellPortabilityFindings() = %v, want a finding = %v", findings, testCase.flag)
			}
		})
	}
}

// A heredoc body is text this script hands to another interpreter, and the
// interpreter is bash 5 inside the Linux image for every such body in the
// tree. The reviewer's own class-D lesson applies: read the commands the
// script runs, not the text it carries.
func TestShellPortabilityIgnoresHeredocBodies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
	}{
		{"quoted heredoc", "docker exec c bash -s <<'SCRIPT'\nstat -c '%a' /etc/hosts\nSCRIPT\n"},
		{"unquoted heredoc", "cat >f <<EOF\nsha256sum x\nEOF\n"},
		{"indented heredoc", "\tcat >f <<-EOF\n\tmapfile -t a <x\n\tEOF\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if findings := metadatautil.ShellPortabilityFindings(testCase.script, "fixture.sh"); len(findings) != 0 {
				t.Fatalf("expected no finding, found %v", findings)
			}
		})
	}
	// The same construct outside a heredoc still fails.
	if findings := metadatautil.ShellPortabilityFindings("stat -c '%a' /etc/hosts\n", "fixture.sh"); len(findings) != 1 {
		t.Fatalf("expected one finding outside a heredoc, found %v", findings)
	}
}

// A script that re-executes itself under bash 4 has proved the builtin is
// present. scripts/validate-repo.sh does exactly this and then uses
// declare -A, which is correct and must not be reported.
func TestShellPortabilityHonoursTheModernBashAssertion(t *testing.T) {
	t.Parallel()
	body := "declare -A table=()\nmapfile -t lines <input\n"
	if findings := metadatautil.ShellPortabilityFindings(body, "fixture.sh"); len(findings) != 2 {
		t.Fatalf("expected two findings without the assertion, found %v", findings)
	}
	asserted := "workcell_require_modern_privileged_bash \"$@\"\n" + body
	if findings := metadatautil.ShellPortabilityFindings(asserted, "fixture.sh"); len(findings) != 0 {
		t.Fatalf("expected no bash-version finding after the assertion, found %v", findings)
	}
	// The userland rules do not depend on the shell version.
	withUserland := "workcell_require_modern_privileged_bash \"$@\"\nstat -c '%a' f\n"
	if findings := metadatautil.ShellPortabilityFindings(withUserland, "fixture.sh"); len(findings) != 1 {
		t.Fatalf("expected the userland finding to survive the assertion, found %v", findings)
	}
}

func TestShellPortabilityHonoursTheFileExemption(t *testing.T) {
	t.Parallel()
	script := "#!/usr/bin/env bash\n# portability-exempt: linux-only (runs inside the Debian runtime image)\nstat -c '%a' f\n"
	if findings := metadatautil.ShellPortabilityFindings(script, "fixture.sh"); len(findings) != 0 {
		t.Fatalf("expected no finding, found %v", findings)
	}
	// The declaration has to sit in the header, so a mention further down
	// cannot exempt the file.
	deep := "#!/usr/bin/env bash\n" + strings.Repeat("true\n", 30) +
		"# portability-exempt: linux-only (late)\nstat -c '%a' f\n"
	if findings := metadatautil.ShellPortabilityFindings(deep, "fixture.sh"); len(findings) != 1 {
		t.Fatalf("expected one finding, found %v", findings)
	}
}

func TestCheckShellPortabilityRejectsEmptyInventory(t *testing.T) {
	t.Parallel()
	if err := metadatautil.CheckShellPortability(t.TempDir()); err == nil {
		t.Fatal("expected an empty shell script inventory to fail")
	}
}
