// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/hoststate"
)

func TestReadInjectionFileAllowsExactPerFileLimit(t *testing.T) {
	data, err := readInjectionFile(bytes.NewReader(make([]byte, maxInjectionFileBytes)), "exact", nil)
	if err != nil {
		t.Fatalf("readInjectionFile exact-limit error: %v", err)
	}
	if int64(len(data)) != maxInjectionFileBytes {
		t.Fatalf("readInjectionFile length = %d, want %d", len(data), maxInjectionFileBytes)
	}
}

func TestReadInjectionFileRejectsOverPerFileLimit(t *testing.T) {
	_, err := readInjectionFile(bytes.NewReader(make([]byte, maxInjectionFileBytes+1)), "oversize", nil)
	if err == nil || !strings.Contains(err.Error(), "per-file limit") {
		t.Fatalf("readInjectionFile error = %v, want per-file limit", err)
	}
}

func TestCopySourceAllowsExactAggregateTreeLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	// copySource charges one entry for the source directory itself, so the
	// aggregate byte allowance is what this case has to land on exactly.
	for index := int64(0); index < maxInjectionTreeBytes/maxInjectionFileBytes; index++ {
		writeSparseInjectionFile(t, filepath.Join(source, "file-"+strconv.FormatInt(index, 10)), maxInjectionFileBytes)
	}

	if _, err := copySource(Path(source), Path(filepath.Join(root, "output"))); err != nil {
		t.Fatalf("copySource exact aggregate limit: %v", err)
	}
}

func TestCopySourceRejectsAggregateTreeLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := int64(0); index < maxInjectionTreeBytes/maxInjectionFileBytes; index++ {
		writeSparseInjectionFile(t, filepath.Join(source, "file-"+strconv.FormatInt(index, 10)), maxInjectionFileBytes)
	}
	writeSparseInjectionFile(t, filepath.Join(source, "file-extra"), 1)

	_, err := copySource(Path(source), Path(filepath.Join(root, "output")))
	if err == nil || !strings.Contains(err.Error(), "aggregate tree limit") {
		t.Fatalf("copySource error = %v, want aggregate tree limit", err)
	}
}

func TestCopySourceRejectsAggregateTreeEntryLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxInjectionTreeEntries; index++ {
		writeSparseInjectionFile(t, filepath.Join(source, "file-"+strconv.Itoa(index)), 0)
	}

	_, err := copySource(Path(source), Path(filepath.Join(root, "output")))
	if err == nil || !strings.Contains(err.Error(), "aggregate entry limit") {
		t.Fatalf("copySource error = %v, want aggregate entry limit", err)
	}
}

func TestStageDirectMountsAllowsExactPerFileLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeSparseInjectionFile(t, source, maxInjectionFileBytes)
	spec := filepath.Join(root, "mounts.json")
	writeMountSpec(t, spec, []map[string]any{{
		"source":     source,
		"mount_path": "/opt/workcell/host-inputs/source",
	}})

	if _, err := StageDirectMounts(root, spec); err != nil {
		t.Fatalf("StageDirectMounts exact file limit: %v", err)
	}
	staged := filepath.Join(root, "direct-mounts", hoststate.DirectMountCacheKey(source, "/opt/workcell/host-inputs/source"))
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("stat staged file: %v", err)
	}
	if info.Size() != maxInjectionFileBytes {
		t.Fatalf("staged file size = %d, want %d", info.Size(), maxInjectionFileBytes)
	}
}

func TestStageDirectMountsRejectsOverPerFileLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeSparseInjectionFile(t, source, maxInjectionFileBytes+1)
	spec := filepath.Join(root, "mounts.json")
	mountPath := "/opt/workcell/host-inputs/source"
	writeMountSpec(t, spec, []map[string]any{{"source": source, "mount_path": mountPath}})

	if _, err := StageDirectMounts(root, spec); err == nil || !strings.Contains(err.Error(), "per-file limit") {
		t.Fatalf("StageDirectMounts error = %v, want per-file limit", err)
	}
	staged := filepath.Join(root, "direct-mounts", hoststate.DirectMountCacheKey(source, mountPath))
	if _, err := os.Lstat(staged); !os.IsNotExist(err) {
		t.Fatalf("oversize input left staged output, lstat error = %v", err)
	}
}

func TestStageDirectMountsRejectsAggregateTreeLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := int64(0); index < maxInjectionTreeBytes/maxInjectionFileBytes; index++ {
		writeSparseInjectionFile(t, filepath.Join(source, "file-"+strconv.FormatInt(index, 10)), maxInjectionFileBytes)
	}
	writeSparseInjectionFile(t, filepath.Join(source, "file-extra"), 1)
	spec := filepath.Join(root, "mounts.json")
	mountPath := "/opt/workcell/host-inputs/source"
	writeMountSpec(t, spec, []map[string]any{{"source": source, "mount_path": mountPath}})

	if _, err := StageDirectMounts(root, spec); err == nil || !strings.Contains(err.Error(), "aggregate tree limit") {
		t.Fatalf("StageDirectMounts error = %v, want aggregate tree limit", err)
	}
}

