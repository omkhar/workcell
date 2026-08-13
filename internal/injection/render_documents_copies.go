// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/providerid"
	"golang.org/x/sys/unix"
)

// allowedCopyEntryKeys is the parser-accepted key set for a single `[[copies]]`
// entry, shared as a single source of truth between renderCopies and the
// schema-doc drift test.
var allowedCopyEntryKeys = mapKeysSet([]string{"source", "target", "classification", "providers", "modes"})

func renderDocuments(policy map[string]any, outputRoot, policyDir Path) (map[string]string, error) {
	raw := policy["documents"]
	if raw == nil {
		return map[string]string{}, nil
	}
	documents, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("documents must be a TOML table")
	}
	if err := validateAllowedKeys(documents, providerid.DocumentKeySet(), "documents"); err != nil {
		return nil, err
	}

	rendered := map[string]string{}
	for _, key := range providerid.DocumentKeys {
		relpath := path.Join("documents", key+".md")
		rawValue, ok := documents[key]
		if !ok || rawValue == nil {
			continue
		}
		source, err := validateSourcePath(rawValue, "documents."+key, policyDir)
		if err != nil {
			return nil, err
		}
		if err := ensureIsFile(source, fmt.Sprintf("documents.%s", key)); err != nil {
			return nil, err
		}
		if err := stageFile(source, outputRoot, relpath); err != nil {
			return nil, err
		}
		rendered[key] = relpath
	}
	return rendered, nil
}

func renderCopies(policy map[string]any, outputRoot, policyDir Path, agent, mode string) ([]map[string]any, error) {
	raw := policy["copies"]
	if raw == nil {
		return []map[string]any{}, nil
	}
	copies, ok := raw.([]any)
	if !ok {
		return nil, errors.New("copies must be a TOML array of tables")
	}
	rendered := make([]map[string]any, 0, len(copies))
	copyIndex := 0
	for _, rawEntry := range copies {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return nil, errors.New("each copies entry must be a table")
		}
		if err := validateAllowedKeys(entry, allowedCopyEntryKeys, "copies entry"); err != nil {
			return nil, err
		}
		ok, err := selectedFor(entry["providers"], agent, "copies.providers", supportedAgents)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ok, err = selectedFor(entry["modes"], mode, "copies.modes", supportedModes)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		sourceValue, err := validateSourcePath(entry["source"], "copies.source", policyDir)
		if err != nil {
			return nil, err
		}
		targetRaw, ok := entry["target"]
		if !ok {
			targetRaw = ""
		}
		target, err := validateContainerTarget(normalizeContainerTarget(fmt.Sprint(targetRaw)))
		if err != nil {
			return nil, err
		}
		classification, ok := entry["classification"].(string)
		if !ok {
			return nil, errors.New("copies.classification is required")
		}
		kind := "file"
		relpath := fmt.Sprintf("copies/%d", copyIndex)
		mountPath := directMountRoot + "/copies/" + strconv.Itoa(copyIndex)
		copyIndex++
		fileMode, dirMode, err := classificationModes(classification)
		if err != nil {
			return nil, err
		}

		var renderedSource any
		if classification == "secret" {
			if err := validateSecretTree(sourceValue, "copies.source"); err != nil {
				return nil, err
			}
			kind = "file"
			if sourceValue.IsDir() {
				kind = "dir"
			}
			renderedSource = directMountEntry(sourceValue, mountPath)
		} else {
			kind, err = copySource(sourceValue, outputRoot.Join(relpath))
			if err != nil {
				return nil, err
			}
			renderedSource = relpath
		}

		rendered = append(rendered, map[string]any{
			"source":         renderedSource,
			"target":         target,
			"kind":           kind,
			"file_mode":      fileMode,
			"dir_mode":       dirMode,
			"classification": classification,
		})
	}
	return rendered, nil
}

func copySource(source, destination Path) (string, error) {
	sourceFile, _, kind, err := openDirectMountSource(source.String())
	if err != nil {
		return "", err
	}
	defer sourceFile.Close()
	if kind == directMountSourceDir {
		if err := os.MkdirAll(destination.Parent().String(), 0o755); err != nil {
			return "", err
		}
		if err := os.Mkdir(destination.String(), 0o700); err != nil {
			return "", err
		}
		destinationRoot, err := os.OpenRoot(destination.String())
		if err != nil {
			return "", err
		}
		defer destinationRoot.Close()
		if err := copyOpenDirectoryToRoot(sourceFile, destinationRoot, source.String(), ".", openDirectMountChild); err != nil {
			return "", err
		}
		if err := os.Chmod(destination.String(), 0o700); err != nil {
			return "", err
		}
		return "dir", nil
	}
	if kind != directMountSourceRegular {
		return "", fmt.Errorf("injection source must be a file or directory: %s", source)
	}
	if err := os.MkdirAll(destination.Parent().String(), 0o755); err != nil {
		return "", err
	}
	parentRoot, err := os.OpenRoot(destination.Parent().String())
	if err != nil {
		return "", err
	}
	defer parentRoot.Close()
	data, err := io.ReadAll(sourceFile)
	if err != nil {
		return "", err
	}
	if err := writeFileExclusive(parentRoot, destination.Base(), data, 0o600); err != nil {
		return "", err
	}
	if err := parentRoot.Chmod(destination.Base(), 0o600); err != nil {
		return "", err
	}
	return "file", nil
}

