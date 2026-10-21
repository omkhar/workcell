// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package bashquote

import "testing"

func TestQuoteShellSafe(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                      "''",
		"abc":                   "abc",
		"a-b_c.d":               "a-b_c.d",
		"/usr/bin/git":          "/usr/bin/git",
		"with space":            `with\ space`,
		"feature/x":             "feature/x",
		"+refs/heads/x":         "+refs/heads/x",
		"Existing branch title": `Existing\ branch\ title`,
	}
	for in, want := range cases {
		got := Quote(in)
		if got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}
