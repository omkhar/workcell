// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/injectionpolicy"
	"github.com/omkhar/workcell/internal/rootio"
	"golang.org/x/sys/unix"
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

func TestLoadPolicyMetadataOverrideUsesBoundedNoFollowRead(t *testing.T) {
	root := t.TempDir()
	valid := []byte(`{"policy_entrypoint":"policy.toml","policy_sources":[{"path":"policy.toml","sha256":"sha256:original"}]}`)
	exact := append(valid, bytes.Repeat([]byte{' '}, int(rootio.MaxManifestBytes)-len(valid))...)
	exactPath := filepath.Join(root, "exact.json")
	if err := os.WriteFile(exactPath, exact, 0o600); err != nil {
		t.Fatal(err)
	}
	if entrypoint, sources, err := loadPolicyMetadataOverride(exactPath); err != nil || entrypoint != "policy.toml" || len(sources) != 1 {
		t.Fatalf("loadPolicyMetadataOverride exact limit = %q, %#v, %v", entrypoint, sources, err)
	}
	overLimitPath := filepath.Join(root, "over-limit.json")
	if err := os.WriteFile(overLimitPath, append(exact, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadPolicyMetadataOverride(overLimitPath); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("loadPolicyMetadataOverride over-limit error = %v", err)
	}
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	leafLink := filepath.Join(root, "leaf-link.json")
	if err := os.Symlink(filepath.Base(target), leafLink); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if _, _, err := loadPolicyMetadataOverride(leafLink); err == nil {
		t.Fatal("loadPolicyMetadataOverride accepted a leaf symlink")
	}

	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realParent, "metadata.json"), valid, 0o600); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked-parent")
	if err := os.Symlink(filepath.Base(realParent), linkedParent); err != nil {
		t.Skipf("os.Symlink unavailable: %v", err)
	}
	if _, _, err := loadPolicyMetadataOverride(filepath.Join(linkedParent, "metadata.json")); err == nil {
		t.Fatal("loadPolicyMetadataOverride accepted a symlinked parent")
	}

	fifo := filepath.Join(root, "metadata.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("unix.Mkfifo unavailable: %v", err)
	}
	if _, _, err := loadPolicyMetadataOverride(fifo); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("loadPolicyMetadataOverride FIFO error = %v", err)
	}
}

func writeReaderPolicy(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
