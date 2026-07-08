// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hardeningprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRepo materializes a fake repo under a temp dir: policy holds the
// policy/hardening-profile.toml body (empty means "do not create it"), and
// files maps repo-relative target paths to their contents.
func writeRepo(t *testing.T, policy string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if policy != "" {
		writeFile(t, root, profileRelPath, policy)
	}
	for rel, body := range files {
		writeFile(t, root, rel, body)
	}
	return root
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// happyPolicy is a minimal but structurally faithful hardening profile: one
// required literal and one forbidden literal over a single target file.
const happyPolicy = `version = 1

[capabilities]
target = "scripts/workcell"
required = ["--cap-drop ALL"]
forbidden = ["--privileged"]
`

const happyLauncher = `#!/bin/bash
docker run --cap-drop ALL --security-opt no-new-privileges:true "$@"
`

func TestCheckHappy(t *testing.T) {
	root := writeRepo(t, happyPolicy, map[string]string{"scripts/workcell": happyLauncher})
	if err := Check(root); err != nil {
		t.Fatalf("Check(happy) = %v, want nil", err)
	}
}

func TestCheckMissingRequired(t *testing.T) {
	// Launcher drops the required --cap-drop ALL: a weakening drift.
	launcher := "#!/bin/bash\ndocker run --security-opt no-new-privileges:true \"$@\"\n"
	root := writeRepo(t, happyPolicy, map[string]string{"scripts/workcell": launcher})
	err := Check(root)
	if err == nil {
		t.Fatal("Check(missing required) = nil, want violation")
	}
	if !strings.Contains(err.Error(), "missing required posture literal \"--cap-drop ALL\"") {
		t.Fatalf("unexpected message: %v", err)
	}
	if !strings.Contains(err.Error(), "[capabilities]") {
		t.Fatalf("message must name the section: %v", err)
	}
}

func TestCheckForbiddenPresent(t *testing.T) {
	// Launcher gains the forbidden --privileged: a weakening drift.
	launcher := "#!/bin/bash\ndocker run --cap-drop ALL --privileged \"$@\"\n"
	root := writeRepo(t, happyPolicy, map[string]string{"scripts/workcell": launcher})
	err := Check(root)
	if err == nil {
		t.Fatal("Check(forbidden present) = nil, want violation")
	}
	if !strings.Contains(err.Error(), "contains forbidden posture literal \"--privileged\"") {
		t.Fatalf("unexpected message: %v", err)
	}
}

func TestCheckMissingTargetFile(t *testing.T) {
	// A missing target file is treated as empty content, so the required
	// literal fails.
	root := writeRepo(t, happyPolicy, nil)
	if err := Check(root); err == nil {
		t.Fatal("Check(missing target) = nil, want violation")
	}
}

func TestCheckMissingPolicy(t *testing.T) {
	root := writeRepo(t, "", map[string]string{"scripts/workcell": happyLauncher})
	err := Check(root)
	if err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("Check(missing policy) = %v, want read error", err)
	}
}

func TestCheckBadVersion(t *testing.T) {
	policy := "version = 2\n\n[capabilities]\ntarget = \"scripts/workcell\"\nrequired = [\"--cap-drop ALL\"]\n"
	root := writeRepo(t, policy, map[string]string{"scripts/workcell": happyLauncher})
	err := Check(root)
	if err == nil || !strings.Contains(err.Error(), "version = 1") {
		t.Fatalf("Check(bad version) = %v, want version error", err)
	}
}

func TestCheckEmptySection(t *testing.T) {
	policy := "version = 1\n\n[capabilities]\ntarget = \"scripts/workcell\"\n"
	root := writeRepo(t, policy, map[string]string{"scripts/workcell": happyLauncher})
	err := Check(root)
	if err == nil || !strings.Contains(err.Error(), "neither required nor forbidden") {
		t.Fatalf("Check(empty section) = %v, want empty-section error", err)
	}
}

