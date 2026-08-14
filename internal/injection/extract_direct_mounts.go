// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/omkhar/workcell/internal/pathutil"
	"github.com/omkhar/workcell/internal/rootio"
)

// DirectMount is the JSON shape this binary emits for each direct mount.
type DirectMount struct {
	Source    string `json:"source"`
	MountPath string `json:"mount_path"`
}

// RequireDirectMount removes the host source from an entry and returns the direct mount record.
func RequireDirectMount(entry map[string]any, label string) (DirectMount, error) {
	source, _ := entry["source"].(string)
	delete(entry, "source")

	mountPath, _ := entry["mount_path"].(string)
	if source == "" {
		return DirectMount{}, fmt.Errorf("%s is missing its host source path", label)
	}
	if mountPath == "" {
		return DirectMount{}, fmt.Errorf("%s is missing its direct mount path", label)
	}
	return DirectMount{Source: source, MountPath: mountPath}, nil
}

// RunExtractDirectMounts reproduces the legacy extract_direct_mounts helper.
func RunExtractDirectMounts(manifestPath, mountSpecPath string) error {
	return runExtractDirectMounts(manifestPath, mountSpecPath, nil)
}

// runExtractDirectMounts retains the descriptor for the manifest parent from
// the read through the atomic replacement. beforeManifestWrite is a test seam
// for deterministic leaf mutation checks; production callers pass nil.
func runExtractDirectMounts(manifestPath, mountSpecPath string, beforeManifestWrite func() error) error {
	manifestInputPath, err := absoluteInputPath(manifestPath)
	if err != nil {
		return err
	}
	mountSpecOutputPath, err := absoluteInputPath(mountSpecPath)
	if err != nil {
		return err
	}

	manifestParent, manifestName, manifest, err := openJSONObjectForRewrite(manifestInputPath)
	if err != nil {
		return err
	}
	defer manifestParent.Close()

	directMounts, err := collectDirectMounts(manifest)
	if err != nil {
		return err
	}

	if err := writeIndentedJSONAtNoFollow(manifestParent, manifestName, manifest, 0o600, beforeManifestWrite); err != nil {
		return err
	}

	mountSpecParent, cleanedMountSpecPath, err := rootio.OpenParentDirectoryNoFollow(mountSpecOutputPath)
	if err != nil {
		return err
	}
	defer mountSpecParent.Close()
	if err := writeIndentedJSONAtNoFollow(mountSpecParent, filepath.Base(cleanedMountSpecPath), directMounts, 0o600, nil); err != nil {
		return err
	}
	return nil
}

func collectDirectMounts(manifest map[string]any) ([]DirectMount, error) {
	var directMounts []DirectMount

	if rawCredentials, ok := manifest["credentials"]; ok && rawCredentials != nil {
		credentials, err := asObjectMap(rawCredentials, "credentials")
		if err != nil {
			return nil, err
		}
		for _, key := range sortedKeys(credentials) {
			entry, err := asObjectMap(credentials[key], "credentials."+key)
			if err != nil {
				return nil, err
			}
			directMount, err := RequireDirectMount(entry, "credentials."+key)
			if err != nil {
				return nil, err
			}
			directMounts = append(directMounts, directMount)
		}
	}

	if rawCopies, ok := manifest["copies"]; ok && rawCopies != nil {
		copies, err := asArray(rawCopies, "copies")
		if err != nil {
			return nil, err
		}
		for index, rawEntry := range copies {
			entry, err := asObjectMap(rawEntry, fmt.Sprintf("copies[%d]", index))
			if err != nil {
				return nil, err
			}
			if source, ok := entry["source"].(map[string]any); ok {
				directMount, err := RequireDirectMount(source, fmt.Sprintf("copies[%d].source", index))
				if err != nil {
					return nil, err
				}
				directMounts = append(directMounts, directMount)
			}
		}
	}

	rawSSH, ok := manifest["ssh"]
	if !ok || rawSSH == nil {
		return sortDirectMounts(directMounts), nil
	}

	ssh, err := asObjectMap(rawSSH, "ssh")
	if err != nil {
		return nil, err
	}

	for _, key := range []string{"config", "known_hosts"} {
		if rawEntry, ok := ssh[key]; ok && rawEntry != nil {
			entry, err := asObjectMap(rawEntry, "ssh."+key)
			if err != nil {
				return nil, err
			}
			directMount, err := RequireDirectMount(entry, "ssh."+key)
			if err != nil {
				return nil, err
			}
			directMounts = append(directMounts, directMount)
		}
	}

	if rawIdentities, ok := ssh["identities"]; ok && rawIdentities != nil {
		identities, err := asArray(rawIdentities, "ssh.identities")
		if err != nil {
			return nil, err
		}
		for index, rawIdentity := range identities {
			entry, err := asObjectMap(rawIdentity, fmt.Sprintf("ssh.identities[%d]", index))
			if err != nil {
				return nil, err
			}
			if _, ok := entry["mount_path"]; ok {
				directMount, err := RequireDirectMount(entry, fmt.Sprintf("ssh.identities[%d]", index))
				if err != nil {
					return nil, err
				}
				directMounts = append(directMounts, directMount)
			}
		}
	}

	return sortDirectMounts(directMounts), nil
}

func sortDirectMounts(directMounts []DirectMount) []DirectMount {
	sort.SliceStable(directMounts, func(i, j int) bool {
		return directMounts[i].MountPath < directMounts[j].MountPath
	})
	return directMounts
}

func openJSONObjectForRewrite(path string) (*os.File, string, map[string]any, error) {
	parent, cleaned, err := rootio.OpenParentDirectoryNoFollow(path)
	if err != nil {
		return nil, "", nil, err
	}
	name := filepath.Base(cleaned)
	data, err := rootio.ReadFileAtNoFollow(parent, name, "injection manifest", maxInjectionManifestBytes)
	if err != nil {
		parent.Close()
		return nil, "", nil, err
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		parent.Close()
		return nil, "", nil, err
	}
	return parent, name, manifest, nil
}

func writeIndentedJSONAtNoFollow(parent *os.File, name string, value any, mode os.FileMode, beforeWrite func() error) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return err
		}
	}
	return rootio.WriteFileAtomicAtNoFollow(parent, name, data, mode, ".workcell-manifest-")
}

func absoluteInputPath(raw string) (string, error) {
	expanded, err := pathutil.ExpandUserPathStrictRequireNonEmpty(raw)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func asObjectMap(value any, label string) (map[string]any, error) {
	if value == nil {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	return object, nil
}

func asArray(value any, label string) ([]any, error) {
	if value == nil {
		return nil, fmt.Errorf("%s must be a JSON array", label)
	}
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON array", label)
	}
	return array, nil
}

func resolveAbsPath(raw string) (string, error) {
	expanded, err := pathutil.ExpandUserPathStrictRequireNonEmpty(raw)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(absolute)
	if clean == string(filepath.Separator) {
		return clean, nil
	}

	existing := clean
	suffix := make([]string, 0)
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return clean, nil
		}
		suffix = append([]string{filepath.Base(existing)}, suffix...)
		existing = parent
	}

	resolvedExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	if len(suffix) == 0 {
		return filepath.Clean(resolvedExisting), nil
	}
	return filepath.Clean(filepath.Join(append([]string{resolvedExisting}, suffix...)...)), nil
}
