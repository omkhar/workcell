// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin && cgo

package injectionpolicy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBundleReaderDarwinACL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		wantOK  bool
	}{
		{"standard deny", []string{"group:everyone deny delete"}, true},
		{"grant", []string{"group:everyone allow read"}, false},
		{"other deny", []string{"group:staff deny delete"}, false},
		{"other permission", []string{"group:everyone deny write"}, false},
		{"inherited", []string{"group:everyone deny delete,file_inherit"}, false},
		{"additional entry", []string{"group:staff deny delete", "group:everyone deny delete"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "policy")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := writePolicyFile(t, dir, "policy.toml", []byte("version = 1\n"))
			t.Cleanup(func() { _ = exec.Command("/bin/chmod", "-N", dir).Run() })
			for _, entry := range tc.entries {
				if output, err := exec.Command("/bin/chmod", "+a", entry, dir).CombinedOutput(); err != nil {
					t.Fatalf("chmod: %v: %s", err, output)
				}
			}
			_, err := NewBundleReader().Read(path)
			if (err == nil) != tc.wantOK {
				t.Fatalf("Read error = %v, want success=%t", err, tc.wantOK)
			}
		})
	}
}