func TestCheckMalformedPolicy(t *testing.T) {
	root := writeRepo(t, "this is not = = toml [[[", map[string]string{"scripts/workcell": happyLauncher})
	if err := Check(root); err == nil {
		t.Fatal("Check(malformed policy) = nil, want parse error")
	}
}

// TestCheckCommentOnlySatisfiesNothing asserts the reported drift-detection
// hole is closed: a required literal that appears ONLY in a comment (removed
// from real code) must FAIL, because comments are stripped before scanning.
func TestCheckCommentOnlySatisfiesNothing(t *testing.T) {
	policy := "version = 1\n\n[capabilities]\ntarget = \"scripts/workcell\"\nrequired = [\"--cap-add SETUID\"]\n"
	// Full-line notice comment naming the literal, but no real --cap-add arg.
	launcher := "#!/bin/bash\n# --cap-add SETUID/--cap-add SETGID block in DOCKER_RUN_BASE composition\ndocker run --cap-drop ALL \"$@\"\n"
	root := writeRepo(t, policy, map[string]string{"scripts/workcell": launcher})
	err := Check(root)
	if err == nil {
		t.Fatal("Check(literal only in comment) = nil, want violation (comment must not satisfy presence)")
	}
	if !strings.Contains(err.Error(), "missing required posture literal \"--cap-add SETUID\"") {
		t.Fatalf("unexpected message: %v", err)
	}
	// Sanity: the SAME literal on a real code line passes.
	launcher2 := "#!/bin/bash\ndocker run --cap-drop ALL --cap-add SETUID \"$@\"\n"
	root2 := writeRepo(t, policy, map[string]string{"scripts/workcell": launcher2})
	if err := Check(root2); err != nil {
		t.Fatalf("Check(literal in code) = %v, want nil", err)
	}
}

// TestCheckForbiddenOnlyInCommentPasses asserts a forbidden literal that
// appears only in a comment is NOT a real weakening and must not fail CI.
func TestCheckForbiddenOnlyInCommentPasses(t *testing.T) {
	policy := "version = 1\n\n[security_opt]\ntarget = \"scripts/workcell\"\nrequired = [\"--cap-drop ALL\"]\nforbidden = [\"seccomp=unconfined\"]\n"
	launcher := "#!/bin/bash\n# never pass seccomp=unconfined here\ndocker run --cap-drop ALL \"$@\"\n"
	root := writeRepo(t, policy, map[string]string{"scripts/workcell": launcher})
	if err := Check(root); err != nil {
		t.Fatalf("Check(forbidden only in comment) = %v, want nil", err)
	}
}

