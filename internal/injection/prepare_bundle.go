// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/omkhar/workcell/internal/authresolve"
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/launcher"
)

// PrepareBundleOptions captures every bash global that the legacy
// prepare_injection_bundle helper consumed.  The bash caller supplies these
// via command-line flags on the launcher subcommand.
type PrepareBundleOptions struct {
	Agent                                 string
	Mode                                  string
	PolicyPath                            string
	UseDefaultPolicy                      bool
	AuthStatus                            bool
	Doctor                                bool
	Inspect                               bool
	DryRun                                bool
	SelfStagingProbeSyntheticCodexAuth    bool
	SelfStagingProbeSyntheticClaudeExport bool
	RealHome                              string
	ProcessPID                            int
	BundleParentOverride                  string // optional; bash currently always supplies the default
}

// PrepareBundleResult is the byte-for-byte translation of the env-var state
// that the legacy bash helper produced.  Each field maps to one
// INJECTION_* / DIRECT_* shell variable so the bash shim can re-export the
// values verbatim.
type PrepareBundleResult struct {
	InjectionBundleRoot                 string
	DirectMountSpecPath                 string
	DirectSourceMounts                  []string
	InjectionPolicySHA256               string
	InjectionCredentialKeys             string
	InjectionCredentialInputKinds       string
	InjectionCredentialResolvers        string
	InjectionCredentialMaterialization  string
	InjectionCredentialResolutionStates string
	InjectionProviderAuthReadyStates    string
	InjectionSharedAuthReadyStates      string
	InjectionExtraEndpoints             string
	InjectionSSHEnabled                 string
	InjectionSSHConfigAssurance         string
	InjectionSecretCopyTargets          string
}

// DefaultInjectionPolicyPath returns the path the bash helper would have
// produced for the implicit per-user policy.
func DefaultInjectionPolicyPath(realHome string) string {
	return filepath.Join(realHome, ".config", "workcell", "injection-policy.toml")
}

// DefaultInjectionBundleParent returns the canonical parent directory the
// bash helper used as the host-inputs cache root.  The caller is expected
// to pass a real home path that has already been canonicalized.
func DefaultInjectionBundleParent(realHome string) string {
	return filepath.Join(realHome, "Library", "Caches", "colima", "workcell-host-inputs")
}

