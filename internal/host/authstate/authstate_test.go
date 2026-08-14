// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authstate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/pathutil"
)

func TestRejectCredentialSourceFailsClosedWhenHomeIsUnavailable(t *testing.T) {
	t.Setenv("HOME", "")
	err := RejectCredentialSource("/tmp/source", "credential")
	if err == nil || !strings.Contains(err.Error(), "cannot check") {
		t.Fatalf("RejectCredentialSource error = %v", err)
	}
	err = RejectCredentialDirectorySource("/tmp/source", "credential")
	if err == nil || !strings.Contains(err.Error(), "cannot check") {
		t.Fatalf("RejectCredentialDirectorySource error = %v", err)
	}
}

func TestForbiddenCredentialSourceRootRejectsInvalidUTF8WithoutDisclosure(t *testing.T) {
	home := t.TempDir()
	invalid := "secret-prefix-" + string([]byte{0xff})
	_, _, err := forbiddenCredentialSourceRoot(invalid, home, true)
	if !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("forbiddenCredentialSourceRoot error = %v", err)
	}
	_, _, err = forbiddenCredentialDirectorySourceRoot(invalid, home, true)
	if !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("forbiddenCredentialDirectorySourceRoot error = %v", err)
	}
}

func TestForbiddenCredentialRootsRejectUnicodeAliasesAndSiblings(t *testing.T) {
	for _, pair := range [][2]string{{"café", "cafe\u0301"}, {"straße", "STRASSE"}, {"Σ", "ς"}, {"ﬀ", "ff"}, {"µ", "Μ"}, {"ś", "ſ\u0301"}} {
		home := filepath.Join(t.TempDir(), pair[0])
		sourceHome := filepath.Join(filepath.Dir(home), pair[1])
		file := filepath.Join(sourceHome, ".codex", "auth.json")
		if _, ok, err := forbiddenCredentialSourceRoot(file, home, true); err != nil || !ok {
			t.Fatalf("file alias %q/%q = %v, %v", pair[0], pair[1], ok, err)
		}
		if _, ok, err := forbiddenCredentialDirectorySourceRoot(sourceHome, home, true); err != nil || !ok {
			t.Fatalf("directory alias %q/%q = %v, %v", pair[0], pair[1], ok, err)
		}
		sibling := sourceHome + "-sibling"
		if _, ok, err := forbiddenCredentialSourceRoot(filepath.Join(sibling, ".codex", "auth.json"), home, true); err != nil || ok {
			t.Fatalf("file sibling %q = %v, %v", sibling, ok, err)
		}
		if _, ok, err := forbiddenCredentialDirectorySourceRoot(sibling, home, true); err != nil || ok {
			t.Fatalf("directory sibling %q = %v, %v", sibling, ok, err)
		}
	}
}

func TestForbiddenCredentialSourceRootRejectsProviderState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	source := filepath.Join(home, ".config", "gh", "hosts.yml")
	root, ok, err := ForbiddenCredentialSourceRoot(source)
	if err != nil {
		t.Fatal(err)
	}
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
			root, ok, err := forbiddenCredentialSourceRoot(tc.source, home, true)
			if err != nil {
				t.Fatal(err)
			}
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
		if root, ok, err := ForbiddenCredentialSourceRoot(source); err != nil {
			t.Fatal(err)
		} else if ok {
			t.Fatalf("ForbiddenCredentialSourceRoot(%q) rejected allowed source under %q", source, root)
		}
	}
}

func TestForbiddenCredentialDirectorySourceRootRejectsAncestorProviderState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Home")

	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "xdg config ancestor",
			source: filepath.Join(home, ".config"),
			want:   filepath.Join(home, ".config", "claude-code"),
		},
		{
			name:   "case varied xdg config ancestor",
			source: filepath.Join(home, ".Config"),
			want:   filepath.Join(home, ".config", "claude-code"),
		},
		{
			name:   "home ancestor",
			source: home,
			want:   filepath.Join(home, ".codex"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, ok, err := forbiddenCredentialDirectorySourceRoot(tc.source, home, true)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatalf("forbiddenCredentialDirectorySourceRoot(%q) did not reject directory containing provider state", tc.source)
			}
			if root != tc.want {
				t.Fatalf("root = %q, want %q", root, tc.want)
			}
		})
	}
}

func TestForbiddenCredentialDirectorySourceRootAllowsSibling(t *testing.T) {
	home := t.TempDir()

	for _, source := range []string{
		filepath.Join(home, ".config", "github-copilot-export"),
		filepath.Join(home, ".local", "state", "workcell"),
	} {
		if root, ok, err := forbiddenCredentialDirectorySourceRoot(source, home, true); err != nil {
			t.Fatal(err)
		} else if ok {
			t.Fatalf("forbiddenCredentialDirectorySourceRoot(%q) rejected allowed directory under %q", source, root)
		}
	}
}