// copyOpenDirectoryToRoot copies an already-open source directory. Each
// descendant is opened from its parent descriptor without following links.
func copyOpenDirectoryToRoot(
	source *os.File,
	destination *os.Root,
	sourceDisplay, relative string,
	openChild func(*os.File, string, string) (*os.File, os.FileMode, directMountSourceKind, error),
) error {
	return copyOpenDirectoryToRootWithState(source, destination, sourceDisplay, relative, openChild, newInjectionDestinationState())
}

func copyOpenDirectoryToRootWithState(
	source *os.File,
	destination *os.Root,
	sourceDisplay, relative string,
	openChild func(*os.File, string, string) (*os.File, os.FileMode, directMountSourceKind, error),
	state *injectionDestinationState,
) error {
	entries, err := source.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		displayPath := filepath.Join(sourceDisplay, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("injection source must not contain a symbolic link: %s", displayPath)
		}
		child, _, kind, err := openChild(source, entry.Name(), displayPath)
		if err != nil {
			if errors.Is(err, unix.ELOOP) {
				return fmt.Errorf("injection source must not contain a symbolic link: %s", displayPath)
			}
			return fmt.Errorf("open injection source entry %s: %w", displayPath, err)
		}
		childRelative := filepath.Join(relative, entry.Name())
		switch kind {
		case directMountSourceDir:
			err = state.reserve(childRelative, "directory")
			if err == nil {
				err = destination.Mkdir(childRelative, 0o700)
			}
			if err == nil {
				err = copyOpenDirectoryToRootWithState(child, destination, displayPath, childRelative, openChild, state)
			}
		case directMountSourceRegular:
			var data []byte
			data, err = io.ReadAll(child)
			if err == nil {
				err = state.reserve(childRelative, "regular file")
			}
			if err == nil {
				err = writeFileExclusive(destination, childRelative, data, 0o600)
			}
		default:
			err = fmt.Errorf("injection source contains an unsupported entry: %s", displayPath)
		}
		closeErr := child.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// injectionDestinationState reserves each target path before it is created.
// The key is case-insensitive because the standard Apple filesystem treats
// `A` and `a` as the same destination. Unicode normalization is separate work.
type injectionDestinationState struct {
	paths map[string]string
}

func newInjectionDestinationState() *injectionDestinationState {
	return &injectionDestinationState{paths: make(map[string]string)}
}

func (s *injectionDestinationState) reserve(path, kind string) error {
	key := strings.ToLower(filepath.Clean(path))
	if previous, exists := s.paths[key]; exists {
		return fmt.Errorf("injection destination path collision between %s and %s", previous, path)
	}
	s.paths[key] = fmt.Sprintf("%s (%s)", path, kind)
	return nil
}

func writeFileExclusive(root *os.Root, path string, data []byte, perm os.FileMode) (retErr error) {
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()
	written, err := file.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func stageFile(source, outputRoot Path, relpath string) error {
	root, err := os.OpenRoot(outputRoot.String())
	if err != nil {
		return err
	}
	defer root.Close()
	relpath = filepath.Clean(relpath)
	if parent := filepath.Dir(relpath); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	sourceFile, _, kind, err := openDirectMountSource(source.String())
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	if kind != directMountSourceRegular {
		return fmt.Errorf("injection source must be a file: %s", source)
	}
	data, err := io.ReadAll(sourceFile)
	if err != nil {
		return err
	}
	if err := writeFileExclusive(root, relpath, data, 0o600); err != nil {
		return err
	}
	return root.Chmod(relpath, 0o600)
}

func validateContainerTarget(candidate string) (string, error) {
	if err := validateManifestPathField(candidate, "injection target"); err != nil {
		return "", err
	}
	if containsParentPathSegment(candidate) {
		return "", fmt.Errorf("injection target must not contain parent path segments: %s", candidate)
	}
	if !targetIsUnder(candidate, sessionHomeRoot) && !targetIsUnder(candidate, runInjectedRoot) {
		return "", fmt.Errorf("injection target must stay under /state/agent-home or /state/injected: %s", candidate)
	}
	if targetIsReserved(candidate) {
		return "", fmt.Errorf("injection target collides with a Workcell-managed control-plane path: %s", candidate)
	}
	return candidate, nil
}

func normalizeContainerTarget(raw string) string {
	if strings.HasPrefix(raw, "~/") {
		raw = sessionHomeRoot + "/" + raw[2:]
	}
	if containsParentPathSegment(raw) {
		return raw
	}
	candidate := path.Clean(raw)
	if !path.IsAbs(candidate) {
		return raw
	}
	return candidate
}

func containsParentPathSegment(candidate string) bool {
	for _, segment := range strings.Split(candidate, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func targetIsUnder(candidate, root string) bool {
	candidate = path.Clean(candidate)
	root = path.Clean(root)
	return candidate == root || strings.HasPrefix(candidate, root+"/")
}

func targetIsReserved(candidate string) bool {
	candidate = path.Clean(candidate)
	for _, reserved := range reservedTargets {
		if candidate == reserved || strings.HasPrefix(candidate, reserved+"/") {
			return true
		}
	}
	return false
}

func classificationModes(classification string) (string, string, error) {
	if _, ok := supportedClassifications[classification]; !ok {
		return "", "", fmt.Errorf("unsupported injection classification: %s", classification)
	}
	if classification == "secret" {
		return "0600", "0700", nil
	}
	return "0644", "0755", nil
}
