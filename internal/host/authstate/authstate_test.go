// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authstate

import (
	"path/filepath"
	"testing"
)

func TestForbiddenCredentialSourceRootRejectsProviderState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	source := filepath.Join(home, ".config", "gh", "hosts.yml")
	root, ok := ForbiddenCredentialSourceRoot(source)
	if !ok {
		t.Fatalf("ForbiddenCredentialSourceRoot(%q) did not reject provider state", source)
	}
	if want := filepath.Join(home, ".config", "gh"); root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
}

func TestForbiddenCredentialSourceRootRejectsCaseVariedProviderState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Home")

	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "github cli auth",
			source: filepath.Join(home, ".Config", "gh", "hosts.yml"),
			want:   filepath.Join(home, ".config", "gh"),
		},
		{
			name:   "claude top-level auth mirror",
			source: filepath.Join(home, ".Claude.Json"),
			want:   filepath.Join(home, ".claude.json"),
		},
		{
			name:   "claude xdg auth mirror",
			source: filepath.Join(home, ".Config", "claude-code", "auth.json"),
			want:   filepath.Join(home, ".config", "claude-code"),
		},
		{
			name:   "top-level mcp registry",
			source: filepath.Join(home, ".Mcp.Json"),
			want:   filepath.Join(home, ".mcp.json"),
		},
		{
			name:   "gcloud adc",
			source: filepath.Join(home, ".Config", "gcloud", "application_default_credentials.json"),
			want:   filepath.Join(home, ".config", "gcloud"),
		},
		{
			name:   "xdg git auth",
			source: filepath.Join(home, ".Config", "git", "credentials"),
			want:   filepath.Join(home, ".config", "git"),
		},
		{
			name:   "ssh auth",
			source: filepath.Join(home, ".SSH", "id_ed25519"),
			want:   filepath.Join(home, ".ssh"),
		},
		{
			name:   "docker auth",
			source: filepath.Join(home, ".Docker", "config.json"),
			want:   filepath.Join(home, ".docker"),
		},
		{
			name:   "kube auth",
			source: filepath.Join(home, ".Kube", "config"),
			want:   filepath.Join(home, ".kube"),
		},
		{
			name:   "keychain",
			source: filepath.Join(home, "library", "keychains", "login.keychain-db"),
			want:   filepath.Join(home, "Library", "Keychains"),
		},
		{
			name:   "netrc auth",
			source: filepath.Join(home, ".Netrc"),
			want:   filepath.Join(home, ".netrc"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, ok := forbiddenCredentialSourceRoot(tc.source, home, true)
			if !ok {
				t.Fatalf("forbiddenCredentialSourceRoot(%q) did not reject case-varied provider state", tc.source)
			}
			if root != tc.want {
				t.Fatalf("root = %q, want %q", root, tc.want)
			}
		})
	}
}

func TestForbiddenCredentialSourceRootAllowsSiblingAndManagedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, source := range []string{
		filepath.Join(home, ".config", "github-copilot-export", "token.txt"),
		filepath.Join(home, ".local", "state", "workcell", "credentials", "copilot", "github-token.txt"),
	} {
		if root, ok := ForbiddenCredentialSourceRoot(source); ok {
			t.Fatalf("ForbiddenCredentialSourceRoot(%q) rejected allowed source under %q", source, root)
		}
	}
}
