// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/authresolve"
	"github.com/omkhar/workcell/internal/providerid"
)

func commandInit(policyPath string, managedRoot string) error {
	if err := validateManagedPath(managedRoot, managedRoot, "managed_root"); err != nil {
		return err
	}
	if _, err := os.Stat(policyPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := writeVerifiedPolicy(policyPath, map[string]any{"version": 1}); err != nil {
			return err
		}
	} else {
		if _, _, err := loadPolicyBundle(policyPath); err != nil {
			return err
		}
	}
	if err := ensureDirectory(managedRoot); err != nil {
		return err
	}
	if err := writeManagedRootMarker(managedRoot); err != nil {
		return err
	}
	for _, name := range []string{providerid.Codex, providerid.Claude, providerid.Gemini, "shared"} {
		path := filepath.Join(managedRoot, name)
		if err := validateManagedPath(managedRoot, path, "managed_root/"+name); err != nil {
			return err
		}
		if err := ensureDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

func commandSet(opts setOptions) error {
	if err := validateAgentCredential(opts.agent, opts.credential); err != nil {
		return err
	}
	if (opts.sourceRaw == "") == (opts.resolver == "") {
		return die("Specify exactly one of --source or --resolver")
	}
	if err := validateManagedPath(opts.managedRoot, opts.managedRoot, "managed_root"); err != nil {
		return err
	}

	policy, err := loadMutablePolicy(opts.policyPath)
	if err != nil {
		return err
	}
	credentials, _ := policy["credentials"].(map[string]any)
	if credentials == nil {
		credentials = map[string]any{}
		policy["credentials"] = credentials
	}
	if err := ensureCredentialNotOnlyInIncludes(opts.policyPath, credentials, opts.credential, "set"); err != nil {
		return err
	}
	existingEntry := credentials[opts.credential]
	if err := ensureNoForeignManagedSource(opts.policyPath, opts.managedRoot, opts.credential, existingEntry, "set"); err != nil {
		return err
	}
	selectors, err := desiredSelectors(credentials, opts.credential, opts.agent)
	if err != nil {
		return err
	}

	if opts.sourceRaw != "" {
		var sourceBase string
		if opts.sourceBaseRaw != "" {
			sourceBase = resolveInputPath(opts.sourceBaseRaw)
		} else {
			sourceBase, err = filepath.Abs(".")
			if err != nil {
				return err
			}
		}
		source, err := validateSourcePath(opts.sourceRaw, "credentials."+opts.credential, sourceBase)
		if err != nil {
			return err
		}
		if _, err := requireSecretFile(source, "credentials."+opts.credential); err != nil {
			return err
		}
		destination, err := canonicalDestinationPath(opts.managedRoot, opts.credential)
		if err != nil {
			return err
		}
		if err := validateManagedPath(opts.managedRoot, destination, "managed credential path for "+opts.credential); err != nil {
			return err
		}
		managedRootFS, err := openManagedRoot(opts.managedRoot)
		if err != nil {
			return err
		}
		defer managedRootFS.Close()
		destinationRel := canonicalDestinationPathPart(opts.credential)
		priorManagedPath, err := managedSourcePathForEntry(opts.policyPath, opts.managedRoot, opts.credential, existingEntry)
		if err != nil {
			return err
		}
		priorManagedRel, err := managedRelativePath(opts.managedRoot, priorManagedPath, "managed credential path for "+opts.credential)
		if err != nil {
			return err
		}
		if priorManagedPath != "" && priorManagedPath != destination {
			if err := requireNoSymlinkInPathChain(priorManagedPath, "managed credential path for "+opts.credential); err != nil {
				return err
			}
		}
		previousDestination, err := stageExistingFile(managedRootFS, destinationRel)
		if err != nil {
			return err
		}
		var priorManagedBackup string
		if priorManagedPath != "" && priorManagedPath != destination {
			priorManagedBackup, err = stageExistingFile(managedRootFS, priorManagedRel)
			if err != nil {
				_ = restoreStagedFile(managedRootFS, previousDestination, destinationRel)
				return err
			}
		}
		if err := writeSourceFile(managedRootFS, source, destinationRel); err != nil {
			_ = restoreStagedFile(managedRootFS, previousDestination, destinationRel)
			_ = restoreStagedFile(managedRootFS, priorManagedBackup, priorManagedRel)
			return err
		}
		credentials[opts.credential] = mergeSelectors(selectors, map[string]any{
			"source": destination,
		})
		if err := writeVerifiedPolicy(opts.policyPath, policy); err != nil {
			_ = cleanupStagedFile(managedRootFS, destinationRel)
			_ = restoreStagedFile(managedRootFS, previousDestination, destinationRel)
			_ = restoreStagedFile(managedRootFS, priorManagedBackup, priorManagedRel)
			return err
		}
		_ = writeManagedRootMarker(opts.managedRoot)
		_ = cleanupStagedFile(managedRootFS, previousDestination)
		_ = cleanupStagedFile(managedRootFS, priorManagedBackup)
		return nil
	}

	if !authresolve.ResolverSupported(opts.credential, opts.resolver) {
		return die(fmt.Sprintf("%s does not support resolver %s", opts.credential, opts.resolver))
	}
	if !opts.ackHostResolver {
		return die("set --resolver requires --ack-host-resolver")
	}
	managedPath, err := managedSourcePathForEntry(opts.policyPath, opts.managedRoot, opts.credential, existingEntry)
	if err != nil {
		return err
	}
	if managedPath != "" {
		if err := requireNoSymlinkInPathChain(managedPath, "managed credential path for "+opts.credential); err != nil {
			return err
		}
	}
	managedRootFS, err := openManagedRoot(opts.managedRoot)
	if err != nil {
		return err
	}
	defer managedRootFS.Close()
	managedPathRel, err := managedRelativePath(opts.managedRoot, managedPath, "managed credential path for "+opts.credential)
	if err != nil {
		return err
	}
	stagedManagedPath, err := stageExistingFile(managedRootFS, managedPathRel)
	if err != nil {
		return err
	}
	credentials[opts.credential] = mergeSelectors(selectors, map[string]any{
		"resolver":        opts.resolver,
		"materialization": "ephemeral",
	})
	if err := writeVerifiedPolicy(opts.policyPath, policy); err != nil {
		_ = restoreStagedFile(managedRootFS, stagedManagedPath, managedPathRel)
		return err
	}
	_ = cleanupStagedFile(managedRootFS, stagedManagedPath)
	return nil
}

func commandUnset(policyPath string, managedRoot string, credential string) (int, error) {
	if err := validateManagedPath(managedRoot, managedRoot, "managed_root"); err != nil {
		return 0, err
	}
	policy, err := loadMutablePolicy(policyPath)
	if err != nil {
		return 0, err
	}
	credentials, _ := policy["credentials"].(map[string]any)
	if credentials == nil {
		credentials = map[string]any{}
	}
	if err := ensureCredentialNotOnlyInIncludes(policyPath, credentials, credential, "unset"); err != nil {
		return 0, err
	}
	existingEntry := credentials[credential]
	if err := ensureNoForeignManagedSource(policyPath, managedRoot, credential, existingEntry, "unset"); err != nil {
		return 0, err
	}
	if _, ok := credentials[credential]; !ok {
		return 0, nil
	}
	delete(credentials, credential)
	if len(credentials) == 0 {
		delete(policy, "credentials")
	} else {
		policy["credentials"] = credentials
	}
	managedPath, err := managedSourcePathForEntry(policyPath, managedRoot, credential, existingEntry)
	if err != nil {
		return 0, err
	}
	if managedPath != "" {
		if err := requireNoSymlinkInPathChain(managedPath, "managed credential path for "+credential); err != nil {
			return 0, err
		}
	}
	managedRootFS, err := openManagedRoot(managedRoot)
	if err != nil {
		return 0, err
	}
	defer managedRootFS.Close()
	managedPathRel, err := managedRelativePath(managedRoot, managedPath, "managed credential path for "+credential)
	if err != nil {
		return 0, err
	}
	stagedManagedPath, err := stageExistingFile(managedRootFS, managedPathRel)
	if err != nil {
		return 0, err
	}
	if err := writeVerifiedPolicy(policyPath, policy); err != nil {
		_ = restoreStagedFile(managedRootFS, stagedManagedPath, managedPathRel)
		return 0, err
	}
	_ = cleanupStagedFile(managedRootFS, stagedManagedPath)
	return 1, nil
}

func printSetOutput(stdout io.Writer, opts setOptions) {
	fmt.Fprintln(stdout, "policy_path="+opts.policyPath)
	fmt.Fprintln(stdout, "credential="+opts.credential)
	if opts.sourceRaw != "" {
		fmt.Fprintln(stdout, "source="+resolveInputPath(opts.sourceRaw))
		fmt.Fprintln(stdout, "managed_source="+filepath.Join(opts.managedRoot, canonicalDestinationPathPart(opts.credential)))
		if selectors, err := desiredSelectorsFromPolicy(opts.policyPath, opts.credential, opts.agent); err == nil {
			if providers, ok := selectors["providers"].([]string); ok {
				fmt.Fprintln(stdout, "providers="+strings.Join(providers, ","))
			}
			if modes, ok := selectors["modes"].([]string); ok {
				fmt.Fprintln(stdout, "modes="+strings.Join(modes, ","))
			}
		}
		return
	}
	fmt.Fprintln(stdout, "resolver="+opts.resolver)
	fmt.Fprintln(stdout, "materialization=ephemeral")
	resolverStatus := "configured-fail-closed"
	if readiness, err := resolverReadinessForStatus(opts.credential, opts.resolver); err == nil {
		switch readiness {
		case "host-source":
			resolverStatus = "configured-launch-ready"
		case "configured-only":
			if opts.resolver != "claude-macos-keychain" {
				resolverStatus = "configured-awaiting-host-source"
			}
		}
	}
	fmt.Fprintln(stdout, "resolver_status="+resolverStatus)
	if selectors, err := desiredSelectorsFromPolicy(opts.policyPath, opts.credential, opts.agent); err == nil {
		if providers, ok := selectors["providers"].([]string); ok {
			fmt.Fprintln(stdout, "providers="+strings.Join(providers, ","))
		}
		if modes, ok := selectors["modes"].([]string); ok {
			fmt.Fprintln(stdout, "modes="+strings.Join(modes, ","))
		}
	}
}

func desiredSelectorsFromPolicy(policyPath string, credential string, agent string) (map[string]any, error) {
	policy, err := loadMutablePolicy(policyPath)
	if err != nil {
		return nil, err
	}
	credentials, _ := policy["credentials"].(map[string]any)
	if credentials == nil {
		credentials = map[string]any{}
	}
	return desiredSelectors(credentials, credential, agent)
}

// Managed-root file ops (canonicalDestinationPath, ensureDirectory,
// validateManagedPath, writeManagedRootMarker, isWorkcellManagedRoot,
// cleanupStagedFile, stageExistingFile, restoreStagedFile,
// openManagedRoot, managedRelativePath, writeSourceFile,
// normalizeRootRelativePath, reserveRootTempPath, randomTempSuffix)
// live in staging.go.

func loadMutablePolicy(policyPath string) (map[string]any, error) {
	policy, err := loadRawPolicy(policyPath)
	if err != nil {
		return nil, err
	}
	if _, ok := policy["version"]; !ok {
		policy["version"] = 1
	}
	return policy, nil
}

func ensureCredentialNotOnlyInIncludes(policyPath string, credentials map[string]any, credential string, command string) error {
	if _, ok := credentials[credential]; ok || policyPath == "" {
		return nil
	}
	mergedPolicy, policySources, err := loadPolicyBundle(policyPath)
	if err != nil {
		return err
	}
	mergedCredentials, _ := mergedPolicy["credentials"].(map[string]any)
	if mergedCredentials == nil {
		return nil
	}
	if _, ok := mergedCredentials[credential]; !ok {
		return nil
	}
	fragmentPath := ""
	for _, source := range policySources {
		sourcePolicy, err := loadRawPolicy(source.Path)
		if err != nil {
			return err
		}
		sourceCredentials, _ := sourcePolicy["credentials"].(map[string]any)
		if sourceCredentials != nil {
			if _, ok := sourceCredentials[credential]; ok {
				fragmentPath = source.Path
				break
			}
		}
	}
	fragmentHint := ""
	if fragmentPath != "" {
		fragmentHint = ": " + fragmentPath
	}
	return die(fmt.Sprintf("credentials.%s is declared by an included policy fragment; update that fragment directly before using workcell auth %s%s", credential, command, fragmentHint))
}

func desiredSelectors(credentials map[string]any, credential string, agent string) (map[string]any, error) {
	existing := credentials[credential]
	if existing == nil {
		if _, ok := SharedCredentialKeys[credential]; ok {
			return map[string]any{"providers": []string{agent}}, nil
		}
		return map[string]any{}, nil
	}
	if existingMap, ok := existing.(map[string]any); ok {
		if err := validateAllowedKeys(existingMap, entryAllowedKeys, "credentials."+credential); err != nil {
			return nil, err
		}
		selectors := map[string]any{}
		if modes, ok := existingMap["modes"]; ok {
			selectors["modes"] = modes
		}
		if _, ok := SharedCredentialKeys[credential]; ok {
			if providers, ok := existingMap["providers"]; ok {
				selectors["providers"] = providers
			} else {
				selectors["providers"] = []string{agent}
			}
		} else if providers, ok := existingMap["providers"]; ok {
			selectors["providers"] = providers
		}
		return selectors, nil
	}
	if _, ok := SharedCredentialKeys[credential]; ok {
		return map[string]any{"providers": []string{agent}}, nil
	}
	return map[string]any{}, nil
}

func mergeSelectors(selectors map[string]any, extra map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range selectors {
		result[key] = value
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func entrySourcePath(policyPath string, existingSource string) (string, error) {
	return expandHostPath(existingSource, filepath.Dir(policyPath))
}

func managedSourcePathForEntry(policyPath string, managedRoot string, credential string, existingEntry any) (string, error) {
	if _, ok := canonicalCredentialDestinations[credential]; !ok {
		return "", nil
	}
	candidate, err := canonicalDestinationPath(managedRoot, credential)
	if err != nil {
		return "", err
	}
	var existingSource any
	switch typed := existingEntry.(type) {
	case map[string]any:
		existingSource = typed["source"]
	case string:
		existingSource = typed
	}
	existingSourceString, ok := existingSource.(string)
	if ok && existingSourceString == candidate {
		return candidate, nil
	}
	if !ok {
		return "", nil
	}
	existingPath, err := entrySourcePath(policyPath, existingSourceString)
	if err != nil {
		return "", err
	}
	if pathsEquivalent(existingPath, candidate) {
		return candidate, nil
	}
	return "", nil
}

func foreignManagedSourcePathForEntry(policyPath string, managedRoot string, credential string, existingEntry any) (string, error) {
	parts, ok := canonicalCredentialDestinations[credential]
	if !ok {
		return "", nil
	}
	candidate, err := canonicalDestinationPath(managedRoot, credential)
	if err != nil {
		return "", err
	}
	var existingSource any
	switch typed := existingEntry.(type) {
	case map[string]any:
		existingSource = typed["source"]
	case string:
		existingSource = typed
	}
	existingSourceString, ok := existingSource.(string)
	if !ok {
		return "", nil
	}
	existingPath, err := entrySourcePath(policyPath, existingSourceString)
	if err != nil {
		return "", err
	}
	if pathsEquivalent(existingPath, candidate) {
		return "", nil
	}
	if filepath.Base(existingPath) != parts[1] || filepath.Base(filepath.Dir(existingPath)) != parts[0] {
		return "", nil
	}
	rootCandidate := filepath.Dir(filepath.Dir(existingPath))
	if isWorkcellManagedRoot(rootCandidate) {
		return existingPath, nil
	}
	return "", nil
}

func ensureNoForeignManagedSource(policyPath string, managedRoot string, credential string, existingEntry any, command string) error {
	foreignPath, err := foreignManagedSourcePathForEntry(policyPath, managedRoot, credential, existingEntry)
	if err != nil {
		return err
	}
	if foreignPath == "" {
		return nil
	}
	return die(fmt.Sprintf("credentials.%s is already managed under a different --managed-root (%s); run workcell auth %s with that --managed-root before switching roots", credential, filepath.Dir(filepath.Dir(foreignPath)), command))
}

func validateAgentCredential(agent string, credential string) error {
	if !credentialAllowedForAgent(agent, credential) {
		return die(fmt.Sprintf("%s is not valid for agent %s", credential, agent))
	}
	return nil
}

func writeVerifiedPolicy(policyPath string, policy map[string]any) error {
	if err := ensureDirectory(filepath.Dir(policyPath)); err != nil {
		return err
	}
	tempFile, err := os.CreateTemp(filepath.Dir(policyPath), filepath.Base(policyPath)+".*.tmp")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if err := writePolicyFile(tempPath, policy); err != nil {
		return err
	}
	if _, _, err := loadPolicyBundle(tempPath); err != nil {
		return err
	}
	if err := os.Rename(tempPath, policyPath); err != nil {
		return err
	}
	return os.Chmod(policyPath, 0o600)
}
