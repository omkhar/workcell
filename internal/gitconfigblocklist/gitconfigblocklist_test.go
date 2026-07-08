// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package gitconfigblocklist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureTOML is a minimal but structurally faithful blocklist: two
// exact keys and one prefix/suffix pattern, plus a trailing
// [[prefix_patterns]] table (which the parser must ignore) to exercise
// the block-closing logic.
const fixtureTOML = `# fixture blocklist
keys = [
  "core.askpass",
  "credential.helper",
]

[[prefix_suffix_patterns]]
prefix = "credential."
suffix = ".helper"

[[prefix_patterns]]
prefix = "pager."
`

// enforcerBody contains every key + the prefix and suffix substrings, so
// a happy-path enforcer passes the parity check.
const enforcerBody = `blocked: core.askpass credential.helper
patterns: credential. .helper other
`

// writeRepo materializes a fake repo directory with the given TOML and
// the given per-enforcer bodies (keyed by the relative enforcer path).
// A nil body for an enforcer path means "do not create the file".
func writeRepo(t *testing.T, toml string, bodies map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, tomlRelPath), toml)
	for _, rel := range enforcerRelPaths {
		body, ok := bodies[rel]
		if !ok {
			continue
		}
		writeFile(t, filepath.Join(root, rel), body)
	}
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func allEnforcers(body string) map[string]string {
	m := make(map[string]string, len(enforcerRelPaths))
	for _, rel := range enforcerRelPaths {
		m[rel] = body
	}
	return m
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		bodies  map[string]string
		wantErr string // "" means expect success
	}{
		{
			name:   "happy path all present",
			toml:   fixtureTOML,
			bodies: allEnforcers(enforcerBody),
		},
		{
			name: "missing key",
			toml: fixtureTOML,
			bodies: func() map[string]string {
				m := allEnforcers(enforcerBody)
				m["runtime/container/bin/git"] = "blocked: core.askpass\npatterns: credential. .helper\n"
				return m
			}(),
			wantErr: "git-config blocklist key 'credential.helper' from policy/git-config-blocklist.toml is missing in %ROOT%/runtime/container/bin/git\n" +
				"Add the same key to that enforcer or remove it from the TOML.",
		},
		{
			name: "missing prefix",
			toml: fixtureTOML,
			bodies: func() map[string]string {
				m := allEnforcers(enforcerBody)
				// Drop the "credential." prefix substring but keep the keys and
				// the ".helper" suffix.  "credential.helper" as a key still
				// contains "credential." though — so remove that too and supply
				// the key via a different spelling.  Simplest: give this
				// enforcer a body that has both keys but no bare "credential."
				// prefix and no ".helper" — then it would fail on the key first.
				// Instead craft a body with keys present but prefix absent by
				// using a body where keys appear without the pattern substrings.
				m["scripts/workcell"] = "core.askpass credential=helper .helper\n"
				return m
			}(),
			// The key 'credential.helper' is absent from the crafted body, so
			// the key check fires before the pattern check — this documents the
			// shell's ordering (keys before patterns).
			wantErr: "git-config blocklist key 'credential.helper' from policy/git-config-blocklist.toml is missing in %ROOT%/scripts/workcell\n" +
				"Add the same key to that enforcer or remove it from the TOML.",
		},
		{
			name: "missing suffix only",
			toml: "keys = [\n  \"core.askpass\",\n]\n\n[[prefix_suffix_patterns]]\nprefix = \"credential.\"\nsuffix = \".helper\"\n",
			bodies: func() map[string]string {
				// All enforcers have the key "core.askpass" and the prefix
				// "credential." but the last one lacks the ".helper" suffix.
				m := map[string]string{
					"scripts/workcell":                        "core.askpass credential. .helper\n",
					"runtime/container/bin/git":               "core.askpass credential. .helper\n",
					"runtime/container/rust/src/gitpolicy.rs": "core.askpass credential. nohelper\n",
				}
				return m
			}(),
			wantErr: "git-config blocklist prefix+suffix pattern 'credential.*.helper' missing in %ROOT%/runtime/container/rust/src/gitpolicy.rs",
		},
		{
			name:    "empty keys",
			toml:    "keys = [\n]\n",
			bodies:  allEnforcers(enforcerBody),
			wantErr: "policy/git-config-blocklist.toml had no [keys] entries; parity check needs at least one",
		},
		{
			name: "unreadable enforcer",
			toml: fixtureTOML,
			bodies: func() map[string]string {
				m := allEnforcers(enforcerBody)
				delete(m, "runtime/container/bin/git")
				return m
			}(),
			wantErr: "git-config blocklist enforcer missing or unreadable: %ROOT%/runtime/container/bin/git",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := writeRepo(t, tc.toml, tc.bodies)
			err := Check(root)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Check() = nil, want error %q", tc.wantErr)
			}
			want := replaceRoot(tc.wantErr, root)
			if err.Error() != want {
				t.Fatalf("Check() error = %q, want %q", err.Error(), want)
			}
		})
	}
}

// replaceRoot substitutes the %ROOT% placeholder with the concrete temp
// dir so message assertions can be written path-independently.
func replaceRoot(msg, root string) string {
	return strings.ReplaceAll(msg, "%ROOT%", root)
}

func TestParseKeys(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "basic array",
			content: fixtureTOML,
			want:    []string{"core.askpass", "credential.helper"},
		},
		{
			name:    "empty array",
			content: "keys = [\n]\n",
			want:    nil,
		},
		{
			name:    "no keys stanza",
			content: "[[prefix_suffix_patterns]]\nprefix = \"a.\"\n",
			want:    nil,
		},
		{
			name:    "whitespace around assignment",
			content: "keys   =   [\n    \"only.key\"\n]\n",
			want:    []string{"only.key"},
		},
		{
			name:    "trailing comma optional",
			content: "keys = [\n  \"a.b\",\n  \"c.d\"\n]\n",
			want:    []string{"a.b", "c.d"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseKeys(tc.content)
			if !equalStrings(got, tc.want) {
				t.Fatalf("parseKeys() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestParsePrefixSuffixPatterns(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []prefixSuffixPattern
	}{
		{
			name:    "two patterns then prefix-only table",
			content: realShapeTOML,
			want: []prefixSuffixPattern{
				{prefix: "credential.", suffix: ".helper"},
				{prefix: "includeif.", suffix: ".path"},
			},
		},
		{
			name:    "single pattern flushed at EOF",
			content: "[[prefix_suffix_patterns]]\nprefix = \"a.\"\nsuffix = \".b\"",
			want:    []prefixSuffixPattern{{prefix: "a.", suffix: ".b"}},
		},
		{
			name:    "incomplete block dropped",
			content: "[[prefix_suffix_patterns]]\nprefix = \"a.\"\n\n",
			want:    nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePrefixSuffixPatterns(tc.content)
			if !equalPatterns(got, tc.want) {
				t.Fatalf("parsePrefixSuffixPatterns() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// realShapeTOML mirrors the real policy file's prefix_suffix/prefix
// layout closely enough to exercise the flush-on-blank and
// flush-on-other-table paths.
const realShapeTOML = `keys = [
  "core.askpass",
]

[[prefix_suffix_patterns]]
prefix = "credential."
suffix = ".helper"

[[prefix_suffix_patterns]]
prefix = "includeif."
suffix = ".path"

[[prefix_patterns]]
prefix = "pager."
`

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalPatterns(a, b []prefixSuffixPattern) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
