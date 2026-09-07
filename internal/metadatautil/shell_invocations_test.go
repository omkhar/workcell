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
			name:   "a continuation joins the halves with nothing between them",
			script: "oras cp --to-oci-layout\\\ndist/release-image:amd64\n",
			want:   [][]string{{"--to-oci-layoutdist/release-image:amd64"}},
		},
		{
			name:   "a space after a backslash ends the line, so the next line is another command",
			script: "oras cp one \\ \n--to-oci-layout dist/release-image:amd64\n",
			want:   [][]string{{"one", " "}},
		},
		{
			name:   "an escaped backslash ends the line, so the next line is another command",
			script: "oras cp one \\\\\n--to-oci-layout dist/release-image:amd64\n",
			want:   [][]string{{"one", "\\"}},
		},
		{
			name:   "an indented terminator leaves a plain heredoc body open",
			script: "cat <<PLAN\n  PLAN\noras cp one\nPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a tab-indented terminator leaves a plain heredoc body open",
			script: "cat <<PLAN\n\tPLAN\noras cp one\nPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a space-indented terminator leaves a tab-stripped heredoc body open",
			script: "cat <<-PLAN\n  PLAN\noras cp one\n\tPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a terminator with trailing text leaves the body open",
			script: "cat <<PLAN\nPLAN \noras cp one\nPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
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
			name:   "a backslash before an ordinary character stays literal in double quotes",
			script: "\"or\\as\" cp one\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a command substitution inside double quotes opens its heredoc",
			script: "printf '%s' \"$(cat <<PLAN\noras cp one\nPLAN\n)\"\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a command substitution inside double quotes drops the line's words",
			script: "oras cp --recursive --from-oci-layout \"$(printf x)\\ # --to-oci-layout dist/release-image:amd64 ignored\" || true\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a delimiter word ends at an operator on the same line",
			script: "cat <<EOF; oras cp one\nbody\nEOF\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a list operator at the end of a line carries to the next",
			script: "false &&\noras cp --recursive --from-oci-layout one\noras cp --recursive --from-oci-layout two\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "two"}},
		},
		{
			name:   "an arithmetic shift is not a heredoc operator",
			script: ": $((1 << 2))\noras cp --recursive --from-oci-layout one\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "one"}},
		},
		{
			name:   "a closing backtick restores the quote its substitution suspended",
			script: ": \"`true`\noras cp --recursive --from-oci-layout one\n\"\noras cp --recursive --from-oci-layout two\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "two"}},
		},
		{
			name:   "nothing after an unconditional exit is an invocation",
			script: "oras cp --recursive --from-oci-layout one\nexit 0\noras cp --recursive --from-oci-layout two\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "one"}},
		},
		{
			name:   "an exit inside a branch does not end the scan",
			script: "if false; then\nexit 0\nfi\noras cp --recursive --from-oci-layout one\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "one"}},
		},
		{
			name:   "an alias over the command proves no invocation of it",
			script: "shopt -s expand_aliases\nalias oras=':'\noras cp --recursive --from-oci-layout one\n",
			want:   nil,
		},
		{
			name:   "a step that redefines the command proves no invocation of it",
			script: "oras() { :; }\noras cp --recursive --from-oci-layout one\n",
			want:   nil,
		},
		{
			name:   "a definition whose brace opens on the next line still hides its body",
			script: "never_called ()\n{\noras cp --recursive --from-oci-layout one\n}\noras cp --recursive --from-oci-layout two\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "two"}},
		},
		{
			name:   "a heredoc opened as a quoted span closes still hides its body",
			script: ": \"\nx\n\" <<PLAN\noras cp --recursive --from-oci-layout one\nPLAN\noras cp --recursive --from-oci-layout two\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "two"}},
		},
		{
			name:   "arguments end at a control operator",
			script: "oras cp --recursive --from-oci-layout missing || true; : --to-oci-layout dist/release-image:amd64\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "missing"}},
		},
		{
			name:   "a command in a branch bash never runs is not an invocation",
			script: "if false; then\noras cp --recursive --from-oci-layout one\nfi\noras cp --recursive --from-oci-layout two\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "two"}},
		},
		{
			name:   "a redirection keeps its own ampersand",
			script: "oras cp --recursive --from-oci-layout one >/dev/null 2>&1\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "one", ">/dev/null", "2>&1"}},
		},
		{
			name:   "a quoted argument stays one word, so an option inside it is text",
			script: "oras cp 'ignored --to-oci-layout dist/release-image:amd64 ignored' || true\n",
			want:   [][]string{{"ignored --to-oci-layout dist/release-image:amd64 ignored"}},
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
		{
			name:   "words after a closing quote are arguments of the command that opened it",
			script: ": \"\nx\n\" oras cp --recursive --from-oci-layout one\noras cp --recursive --from-oci-layout two\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "two"}},
		},
		{
			name:   "an operator after a closing quote still starts a command",
			script: ": \"\nx\n\"; oras cp --recursive --from-oci-layout one\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "one"}},
		},
		{
			name:   "an exec that names a program ends the scan",
			script: "exec true\noras cp --recursive --from-oci-layout one\n",
			want:   nil,
		},
		{
			name:   "an exec of redirections alone leaves the script running",
			script: "exec 2>&1\noras cp --recursive --from-oci-layout one\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "one"}},
		},
		{
			name:   "a noclobber redirection keeps its own bar",
			script: ": >| oras cp --recursive --from-oci-layout one\noras cp --recursive --from-oci-layout two\n",
			want:   [][]string{{"--recursive", "--from-oci-layout", "two"}},
		},
		{
			name:   "a hashed path over the command proves no invocation of it",
			script: "hash -p /bin/true oras\noras cp --recursive --from-oci-layout one\n",
			want:   nil,
		},
		{
			name:   "a quoted separator is an argument, not the end of a command",
			script: ": \";\" oras cp one\n",
			want:   nil,
		},
		{
			name:   "an escaped separator is an argument, not the end of a command",
			script: ": \\; oras cp one\n",
			want:   nil,
		},
		{
			name:   "a quoted reserved word closes no compound command",
			script: "if false; then\n\"fi\"\noras cp one\nfi\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a quoted brace closes no definition body",
			script: "never_called() {\n\"}\"\noras cp one\n}\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "an ANSI-C delimiter ends its body at the word the quotes hold",
			script: ": <<$'PLAN'\n$PLAN\noras cp one\nPLAN\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "a backslash inside single quotes does not continue the line",
			script: "'or\\\nas' cp one\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "conditional status survives a command group",
			script: "false && {\noras cp one\n}\noras cp two\n",
			want:   [][]string{{"two"}},
		},
		{
			name:   "an unconditional command group runs its body",
			script: "{\noras cp one\n}\n",
			want:   [][]string{{"one"}},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var got [][]string
			for _, invocation := range metadatautil.ShellInvocations(testCase.script, "oras cp") {
				got = append(got, invocation.Args)
			}
			if !slices.EqualFunc(got, testCase.want, slices.Equal) {
				t.Fatalf("ShellInvocations() = %q, want %q", got, testCase.want)
			}
		})
	}
}
