package runtimeutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omkhar/workcell/internal/pathutil"
	"github.com/omkhar/workcell/internal/rootio"
)

type DirectMount struct {
	Source    string `json:"source"`
	MountPath string `json:"mount_path"`
}

func CanonicalizePath(raw string) (string, error) {
	expanded, err := expandUserPath(raw)
	if err != nil {
		return "", err
	}
	return pathutil.CanonicalizeExpandedPath(expanded)
}

func ResolveIPs(host string) ([]string, error) {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if host == "" {
		return nil, errors.New("host is required")
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	ips := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		ip := addr.IP.String()
		if ip == "" {
			continue
		}
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}
	return ips, nil
}

func ListDirectMounts(mountSpecPath string) ([]DirectMount, error) {
	data, err := os.ReadFile(mountSpecPath)
	if err != nil {
		return nil, err
	}
	var entries []map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	directMounts := make([]DirectMount, 0, len(entries))
	for _, entry := range entries {
		source, _ := entry["source"].(string)
		mountPath, _ := entry["mount_path"].(string)
		if source == "" || mountPath == "" {
			continue
		}
		directMounts = append(directMounts, DirectMount{Source: source, MountPath: mountPath})
	}
	sort.SliceStable(directMounts, func(i, j int) bool {
		return directMounts[i].MountPath < directMounts[j].MountPath
	})
	return directMounts, nil
}

func RewriteBundleCredentialOverride(manifestPath, mountSpecPath, credentialKey, overrideSource string) error {
	manifestRoot, err := os.OpenRoot(filepath.Dir(manifestPath))
	if err != nil {
		return err
	}
	defer manifestRoot.Close()
	manifestName := filepath.Base(manifestPath)
	manifestData, err := manifestRoot.ReadFile(manifestName)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return err
	}
	mountSources := map[string]string{}
	if mountSpecPath != "" {
		if entries, err := ListDirectMounts(mountSpecPath); err == nil {
			for _, entry := range entries {
				mountSources[entry.MountPath] = entry.Source
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if rawCredentials, ok := manifest["credentials"]; ok && rawCredentials != nil {
		credentials, ok := rawCredentials.(map[string]any)
		if !ok {
			return fmt.Errorf("manifest credentials must be a JSON object")
		}
		for _, entryValue := range credentials {
			entry, ok := entryValue.(map[string]any)
			if !ok {
				continue
			}
			if _, hasSource := entry["source"]; hasSource {
				continue
			}
			if mountPath, ok := entry["mount_path"].(string); ok {
				if source, ok := mountSources[mountPath]; ok && source != "" {
					entry["source"] = source
				}
			}
		}
	}
	credentials, ok := manifest["credentials"].(map[string]any)
	if !ok {
		return fmt.Errorf("manifest credentials must be a JSON object")
	}
	credential, ok := credentials[credentialKey].(map[string]any)
	if !ok {
		return fmt.Errorf("manifest credential %q must be a JSON object", credentialKey)
	}
	credential["source"] = overrideSource

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return rootio.WriteFileAtomic(manifestRoot, manifestName, data, 0o600, ".workcell-manifest-")
}

func expandUserPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty path")
	}
	return pathutil.ExpandUserPathStrict(raw)
}
