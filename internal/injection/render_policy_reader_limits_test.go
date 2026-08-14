// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/injectionpolicy"
)

func TestLoadPolicyBundleReaderLimits(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "policy.toml")
	fragment := filepath.Join(dir, "fragment.toml")
	entryText := "version = 1\nincludes = [\"fragment.toml\"]\n"
	fragmentText := "version = 1\n"
	writeReaderPolicy(t, entry, entryText)
	writeReaderPolicy(t, fragment, fragmentText)
	size := int64(len(entryText) + len(fragmentText))
	for _, tc := range []struct {
		want  string
		bytes int64
		files int
	}{
		{"", size, 2}, {"aggregate bundle limit", size - 1, 2}, {"include/file count limit", size, 1},
	} {
		_, _, err := loadPolicyBundleWithReader(Path(entry), injectionpolicy.NewBundleReaderWithLimits(128, tc.bytes, tc.files))
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Fatalf("load policy bundle error = %v, want %q", err, tc.want)
		}
	}
}

func TestLoadPolicyBundleBindsSnapshotAndDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.toml")
	original := "version = 1\n[documents]\noriginal = \"original.md\"\n"
	writeReaderPolicy(t, path, original)
	reader := injectionpolicy.NewBundleReader()
	if _, err := reader.ReadAndPin(path); err != nil {
		t.Fatal(err)
	}
	writeReaderPolicy(t, path, "version = 1\n")
	policy, sources, err := loadPolicyBundleWithReader(Path(path), reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := policy["documents"].(map[string]any)["original"]; !ok {
		t.Fatalf("policy = %#v", policy)
	}
	sum := sha256.Sum256([]byte(original))
	if len(sources) != 1 || sources[0].Sha256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("sources = %#v", sources)
	}
}

func TestLoadPolicyBundleRejectsUnsafeInclude(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	writeReaderPolicy(t, filepath.Join(target, "fragment.toml"), "version = 1\n")
	if err := os.Symlink(filepath.Join(target, "fragment.toml"), filepath.Join(root, "fragment.toml")); err != nil {
		t.Fatal(err)
	}
	writeReaderPolicy(t, filepath.Join(root, "policy.toml"), "version = 1\nincludes = [\"fragment.toml\"]\n")
	if _, _, err := loadPolicyBundle(Path(filepath.Join(root, "policy.toml"))); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("load error = %v", err)
	}
}

func TestLoadPolicyBundleRejectsInvalidUTF8(t *testing.T) {
	for _, include := range []bool{false, true} {
		dir, path := t.TempDir(), ""
		path = filepath.Join(dir, "policy.toml")
		content := string([]byte{'\xff'})
		if include {
			content = "version = 1\nincludes = [\"fragment.toml\"]\n"
			writeReaderPolicy(t, filepath.Join(dir, "fragment.toml"), string([]byte{'\xff'}))
		}
		writeReaderPolicy(t, path, content)
		if _, _, err := loadPolicyBundle(Path(path)); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
			t.Fatalf("load error = %v", err)
		}
	}
}

func writeReaderPolicy(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
