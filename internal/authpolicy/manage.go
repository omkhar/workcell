// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
)

var (
	canonicalCredentialDestinations = map[string][2]string{
		"codex_auth":      {"codex", "auth.json"},
		"claude_api_key":  {"claude", "api-key.txt"},
		"claude_mcp":      {"claude", "mcp.json"},
		"gemini_env":      {"gemini", "gemini.env"},
		"gemini_oauth":    {"gemini", "oauth_creds.json"},
		"gemini_projects": {"gemini", "projects.json"},
		"gcloud_adc":      {"gemini", "gcloud-adc.json"},
		"github_hosts":    {"shared", "github-hosts.yml"},
		"github_config":   {"shared", "github-config.yml"},
	}
	allowedResolvers = map[string]map[string]struct{}{
		"claude_auth": {"claude-macos-keychain": {}},
	}
	statusOrder = map[string][]string{
		"codex":  {"codex_auth"},
		"claude": {"claude_api_key", "claude_auth"},
		"gemini": {"gemini_env", "gemini_oauth"},
	}
	entryAllowedKeys = map[string]struct{}{
		"source":          {},
		"resolver":        {},
		"materialization": {},
		"providers":       {},
		"modes":           {},
	}
)

func Run(program string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage(program))
		return 2
	}
	switch args[0] {
	case "init":
		policyPath, managedRoot, err := parseInitArgs(program, args[1:], stderr)
		if err != nil {
			return 2
		}
		if err := commandInit(policyPath, managedRoot); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "policy_path="+resolveOutputPath(policyPath))
		fmt.Fprintln(stdout, "managed_root="+resolveOutputPath(managedRoot))
		return 0
	case "set":
		opts, err := parseSetArgs(program, args[1:], stderr)
		if err != nil {
			return 2
		}
		if err := commandSet(opts); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printSetOutput(stdout, opts)
		return 0
	case "unset":
		policyPath, managedRoot, credential, err := parseUnsetArgs(program, args[1:], stderr)
		if err != nil {
			return 2
		}
		removed, err := commandUnset(policyPath, managedRoot, credential)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "policy_path="+policyPath)
		fmt.Fprintln(stdout, "credential="+credential)
		fmt.Fprintf(stdout, "removed=%d\n", removed)
		return 0
	case "status":
		opts, err := parseStatusArgs(program, args[1:], stderr)
		if err != nil {
			return 2
		}
		if err := commandStatus(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, usage(program))
		fmt.Fprintf(stderr, "%s: unsupported command: %s\n", program, args[0])
		return 2
	}
}

type setOptions struct {
	policyPath      string
	managedRoot     string
	agent           string
	credential      string
	sourceRaw       string
	sourceBaseRaw   string
	resolver        string
	ackHostResolver bool
}

type statusOptions struct {
	policyPath string
	agent      string
	mode       string
}

func usage(program string) string {
	if program == "" {
		program = "workcell-manage-injection-policy"
	}
	return fmt.Sprintf(
		"Usage: %s {init,set,unset,status} ...",
		program,
	)
}

func parseInitArgs(program string, args []string, stderr io.Writer) (string, string, error) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policy := fs.String("policy", "", "")
	managedRoot := fs.String("managed-root", "", "")
	fs.Usage = func() { fmt.Fprintln(stderr, usage(program)) }
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if fs.NArg() != 0 || *policy == "" || *managedRoot == "" {
		fs.Usage()
		return "", "", fmt.Errorf("--policy and --managed-root are required")
	}
	return resolveInputPath(*policy), resolveInputPath(*managedRoot), nil
}

