// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/pathutil"
)

func TestValidateInjectionDescendantNameRejectsInvalidUTF8WithoutDisclosure(t *testing.T) {
	invalid := "secret-prefix-" + string([]byte{0xff})
	err := validateInjectionDescendantName(invalid)
	if !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("entry-name error = %v", err)
	}
}

func TestCopySourceRejectsUnsafeDescendantBeforeReadOnlyDestination(t *testing.T) {
	for _, testCase := range unsafeInjectionDescendantNames {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, testCase.value), []byte("approved"), 0o600); err != nil {
				t.Fatal(err)
			}
			destinationParent := filepath.Join(root, "read-only")
			if err := os.Mkdir(destinationParent, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(destinationParent, 0o700) })
			_, err := copySource(Path(source), Path(filepath.Join(destinationParent, "output")))
			assertUnsafeInjectionDescendantError(t, err)
		})
	}
}

var unsafeInjectionDescendantNames = []struct {
	name  string
	value string
}{
	{name: "newline", value: "secret-prefix-\n"},
	{name: "escape", value: "secret-prefix-\x1b"},
	{name: "line separator", value: "secret-prefix-\u2028"},
	{name: "paragraph separator", value: "secret-prefix-\u2029"},
}

func assertUnsafeInjectionDescendantError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, pathutil.ErrUnsafePathControl) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("unsafe descendant error = %v", err)
	}
}

func TestCopyOpenDirectoryToRootWithStateRejectsLinuxInvalidUTF8Symlink(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux permits invalid UTF-8 descendant names")
	}
	sourcePath := t.TempDir()
	invalid := "secret-prefix-" + string([]byte{0xff})
	if err := os.Symlink("target", filepath.Join(sourcePath, invalid)); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destinationPath := t.TempDir()
	destination, err := os.OpenRoot(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	err = copyOpenDirectoryToRootWithState(source, destination, sourcePath, ".", openDirectMountChild, newInjectionDestinationState())
	if !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("copy error = %v", err)
	}
	entries, err := os.ReadDir(destinationPath)
	if err != nil || len(entries) != 0 {
		t.Fatalf("destination entries = %#v, %v", entries, err)
	}
}

func TestInjectionDestinationStateRejectsUnsafeControlNames(t *testing.T) {
	for _, testCase := range unsafeInjectionDescendantNames {
		t.Run(testCase.name, func(t *testing.T) {
			assertUnsafeInjectionDescendantError(t, newInjectionDestinationState().reserve(testCase.value, "regular file"))
		})
	}
}

