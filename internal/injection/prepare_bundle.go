// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	// in the same Go process do not leak state.  Defer the restore BEFORE
	// checking err: installSyntheticProbeEnv may fail after it has already
	// pointed HOME at the synthetic codex home (e.g. the synthetic Claude
	// export branch fails), and we still owe the caller a clean HOME.
	restoreEnv, err := installSyntheticProbeEnv(bundleRoot,
		opts.SelfStagingProbeSyntheticCodexAuth,
		opts.SelfStagingProbeSyntheticClaudeExport,
	)
	defer restoreEnv()
	if err != nil {
		return nil, err
	}

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
// callback the caller MUST defer BEFORE checking the error; otherwise the
// calling Go process inherits the test-only env vars (specifically HOME)
// on partial failure.  The returned callback is always non-nil and always
// safe to call exactly once.
//
// Failure model: every os.Setenv/os.MkdirAll/writeFile0600 call may
// happen after we've already pointed HOME at the synthetic codex home,
// so the partial-state cleanup MUST always be the canonical restore
// callback (not the no-op `func(){}` shadow that an earlier draft of
// this helper used).  See prepare_bundle_test.go::TestPrepareBundleRestoresHOMEOnPartialFailure
// for the regression that motivated this contract.
func installSyntheticProbeEnv(bundleRoot string, syntheticCodex, syntheticClaude bool) (func(), error) {
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
			return cleanup, err
		}
		if err := writeFile0600(syntheticCodexAuth, []byte("{\"token\":\"codex\"}\n")); err != nil {
			return cleanup, err
		}
		if err := os.Setenv("HOME", syntheticCodexHome); err != nil {
			return cleanup, err
		}
	}
	if syntheticClaude {
		syntheticClaudeExport := filepath.Join(bundleRoot, "self-staging-probe-claude-export.json")
		if err := writeFile0600(syntheticClaudeExport, []byte("{\"token\":\"claude\"}\n")); err != nil {
			return cleanup, err
		}
		if err := os.Setenv("WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE", syntheticClaudeExport); err != nil {
			return cleanup, err
		}
	}
	return cleanup, nil
}

// writeFile0600 writes data to path with mode 0o600 (matching the bash
// "umask 077" + printf > path sequence for synthetic probes).  The
// parent directory is created with 0o700 — the synthetic probe inputs
// MUST NOT be readable by other users on the host.
func writeFile0600(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// devNull discards writes; we use it where bash redirected ">/dev/null".
type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// FormatBundleResultForShell emits the result as one KEY=VALUE line per
// variable, in a format that bash can re-import via "while read".  The
// DirectSourceMounts slice is emitted as a count followed by per-index
// keyed lines (DIRECT_SOURCE_MOUNTS_COUNT=N then
// DIRECT_SOURCE_MOUNTS_0=val, DIRECT_SOURCE_MOUNTS_1=val, ...) so the
// shim can rebuild the bash array without needing to parse any in-line
// separator.
func FormatBundleResultForShell(result *PrepareBundleResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "INJECTION_BUNDLE_ROOT=%s\n", result.InjectionBundleRoot)
	fmt.Fprintf(&b, "DIRECT_MOUNT_SPEC_PATH=%s\n", result.DirectMountSpecPath)
	fmt.Fprintf(&b, "DIRECT_SOURCE_MOUNTS_COUNT=%s\n", strconv.Itoa(len(result.DirectSourceMounts)))
	for i, arg := range result.DirectSourceMounts {
		fmt.Fprintf(&b, "DIRECT_SOURCE_MOUNTS_%d=%s\n", i, arg)
	}
	fmt.Fprintf(&b, "INJECTION_POLICY_SHA256=%s\n", result.InjectionPolicySHA256)
	fmt.Fprintf(&b, "INJECTION_CREDENTIAL_KEYS=%s\n", result.InjectionCredentialKeys)
	fmt.Fprintf(&b, "INJECTION_CREDENTIAL_INPUT_KINDS=%s\n", result.InjectionCredentialInputKinds)
	fmt.Fprintf(&b, "INJECTION_CREDENTIAL_RESOLVERS=%s\n", result.InjectionCredentialResolvers)
	fmt.Fprintf(&b, "INJECTION_CREDENTIAL_MATERIALIZATION=%s\n", result.InjectionCredentialMaterialization)
	fmt.Fprintf(&b, "INJECTION_CREDENTIAL_RESOLUTION_STATES=%s\n", result.InjectionCredentialResolutionStates)
	fmt.Fprintf(&b, "INJECTION_PROVIDER_AUTH_READY_STATES=%s\n", result.InjectionProviderAuthReadyStates)
	fmt.Fprintf(&b, "INJECTION_SHARED_AUTH_READY_STATES=%s\n", result.InjectionSharedAuthReadyStates)
	fmt.Fprintf(&b, "INJECTION_EXTRA_ENDPOINTS=%s\n", result.InjectionExtraEndpoints)
	fmt.Fprintf(&b, "INJECTION_SSH_ENABLED=%s\n", result.InjectionSSHEnabled)
	fmt.Fprintf(&b, "INJECTION_SSH_CONFIG_ASSURANCE=%s\n", result.InjectionSSHConfigAssurance)
	fmt.Fprintf(&b, "INJECTION_SECRET_COPY_TARGETS=%s\n", result.InjectionSecretCopyTargets)
	return b.String()
}