func parseSetArgs(program string, args []string, stderr io.Writer) (setOptions, error) {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts setOptions
	fs.StringVar(&opts.policyPath, "policy", "", "")
	fs.StringVar(&opts.managedRoot, "managed-root", "", "")
	fs.StringVar(&opts.agent, "agent", "", "")
	fs.StringVar(&opts.credential, "credential", "", "")
	fs.StringVar(&opts.sourceRaw, "source", "", "")
	fs.StringVar(&opts.sourceBaseRaw, "source-base", "", "")
	fs.StringVar(&opts.resolver, "resolver", "", "")
	fs.BoolVar(&opts.ackHostResolver, "ack-host-resolver", false, "")
	fs.Usage = func() { fmt.Fprintln(stderr, usage(program)) }
	if err := fs.Parse(args); err != nil {
		return setOptions{}, err
	}
	if fs.NArg() != 0 || opts.policyPath == "" || opts.managedRoot == "" || opts.agent == "" || opts.credential == "" {
		fs.Usage()
		return setOptions{}, fmt.Errorf("missing required flags")
	}
	if _, ok := SupportedAgents[opts.agent]; !ok {
		return setOptions{}, fmt.Errorf("invalid agent: %s", opts.agent)
	}
	if _, ok := CredentialKeys[opts.credential]; !ok {
		return setOptions{}, fmt.Errorf("invalid credential: %s", opts.credential)
	}
	if opts.sourceRaw != "" && opts.resolver != "" {
		// Let the command path report the exact Python-style error.
	}
	opts.policyPath = resolveInputPath(opts.policyPath)
	opts.managedRoot = resolveInputPath(opts.managedRoot)
	return opts, nil
}

func parseUnsetArgs(program string, args []string, stderr io.Writer) (string, string, string, error) {
	fs := flag.NewFlagSet("unset", flag.ContinueOnError)
	fs.SetOutput(stderr)
	policy := fs.String("policy", "", "")
	managedRoot := fs.String("managed-root", "", "")
	credential := fs.String("credential", "", "")
	fs.Usage = func() { fmt.Fprintln(stderr, usage(program)) }
	if err := fs.Parse(args); err != nil {
		return "", "", "", err
	}
	if fs.NArg() != 0 || *policy == "" || *managedRoot == "" || *credential == "" {
		fs.Usage()
		return "", "", "", fmt.Errorf("missing required flags")
	}
	if _, ok := CredentialKeys[*credential]; !ok {
		return "", "", "", fmt.Errorf("invalid credential: %s", *credential)
	}
	return resolveInputPath(*policy), resolveInputPath(*managedRoot), *credential, nil
}

func parseStatusArgs(program string, args []string, stderr io.Writer) (statusOptions, error) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts statusOptions
	fs.StringVar(&opts.policyPath, "policy", "", "")
	fs.StringVar(&opts.agent, "agent", "", "")
	fs.StringVar(&opts.mode, "mode", "strict", "")
	fs.Usage = func() { fmt.Fprintln(stderr, usage(program)) }
	if err := fs.Parse(args); err != nil {
		return statusOptions{}, err
	}
	if fs.NArg() != 0 || opts.policyPath == "" {
		fs.Usage()
		return statusOptions{}, fmt.Errorf("missing required flags")
	}
	if opts.agent != "" {
		if _, ok := SupportedAgents[opts.agent]; !ok {
			return statusOptions{}, fmt.Errorf("invalid agent: %s", opts.agent)
		}
	}
	if _, ok := SupportedModes[opts.mode]; !ok {
		return statusOptions{}, fmt.Errorf("invalid mode: %s", opts.mode)
	}
	opts.policyPath = resolveInputPath(opts.policyPath)
	return opts, nil
}

func resolveInputPath(raw string) string {
	expanded, err := expandUserPath(raw)
	if err != nil {
		return raw
	}
	if !filepath.IsAbs(expanded) {
		expanded, err = filepath.Abs(expanded)
		if err != nil {
			return raw
		}
	}
	return filepath.Clean(expanded)
}

