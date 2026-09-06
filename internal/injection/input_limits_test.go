// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/runtimeutil"
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
		entries := make([]map[string]any, runtimeutil.MaxDirectMountEntries)
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
		entries := make([]map[string]any, runtimeutil.MaxDirectMountEntries+1)
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
			_, err := pathMaterialSHA256(path, newInjectionTreeBudget())
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

// The pre-copy validation walk reads its own listing, so it has to stop at the
// entry allowance instead of materialising every entry for the copy to refuse.
func TestValidateInjectionDirectoryDescendantsRejectsAggregateTreeEntryLimit(t *testing.T) {
	root := t.TempDir()
	for index := 0; index <= maxInjectionTreeEntries; index++ {
		writeSparseInjectionFile(t, filepath.Join(root, "file-"+strconv.Itoa(index)), 0)
	}
	source, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	err = validateInjectionDirectoryDescendants(source, root, newInjectionTreeBudget())
	if err == nil || !strings.Contains(err.Error(), "aggregate entry limit") {
		t.Fatalf("validateInjectionDirectoryDescendants error = %v, want aggregate entry limit", err)
	}
}

// A pseudo-file reports an undersized st_size, so the copy is the only place
// that can charge and bound the bytes it actually reads.
func TestCopyOpenFileWithModeBoundsUndersizedReportedSize(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs a pseudo-file that under-reports st_size")
	}
	open := func(t *testing.T) *os.File {
		t.Helper()
		file, err := os.Open("/proc/" + strconv.Itoa(os.Getpid()) + "/status")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != 0 {
			t.Skipf("pseudo-file reports size %d, not an under-report", info.Size())
		}
		return file
	}

	t.Run("charges the copied bytes", func(t *testing.T) {
		destination := filepath.Join(t.TempDir(), "staged")
		budget := newInjectionTreeBudget()
		if err := copyOpenFileWithMode(open(t), destination, "status", 0o600, budget); err != nil {
			t.Fatalf("copyOpenFileWithMode: %v", err)
		}
		info, err := os.Stat(destination)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() == 0 || budget.bytes != info.Size() {
			t.Fatalf("budget charged %d bytes for a %d byte copy", budget.bytes, info.Size())
		}
	})

	t.Run("rejects and removes output past the allowance", func(t *testing.T) {
		destination := filepath.Join(t.TempDir(), "staged")
		budget := newInjectionTreeBudget()
		budget.bytes = maxInjectionTreeBytes - 8
		err := copyOpenFileWithMode(open(t), destination, "status", 0o600, budget)
		if err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("copyOpenFileWithMode error = %v, want a byte-limit error", err)
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			t.Fatalf("over-limit copy left output, lstat error = %v", err)
		}
	})
}

// The secret-tree symlink scan used to run as a separate unbounded walk ahead
// of the accounting, so it reached a symlink past the entry allowance. One
// bounded walk now stops at the allowance first.
func TestValidateSecretTreeBoundsTheWalkBeforeTheSymlinkScan(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "secret")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxInjectionTreeEntries; index++ {
		writeSparseInjectionFile(t, filepath.Join(source, "file-"+strconv.Itoa(index)), 0)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}

	err := validateSecretTree(Path(source), "copies.source")
	if err == nil || !strings.Contains(err.Error(), "aggregate entry limit") {
		t.Fatalf("validateSecretTree error = %v, want aggregate entry limit", err)
	}
}

// The directory fingerprint reads host material that can change after
// validation, so it enforces the limits itself.
func TestPathMaterialSHA256BoundsDirectoryMaterial(t *testing.T) {
	t.Run("per-file limit", func(t *testing.T) {
		source := t.TempDir()
		writeSparseInjectionFile(t, filepath.Join(source, "oversize"), maxInjectionFileBytes+1)
		if _, err := pathMaterialSHA256(Path(source), newInjectionTreeBudget()); err == nil || !strings.Contains(err.Error(), "per-file limit") {
			t.Fatalf("pathMaterialSHA256 error = %v, want per-file limit", err)
		}
	})

	t.Run("aggregate entry limit", func(t *testing.T) {
		source := t.TempDir()
		for index := 0; index <= maxInjectionTreeEntries; index++ {
			writeSparseInjectionFile(t, filepath.Join(source, "file-"+strconv.Itoa(index)), 0)
		}
		if _, err := pathMaterialSHA256(Path(source), newInjectionTreeBudget()); err == nil || !strings.Contains(err.Error(), "aggregate entry limit") {
			t.Fatalf("pathMaterialSHA256 error = %v, want aggregate entry limit", err)
		}
	})

	t.Run("charges every hashed root", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "input")
		writeSparseInjectionFile(t, source, 0)

		budget := newInjectionTreeBudget()
		budget.entries = maxInjectionTreeEntries
		if _, err := pathMaterialSHA256(Path(source), budget); err == nil || !strings.Contains(err.Error(), "aggregate entry limit") {
			t.Fatalf("pathMaterialSHA256 error = %v, want aggregate entry limit", err)
		}
	})

	t.Run("shares one budget across hashed paths", func(t *testing.T) {
		root := t.TempDir()
		first := filepath.Join(root, "first")
		writeSparseInjectionFile(t, first, maxInjectionFileBytes)
		second := filepath.Join(root, "second")
		writeSparseInjectionFile(t, second, 1)

		budget := newInjectionTreeBudget()
		budget.bytes = maxInjectionTreeBytes - maxInjectionFileBytes
		if _, err := pathMaterialSHA256(Path(first), budget); err != nil {
			t.Fatalf("first hash: %v", err)
		}
		if _, err := pathMaterialSHA256(Path(second), budget); err == nil || !strings.Contains(err.Error(), "aggregate tree limit") {
			t.Fatalf("second hash error = %v, want aggregate tree limit", err)
		}
	})

	t.Run("accepts a tree inside the limits", func(t *testing.T) {
		source := t.TempDir()
		if err := os.Mkdir(filepath.Join(source, "nested"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "nested", "file"), []byte("material"), 0o600); err != nil {
			t.Fatal(err)
		}
		sum, err := pathMaterialSHA256(Path(source), newInjectionTreeBudget())
		if err != nil || sum == "" {
			t.Fatalf("pathMaterialSHA256 = %q, %v", sum, err)
		}
	})
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
