// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/omkhar/workcell/internal/adapters"
	"github.com/omkhar/workcell/internal/injectionpolicy"
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
		var identityEntries []map[string]any
		identitiesFound := false
		switch v := renderedSSH["identities"].(type) {
		case []map[string]any:
			identitiesFound = true
			identityEntries = v
		case []any:
			identitiesFound = true
			identityEntries = make([]map[string]any, 0, len(v))
			for _, rawEntry := range v {
				identityEntries = append(identityEntries, rawEntry.(map[string]any))
			}
		}
		if identitiesFound {
			renderedIdentities := make([]map[string]any, 0, len(identityEntries))
			for _, entry := range identityEntries {
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