// PrepareBundle implements the legacy prepare_injection_bundle helper.
//
// The function returns a fully-populated PrepareBundleResult.  When no policy
// is configured (legacy: INJECTION_POLICY empty and USE_DEFAULT_INJECTION_POLICY=0,
// or the default policy file does not exist), the result has empty string
// fields and an SSH-enabled marker of "0", mirroring the bash early-return
// branch.
func PrepareBundle(opts PrepareBundleOptions) (*PrepareBundleResult, error) {
	if opts.Agent == "" || opts.Mode == "" {
		return nil, errors.New("agent and mode are required")
	}
	if opts.RealHome == "" {
		return nil, errors.New("real home is required")
	}

	policyPath := opts.PolicyPath
	if policyPath == "" && opts.UseDefaultPolicy {
		candidate := DefaultInjectionPolicyPath(opts.RealHome)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			policyPath = candidate
		}
	}

	bundleParent := opts.BundleParentOverride
	if bundleParent == "" {
		bundleParent = DefaultInjectionBundleParent(opts.RealHome)
	}
	if err := os.MkdirAll(bundleParent, 0o755); err != nil {
		return nil, err
	}
	_ = os.Chmod(bundleParent, 0o700) // best effort; bash had "|| true"
	if err := hoststate.CleanupStaleInjectionBundles(bundleParent); err != nil {
		return nil, err
	}

	// Empty-policy branch: the bash helper cleared every INJECTION_* env var
	// and the SSH flag, then returned.
	if policyPath == "" {
		return &PrepareBundleResult{InjectionSSHEnabled: "0", InjectionSSHConfigAssurance: "off"}, nil
	}

	canonicalPolicy, err := launcher.CanonicalizePath(policyPath)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(canonicalPolicy); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Injection policy file does not exist: %s", canonicalPolicy)
	}

	bundleRoot, err := os.MkdirTemp(bundleParent, "workcell-injections.*")
	if err != nil {
		return nil, err
	}
	bundleRoot, err = launcher.CanonicalizePath(bundleRoot)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(bundleRoot, 0o700); err != nil {
		return nil, err
	}
	if err := launcher.WriteProfileOwner(filepath.Join(bundleRoot, "owner.json"), opts.ProcessPID); err != nil {
		return nil, err
	}

	resolutionMode := "launch"
	if opts.AuthStatus || opts.Doctor || opts.Inspect || opts.DryRun {
		resolutionMode = "metadata"
	}
	resolvedPolicyPath := filepath.Join(bundleRoot, "resolved-policy.toml")
	resolverMetadataPath := filepath.Join(bundleRoot, "resolver-metadata.json")

	// Stage synthetic probe inputs (test-only) before invoking the resolver,
	// matching the env-var contract the bash helper installed for the child
	// process.  The vars are restored unconditionally so concurrent callers
	// in the same Go process do not leak state.
	restoreEnv, err := installSyntheticProbeEnv(bundleRoot,
		opts.SelfStagingProbeSyntheticCodexAuth,
		opts.SelfStagingProbeSyntheticClaudeExport,
	)
	if err != nil {
		return nil, err
	}
	defer restoreEnv()

	resolverArgs := []string{
		"--policy", canonicalPolicy,
		"--agent", opts.Agent,
		"--mode", opts.Mode,
		"--resolution-mode", resolutionMode,
		"--output-policy", resolvedPolicyPath,
		"--output-metadata", resolverMetadataPath,
		"--output-root", bundleRoot,
	}
	if rc := authresolve.Run(resolverArgs, devNull{}, os.Stderr); rc != 0 {
		return nil, fmt.Errorf("resolve_credential_sources exited %d", rc)
	}

	if err := RunRenderInjectionBundle(resolvedPolicyPath, opts.Agent, opts.Mode, bundleRoot, resolverMetadataPath); err != nil {
		return nil, err
	}
	manifestPath := filepath.Join(bundleRoot, "manifest.json")
	if info, err := os.Stat(manifestPath); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Injection manifest was not rendered: %s", manifestPath)
	}

	mountSpecPath, err := launcher.CanonicalizePath(bundleRoot + ".mounts.json")
	if err != nil {
		return nil, err
	}
	if err := RunExtractDirectMounts(manifestPath, mountSpecPath); err != nil {
		return nil, err
	}

	dockerArgs, err := StageDirectMounts(bundleRoot, mountSpecPath)
	if err != nil {
		return nil, err
	}

	manifestLines, err := hoststate.ManifestMetadataLines(manifestPath)
	if err != nil {
		return nil, err
	}
	resolverLines, err := hoststate.ResolverMetadataLines(resolverMetadataPath)
	if err != nil {
		return nil, err
	}
	if len(manifestLines) < 6 || len(resolverLines) < 6 {
		return nil, errors.New("manifest or resolver metadata returned fewer than six lines")
	}

	return &PrepareBundleResult{
		InjectionBundleRoot:                 bundleRoot,
		DirectMountSpecPath:                 mountSpecPath,
		DirectSourceMounts:                  dockerArgs,
		InjectionPolicySHA256:               manifestLines[0],
		InjectionCredentialKeys:             manifestLines[1],
		InjectionExtraEndpoints:             manifestLines[2],
		InjectionSSHEnabled:                 manifestLines[3],
		InjectionSSHConfigAssurance:         manifestLines[4],
		InjectionSecretCopyTargets:          manifestLines[5],
		InjectionCredentialInputKinds:       resolverLines[0],
		InjectionCredentialResolvers:        resolverLines[1],
		InjectionCredentialMaterialization:  resolverLines[2],
		InjectionCredentialResolutionStates: resolverLines[3],
		InjectionProviderAuthReadyStates:    resolverLines[4],
		InjectionSharedAuthReadyStates:      resolverLines[5],
	}, nil
}

