// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A bare substring search over the wrapper's text is satisfied by the same
// words in a comment, a quoted decoy, or a heredoc body, so it would keep
// passing after the real delegation was deleted. Both checks are anchored to
// the start of a line instead, and the decoy table below is their negative
// fixture.
//
// The shared shell-invocation parser cannot answer for this file at all: an
// `exec` that names a program is a scan terminator there (`replacesShell`),
// never an invocation, so `ShellInvocations(source, "exec")` returns nothing
// for any script. The delegation is unrepresentable through it.
//
// A line anchor cannot see a heredoc body either, so the wrapper is required to
// have no heredoc. That keeps the anchors below the only way its text can
// appear, and it fails loudly if anyone adds one.
//
// Nor can a line anchor see reachability: the same line parked inside
// `if false; then ... fi` satisfies it while nothing runs it. The delegation is
// therefore required to be the wrapper's final command, which is what an exec
// that replaces the shell has to be anyway.
// sudoWrapperDelegationLine is the delegation exactly as the wrapper's final
// command must be written, unindented.
const sudoWrapperDelegationLine = `exec "${broker_client}" --sudo-compat "$@"`

// commandLines returns the lines of a script that carry a command, in order:
// blank lines and whole-line comments are dropped, and a trailing comment is
// cut. It is not a parser -- it answers only which lines run and in what order.
func commandLines(source string) []string {
	var carried []string
	for _, line := range strings.Split(source, "\n") {
		if cut := trailingComment.FindStringIndex(line); cut != nil {
			line = line[:cut[0]]
		}
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		carried = append(carried, line)
	}
	return carried
}

// lastShellCommand returns the last line of a script that carries a command,
// which is what an exec that replaces the shell has to be.
func lastShellCommand(source string) string {
	carried := commandLines(source)
	if len(carried) == 0 {
		return ""
	}
	return carried[len(carried)-1]
}

// continuedGuard returns the operator with which the line before the script's
// last command carries onto it, or the empty string. A penultimate `false &&`
// makes the final line conditional: it is still written last and still matches
// a line anchor, but bash may never reach it.
func continuedGuard(source string) string {
	carried := commandLines(source)
	if len(carried) < 2 {
		return ""
	}
	previous := carried[len(carried)-2]
	for _, operator := range []string{"&&", "||", "|", "\\"} {
		if strings.HasSuffix(previous, operator) {
			return operator
		}
	}
	return ""
}

var (
	// trailingComment matches a comment opened after whitespace, which is the
	// only spelling the wrapper could grow on its final line.
	trailingComment       = regexp.MustCompile(`[ \t]#.*$`)
	sudoWrapperHeredoc    = regexp.MustCompile(`<<-?`)
	sudoWrapperDelegation = regexp.MustCompile(
		`(?m)^exec "\$\{broker_client\}" --sudo-compat "\$@"$`,
	)
	sudoWrapperBrokerClient = regexp.MustCompile(
		`(?m)^broker_client="/usr/local/libexec/workcell/workcell-apt-broker-client"$`,
	)
)

func readRepoFile(tb testing.TB, parts ...string) string {
	tb.Helper()

	body, err := os.ReadFile(filepath.Join(append([]string{repoRoot(tb)}, parts...)...))
	if err != nil {
		tb.Fatal(err)
	}
	return string(body)
}