func TestStageDirectMountsSharesAggregateLimitAcrossMounts(t *testing.T) {
	root := t.TempDir()
	entries := make([]map[string]any, 0, 5)
	for index := 0; index < 5; index++ {
		source := filepath.Join(root, "source-"+strconv.Itoa(index))
		writeSparseInjectionFile(t, source, maxInjectionFileBytes)
		entries = append(entries, map[string]any{
			"source":     source,
			"mount_path": "/opt/workcell/host-inputs/source-" + strconv.Itoa(index),
		})
	}
	spec := filepath.Join(root, "mounts.json")
	writeMountSpec(t, spec, entries)
	if _, err := StageDirectMounts(root, spec); err == nil || !strings.Contains(err.Error(), "aggregate tree limit") {
		t.Fatalf("StageDirectMounts error = %v, want aggregate tree limit across mounts", err)
	}
}

func TestStageDirectMountsRejectsAggregateTreeEntryLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxInjectionTreeEntries; index++ {
		writeSparseInjectionFile(t, filepath.Join(source, "file-"+strconv.Itoa(index)), 0)
	}
	spec := filepath.Join(root, "mounts.json")
	writeMountSpec(t, spec, []map[string]any{{
		"source":     source,
		"mount_path": "/opt/workcell/host-inputs/source",
	}})

	if _, err := StageDirectMounts(root, spec); err == nil || !strings.Contains(err.Error(), "aggregate entry limit") {
		t.Fatalf("StageDirectMounts error = %v, want aggregate entry limit", err)
	}
}

func TestValidateSecretTreeAllowsExactPerFileLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "secret")
	writeSparseInjectionFile(t, source, maxInjectionFileBytes)
	if err := validateSecretTree(Path(source), "copies.source"); err != nil {
		t.Fatalf("validateSecretTree exact file limit: %v", err)
	}
}

func TestValidateSecretTreeRejectsOverPerFileLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "secret")
	writeSparseInjectionFile(t, source, maxInjectionFileBytes+1)
	if err := validateSecretTree(Path(source), "copies.source"); err == nil || !strings.Contains(err.Error(), "per-file limit") {
		t.Fatalf("validateSecretTree error = %v, want per-file limit", err)
	}
}

func TestValidateSecretTreeRejectsAggregateTreeEntryLimit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "secret")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxInjectionTreeEntries; index++ {
		writeSparseInjectionFile(t, filepath.Join(source, "file-"+strconv.Itoa(index)), 0)
	}
	if err := validateSecretTree(Path(source), "copies.source"); err == nil || !strings.Contains(err.Error(), "aggregate entry limit") {
		t.Fatalf("validateSecretTree error = %v, want aggregate entry limit", err)
	}
}

func TestRenderMaterialSharesBundleAggregateByteLimit(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "output")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 5)
	for index := range paths {
		paths[index] = filepath.Join(root, "input-"+strconv.Itoa(index))
		writeSparseInjectionFile(t, paths[index], maxInjectionFileBytes)
	}
	budget := newInjectionTreeBudget()
	if _, err := renderDocumentsWithBudget(map[string]any{
		"documents": map[string]any{"common": paths[0]},
	}, Path(output), Path(root), budget); err != nil {
		t.Fatalf("render document: %v", err)
	}
	if _, err := renderCopiesWithBudget(map[string]any{
		"copies": []any{map[string]any{
			"source": paths[1], "target": "/state/injected/copy", "classification": "public",
		}},
	}, Path(output), Path(root), "codex", "strict", budget); err != nil {
		t.Fatalf("render copy: %v", err)
	}
	if _, err := renderSSHWithBudget(map[string]any{
		"ssh": map[string]any{"enabled": true, "known_hosts": paths[2]},
	}, Path(output), Path(root), "codex", "strict", budget); err != nil {
		t.Fatalf("render ssh: %v", err)
	}
	if _, err := renderCredentialsWithBudget(map[string]any{
		"credentials": map[string]any{"codex_auth": paths[3]},
	}, Path(root), "codex", "strict", budget); err != nil {
		t.Fatalf("render credentials: %v", err)
	}
	if _, err := renderDocumentsWithBudget(map[string]any{
		"documents": map[string]any{"codex": paths[4]},
	}, Path(output), Path(root), budget); err == nil || !strings.Contains(err.Error(), "aggregate tree limit") {
		t.Fatalf("fifth selected item error = %v, want aggregate tree limit", err)
	}
}

