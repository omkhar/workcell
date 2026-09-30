// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A -c override of the projects table can mark a directory trusted, so Codex
// then loads a workspace-written .codex/config.toml above the managed layers.
// Every spelling of that override must stay blocked.
func TestCodexPolicyRejectsProjectsTrustOverrides(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	policy := filepath.Join(repoRoot(t), "runtime", "container", "provider-policy.sh")
	blocked := [][]string{
		{"-c", `projects./workspace/sub.trust_level="trusted"`},
		{"-c", `projects."/workspace".trust_level="trusted"`},
		{"-c", `projects={"/workspace/sub"={trust_level="trusted"}}`},
		{"--config", `projects={"/workspace/sub"={trust_level="trusted"}}`},
		{`--config=projects.x.trust_level="trusted"`},
		{`-cprojects.x.trust_level="trusted"`},
		{"-c", "\nprojects.\"/workspace\".trust_level=\"trusted\""},
		{"-c", "model\n.x=1\nprojects.x.trust_level=\"trusted\""},
		{"-c", "\u00a0projects={\"/workspace\"={trust_level=\"trusted\"}}"},
		{"-c", "\u2003projects.x.trust_level=\"trusted\""},
		{"-c", "\vprojects.x.trust_level=\"trusted\""},
		{"-c", "profiles=\u00a0{strict={projects={\"/workspace\"={trust_level=\"trusted\"}}}}"},
		{"-c", "features=\u2003{remote_plugin=true}"},
	}
	run := func(args ...string) error {
		cmd := exec.Command(bash, append([]string{"-c", `source "$1"; shift; reject_unsafe_codex_args "$@" exec hi`, "_", policy}, args...)...)
		cmd.Env = append(os.Environ(), "LC_ALL=C") // a non-UTF-8 locale must not let NBSP dodge the guard
		return cmd.Run()
	}
	for _, args := range blocked {
		if run(args...) == nil {
			t.Errorf("expected reject_unsafe_codex_args to block %q", args)
		}
	}
	if err := run("-c", `model="gpt-5"`); err != nil {
		t.Errorf("control override must stay allowed: %v", err)
	}
}
