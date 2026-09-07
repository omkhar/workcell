// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"slices"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestShellInvocations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, script string
		want         [][]string
	}{
		{
			name:   "arguments of each invocation",
			script: "oras cp one\noras cp two\n",
			want:   [][]string{{"one"}, {"two"}},
		},
		{
			name:   "a longer command name is not the requested command",
			script: "oras cpx one\n",
		},
		{
			name:   "continuations join into one invocation",
			script: "oras cp \\\n  one \\\n  two\n",
			want:   [][]string{{"one", "two"}},
		},
		{
			name:   "a full-line comment runs nothing",
			script: "# oras cp one\n",
		},
		{
			name:   "an inline comment ends the invocation",
			script: "oras cp one # oras cp two\n",
			want:   [][]string{{"one"}},
		},
		{
			name:   "a hash inside single quotes is an argument, not a comment",
			script: "oras cp 'note: # here' one\n",
			want:   [][]string{{"note: # here", "one"}},
		},
		{
			name:   "a hash inside double quotes is an argument, not a comment",
			script: "oras cp \"note: # here\" one\n",
			want:   [][]string{{"note: # here", "one"}},
		},
		{
			name:   "a hash inside a word is an argument, not a comment",
			script: "oras cp a#b one\n",
			want:   [][]string{{"a#b", "one"}},
		},
		{
			name:   "a quoted argument stays one word, so an option inside it is text",
			script: "oras cp 'ignored --to-oci-layout dist/release-image:amd64 ignored' || true\n",
			want:   [][]string{{"ignored --to-oci-layout dist/release-image:amd64 ignored", "||", "true"}},
		},
		{
			name:   "an escaped quote does not open a span that hides a delimiter",
			script: ": \\' <<PLAN ''\noras cp one\nPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a heredoc body with a quoted delimiter runs nothing",
			script: "cat <<'PLAN'\noras cp one\nPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "an unquoted delimiter still opens a body, expansions included",
			script: "cat <<PLAN\noras cp ${DECOY}\noras cp $(oras cp one)\nPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a tab-stripped delimiter closes the body it opened",
			script: "cat <<-PLAN\n\toras cp one\n\tPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "two heredocs on one line each consume their own body",
			script: "cat <<'NOTE' <<'PLAN'\noras cp one\nNOTE\noras cp two\nPLAN\noras cp three\n",
			want:   [][]string{{"three"}},
		},
		{
			name:   "a quoted word cannot open a delimiter",
			script: ": <<'A' \"text <<B\"\nA\ncat <<'PLAN'\nB\noras cp one\nPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a here-string does not open a heredoc",
			script: "cat <<<\"${PLAN}\"\noras cp one\n",
			want:   [][]string{{"one"}},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := metadatautil.ShellInvocations(testCase.script, "oras cp")
			if !slices.EqualFunc(got, testCase.want, slices.Equal) {
				t.Fatalf("ShellInvocations() = %q, want %q", got, testCase.want)
			}
		})
	}
}
