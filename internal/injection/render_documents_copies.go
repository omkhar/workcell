// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/providerid"
)

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
		if err := validateAllowedKeys(entry, mapKeysSet([]string{"source", "target", "classification", "providers", "modes"}), "copies entry"); err != nil {
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
	info, err := os.Stat(source.String())
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		if err := ensureNoSymlinksWithin(source); err != nil {
			return "", err
		}
		if err := os.MkdirAll(destination.String(), 0o700); err != nil {
			return "", err
		}
		destinationRoot, err := os.OpenRoot(destination.String())
		if err != nil {
			return "", err
		}
		defer destinationRoot.Close()
		if err := filepath.Walk(source.String(), func(current string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(source.String(), current)
			if err != nil {
				return err
			}
			if rel == "." {
				return nil
			}
			rel = filepath.Clean(rel)
			if info.IsDir() {
				if err := destinationRoot.MkdirAll(rel, 0o755); err != nil {
					return err
				}
				return destinationRoot.Chmod(rel, 0o700)
			}
			data, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			if parent := filepath.Dir(rel); parent != "." {
				if err := destinationRoot.MkdirAll(parent, 0o755); err != nil {
					return err
				}
			}
			if err := destinationRoot.WriteFile(rel, data, 0o600); err != nil {
				return err
			}
			return destinationRoot.Chmod(rel, 0o600)
		}); err != nil {
			return "", err
		}
		if err := os.Chmod(destination.String(), 0o700); err != nil {
			return "", err
		}
		return "dir", nil
	}
	if !info.Mode().IsRegular() {
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
	data, err := os.ReadFile(source.String())
	if err != nil {
		return "", err
	}
	if err := parentRoot.WriteFile(destination.Base(), data, 0o600); err != nil {
		return "", err
	}
	if err := parentRoot.Chmod(destination.Base(), 0o600); err != nil {
		return "", err
	}
	return "file", nil
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
	data, err := os.ReadFile(source.String())
	if err != nil {
		return err
	}
	if err := root.WriteFile(relpath, data, 0o600); err != nil {
		return err
	}
	return root.Chmod(relpath, 0o600)
}

func validateContainerTarget(candidate string) (string, error) {
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
