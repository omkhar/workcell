// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/injectionpolicy"
	"github.com/omkhar/workcell/internal/pathutil"
	"github.com/omkhar/workcell/internal/providerid"
)

const (
	sessionHomeRoot = "/state/agent-home"
	runInjectedRoot = "/state/injected"
	directMountRoot = "/opt/workcell/host-inputs"
)

var (
	supportedAgents = map[string]struct{}{
		providerid.Codex:  {},
		providerid.Claude: {},
		providerid.Gemini: {},
	}
	supportedModes = map[string]struct{}{
		"strict":      {},
		"development": {},
		"build":       {},
		"breakglass":  {},
	}
	supportedClassifications = map[string]struct{}{
		"public": {},
		"secret": {},
	}
	reservedSSHFilnames = map[string]struct{}{
		"config":      {},
		"known_hosts": {},
	}
	riskySSHDirectives = map[string]struct{}{
		"controlmaster":       {},
		"controlpath":         {},
		"controlpersist":      {},
		"forwardagent":        {},
		"identityagent":       {},
		"include":             {},
		"knownhostscommand":   {},
		"localcommand":        {},
		"permitlocalcommand":  {},
		"pkcs11provider":      {},
		"proxycommand":        {},
		"securitykeyprovider": {},
		"sendenv":             {},
		"setenv":              {},
		"userknownhostsfile":  {},
	}
	reservedTargets           = adapters.ReservedTargets()
	credentialContainerPaths  = adapters.CredentialContainerPaths()
	agentScopedCredentialKeys = adapters.AgentScopedCredentialKeys()
	sharedCredentialKeys      = adapters.SharedCredentialKeys()
	googleAuthEndpoints       = adapters.GeminiGoogleAuthEndpoints
	vertexEndpoint            = "aiplatform.googleapis.com:443"
	geminiProjectKeys         = []string{
		"GOOGLE_CLOUD_PROJECT",
		"GOOGLE_CLOUD_PROJECT_ID",
	}
	geminiVertexLocationKeys = []string{
		"GOOGLE_CLOUD_LOCATION",
		"GOOGLE_CLOUD_REGION",
		"CLOUD_ML_REGION",
		"VERTEX_LOCATION",
		"VERTEX_AI_LOCATION",
	}
	geminiSupportedEnvKeys = map[string]struct{}{
		"GEMINI_API_KEY":            {},
		"GOOGLE_API_KEY":            {},
		"GOOGLE_GENAI_USE_GCA":      {},
		"GOOGLE_GENAI_USE_VERTEXAI": {},
		"GOOGLE_CLOUD_PROJECT":      {},
		"GOOGLE_CLOUD_PROJECT_ID":   {},
		"GOOGLE_CLOUD_LOCATION":     {},
		"GOOGLE_CLOUD_REGION":       {},
		"CLOUD_ML_REGION":           {},
		"VERTEX_LOCATION":           {},
		"VERTEX_AI_LOCATION":        {},
	}
	allowedRootPolicyKeys = map[string]struct{}{
		"version":     {},
		"includes":    {},
		"documents":   {},
		"ssh":         {},
		"copies":      {},
		"credentials": {},
	}
)

// PolicySource is an alias for injectionpolicy.PolicySource — the
// canonical cross-package type.  Kept exported here for callers that
// have always imported it as injection.PolicySource; new code should
// reach for injectionpolicy.PolicySource directly.
type PolicySource = injectionpolicy.PolicySource

func ValidateRenderAgentMode(agent, mode string) error {
	if _, ok := supportedAgents[agent]; !ok {
		return fmt.Errorf("unsupported agent: %s", agent)
	}
	if _, ok := supportedModes[mode]; !ok {
		return fmt.Errorf("unsupported mode: %s", mode)
	}
	return nil
}