func resolveOutputPath(raw string) string {
	return resolveInputPath(raw)
}

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
	for _, name := range []string{"codex", "claude", "gemini", "shared"} {
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
		sourceBase := filepath.Clean(".")
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

	if _, ok := allowedResolvers[opts.credential]; !ok {
		return die(fmt.Sprintf("%s does not support resolver %s", opts.credential, opts.resolver))
	}
	if _, ok := allowedResolvers[opts.credential][opts.resolver]; !ok {
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

func commandStatus(opts statusOptions, stdout io.Writer) error {
	if _, err := os.Stat(opts.policyPath); os.IsNotExist(err) {
		fmt.Fprintln(stdout, "injection_policy=none")
		fmt.Fprintln(stdout, "default_injection_policy_path="+opts.policyPath)
		fmt.Fprintln(stdout, "credential_keys=none")
		fmt.Fprintln(stdout, "credential_input_kinds=none")
		fmt.Fprintln(stdout, "credential_resolvers=none")
		fmt.Fprintln(stdout, "credential_materialization=none")
		fmt.Fprintln(stdout, "credential_resolution_states=none")
		if opts.agent != "" {
			fmt.Fprintln(stdout, "provider_auth_mode=none")
			fmt.Fprintln(stdout, "provider_auth_modes=none")
			fmt.Fprintln(stdout, "shared_auth_modes=none")
			fmt.Fprintln(stdout, "github_auth_present=0")
		}
		return nil
	}

	policy, policySources, err := loadPolicyBundle(opts.policyPath)
	if err != nil {
		return err
	}
	selected, err := selectedCredentials(policy, opts.agent, opts.mode)
	if err != nil {
		return err
	}
	for key, raw := range selected {
		if err := validateStatusCredentialSource(key, raw, filepath.Dir(opts.policyPath)); err != nil {
			return err
		}
	}
	inputKinds := map[string]string{}
	resolvers := map[string]string{}
	materialization := map[string]string{}
	resolutionStates := map[string]string{}
	for key, raw := range selected {
		inputKinds[key] = credentialInputKind(raw)
		if rawMap, ok := raw.(map[string]any); ok {
			if resolver, ok := rawMap["resolver"].(string); ok {
				resolvers[key] = resolver
			}
			if mat, ok := rawMap["materialization"].(string); ok {
				materialization[key] = mat
			}
		}
		if inputKinds[key] == "resolver" {
			resolutionStates[key] = "configured-only"
		} else {
			resolutionStates[key] = "source"
		}
	}

	fmt.Fprintln(stdout, "policy_source_sha256="+compositePolicySHA256(policySources))
	fmt.Fprintln(stdout, "credential_keys="+renderModes(sortedKeys(selected)))
	fmt.Fprintln(stdout, "credential_input_kinds="+renderMap(inputKinds))
	fmt.Fprintln(stdout, "credential_resolvers="+renderMap(resolvers))
	fmt.Fprintln(stdout, "credential_materialization="+renderMap(materialization))
	fmt.Fprintln(stdout, "credential_resolution_states="+renderMap(resolutionStates))
	if opts.agent != "" {
		providerAuthModes := make([]string, 0)
		for _, key := range statusOrder[opts.agent] {
			if _, ok := selected[key]; ok && resolutionStates[key] != "configured-only" {
				providerAuthModes = append(providerAuthModes, key)
			}
		}
		sharedAuthModes := make([]string, 0)
		for _, key := range []string{"github_hosts", "github_config"} {
			if _, ok := selected[key]; ok && resolutionStates[key] != "configured-only" {
				sharedAuthModes = append(sharedAuthModes, key)
			}
		}
		providerAuthMode := "none"
		if len(providerAuthModes) > 0 {
			providerAuthMode = providerAuthModes[0]
		}
		fmt.Fprintln(stdout, "provider_auth_mode="+providerAuthMode)
		fmt.Fprintln(stdout, "provider_auth_modes="+renderModes(providerAuthModes))
		fmt.Fprintln(stdout, "shared_auth_modes="+renderModes(sharedAuthModes))
		if len(sharedAuthModes) > 0 {
			fmt.Fprintln(stdout, "github_auth_present=1")
		} else {
			fmt.Fprintln(stdout, "github_auth_present=0")
		}
	}
	return nil
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
	fmt.Fprintln(stdout, "resolver_status=configured-fail-closed")
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

func canonicalDestinationPath(managedRoot string, credential string) (string, error) {
	parts, ok := canonicalCredentialDestinations[credential]
	if !ok {
		return "", die(fmt.Sprintf("workcell auth set does not manage %s automatically", credential))
	}
	return filepath.Join(managedRoot, parts[0], parts[1]), nil
}

func canonicalDestinationPathPart(credential string) string {
	parts := canonicalCredentialDestinations[credential]
	return filepath.Join(parts[0], parts[1])
}

func ensureDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func validateManagedPath(managedRoot string, path string, label string) error {
	if err := requireNoSymlinkInPathChain(path, label); err != nil {
		return err
	}
	return requirePathWithin(managedRoot, path, label)
}

func writeManagedRootMarker(managedRoot string) error {
	if err := ensureDirectory(managedRoot); err != nil {
		return err
	}
	managedRootFS, err := os.OpenRoot(managedRoot)
	if err != nil {
		return err
	}
	defer managedRootFS.Close()
	return rootio.WriteFileAtomic(managedRootFS, managedRootMarker, []byte("managed_by=workcell\n"), 0o600, "."+managedRootMarker+"-")
}

func isWorkcellManagedRoot(path string) bool {
	_, err := os.Stat(filepath.Join(path, managedRootMarker))
	return err == nil
}

func cleanupStagedFile(root *os.Root, path string) error {
	if path == "" {
		return nil
	}
	if err := root.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func stageExistingFile(root *os.Root, path string) (string, error) {
	if path == "" {
		return "", nil
	}
	cleanPath, err := normalizeRootRelativePath(path)
	if err != nil {
		return "", err
	}
	if _, err := root.Stat(cleanPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	tempPath, err := reserveRootTempPath(root, filepath.Dir(cleanPath), ".workcell-stage-")
	if err != nil {
		return "", err
	}
	if err := root.Rename(cleanPath, tempPath); err != nil {
		return "", err
	}
	return tempPath, nil
}

func restoreStagedFile(root *os.Root, stagedPath string, destination string) error {
	if stagedPath == "" || destination == "" {
		return nil
	}
	cleanDestination, err := normalizeRootRelativePath(destination)
	if err != nil {
		return err
	}
	if err := root.Rename(stagedPath, cleanDestination); err != nil {
		return err
	}
	return nil
}

func openManagedRoot(managedRoot string) (*os.Root, error) {
	if err := ensureDirectory(managedRoot); err != nil {
		return nil, err
	}
	return os.OpenRoot(managedRoot)
}

func managedRelativePath(managedRoot string, path string, label string) (string, error) {
	if path == "" {
		return "", nil
	}
	return rootio.RelativePathWithin(managedRoot, path, label)
}

func writeSourceFile(root *os.Root, source string, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	return rootio.WriteFileAtomicFromReader(root, destination, in, 0o600, ".workcell-auth-")
}

func normalizeRootRelativePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be relative to managed root: %s", path)
	}
	cleanPath := filepath.Clean(path)
	if cleanPath == "." || cleanPath == string(filepath.Separator) {
		return "", fmt.Errorf("path must name a file within the managed root: %s", path)
	}
	return cleanPath, nil
}

func reserveRootTempPath(root *os.Root, parent string, prefix string) (string, error) {
	parent = filepath.Clean(parent)
	if parent == "." {
		parent = ""
	}
	for attempt := 0; attempt < 32; attempt++ {
		suffix, err := randomTempSuffix()
		if err != nil {
			return "", err
		}
		name := prefix + suffix + ".tmp"
		tempPath := name
		if parent != "" {
			tempPath = filepath.Join(parent, name)
		}
		tempFile, err := root.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if closeErr := tempFile.Close(); closeErr != nil {
				_ = root.Remove(tempPath)
				return "", closeErr
			}
			if removeErr := root.Remove(tempPath); removeErr != nil {
				return "", removeErr
			}
			return tempPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("unable to allocate temporary staging path under %s", root.Name())
}

func randomTempSuffix() (string, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", data[:]), nil
}

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
	allowed := map[string]struct{}{}
	for key := range SharedCredentialKeys {
		allowed[key] = struct{}{}
	}
	for key := range AgentScopedCredentialKeys[agent] {
		allowed[key] = struct{}{}
	}
	if _, ok := allowed[credential]; !ok {
		return die(fmt.Sprintf("%s is not valid for agent %s", credential, agent))
	}
	return nil
}

func validateSelectorValues(values any, label string, allowedValues map[string]struct{}) error {
	if values == nil {
		return nil
	}
	rawValues, ok := values.([]any)
	if !ok || len(rawValues) == 0 {
		return die(fmt.Sprintf("%s must be a non-empty array when specified", label))
	}
	for _, value := range rawValues {
		s, ok := value.(string)
		if !ok {
			return die(fmt.Sprintf("%s values must be strings", label))
		}
		if _, ok := allowedValues[s]; !ok {
			return die(fmt.Sprintf("%s contains unsupported value: %s", label, s))
		}
	}
	return nil
}

func validateStatusCredentialEntry(key string, raw any) error {
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if err := validateAllowedKeys(rawMap, entryAllowedKeys, "credentials."+key); err != nil {
		return err
	}
	sourceRaw := rawMap["source"]
	resolver, _ := rawMap["resolver"].(string)
	providers := rawMap["providers"]
	materialization, hasMaterialization := rawMap["materialization"]
	if _, ok := SharedCredentialKeys[key]; ok && providers == nil {
		return die(fmt.Sprintf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", key))
	}
	if sourceRaw != nil && resolver != "" {
		return die(fmt.Sprintf("credentials.%s must not declare both source and resolver", key))
	}
	if resolver == "" {
		if hasMaterialization {
			return die(fmt.Sprintf("credentials.%s.materialization is only valid for resolver-backed auth", key))
		}
		if sourceRaw == nil {
			return die(fmt.Sprintf("credentials.%s must declare source or resolver", key))
		}
		return nil
	}
	if _, ok := allowedResolvers[key]; !ok || allowedResolvers[key][resolver] == (struct{}{}) {
		// fallthrough handled below
	}
	if _, ok := allowedResolvers[key]; !ok {
		return die(fmt.Sprintf("credentials.%s.resolver is unsupported: %s", key, resolver))
	}
	if _, ok := allowedResolvers[key][resolver]; !ok {
		return die(fmt.Sprintf("credentials.%s.resolver is unsupported: %s", key, resolver))
	}
	if hasMaterialization {
		if mat, ok := materialization.(string); !ok || mat != "ephemeral" {
			return die(fmt.Sprintf("credentials.%s.materialization must stay ephemeral for resolver-backed auth", key))
		}
	}
	return nil
}

func validateStatusCredentialSource(key string, raw any, policyBase string) error {
	sourceRaw := raw
	if rawMap, ok := raw.(map[string]any); ok {
		sourceRaw = rawMap["source"]
	}
	if sourceRaw == nil {
		return nil
	}
	source, err := validateSourcePath(sourceRaw, "credentials."+key, policyBase)
	if err != nil {
		return err
	}
	_, err = requireSecretFile(source, "credentials."+key)
	return err
}

func renderMap(value map[string]string) string {
	if len(value) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+":"+value[key])
	}
	return strings.Join(parts, ",")
}

func renderModes(keys []string) string {
	if len(keys) == 0 {
		return "none"
	}
	return strings.Join(keys, ",")
}

func credentialInputKind(raw any) string {
	if rawMap, ok := raw.(map[string]any); ok {
		if rawMap["resolver"] != nil {
			return "resolver"
		}
	}
	return "source"
}

func selectedCredentials(policy map[string]any, agent string, mode string) (map[string]any, error) {
	credentials, _ := policy["credentials"].(map[string]any)
	if credentials == nil {
		return map[string]any{}, nil
	}
	if agent == "" {
		selected := map[string]any{}
		for key, raw := range credentials {
			if rawMap, ok := raw.(map[string]any); ok {
				if err := validateStatusCredentialEntry(key, rawMap); err != nil {
					return nil, err
				}
				if err := validateSelectorValues(rawMap["providers"], "credentials."+key+".providers", SupportedAgents); err != nil {
					return nil, err
				}
				ok, err := selectedFor(rawMap["modes"], mode, "credentials."+key+".modes", SupportedModes)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			selected[key] = raw
		}
		return selected, nil
	}
	relevant := map[string]struct{}{}
	for key := range SharedCredentialKeys {
		relevant[key] = struct{}{}
	}
	for key := range AgentScopedCredentialKeys[agent] {
		relevant[key] = struct{}{}
	}
	selected := map[string]any{}
	keys := make([]string, 0, len(relevant))
	for key := range relevant {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		raw, ok := credentials[key]
		if !ok {
			continue
		}
		if rawMap, ok := raw.(map[string]any); ok {
			if err := validateStatusCredentialEntry(key, rawMap); err != nil {
				return nil, err
			}
			ok, err := selectedFor(rawMap["providers"], agent, "credentials."+key+".providers", SupportedAgents)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			ok, err = selectedFor(rawMap["modes"], mode, "credentials."+key+".modes", SupportedModes)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		selected[key] = raw
	}
	return selected, nil
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