func TestStripLineComment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# whole line", ""},
		{"   # indented comment", "   "},
		{"code --flag  # trailing comment", "code --flag  "},
		{`echo "a#b"`, `echo "a#b"`},                                           // '#' inside double quotes preserved
		{`echo 'x#y' # tail`, `echo 'x#y' `},                                   // quoted '#' preserved, tail stripped
		{`git config core.hookspath=#none`, `git config core.hookspath=#none`}, // '#' not word-start (no preceding space)
		{`val=a\#b`, `val=a\#b`},                                               // backslash-escaped '#' preserved
		{"no comment here", "no comment here"},
	}
	for _, tc := range cases {
		if got := stripLineComment(tc.in); got != tc.want {
			t.Errorf("stripLineComment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// egressFixture has one host declared in TWO functions; the policy scopes each
// section to a distinct block so a host must appear in ITS block.
const egressTwoBlocks = `#!/bin/bash
provider_endpoints() {
  echo "api.openai.com:443 shared.example.com:443"
}
bootstrap_endpoints() {
  echo "registry.example.com:443 shared.example.com:443"
}
`

// TestCheckBlockScopingCatchesWrongBlock is the FIX 1 load-bearing test: a
// required literal scoped to bootstrap_endpoints() must FAIL when the host is
// removed from THAT block, even though the same host still appears in
// provider_endpoints(). Under a whole-file scan this drift went undetected.
func TestCheckBlockScopingCatchesWrongBlock(t *testing.T) {
	policy := `version = 1

[egress_bootstrap]
target = "scripts/lib/launcher/egress-endpoints.sh"
block = "bootstrap_endpoints"
required = ["registry.example.com:443"]
`
	// Baseline: registry host is in bootstrap_endpoints() -> passes.
	root := writeRepo(t, policy, map[string]string{"scripts/lib/launcher/egress-endpoints.sh": egressTwoBlocks})
	if err := Check(root); err != nil {
		t.Fatalf("Check(baseline block-scoped) = %v, want nil", err)
	}

	// Drift: move registry.example.com out of bootstrap_endpoints() into
	// provider_endpoints(). File-wide the host still exists, but the block that
	// must contain it no longer does -> Check must FAIL.
	moved := `#!/bin/bash
provider_endpoints() {
  echo "api.openai.com:443 shared.example.com:443 registry.example.com:443"
}
bootstrap_endpoints() {
  echo "shared.example.com:443"
}
`
	root2 := writeRepo(t, policy, map[string]string{"scripts/lib/launcher/egress-endpoints.sh": moved})
	err := Check(root2)
	if err == nil {
		t.Fatal("Check(host moved to wrong block) = nil, want violation")
	}
	if !strings.Contains(err.Error(), "block bootstrap_endpoints()") {
		t.Fatalf("message must name the guarded block: %v", err)
	}
	if !strings.Contains(err.Error(), "registry.example.com:443") {
		t.Fatalf("message must name the missing host: %v", err)
	}
}

// TestCheckCapAddEquivalentSpellings is the FIX 2 load-bearing test: required
// caps are satisfied by any Docker-equivalent spelling, and a forbidden cap is
// caught in every equivalent spelling.
func TestCheckCapAddEquivalentSpellings(t *testing.T) {
	policy := `version = 1

[capabilities]
target = "scripts/workcell"
required = ["--cap-add SETUID", "--cap-add SETGID"]
forbidden = ["--cap-add SYS_ADMIN"]
`
	// Required caps present only in the =CAP_ form must still satisfy.
	ok := "#!/bin/bash\ndocker run --cap-add=CAP_SETUID --cap-add=cap_setgid \"$@\"\n"
	if err := Check(writeRepo(t, policy, map[string]string{"scripts/workcell": ok})); err != nil {
		t.Fatalf("Check(equivalent required spellings) = %v, want nil", err)
	}

	// Forbidden cap added via each equivalent spelling must be CAUGHT.
	for _, spelling := range []string{
		"--cap-add=SYS_ADMIN",
		"--cap-add CAP_SYS_ADMIN",
		"--cap-add=CAP_SYS_ADMIN",
		"--cap-add sys_admin",
	} {
		body := "#!/bin/bash\ndocker run --cap-add SETUID --cap-add SETGID " + spelling + " \"$@\"\n"
		err := Check(writeRepo(t, policy, map[string]string{"scripts/workcell": body}))
		if err == nil {
			t.Fatalf("Check(forbidden %q) = nil, want violation", spelling)
		}
		if !strings.Contains(err.Error(), "forbidden posture literal") {
			t.Fatalf("spelling %q: unexpected message: %v", spelling, err)
		}
	}

	// A capability that merely shares a prefix must NOT match (word boundary).
	safe := "#!/bin/bash\ndocker run --cap-add SETUID --cap-add SETGID --cap-add SYS_ADMINX \"$@\"\n"
	if err := Check(writeRepo(t, policy, map[string]string{"scripts/workcell": safe})); err != nil {
		t.Fatalf("Check(SYS_ADMINX must not match SYS_ADMIN) = %v, want nil", err)
	}
}

// TestCheckEndpointBoundary asserts a required host:port is matched on host
// boundaries: github.com:443 must NOT be satisfied by api.github.com:443 alone.
func TestCheckEndpointBoundary(t *testing.T) {
	policy := `version = 1

[egress]
target = "scripts/lib/launcher/egress-endpoints.sh"
block = "provider_endpoints"
required = ["github.com:443"]
`
	// Only api.github.com:443 present -> the bare github.com:443 must NOT match.
	masked := "#!/bin/bash\nprovider_endpoints() {\n  echo \"api.github.com:443\"\n}\n"
	if err := Check(writeRepo(t, policy, map[string]string{"scripts/lib/launcher/egress-endpoints.sh": masked})); err == nil {
		t.Fatal("Check(github.com:443 masked by api.github.com:443) = nil, want violation")
	}
	// Standalone github.com:443 present -> matches.
	present := "#!/bin/bash\nprovider_endpoints() {\n  echo \"api.github.com:443 github.com:443\"\n}\n"
	if err := Check(writeRepo(t, policy, map[string]string{"scripts/lib/launcher/egress-endpoints.sh": present})); err != nil {
		t.Fatalf("Check(standalone github.com:443) = %v, want nil", err)
	}
}

// TestCheckExactEndpointsBidirectional is the FIX (L153) load-bearing test:
// with exact_endpoints, a section must EQUAL the set its block emits — a source
// endpoint not declared fails (the new direction), and a declared endpoint
// missing from source fails (existing direction).
func TestCheckExactEndpointsBidirectional(t *testing.T) {
	policy := `version = 1

[egress]
target = "scripts/lib/launcher/egress-endpoints.sh"
block = "provider_endpoints"
exact_endpoints = true
required = ["api.openai.com:443", "auth.openai.com:443"]
`
	// Baseline: declared set equals emitted set -> passes.
	match := "#!/bin/bash\nprovider_endpoints() {\n  echo \"api.openai.com:443 auth.openai.com:443\"\n}\n"
	if err := Check(writeRepo(t, policy, map[string]string{"scripts/lib/launcher/egress-endpoints.sh": match})); err != nil {
		t.Fatalf("Check(exact match) = %v, want nil", err)
	}

	// NEW direction: block emits an extra host the artifact never declared.
	extra := "#!/bin/bash\nprovider_endpoints() {\n  echo \"api.openai.com:443 auth.openai.com:443 example.com:443\"\n}\n"
	err := Check(writeRepo(t, policy, map[string]string{"scripts/lib/launcher/egress-endpoints.sh": extra}))
	if err == nil {
		t.Fatal("Check(source endpoint not declared) = nil, want violation")
	}
	if !strings.Contains(err.Error(), "emits endpoint \"example.com:443\" not declared") {
		t.Fatalf("unexpected message: %v", err)
	}

	// Existing direction: a declared endpoint removed from the source.
	missing := "#!/bin/bash\nprovider_endpoints() {\n  echo \"api.openai.com:443\"\n}\n"
	err = Check(writeRepo(t, policy, map[string]string{"scripts/lib/launcher/egress-endpoints.sh": missing}))
	if err == nil || !strings.Contains(err.Error(), "missing required posture literal \"auth.openai.com:443\"") {
		t.Fatalf("Check(declared missing from source) = %v, want missing-required violation", err)
	}
}

// TestCheckExactEndpointsRequiresBlock asserts exact_endpoints without a block
// is a schema error.
func TestCheckExactEndpointsRequiresBlock(t *testing.T) {
	policy := "version = 1\n\n[egress]\ntarget = \"scripts/workcell\"\nexact_endpoints = true\nrequired = [\"example.com:443\"]\n"
	launcher := "#!/bin/bash\necho example.com:443\n"
	err := Check(writeRepo(t, policy, map[string]string{"scripts/workcell": launcher}))
	if err == nil || !strings.Contains(err.Error(), "exact_endpoints but declares no block") {
		t.Fatalf("Check(exact without block) = %v, want schema error", err)
	}
}

// TestCheckSecurityOptEquivalentSpellings is the FIX (L247) load-bearing test:
// a required --security-opt is satisfied by any equivalent spelling, and a
// forbidden --security-opt is caught in the = form and the bash-array quoted
// form.
func TestCheckSecurityOptEquivalentSpellings(t *testing.T) {
	policy := `version = 1

[security_opt]
target = "scripts/workcell"
required = ["--security-opt no-new-privileges:true"]
forbidden = ["--security-opt label=disable"]
`
	// Required present only in the = form still satisfies.
	ok := "#!/bin/bash\ndocker run --security-opt=no-new-privileges:true \"$@\"\n"
	if err := Check(writeRepo(t, policy, map[string]string{"scripts/workcell": ok})); err != nil {
		t.Fatalf("Check(equivalent required --security-opt) = %v, want nil", err)
	}

	// Forbidden caught in each equivalent spelling, including the bash-array
	// form (flag and quoted value as separate elements).
	for _, spelling := range []string{
		`--security-opt=label=disable`,
		`--security-opt "label=disable"`,
		`--security-opt label=disable`,
	} {
		body := "#!/bin/bash\ndocker run --security-opt no-new-privileges:true " + spelling + " \"$@\"\n"
		err := Check(writeRepo(t, policy, map[string]string{"scripts/workcell": body}))
		if err == nil {
			t.Fatalf("Check(forbidden %q) = nil, want violation", spelling)
		}
		if !strings.Contains(err.Error(), "forbidden posture literal") {
			t.Fatalf("spelling %q: unexpected message: %v", spelling, err)
		}
	}
}

func TestParseSecurityOptLiteral(t *testing.T) {
	cases := []struct {
		in      string
		wantVal string
		wantOK  bool
	}{
		{"--security-opt no-new-privileges:true", "no-new-privileges:true", true},
		{"--security-opt=label=disable", "label=disable", true},
		{`--security-opt "seccomp=unconfined"`, "seccomp=unconfined", true},
		{"--privileged", "", false},
		{"--cap-add SETUID", "", false},
	}
	for _, tc := range cases {
		val, ok := parseSecurityOptLiteral(tc.in)
		if ok != tc.wantOK || val != tc.wantVal {
			t.Errorf("parseSecurityOptLiteral(%q) = (%q,%v), want (%q,%v)", tc.in, val, ok, tc.wantVal, tc.wantOK)
		}
	}
}

func TestParseCapLiteral(t *testing.T) {
	cases := []struct {
		in       string
		wantVerb string
		wantCap  string
		wantOK   bool
	}{
		{"--cap-add SETUID", "add", "SETUID", true},
		{"--cap-add=CAP_SETUID", "add", "SETUID", true},
		{"--cap-drop ALL", "drop", "ALL", true},
		{"--cap-add cap_sys_admin", "add", "SYS_ADMIN", true},
		{"--privileged", "", "", false},
		{"api.github.com:443", "", "", false},
	}
	for _, tc := range cases {
		verb, cap, ok := parseCapLiteral(tc.in)
		if ok != tc.wantOK || verb != tc.wantVerb || cap != tc.wantCap {
			t.Errorf("parseCapLiteral(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.in, verb, cap, ok, tc.wantVerb, tc.wantCap, tc.wantOK)
		}
	}
}

// TestCheckRealRepo asserts the shipped policy/hardening-profile.toml conforms
// to the real scripts/workcell and egress-endpoints.sh — the load-bearing A6
// gate. It is skipped when run outside the repo tree.
func TestCheckRealRepo(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(repoRoot, profileRelPath)); err != nil {
		t.Skipf("real %s not found: %v", profileRelPath, err)
	}
	if err := Check(repoRoot); err != nil {
		t.Fatalf("Check(real repo) = %v, want nil", err)
	}
}
