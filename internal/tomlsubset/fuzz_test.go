// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package tomlsubset

import "testing"

// FuzzParse exercises the hand-rolled TOML subset parser against arbitrary
// input. The invariant is "no panic": for any byte slice, Parse must either
// return a non-nil error or a non-nil map; it must not crash. The parser
// gates auth- and injection-policy TOML files, which can carry
// attacker-influenceable content via includes/overrides, so a panic in
// Parse would surface as a process-level DoS at policy load time.
//
// Seed corpus mirrors the cases driving parse_test.go (Workcell-shaped
// valid documents and every reject case the parser enumerates).
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"\n",
		"# only a comment\n",
		"name = \"workcell\"\ncount = 42\nenabled = true\n",
		"[alpha]\nx = 1\ny = 2\n[beta]\nz = 3\n",
		"tags = [\"alpha\", \"beta\"]\n",
		"tags = [\n  \"a\",\n  \"b\",\n]\n",
		"[credentials.api]\nkey = \"value\"\n",
		"[t]\nk = \"line with # hash\"\n",
		"[t]\nk = -17\n",
		"[t]\nk = 0\n",
		"[t]\nk = true\n",
		"[t]\nk = false\n",
		"[t]\nk = []\n",
		// reject cases
		"[[copies]]\nx = 1\n",
		"a.b = 1\n",
		"[t]\nk = 1\nk = 2\n",
		"[t]\nk = 1\n[t]\nm = 2\n",
		"[t]\nk = \"\"\"hello\"\"\"\n",
		"[t]\nk = '''hello'''\n",
		"[t]\nk = { a = 1 }\n",
		"[t]\nk = 1979-05-27T07:32:00Z\n",
		"[t]\nk = 07:32:00\n",
		"[t]\nk = 1979-05-27\n",
		"[t]\n = 1\n",
		"[]\nk = 1\n",
		"[t]\nbare_line\n",
		"[t]\nk = [\n  \"a\",\n",
		"[t]\nk = banana\n",
		// edge shapes
		"[",
		"]",
		"=",
		"= =",
		"\"",
		"\\",
		"[t]\nk = \"\\u\"\n",
		"[t]\nk = \"\\x00\"\n",
		"[t]\nk = \"\\n\"\n",
		"\x00",
		"k=\xff\xfe\xfd",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, content string) {
		root, err := Parse(content, "fuzz.toml")
		if err == nil && root == nil {
			t.Fatalf("Parse returned nil map with nil error for %q", content)
		}
	})
}