func TestRenderMaterialSharesBundleAggregateEntryLimit(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "output")
	if err := os.MkdirAll(output, 0o700); err != nil {
		t.Fatal(err)
	}
	entries := make([]any, 0, maxInjectionTreeEntries+1)
	for index := 0; index <= maxInjectionTreeEntries; index++ {
		source := filepath.Join(root, "input-"+strconv.Itoa(index))
		writeSparseInjectionFile(t, source, 0)
		entries = append(entries, map[string]any{
			"source":         source,
			"target":         "/state/injected/item-" + strconv.Itoa(index),
			"classification": "public",
		})
	}
	if _, err := renderCopiesWithBudget(map[string]any{"copies": entries}, Path(output), Path(root), "codex", "strict", newInjectionTreeBudget()); err == nil || !strings.Contains(err.Error(), "aggregate entry limit") {
		t.Fatalf("many selected items error = %v, want aggregate entry limit", err)
	}
}

func TestStageDirectMountsBoundsMountSpecification(t *testing.T) {
	t.Run("exact bytes", func(t *testing.T) {
		root := t.TempDir()
		spec := filepath.Join(root, "mounts.json")
		data := append([]byte("[]"), bytes.Repeat([]byte{' '}, int(maxInjectionMountSpecBytes-2))...)
		if err := os.WriteFile(spec, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := StageDirectMounts(root, spec); err != nil {
			t.Fatalf("exact mount specification limit: %v", err)
		}
	})
	t.Run("over bytes", func(t *testing.T) {
		root := t.TempDir()
		spec := filepath.Join(root, "mounts.json")
		data := append([]byte("[]"), bytes.Repeat([]byte{' '}, int(maxInjectionMountSpecBytes-1))...)
		if err := os.WriteFile(spec, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := StageDirectMounts(root, spec); err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("over-size mount specification error = %v", err)
		}
	})
	t.Run("exact count", func(t *testing.T) {
		root := t.TempDir()
		spec := filepath.Join(root, "mounts.json")
		entries := make([]map[string]any, maxInjectionMounts)
		for index := range entries {
			entries[index] = map[string]any{}
		}
		writeMountSpec(t, spec, entries)
		if _, err := StageDirectMounts(root, spec); err != nil {
			t.Fatalf("exact mount count: %v", err)
		}
	})
	t.Run("over count", func(t *testing.T) {
		root := t.TempDir()
		spec := filepath.Join(root, "mounts.json")
		entries := make([]map[string]any, maxInjectionMounts+1)
		for index := range entries {
			entries[index] = map[string]any{}
		}
		writeMountSpec(t, spec, entries)
		if _, err := StageDirectMounts(root, spec); err == nil || !strings.Contains(err.Error(), "mount limit") {
			t.Fatalf("over-count mount specification error = %v", err)
		}
	})
	t.Run("path length", func(t *testing.T) {
		root := t.TempDir()
		spec := filepath.Join(root, "mounts.json")
		writeMountSpec(t, spec, []map[string]any{{
			"source":     strings.Repeat("a", maxInjectionMountPathBytes+1),
			"mount_path": "/opt/workcell/host-inputs/x",
		}})
		if _, err := StageDirectMounts(root, spec); err == nil || !strings.Contains(err.Error(), "length limit") {
			t.Fatalf("over-length mount path error = %v", err)
		}
	})
	t.Run("exact path length", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		writeSparseInjectionFile(t, source, 0)
		prefix := "/opt/workcell/host-inputs/"
		mountPath := prefix + strings.Repeat("x", maxInjectionMountPathBytes-len(prefix))
		spec := filepath.Join(root, "mounts.json")
		writeMountSpec(t, spec, []map[string]any{{"source": source, "mount_path": mountPath}})
		if _, err := StageDirectMounts(root, spec); err != nil {
			t.Fatalf("exact mount path length: %v", err)
		}
	})
}

func TestBoundedValidationReadersRejectOversize(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "oversize")
	writeSparseInjectionFile(t, source, maxInjectionFileBytes+1)

	tests := []struct {
		name string
		call func(Path) error
	}{
		{name: "gemini env", call: func(path Path) error {
			_, err := parseSimpleEnvFile(path)
			return err
		}},
		{name: "json", call: func(path Path) error {
			_, err := validateJSONObjFile(path, "oversize")
			return err
		}},
		{name: "ssh config", call: func(path Path) error {
			return validateSSHConfigSafety(path, false)
		}},
		{name: "material hash", call: func(path Path) error {
			_, err := pathMaterialSHA256(path)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(Path(source)); err == nil || !strings.Contains(err.Error(), "limit") {
				t.Fatalf("error = %v, want bounded-input error", err)
			}
		})
	}
}

func writeSparseInjectionFile(t *testing.T, path string, size int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if err := file.Truncate(size); err != nil {
		file.Close()
		t.Fatalf("truncate %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}