func RunRenderInjectionBundle(policyPath, agent, mode, outputRoot, policyMetadata string) error {
	resolvedPolicyPath, err := resolveAbsPath(policyPath)
	if err != nil {
		return err
	}
	resolvedOutputRoot, err := resolveAbsPath(outputRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(resolvedOutputRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(resolvedOutputRoot, 0o700); err != nil {
		return err
	}
	if err := ValidateRenderAgentMode(agent, mode); err != nil {
		return err
	}

	policy, policySources, err := loadPolicyBundle(Path(resolvedPolicyPath))
	if err != nil {
		return err
	}
	policyEntrypoint := logicalPolicyPath(Path(resolvedPolicyPath), Path(filepath.Dir(resolvedPolicyPath)))
	if policyMetadata != "" {
		policyEntrypoint, policySources, err = loadPolicyMetadataOverride(policyMetadata)
		if err != nil {
			return err
		}
	}

	renderedDocuments, err := renderDocuments(policy, Path(resolvedOutputRoot), Path(filepath.Dir(resolvedPolicyPath)))
	if err != nil {
		return err
	}
	renderedCopies, err := renderCopies(policy, Path(resolvedOutputRoot), Path(filepath.Dir(resolvedPolicyPath)), agent, mode)
	if err != nil {
		return err
	}
	renderedCredentials, err := renderCredentials(policy, Path(filepath.Dir(resolvedPolicyPath)), agent, mode)
	if err != nil {
		return err
	}
	renderedSSH, err := renderSSH(policy, Path(resolvedOutputRoot), Path(filepath.Dir(resolvedPolicyPath)), agent, mode)
	if err != nil {
		return err
	}

	policySHA, err := effectivePolicySHA256(policySources, Path(resolvedOutputRoot), renderedDocuments, renderedCopies, renderedCredentials, renderedSSH)
	if err != nil {
		return fmt.Errorf("compute effective policy sha256: %w", err)
	}

	manifest := map[string]any{
		"version": 1,
		"metadata": map[string]any{
			"policy_entrypoint":   policyEntrypoint,
			"policy_sha256":       policySHA,
			"policy_sources":      policySources,
			"credential_keys":     sortedKeys(renderedCredentials),
			"extra_endpoints":     deriveCredentialExtraEndpoints(renderedCredentials),
			"secret_copy_targets": secretCopyTargets(renderedCopies),
			"ssh_enabled":         len(renderedSSH) > 0,
			"ssh_config_assurance": func() string {
				if renderedSSH == nil {
					return "off"
				}
				if v, ok := renderedSSH["config_assurance"].(string); ok {
					return v
				}
				return "off"
			}(),
		},
		"documents":   renderedDocuments,
		"copies":      renderedCopies,
		"credentials": renderedCredentials,
		"ssh":         renderedSSH,
	}

	manifestPath := filepath.Join(resolvedOutputRoot, "manifest.json")
	if err := writeIndentedJSON(manifestPath, manifest, 0o600); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, manifestPath)
	return nil
}

func renderDocuments(policy map[string]any, outputRoot, policyDir Path) (map[string]string, error) {
	raw := policy["documents"]
	if raw == nil {
		return map[string]string{}, nil
	}
	documents, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("documents must be a TOML table")
	}
	if err := validateAllowedKeys(documents, mapKeysSet([]string{"common", providerid.Codex, providerid.Claude, providerid.Gemini}), "documents"); err != nil {
		return nil, err
	}

	rendered := map[string]string{}
	ordered := []struct {
		key     string
		relpath string
	}{
		{"common", "documents/common.md"},
		{providerid.Codex, "documents/codex.md"},
		{providerid.Claude, "documents/claude.md"},
		{providerid.Gemini, "documents/gemini.md"},
	}
	for _, item := range ordered {
		rawValue, ok := documents[item.key]
		if !ok || rawValue == nil {
			continue
		}
		source, err := validateSourcePath(rawValue, "documents."+item.key, policyDir)
		if err != nil {
			return nil, err
		}
		if err := ensureIsFile(source, fmt.Sprintf("documents.%s", item.key)); err != nil {
			return nil, err
		}
		if err := stageFile(source, outputRoot, item.relpath); err != nil {
			return nil, err
		}
		rendered[item.key] = item.relpath
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

func validateAllowedKeys(table map[string]any, allowed map[string]struct{}, label string) error {
	unknown := make([]string, 0)
	for key := range table {
		if _, ok := allowed[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	slices.Sort(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("%s contains unsupported keys: %s", label, strings.Join(unknown, ", "))
	}
	return nil
}

func selectedFor(values any, current, label string, allowed map[string]struct{}) (bool, error) {
	if values == nil {
		return true, nil
	}
	items, err := stringSlice(values, label)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, fmt.Errorf("%s must be a non-empty array when specified", label)
	}
	for _, s := range items {
		if _, ok := allowed[s]; !ok {
			return false, fmt.Errorf("%s contains unsupported value: %s", label, s)
		}
		if s == current {
			return true, nil
		}
	}
	return false, nil
}

func stringSlice(values any, label string) ([]string, error) {
	switch typed := values.(type) {
	case nil:
		return nil, nil
	case []string:
		return append([]string(nil), typed...), nil
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s values must be strings", label)
			}
			items = append(items, value)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("%s must be a non-empty array when specified", label)
	}
}

func anySlice(values any, label string) ([]any, error) {
	switch typed := values.(type) {
	case nil:
		return nil, nil
	case []any:
		return append([]any(nil), typed...), nil
	case []string:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings when specified", label)
	}
}

func ensureNoSymlinksWithin(root Path) error {
	return filepath.WalkDir(root.String(), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == root.String() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("directory injections must not contain symlinks: %s", current)
		}
		return nil
	})
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

func directMountEntry(source Path, mountPath string) map[string]string {
	return map[string]string{
		"source":     source.String(),
		"mount_path": mountPath,
	}
}

func validateSourcePath(raw any, label string, base Path) (Path, error) {
	rawStr, ok := raw.(string)
	if !ok || rawStr == "" {
		return Path(""), fmt.Errorf("%s must be a non-empty string path", label)
	}
	source, err := expandHostPath(rawStr, base)
	if err != nil {
		return Path(""), err
	}
	if _, err := os.Stat(source.String()); err != nil {
		return Path(""), fmt.Errorf("%s does not exist: %s", label, source)
	}
	offender, err := findUnsafeSymlinkInPathChain(source.String())
	if err != nil {
		return Path(""), err
	}
	if offender != "" {
		return Path(""), fmt.Errorf("%s must not be a symlink: %s", label, offender)
	}
	return source, nil
}

func expandHostPath(raw string, base Path) (Path, error) {
	expanded, err := pathutil.ExpandUserPathStrictRequireNonEmpty(raw)
	if err != nil {
		return Path(""), err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(base.String(), expanded)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return Path(""), err
	}
	return Path(abs), nil
}

func requirePathWithin(root, candidate Path, label string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root.String())
	if err != nil {
		return err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate.String())
	if err != nil {
		return err
	}
	if resolvedCandidate != resolvedRoot && !strings.HasPrefix(resolvedCandidate, resolvedRoot+string(filepath.Separator)) {
		return fmt.Errorf("%s must stay within %s: %s", label, resolvedRoot, resolvedCandidate)
	}
	return nil
}