// installSyntheticProbeEnv mirrors the bash branches that stage synthetic
// credential exports for the self-staging probes.  It returns a restore
// callback the caller MUST defer; otherwise the calling Go process inherits
// the test-only env vars.
func installSyntheticProbeEnv(bundleRoot string, syntheticCodex, syntheticClaude bool) (func(), error) {
	restore := func() {}
	originalHome, hadHome := os.LookupEnv("HOME")
	originalExport, hadExport := os.LookupEnv("WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE")

	cleanup := func() {
		if hadHome {
			_ = os.Setenv("HOME", originalHome)
		}
		if hadExport {
			_ = os.Setenv("WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE", originalExport)
		} else {
			_ = os.Unsetenv("WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE")
		}
	}

	if syntheticCodex {
		syntheticCodexHome := filepath.Join(bundleRoot, "self-staging-probe-codex-home")
		syntheticCodexAuth := filepath.Join(syntheticCodexHome, ".codex", "auth.json")
		if err := os.MkdirAll(filepath.Dir(syntheticCodexAuth), 0o700); err != nil {
			return restore, err
		}
		if err := writeFile0600(syntheticCodexAuth, []byte("{\"token\":\"codex\"}\n")); err != nil {
			return restore, err
		}
		if err := os.Setenv("HOME", syntheticCodexHome); err != nil {
			return restore, err
		}
	}
	if syntheticClaude {
		syntheticClaudeExport := filepath.Join(bundleRoot, "self-staging-probe-claude-export.json")
		if err := writeFile0600(syntheticClaudeExport, []byte("{\"token\":\"claude\"}\n")); err != nil {
			return restore, err
		}
		if err := os.Setenv("WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE", syntheticClaudeExport); err != nil {
			return restore, err
		}
	}
	return cleanup, nil
}

// writeFile0600 writes data to path with mode 0o600 (matching the bash
// "umask 077" + printf > path sequence for synthetic probes).
func writeFile0600(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// devNull discards writes; we use it where bash redirected ">/dev/null".
type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// FormatBundleResultForShell emits the result as one KEY=VALUE line per
// variable, in a format that bash can re-import via "while read".  Array
// values use a NUL byte separator within the value and are tagged with a
// leading "\0" so the shim can split on \0 rather than parsing quoting.
func FormatBundleResultForShell(result *PrepareBundleResult) string {
	if result == nil {
		return ""
	}
	var out string
	out += "INJECTION_BUNDLE_ROOT=" + result.InjectionBundleRoot + "\n"
	out += "DIRECT_MOUNT_SPEC_PATH=" + result.DirectMountSpecPath + "\n"
	out += "DIRECT_SOURCE_MOUNTS_COUNT=" + strconv.Itoa(len(result.DirectSourceMounts)) + "\n"
	for i, arg := range result.DirectSourceMounts {
		out += "DIRECT_SOURCE_MOUNTS_" + strconv.Itoa(i) + "=" + arg + "\n"
	}
	out += "INJECTION_POLICY_SHA256=" + result.InjectionPolicySHA256 + "\n"
	out += "INJECTION_CREDENTIAL_KEYS=" + result.InjectionCredentialKeys + "\n"
	out += "INJECTION_CREDENTIAL_INPUT_KINDS=" + result.InjectionCredentialInputKinds + "\n"
	out += "INJECTION_CREDENTIAL_RESOLVERS=" + result.InjectionCredentialResolvers + "\n"
	out += "INJECTION_CREDENTIAL_MATERIALIZATION=" + result.InjectionCredentialMaterialization + "\n"
	out += "INJECTION_CREDENTIAL_RESOLUTION_STATES=" + result.InjectionCredentialResolutionStates + "\n"
	out += "INJECTION_PROVIDER_AUTH_READY_STATES=" + result.InjectionProviderAuthReadyStates + "\n"
	out += "INJECTION_SHARED_AUTH_READY_STATES=" + result.InjectionSharedAuthReadyStates + "\n"
	out += "INJECTION_EXTRA_ENDPOINTS=" + result.InjectionExtraEndpoints + "\n"
	out += "INJECTION_SSH_ENABLED=" + result.InjectionSSHEnabled + "\n"
	out += "INJECTION_SSH_CONFIG_ASSURANCE=" + result.InjectionSSHConfigAssurance + "\n"
	out += "INJECTION_SECRET_COPY_TARGETS=" + result.InjectionSecretCopyTargets + "\n"
	return out
}
