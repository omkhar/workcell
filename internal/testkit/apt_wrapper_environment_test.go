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
	if !strings.Contains(source, `exec "${broker_client}" --sudo-compat "$@"`) {
		t.Fatal("sudo-wrapper.sh no longer delegates to the broker client")
	}
	if !strings.Contains(source, `broker_client="/usr/local/libexec/workcell/workcell-apt-broker-client"`) {
		t.Fatal("sudo-wrapper.sh no longer names the broker client binary")
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