var (
	preserveEnvPattern  = regexp.MustCompile(`--preserve-env=([A-Za-z0-9_,]+)`)
	allowedEnvBlock     = regexp.MustCompile(`(?s)allowedEnvironmentValues = map\[string\]map\[string\]struct\{\}\{(.*?)\n\}`)
	allowedEnvNameEntry = regexp.MustCompile(`"([A-Z][A-Z0-9_]*)": \{`)
)

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// apt-wrapper.sh names the variables it forwards, and the broker decides which
// names it will accept. They are written in different languages in different
// files, and nothing at build time links them, so a name added to one and not
// the other turns every apt invocation into a rejected malformed request
// instead of failing on the one variable that was wrong.
func TestAptWrapperForwardsOnlyEnvironmentTheBrokerAdmits(t *testing.T) {
	t.Parallel()

	matches := preserveEnvPattern.FindAllStringSubmatch(readRepoFile(t, "runtime", "container", "bin", "apt-wrapper.sh"), -1)
	if len(matches) != 1 {
		t.Fatalf("apt-wrapper.sh has %d --preserve-env= lists, want 1", len(matches))
	}
	forwarded := strings.Split(matches[0][1], ",")

	block := allowedEnvBlock.FindStringSubmatch(readRepoFile(t, "internal", "aptbroker", "protocol.go"))
	if block == nil {
		t.Fatal("could not find allowedEnvironmentValues in internal/aptbroker/protocol.go")
	}
	var admitted []string
	for _, entry := range allowedEnvNameEntry.FindAllStringSubmatch(block[1], -1) {
		admitted = append(admitted, entry[1])
	}

	if got, want := strings.Join(sortedStrings(forwarded), ","), strings.Join(sortedStrings(admitted), ","); got != want {
		t.Fatalf("apt-wrapper.sh forwards %q, broker admits %q", got, want)
	}
}

// The sudo wrapper is the whole unprivileged entry point to the broker. It must
// hand the invocation over intact: the client parses sudo's options, admits the
// package helper alone, and reaches the broker over its socket. A second parser
// here would be a second place for the two to disagree about what was asked.
func TestSudoWrapperHandsUnprivilegedInvocationsToTheBrokerClient(t *testing.T) {
	t.Parallel()

	source := readRepoFile(t, "runtime", "container", "bin", "sudo-wrapper.sh")
	// A heredoc body would let the delegation's text outlive the command that
	// runs it, which a line anchor cannot tell apart.
	if sudoWrapperHeredoc.MatchString(source) {
		t.Fatal("sudo-wrapper.sh gained a heredoc; the line-anchored checks below cannot see into one")
	}
	// exec replaces the shell, so the delegation is the wrapper's last command.
	// A copy parked inside `if false; then ... fi` satisfies a line anchor while
	// nothing reaches it; being the final command is the reachability a line
	// anchor can check.
	if got := lastShellCommand(source); got != sudoWrapperDelegationLine {
		t.Fatalf("sudo-wrapper.sh no longer ends by delegating to the broker client: last command is %q", got)
	}
	// Being written last is not the same as being reached: a penultimate
	// `false &&` carries onto the delegation and bash may skip it.
	if operator := continuedGuard(source); operator != "" {
		t.Fatalf("sudo-wrapper.sh guards its final delegation with a trailing %q", operator)
	}
	if !sudoWrapperDelegation.MatchString(source) {
		t.Fatal("sudo-wrapper.sh no longer delegates to the broker client")
	}
	if !sudoWrapperBrokerClient.MatchString(source) {
		t.Fatal("sudo-wrapper.sh no longer names the broker client binary")
	}
	// Neither check may be satisfied by text the shell never runs.
	for _, decoy := range []string{
		`# exec "${broker_client}" --sudo-compat "$@"`,
		`  exec "${broker_client}" --sudo-compat "$@"  # kept for reference`,
		`echo 'exec "${broker_client}" --sudo-compat "$@"'`,
		`# broker_client="/usr/local/libexec/workcell/workcell-apt-broker-client"`,
	} {
		if sudoWrapperHeredoc.MatchString(decoy) {
			t.Fatalf("decoy must not need the heredoc guard: %q", decoy)
		}
		if sudoWrapperDelegation.MatchString(decoy) || sudoWrapperBrokerClient.MatchString(decoy) {
			t.Fatalf("a decoy satisfies the sudo-wrapper checks: %q", decoy)
		}
	}
	for _, forbidden := range []string{
		"apt-broker.sh",
		"broker_requests_dir",
		"broker_results_dir",
		"broker_pid_file",
		"sudo_wrapper_run_via_broker",
		"sudo_wrapper_broker_available",
		"sudo_wrapper_allowed_preserve_env_name",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("sudo-wrapper.sh still implements the shell broker protocol: %q", forbidden)
		}
	}
}