func TestCopyOpenDirectoryToRootWithStateRejectsPreReservedUnicodeAlias(t *testing.T) {
	sourceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceRoot, "cafe\u0301"), []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destinationPath := t.TempDir()
	destination, err := os.OpenRoot(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	state := newInjectionDestinationState()
	if err := state.reserve("café", "reserved"); err != nil {
		t.Fatal(err)
	}
	err = copyOpenDirectoryToRootWithState(source, destination, sourceRoot, ".", openDirectMountChild, state)
	if err == nil || !strings.Contains(err.Error(), "destination path collision") {
		t.Fatalf("copy error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destinationPath, "cafe\u0301")); !os.IsNotExist(err) {
		t.Fatalf("destination was written: %v", err)
	}
}

func TestCopyOpenDirectoryToRootWithStateRejectsInvalidUTF8(t *testing.T) {
	sourceRoot := t.TempDir()
	invalid := "secret-prefix-" + string([]byte{0xff})
	if err := os.WriteFile(filepath.Join(sourceRoot, invalid), []byte("approved"), 0o600); err != nil {
		err = newInjectionDestinationState().reserve(invalid, "regular file")
		if !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
			t.Fatalf("reservation error = %v", err)
		}
		return
	}
	source, err := os.Open(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destinationPath := t.TempDir()
	destination, err := os.OpenRoot(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	err = copyOpenDirectoryToRootWithState(source, destination, sourceRoot, "target", openDirectMountChild, newInjectionDestinationState())
	if !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("copy error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destinationPath, "target", invalid)); !os.IsNotExist(err) {
		t.Fatalf("destination was written: %v", err)
	}
}

func TestStageFileRejectsValidatedSourcePathReplacement(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(source, []byte("approved"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(secret, []byte("host secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	validated, err := validateSourcePath(source, "documents.common", Path(root))
	if err != nil {
		t.Fatalf("validate source: %v", err)
	}
	if err := os.Rename(source, source+".original"); err != nil {
		t.Fatalf("rename source: %v", err)
	}
	if err := os.Symlink(secret, source); err != nil {
		t.Skipf("symlink is not available: %v", err)
	}

	output := t.TempDir()
	err = stageFile(validated, Path(output), "documents/common.md")
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("stageFile error = %v, want symbolic-link rejection", err)
	}
	if _, statErr := os.Lstat(filepath.Join(output, "documents", "common.md")); !os.IsNotExist(statErr) {
		t.Fatalf("stageFile created output after source replacement, stat error = %v", statErr)
	}
}

func TestCopySourceRejectsValidatedSourcePathReplacement(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	secret := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(source, []byte("approved"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(secret, []byte("host secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	validated, err := validateSourcePath(source, "copies.source", Path(root))
	if err != nil {
		t.Fatalf("validate source: %v", err)
	}
	if err := os.Rename(source, source+".original"); err != nil {
		t.Fatalf("rename source: %v", err)
	}
	if err := os.Symlink(secret, source); err != nil {
		t.Skipf("symlink is not available: %v", err)
	}

	output := filepath.Join(root, "output")
	_, err = copySource(validated, Path(output))
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("copySource error = %v, want symbolic-link rejection", err)
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("copySource created output after source replacement, stat error = %v", statErr)
	}
}

func TestCopySourceRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	destination := filepath.Join(root, "destination.txt")
	if err := os.WriteFile(source, []byte("approved"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(destination, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	if _, err := copySource(Path(source), Path(destination)); err == nil {
		t.Fatal("copySource accepted an existing destination")
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(data) != "preserve" {
		t.Fatalf("destination content = %q, want preserve", data)
	}
}

func TestStageDirectMountEntryRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	destination := filepath.Join(root, "staged.txt")
	if err := os.WriteFile(source, []byte("approved"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(destination, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write destination: %v", err)
	}
	if err := stageDirectMountEntry(source, destination); err == nil {
		t.Fatal("stageDirectMountEntry accepted an existing destination")
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(data) != "preserve" {
		t.Fatalf("destination content = %q, want preserve", data)
	}
}

func TestCopyOpenDirectoryRejectsChildReplacementAfterReadDir(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	childPath := filepath.Join(sourcePath, "child.txt")
	secretPath := filepath.Join(root, "secret.txt")
	if err := os.Mkdir(sourcePath, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(childPath, []byte("approved"), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if err := os.WriteFile(secretPath, []byte("host secret"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	source, _, kind, err := openDirectMountSource(sourcePath)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer source.Close()
	if kind != directMountSourceDir {
		t.Fatalf("source kind = %v, want directory", kind)
	}
	outputPath := filepath.Join(root, "output")
	if err := os.Mkdir(outputPath, 0o700); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	destination, err := os.OpenRoot(outputPath)
	if err != nil {
		t.Fatalf("open output root: %v", err)
	}
	defer destination.Close()

	swapped := false
	openChild := func(parent *os.File, name, displayPath string) (*os.File, os.FileMode, directMountSourceKind, error) {
		if !swapped {
			swapped = true
			if err := os.Rename(childPath, childPath+".original"); err != nil {
				t.Fatalf("rename child: %v", err)
			}
			if err := os.Symlink(secretPath, childPath); err != nil {
				t.Skipf("symlink is not available: %v", err)
			}
		}
		return openDirectMountChild(parent, name, displayPath)
	}

	err = copyOpenDirectoryToRoot(source, destination, sourcePath, ".", openChild)
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("copy directory error = %v, want symbolic-link rejection", err)
	}
	if _, statErr := os.Lstat(filepath.Join(outputPath, "child.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("copy directory created raced child, stat error = %v", statErr)
	}
}

type manifestFramingValue struct {
	name string
	raw  string
}

var manifestFramingValues = []manifestFramingValue{
	{name: "newline", raw: `\n`},
	{name: "unit separator", raw: `\x1f`},
	{name: "line separator", raw: `\u2028`},
	{name: "invalid UTF-8", raw: string([]byte{0xff})},
}

func assertRejectedBundleIsEmpty(t *testing.T, output string) {
	t.Helper()
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatalf("read rejected bundle: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("rejected bundle left partial output: %v", names)
	}
}

func TestRunRenderInjectionBundleRejectsFramedCopyTargets(t *testing.T) {
	for _, framing := range manifestFramingValues {
		t.Run(framing.name, func(t *testing.T) {
			root := t.TempDir()
			writeText(t, filepath.Join(root, "copy.txt"), "approved\n", 0o600)
			policyPath := filepath.Join(root, "policy.toml")
			output := filepath.Join(root, "bundle")
			writeText(t, policyPath, strings.Join([]string{
				"version = 1",
				"[[copies]]",
				`source = "copy.txt"`,
				`target = "/state/injected/value` + framing.raw + `next"`,
				`classification = "public"`,
			}, "\n"), 0o600)

			if err := RunRenderInjectionBundle(policyPath, "codex", "strict", output, ""); err == nil {
				t.Fatal("RunRenderInjectionBundle accepted a framed target")
			}
			assertRejectedBundleIsEmpty(t, output)
		})
	}
}

func TestRunRenderInjectionBundleRejectsFramedDirectMountSources(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy []string
	}{
		{
			name: "secret copy",
			policy: []string{
				"[[copies]]",
				`target = "/state/injected/secret"`,
				`classification = "secret"`,
			},
		},
		{
			name: "ssh config",
			policy: []string{
				"[ssh]",
			},
		},
		{
			name: "ssh known hosts",
			policy: []string{
				"[ssh]",
			},
		},
		{
			name: "ssh identity",
			policy: []string{
				"[ssh]",
			},
		},
	} {
		for _, framing := range manifestFramingValues {
			t.Run(test.name+"/"+framing.name, func(t *testing.T) {
				root := t.TempDir()
				policyPath := filepath.Join(root, "policy.toml")
				output := filepath.Join(root, "bundle")
				policy := append([]string{"version = 1"}, test.policy...)
				source := `"unsafe` + framing.raw + `parent/source"`
				switch test.name {
				case "secret copy":
					policy = append(policy[:2], append([]string{`source = ` + source}, policy[2:]...)...)
				case "ssh config":
					policy = append(policy, `config = `+source)
				case "ssh known hosts":
					policy = append(policy, `known_hosts = `+source)
				case "ssh identity":
					policy = append(policy, `identities = [`+source+`]`)
				}
				writeText(t, policyPath, strings.Join(policy, "\n"), 0o600)

				if err := RunRenderInjectionBundle(policyPath, "codex", "strict", output, ""); err == nil {
					t.Fatal("RunRenderInjectionBundle accepted a framed source")
				}
				assertRejectedBundleIsEmpty(t, output)
			})
		}
	}
}

func TestRunRenderInjectionBundleRejectsDelimitedSourceParentAfterExpansion(t *testing.T) {
	root := t.TempDir()
	policyRoot := filepath.Join(root, "unsafe\x1fparent")
	writeText(t, filepath.Join(policyRoot, "source"), "secret\n", 0o600)
	policyPath := filepath.Join(policyRoot, "policy.toml")
	output := filepath.Join(root, "bundle")
	writeText(t, policyPath, strings.Join([]string{
		"version = 1",
		"[[copies]]",
		`source = "source"`,
		`target = "/state/injected/secret"`,
		`classification = "secret"`,
	}, "\n"), 0o600)

	err := RunRenderInjectionBundle(policyPath, "codex", "strict", output, "")
	if err == nil {
		t.Fatal("RunRenderInjectionBundle accepted an expanded framed source")
	}
	assertRejectedBundleIsEmpty(t, output)
}

func TestInjectionDestinationStateRejectsCaseInsensitiveCollision(t *testing.T) {
	state := newInjectionDestinationState()
	if err := state.reserve("A", "directory"); err != nil {
		t.Fatalf("reserve first destination: %v", err)
	}
	if err := state.reserve("a", "regular file"); err == nil {
		t.Fatal("case-colliding destination was accepted")
	}
}

func TestRunRenderInjectionBundleRejectsCaseInsensitivePublicCopyCollision(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.Mkdir(filepath.Join(source, "A"), 0o755); err != nil {
		t.Fatalf("mkdir A: %v", err)
	}
	if err := os.Mkdir(filepath.Join(source, "a"), 0o755); err != nil {
		t.Skipf("filesystem does not permit distinct case aliases: %v", err)
	}
	policyPath := filepath.Join(root, "policy.toml")
	output := filepath.Join(root, "bundle")
	writeText(t, policyPath, strings.Join([]string{
		"version = 1",
		"[[copies]]",
		`source = "source"`,
		`target = "/state/injected/source"`,
		`classification = "public"`,
	}, "\n"), 0o600)

	err := RunRenderInjectionBundle(policyPath, "codex", "strict", output, "")
	if err == nil || !strings.Contains(err.Error(), "destination path collision") {
		t.Fatalf("RunRenderInjectionBundle error = %v, want destination collision", err)
	}
	if _, statErr := os.Lstat(filepath.Join(output, "manifest.json")); !os.IsNotExist(statErr) {
		t.Fatalf("manifest exists after public-copy collision: %v", statErr)
	}
}

func TestStageDirectMountsRejectsCaseInsensitiveDestinationCollision(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.Mkdir(filepath.Join(source, "A"), 0o755); err != nil {
		t.Fatalf("mkdir A: %v", err)
	}
	if err := os.Mkdir(filepath.Join(source, "a"), 0o755); err != nil {
		t.Skipf("filesystem does not permit distinct case aliases: %v", err)
	}
	specPath := filepath.Join(root, "mounts.json")
	writeMountSpec(t, specPath, []map[string]any{{
		"source": source, "mount_path": "/opt/workcell/host-inputs/source",
	}})

	_, err := StageDirectMounts(filepath.Join(root, "bundle"), specPath)
	if err == nil || !strings.Contains(err.Error(), "destination path collision") {
		t.Fatalf("StageDirectMounts error = %v, want destination collision", err)
	}
}

func TestInjectionDestinationStateRejectsUnicodeAliases(t *testing.T) {
	state := newInjectionDestinationState()
	if err := state.reserve("credentials/café", "directory"); err != nil {
		t.Fatal(err)
	}
	if err := state.reserve("credentials/cafe\u0301", "regular file"); err == nil {
		t.Fatal("NFC collision accepted")
	}
	if err := state.reserve("credentials/straße", "directory"); err != nil {
		t.Fatal(err)
	}
	if err := state.reserve("credentials/STRASSE", "regular file"); err == nil {
		t.Fatal("case-fold collision accepted")
	}
}

func TestRenderSSHAcceptsSafeIdentityBasename(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(root, "id_workcell-safe")
	if err := os.WriteFile(identity, []byte("private key"), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	ssh, err := renderSSH(map[string]any{
		"ssh": map[string]any{"identities": []any{identity}},
	}, Path(t.TempDir()), Path(root), "codex", "strict")
	if err != nil {
		t.Fatalf("renderSSH: %v", err)
	}
	identities := ssh["identities"].([]map[string]any)
	if len(identities) != 1 || identities[0]["target_name"] != "id_workcell-safe" {
		t.Fatalf("rendered identities = %#v", identities)
	}
}

func TestRenderSSHRejectsUnsafeIdentityBasename(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(root, "id\nunsafe")
	if err := os.WriteFile(identity, []byte("private key"), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	_, err := renderSSH(map[string]any{
		"ssh": map[string]any{"identities": []any{identity}},
	}, Path(t.TempDir()), Path(root), "codex", "strict")
	if err == nil || !strings.Contains(err.Error(), "control or line-separator") {
		t.Fatalf("renderSSH error = %v, want manifest field rejection", err)
	}
}
