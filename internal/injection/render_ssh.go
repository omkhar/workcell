// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func renderSSH(policy map[string]any, outputRoot, policyDir Path, agent, mode string) (map[string]any, error) {
	raw := policy["ssh"]
	if raw == nil {
		return map[string]any{}, nil
	}
	ssh, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("ssh must be a TOML table")
	}
	if err := validateAllowedKeys(ssh, mapKeysSet([]string{"enabled", "config", "known_hosts", "identities", "providers", "modes", "allow_unsafe_config"}), "ssh"); err != nil {
		return nil, err
	}
	enabledRaw, hasEnabled := ssh["enabled"]
	hasMaterial := false
	for _, key := range []string{"config", "known_hosts", "identities"} {
		if _, ok := ssh[key]; ok {
			hasMaterial = true
		}
	}
	if hasEnabled {
		enabled, ok := enabledRaw.(bool)
		if !ok {
			return nil, errors.New("ssh.enabled must be a boolean when specified")
		}
		if !enabled {
			return map[string]any{}, nil
		}
	}
	if !hasEnabled && !hasMaterial {
		return map[string]any{}, nil
	}
	ok, err := selectedFor(ssh["providers"], agent, "ssh.providers", supportedAgents)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{}, nil
	}
	ok, err = selectedFor(ssh["modes"], mode, "ssh.modes", supportedModes)
	if err != nil {
		return nil, err
	}
	if !ok {
		return map[string]any{}, nil
	}
	rendered := map[string]any{
		"identities": []map[string]any{},
	}
	allowUnsafeRaw, hasAllowUnsafe := ssh["allow_unsafe_config"]
	allowUnsafeConfig := false
	if hasAllowUnsafe {
		val, ok := allowUnsafeRaw.(bool)
		if !ok {
			return nil, errors.New("ssh.allow_unsafe_config must be a boolean when specified")
		}
		allowUnsafeConfig = val
	}
	configRaw, hasConfig := ssh["config"]
	if !hasConfig || configRaw == nil {
		rendered["config_assurance"] = "no-config"
	} else if allowUnsafeConfig {
		rendered["config_assurance"] = "lower-assurance-unsafe-config"
	} else {
		rendered["config_assurance"] = "safe"
	}
	if hasConfig && configRaw != nil {
		source, err := validateSourcePath(configRaw, "ssh.config", policyDir)
		if err != nil {
			return nil, err
		}
		if _, err := validateSecretFile(source, "ssh.config"); err != nil {
			return nil, err
		}
		if err := validateSSHConfigSafety(source, allowUnsafeConfig); err != nil {
			return nil, err
		}
		rendered["config"] = directMountEntry(source, directMountRoot+"/ssh/config")
	}
	knownHostsRaw, hasKnownHosts := ssh["known_hosts"]
	if hasKnownHosts && knownHostsRaw != nil {
		source, err := validateSourcePath(knownHostsRaw, "ssh.known_hosts", policyDir)
		if err != nil {
			return nil, err
		}
		if _, err := validateKnownHostsFile(source, "ssh.known_hosts"); err != nil {
			return nil, err
		}
		rendered["known_hosts"] = directMountEntry(source, directMountRoot+"/ssh/known_hosts")
	}
	identitiesRaw, hasIdentities := ssh["identities"]
	identities := []any{}
	if hasIdentities {
		if identitiesRaw == nil {
			identities = []any{}
		} else {
			var err error
			identities, err = anySlice(identitiesRaw, "ssh.identities")
			if err != nil {
				return nil, err
			}
		}
	}
	renderedIdentities := make([]map[string]any, 0, len(identities))
	seenIdentityTargets := map[string]struct{}{}
	for index, rawIdentity := range identities {
		source, err := validateSourcePath(rawIdentity, fmt.Sprintf("ssh.identities[%d]", index), policyDir)
		if err != nil {
			return nil, err
		}
		if _, err := validateSecretFile(source, fmt.Sprintf("ssh.identities[%d]", index)); err != nil {
			return nil, err
		}
		if _, reserved := reservedSSHFilnames[source.Base()]; reserved {
			return nil, fmt.Errorf("ssh.identities[%d] basename collides with a reserved SSH file: %s", index, source.Base())
		}
		if _, exists := seenIdentityTargets[source.Base()]; exists {
			return nil, fmt.Errorf("ssh.identities contains duplicate target basename: %s", source.Base())
		}
		seenIdentityTargets[source.Base()] = struct{}{}
		renderedIdentities = append(renderedIdentities, map[string]any{
			"source":      source.String(),
			"mount_path":  directMountRoot + "/ssh/identity-" + strconv.Itoa(index),
			"target_name": source.Base(),
		})
	}
	rendered["identities"] = renderedIdentities
	return rendered, nil
}

func validateKnownHostsFile(source Path, label string) (Path, error) {
	if err := requireNoSymlink(source, label); err != nil {
		return Path(""), err
	}
	info, err := os.Stat(source.String())
	if err != nil {
		return Path(""), err
	}
	if !info.Mode().IsRegular() {
		return Path(""), fmt.Errorf("%s must point at a file: %s", label, source)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return Path(""), fmt.Errorf("%s must not be group/world-writable: %s", label, source)
	}
	return source, nil
}

func parseSSHDirective(line string) (string, string, bool) {
	stripped := strings.TrimSpace(line)
	if stripped == "" || strings.HasPrefix(stripped, "#") {
		return "", "", false
	}
	parts := strings.Fields(stripped)
	directive := strings.ToLower(parts[0])
	remainder := ""
	if len(parts) > 1 {
		remainder = strings.Join(parts[1:], " ")
	}
	return directive, remainder, true
}

func validateSSHConfigSafety(source Path, allowUnsafe bool) error {
	if allowUnsafe {
		return nil
	}
	data, err := os.ReadFile(source.String())
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		directive, remainder, ok := parseSSHDirective(line)
		if !ok {
			continue
		}
		if _, risky := riskySSHDirectives[directive]; risky {
			return fmt.Errorf("ssh.config contains unsafe directive %q at line %d; set ssh.allow_unsafe_config = true only when you explicitly accept lower assurance", directive, i+1)
		}
		if directive == "match" && strings.Contains(" "+strings.ToLower(remainder)+" ", " exec ") {
			return fmt.Errorf("ssh.config contains unsafe Match exec at line %d; set ssh.allow_unsafe_config = true only when you explicitly accept lower assurance", i+1)
		}
	}
	return nil
}
