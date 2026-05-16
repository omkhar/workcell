// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// FuzzIsSafeRelativeSymlinkTarget pins the security oracle that gates
// which symlink targets the injection-staging walker allows. The
// invariant: if isSafeRelativeSymlinkTarget(t) returns true, then t is
// a relative path with no `..` segments. A future regression that
// silently accepted an absolute target or a `..`-laden target would
// re-open the Sec-r2-1 escape that the FIX-10 component-walk closed.
//
// We don't seed `..`-bearing targets through `filepath.Clean` because
// Clean rewrites them; we check the raw segment-by-segment form the
// helper itself inspects.
func FuzzIsSafeRelativeSymlinkTarget(f *testing.F) {
	seeds := []string{
		"",
		"private/var",
		"private/etc",
		"private/tmp",
		"a",
		"a/b/c",
		"/etc",
		"/etc/passwd",
		"..",
		"../etc",
		"../../etc/passwd",
		"a/../b",
		"a/..",
		"/",
		"./relative",
		"a//b",
		"a/./b",
		"\x00",
		"target with spaces",
		strings.Repeat("a/", 64) + "leaf",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, target string) {
		if !isSafeRelativeSymlinkTarget(target) {
			return
		}
		if filepath.IsAbs(target) {
			t.Fatalf("isSafeRelativeSymlinkTarget accepted absolute target %q", target)
		}
		for _, seg := range strings.Split(target, "/") {
			if seg == ".." {
				t.Fatalf("isSafeRelativeSymlinkTarget accepted target with .. segment %q", target)
			}
		}
		if target == "" {
			t.Fatalf("isSafeRelativeSymlinkTarget accepted empty target")
		}
	})
}

// FuzzParseSSHDirective exercises the SSH config line parser that gates
// the risky-directive denylist in validateSSHConfigSafety. The
// structural invariants:
//   - when ok=true, directive is non-empty
//   - when ok=true, directive is fully lowercase (so the riskySSHDirectives
//     map lookup is deterministic)
//   - when ok=true, directive contains no whitespace runes
//   - lines whose first non-space byte is '#' return ok=false
//   - whitespace-only lines return ok=false
//
// A regression that returned a non-lowercase directive would silently
// bypass the unsafe-directive denylist (e.g. `ForwardAgent yes` lookup
// against a lowercase-key map). A regression that returned ok=true on a
// commented line would have the same effect.
func FuzzParseSSHDirective(f *testing.F) {
	seeds := []string{
		"",
		"   ",
		"\t",
		"# comment",
		"  # leading space then comment",
		"Host *",
		"host *",
		"HOST *",
		"ForwardAgent yes",
		"forwardagent yes",
		"ProxyCommand nc -X connect %h %p",
		"Match exec \"true\"",
		"Match user alice exec /bin/true",
		"Include ~/.ssh/extra",
		"IdentityFile ~/.ssh/id_ed25519",
		"PermitLocalCommand yes",
		"SetEnv FOO=bar",
		"\x00",
		"key\tvalue",
		"key\tvalue\twith\ttabs",
		"\xff\xfe",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, line string) {
		directive, _, ok := parseSSHDirective(line)
		stripped := strings.TrimSpace(line)
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			if ok {
				t.Fatalf("parseSSHDirective accepted blank/comment line as directive: %q", line)
			}
			return
		}
		if !ok {
			return
		}
		if directive == "" {
			t.Fatalf("parseSSHDirective returned ok=true with empty directive for %q", line)
		}
		if directive != strings.ToLower(directive) {
			t.Fatalf("parseSSHDirective returned non-lowercase directive %q for %q", directive, line)
		}
		for _, r := range directive {
			if unicode.IsSpace(r) {
				t.Fatalf("parseSSHDirective returned directive containing whitespace: %q for %q", directive, line)
			}
		}
	})
}