func requireNoSymlink(path Path, label string) error {
	if info, err := os.Lstat(path.String()); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink: %s", label, path)
	}
	return nil
}

func requireSecretOwnerOnly(path Path, label string) error {
	info, err := os.Lstat(path.String())
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("unsupported file stat type")
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s must be owned by uid %d: %s", label, os.Getuid(), path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must not be group/world-accessible: %s", label, path)
	}
	return nil
}

func validateSecretFile(source Path, label string) (Path, error) {
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
	if err := requireSecretOwnerOnly(source, label); err != nil {
		return Path(""), err
	}
	return source, nil
}

func validateSecretTree(source Path, label string) error {
	if err := requireNoSymlink(source, label); err != nil {
		return err
	}
	info, err := os.Stat(source.String())
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		_, err = validateSecretFile(source, label)
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must point at a file or directory: %s", label, source)
	}
	if err := requireSecretOwnerOnly(source, label); err != nil {
		return err
	}
	if err := ensureNoSymlinksWithin(source); err != nil {
		return err
	}
	return filepath.WalkDir(source.String(), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == source.String() {
			return nil
		}
		child := Path(current)
		if err := requireNoSymlink(child, label); err != nil {
			return err
		}
		return requireSecretOwnerOnly(child, label)
	})
}

func effectivePolicySHA256(
	policySources []PolicySource,
	outputRoot Path,
	renderedDocuments map[string]string,
	renderedCopies []map[string]any,
	renderedCredentials map[string]map[string]string,
	renderedSSH map[string]any,
) (string, error) {
	documents := map[string]string{}
	for _, key := range sortedKeys(renderedDocuments) {
		hash, err := pathMaterialSHA256(outputRoot.Join(renderedDocuments[key]))
		if err != nil {
			return "", fmt.Errorf("hash document %q: %w", key, err)
		}
		documents[key] = hash
	}
	copies := make([]map[string]any, 0, len(renderedCopies))
	for _, entry := range renderedCopies {
		renderedSource := entry["source"]
		var sourcePath Path
		switch value := renderedSource.(type) {
		case string:
			sourcePath = outputRoot.Join(value)
		case map[string]string:
			// directMountEntry (used for "secret" classification copies)
			// returns map[string]string; the type switch must match it
			// explicitly — historically a silent miss here produced
			// sha256="" in the manifest for every secret copy.
			if hostSource, ok := value["source"]; ok {
				sourcePath = Path(hostSource)
			}
		case map[string]any:
			if hostSource, ok := value["source"].(string); ok {
				sourcePath = Path(hostSource)
			}
		}
		hash, err := pathMaterialSHA256(sourcePath)
		if err != nil {
			return "", fmt.Errorf("hash copy %v: %w", entry["target"], err)
		}
		copies = append(copies, map[string]any{
			"classification": entry["classification"],
			"dir_mode":       entry["dir_mode"],
			"file_mode":      entry["file_mode"],
			"kind":           entry["kind"],
			"sha256":         hash,
			"target":         entry["target"],
		})
	}
	credentials := map[string]map[string]any{}
	for _, key := range sortedKeys(renderedCredentials) {
		value := renderedCredentials[key]
		hash, err := pathMaterialSHA256(Path(value["source"]))
		if err != nil {
			return "", fmt.Errorf("hash credential %q: %w", key, err)
		}
		credentials[key] = map[string]any{
			"mount_path": value["mount_path"],
			"sha256":     hash,
		}
	}
	ssh := map[string]any{}
	if len(renderedSSH) > 0 {
		if value, ok := renderedSSH["config_assurance"].(string); ok {
			ssh["config_assurance"] = value
		} else {
			ssh["config_assurance"] = "off"
		}
		// ssh.config / ssh.known_hosts come from directMountEntry, which
		// returns map[string]string — historically these missed both
		// type assertions below and so were never hashed at all.
		if source, mountPath, ok := sshMountSource(renderedSSH, "config"); ok {
			hash, err := pathMaterialSHA256(Path(source))
			if err != nil {
				return "", fmt.Errorf("hash ssh config: %w", err)
			}
			ssh["config"] = map[string]any{
				"mount_path": mountPath,
				"sha256":     hash,
			}
		}
		if source, mountPath, ok := sshMountSource(renderedSSH, "known_hosts"); ok {
			hash, err := pathMaterialSHA256(Path(source))
			if err != nil {
				return "", fmt.Errorf("hash ssh known_hosts: %w", err)
			}
			ssh["known_hosts"] = map[string]any{
				"mount_path": mountPath,
				"sha256":     hash,
			}
		}
		if identities, ok := renderedSSH["identities"].([]map[string]any); ok {
			renderedIdentities := make([]map[string]any, 0, len(identities))
			for _, entry := range identities {
				hash, err := pathMaterialSHA256(Path(entry["source"].(string)))
				if err != nil {
					return "", fmt.Errorf("hash ssh identity %v: %w", entry["target_name"], err)
				}
				renderedIdentities = append(renderedIdentities, map[string]any{
					"mount_path":  entry["mount_path"],
					"sha256":      hash,
					"target_name": entry["target_name"],
				})
			}
			ssh["identities"] = renderedIdentities
		} else if identities, ok := renderedSSH["identities"].([]any); ok {
			renderedIdentities := make([]map[string]any, 0, len(identities))
			for _, rawEntry := range identities {
				entry := rawEntry.(map[string]any)
				hash, err := pathMaterialSHA256(Path(entry["source"].(string)))
				if err != nil {
					return "", fmt.Errorf("hash ssh identity %v: %w", entry["target_name"], err)
				}
				renderedIdentities = append(renderedIdentities, map[string]any{
					"mount_path":  entry["mount_path"],
					"sha256":      hash,
					"target_name": entry["target_name"],
				})
			}
			ssh["identities"] = renderedIdentities
		}
	}
	canonical, err := json.Marshal(map[string]any{
		"credentials":    credentials,
		"copies":         copies,
		"documents":      documents,
		"policy_sources": policySources,
		"ssh":            ssh,
	})
	if err != nil {
		return "", fmt.Errorf("marshal effective policy: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// pathMaterialSHA256 returns a hash that fingerprints the on-disk material
// at path.  Any I/O error during the walk is propagated; callers MUST refuse
// to emit a manifest entry when the fingerprint cannot be computed — a
// silent empty string here previously rounded-tripped as a valid hash and
// defeated the integrity check the function exists to provide.
func pathMaterialSHA256(path Path) (string, error) {
	info, err := os.Lstat(path.String())
	if err != nil {
		return "", fmt.Errorf("lstat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path.String())
		if err != nil {
			return "", fmt.Errorf("readlink %s: %w", path, err)
		}
		sum := sha256.Sum256([]byte("symlink:" + target))
		return hex.EncodeToString(sum[:]), nil
	}
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(path.String())
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:]), nil
	}
	if info.IsDir() {
		hasher := sha256.New()
		hasher.Write([]byte("dir\n"))
		children := []string{}
		if err := filepath.WalkDir(path.String(), func(current string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if current == path.String() {
				return nil
			}
			children = append(children, current)
			return nil
		}); err != nil {
			return "", fmt.Errorf("walk %s: %w", path, err)
		}
		sort.Slice(children, func(i, j int) bool {
			return filepath.ToSlash(strings.TrimPrefix(children[i], path.String()+string(filepath.Separator))) < filepath.ToSlash(strings.TrimPrefix(children[j], path.String()+string(filepath.Separator)))
		})
		for _, child := range children {
			info, err := os.Lstat(child)
			if err != nil {
				return "", fmt.Errorf("lstat %s: %w", child, err)
			}
			relative, err := filepath.Rel(path.String(), child)
			if err != nil {
				return "", fmt.Errorf("rel %s: %w", child, err)
			}
			relative = filepath.ToSlash(relative)
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(child)
				if err != nil {
					return "", fmt.Errorf("readlink %s: %w", child, err)
				}
				hasher.Write([]byte("symlink:" + relative + ":" + target + "\n"))
			case info.IsDir():
				hasher.Write([]byte("dir:" + relative + "\n"))
			case info.Mode().IsRegular():
				hasher.Write([]byte("file:" + relative + "\n"))
				data, err := os.ReadFile(child)
				if err != nil {
					return "", fmt.Errorf("read %s: %w", child, err)
				}
				hasher.Write(data)
				hasher.Write([]byte("\n"))
			default:
				return "", fmt.Errorf("unsupported file mode for %s: %s", child, info.Mode())
			}
		}
		return hex.EncodeToString(hasher.Sum(nil)), nil
	}
	return "", fmt.Errorf("unsupported file mode for %s: %s", path, info.Mode())
}

