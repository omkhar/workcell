// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"os"
	"path/filepath"
	"testing"
)

// OpenSSH accepts `Keyword=value`, `Keyword = value`, and a quoted keyword.
// The safety check must see the same keyword that ssh(1) sees.
func TestValidateSSHConfigSafetyKeywordForms(t *testing.T) {
	unsafe := []string{
		"ProxyCommand sh -c id",
		"ProxyCommand=sh -c id",
		"ProxyCommand = sh -c id",
		"ProxyCommand= sh -c id",
		"\"ProxyCommand\" sh -c id",
		"\"ProxyCommand\"=sh -c id",
		"ProxyCom\"mand\"=echo HI",
		"\"Proxy\"Command sh -c id",
		"Match=exec \"id\"",
		"Match = user alice exec id",
		"Match \"exec\" \"id\"",
		"Match !exec \"id\"",
		"Match EXEC id",
		"Match exec=\"id\"",
		"Include=/x",
		"PKCS11Provider=/x.so",
		"\tLocalCommand\t=\tid",
		"=ProxyCommand sh -c id",
		"= ProxyCommand sh -c id",
		"ProxyCommand==sh -c id",
	}
	safe := []string{
		"Host *",
		"User=git",
		"HostName=example.com",
		"Match=user exec-user",
		"# ProxyCommand=sh -c id",
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	check := func(line string) error {
		if err := os.WriteFile(path, []byte("Host *\n  "+line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return validateSSHConfigSafety(Path(path), false)
	}
	for _, line := range unsafe {
		if err := check(line); err == nil {
			t.Errorf("validateSSHConfigSafety accepted %q", line)
		}
	}
	for _, line := range safe {
		if err := check(line); err != nil {
			t.Errorf("validateSSHConfigSafety rejected %q: %v", line, err)
		}
	}
}