// sshMountSource pulls the source + mount_path pair out of the
// renderedSSH map for either of the directMountEntry-backed keys
// ("config", "known_hosts").  Those entries are map[string]string, so
// callers cannot use the map[string]any type assertion that the
// outer effectivePolicySHA256 code applies to identities.
func sshMountSource(renderedSSH map[string]any, key string) (source, mountPath string, ok bool) {
	raw, present := renderedSSH[key]
	if !present {
		return "", "", false
	}
	switch v := raw.(type) {
	case map[string]string:
		s, hasSource := v["source"]
		if !hasSource {
			return "", "", false
		}
		return s, v["mount_path"], true
	case map[string]any:
		s, hasSource := v["source"].(string)
		if !hasSource {
			return "", "", false
		}
		mp, _ := v["mount_path"].(string)
		return s, mp, true
	}
	return "", "", false
}

func ensureIsFile(source Path, label string) error {
	info, err := os.Stat(source.String())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must point at a file: %s", label, source)
	}
	return nil
}

func writeIndentedJSON(pathname string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(pathname, data, mode); err != nil {
		return err
	}
	return os.Chmod(pathname, mode)
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

func secretCopyTargets(renderedCopies []map[string]any) []string {
	targets := []string{}
	for _, entry := range renderedCopies {
		if classification, _ := entry["classification"].(string); classification == "secret" {
			if target, ok := entry["target"].(string); ok {
				targets = append(targets, target)
			}
		}
	}
	slices.Sort(targets)
	return targets
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func mapKeysSet(keys []string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	return allowed
}

// Path lives in path.go.  cloneMap / containsPath were replaced with
// stdlib maps.Clone / slices.Contains in PR #270.
