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
	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/launcher"
	"github.com/omkhar/workcell/internal/shellproto"
)

// PrepareBundleOptions captures every bash global that the legacy
// prepare_injection_bundle helper consumed.  The bash caller supplies these
// via command-line flags on the helper subcommand.
type PrepareBundleOptions struct {
	// Agent is the target agent name (e.g. "claude", "codex"); required.
	Agent string
	// Mode is the agent execution mode (e.g. "auto", "manual"); required.
	Mode string
	// PolicyPath is an explicit injection-policy TOML path.  When empty
	// and UseDefaultPolicy is true, the default
	// DefaultInjectionPolicyPath is consulted.
	PolicyPath string
	// UseDefaultPolicy enables the implicit per-user policy fallback
	// when PolicyPath is empty.
	UseDefaultPolicy bool
	// AuthStatus, when true, runs the auth-status diagnostic path
	// (no bundle materialization).
	AuthStatus bool
	// Doctor, when true, runs the doctor diagnostic path.
	Doctor bool
	// Inspect, when true, runs the inspect diagnostic path.
	Inspect bool
	// DryRun, when true, plans the bundle without writing any files.
	DryRun bool
	// SelfStagingProbeSyntheticCodexAuth is a test-only flag that
	// stages a synthetic ~/.codex/auth.json credential to exercise the
	// self-staging probe code path.  Production callers MUST leave
	// this false.
	SelfStagingProbeSyntheticCodexAuth bool
	// SelfStagingProbeSyntheticClaudeExport is a test-only flag that
	// stages a synthetic Claude keychain export to exercise the
	// self-staging probe code path.  Production callers MUST leave
	// this false.
	SelfStagingProbeSyntheticClaudeExport bool
	// RealHome is the real host HOME, used to derive the default
	// policy and bundle parent paths.  Required.
	RealHome string
	// ProcessPID is the launching process PID; used to namespace the
	// per-run bundle directory under the cache parent.
	ProcessPID int
	// BundleParentOverride is an optional bundle parent directory.
	// Empty in production (the bash caller always supplies the
	// default); tests use this to redirect into a t.TempDir.
	BundleParentOverride string
}

// PrepareBundleResult is the byte-for-byte translation of the env-var state
// that the legacy bash helper produced.  Each field maps to one
// INJECTION_* / DIRECT_* shell variable so the bash shim can re-export the
// values verbatim.
type PrepareBundleResult struct {
	// InjectionBundleRoot is the absolute path to the per-run bundle
	// staging directory (INJECTION_BUNDLE_ROOT).
	InjectionBundleRoot string
	// DirectMountSpecPath is the path to the direct-mount spec file
	// (DIRECT_MOUNT_SPEC_PATH).
	DirectMountSpecPath string
	// DirectSourceMounts is the list of resolved source-mount paths
	// (DIRECT_SOURCE_MOUNTS, one per line in bash).
	DirectSourceMounts []string
	// InjectionPolicySHA256 is the SHA-256 of the policy file consumed
	// for this run (INJECTION_POLICY_SHA256).
	InjectionPolicySHA256 string
	// InjectionCredentialKeys is the newline-joined list of credential
	// keys the policy materialized (INJECTION_CREDENTIAL_KEYS).
	InjectionCredentialKeys string
	// InjectionCredentialInputKinds carries the per-credential input
	// kind classification (INJECTION_CREDENTIAL_INPUT_KINDS).
	InjectionCredentialInputKinds string
	// InjectionCredentialResolvers carries the per-credential resolver
	// names (INJECTION_CREDENTIAL_RESOLVERS).
	InjectionCredentialResolvers string
	// InjectionCredentialMaterialization carries the per-credential
	// materialization mode (INJECTION_CREDENTIAL_MATERIALIZATION).
	InjectionCredentialMaterialization string
	// InjectionCredentialResolutionStates carries the per-credential
	// resolution-state token (INJECTION_CREDENTIAL_RESOLUTION_STATES).
	InjectionCredentialResolutionStates string
	// InjectionProviderAuthReadyStates carries the provider-auth
	// readiness state lines (INJECTION_PROVIDER_AUTH_READY_STATES).
	InjectionProviderAuthReadyStates string
	// InjectionSharedAuthReadyStates carries the shared-auth readiness
	// state lines (INJECTION_SHARED_AUTH_READY_STATES).
	InjectionSharedAuthReadyStates string
	// InjectionExtraEndpoints carries any additional endpoint URLs the
	// policy declared (INJECTION_EXTRA_ENDPOINTS).
	InjectionExtraEndpoints string
	// InjectionSSHEnabled is "1" when the policy enables SSH
	// passthrough, "0" otherwise (INJECTION_SSH_ENABLED).
	InjectionSSHEnabled string
	// InjectionSSHConfigAssurance carries the SSH config assurance
	// level marker (INJECTION_SSH_CONFIG_ASSURANCE).
	InjectionSSHConfigAssurance string
	// InjectionSecretCopyTargets carries the secret-copy target paths
	// (INJECTION_SECRET_COPY_TARGETS).
	InjectionSecretCopyTargets string
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
	if runErr := authresolve.Run(resolverArgs, devNull{}, os.Stderr); runErr != nil {
		// authresolve.Run returns *cliexit.ExitCodeError; lift the Code
		// into the diagnostic so the existing exit-status message stays
		// byte-identical.
		code := 1
		if ec, ok := cliexit.IsExitCodeError(runErr); ok {
			code = ec.Code
		}
		return nil, fmt.Errorf("resolve_credential_sources exited %d", code)
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
// this helper used).  See prepare_bundle_test.go::TestInstallSyntheticProbeEnvRestoresHomeOnPartialFailure
// for the regression that motivated this contract.
func installSyntheticProbeEnv(bundleRoot string, syntheticCodex, syntheticClaude bool) (func(), error) {
	originalHome, hadHome := os.LookupEnv("HOME")
	originalExport, hadExport := os.LookupEnv("WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE")

	cleanup := func() {
		if hadHome {
			_ = os.Setenv("HOME", originalHome)
		} else {
			_ = os.Unsetenv("HOME")
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
//
// Fail-closed: if any field value contains a forbidden control
// character (newline or carriage return) the function returns an error
// and emits NOTHING — the caller MUST surface the error so the bash
// shim never re-imports a partial plan with a smuggled-in extra KEY=
// line.  Upstream extractors already constrain values to printable
// tokens, so this should only fire if a contract break slips in.
func FormatBundleResultForShell(result *PrepareBundleResult) (string, error) {
	if result == nil {
		return "", nil
	}
	fields := make([]shellproto.Field, 0, 16+len(result.DirectSourceMounts))
	fields = append(fields,
		shellproto.Field{Key: "INJECTION_BUNDLE_ROOT", Value: result.InjectionBundleRoot},
		shellproto.Field{Key: "DIRECT_MOUNT_SPEC_PATH", Value: result.DirectMountSpecPath},
		shellproto.Field{Key: "DIRECT_SOURCE_MOUNTS_COUNT", Value: strconv.Itoa(len(result.DirectSourceMounts))},
	)
	for i, arg := range result.DirectSourceMounts {
		fields = append(fields, shellproto.Field{
			Key:   fmt.Sprintf("DIRECT_SOURCE_MOUNTS_%d", i),
			Value: arg,
		})
	}
	fields = append(fields,
		shellproto.Field{Key: "INJECTION_POLICY_SHA256", Value: result.InjectionPolicySHA256},
		shellproto.Field{Key: "INJECTION_CREDENTIAL_KEYS", Value: result.InjectionCredentialKeys},
		shellproto.Field{Key: "INJECTION_CREDENTIAL_INPUT_KINDS", Value: result.InjectionCredentialInputKinds},
		shellproto.Field{Key: "INJECTION_CREDENTIAL_RESOLVERS", Value: result.InjectionCredentialResolvers},
		shellproto.Field{Key: "INJECTION_CREDENTIAL_MATERIALIZATION", Value: result.InjectionCredentialMaterialization},
		shellproto.Field{Key: "INJECTION_CREDENTIAL_RESOLUTION_STATES", Value: result.InjectionCredentialResolutionStates},
		shellproto.Field{Key: "INJECTION_PROVIDER_AUTH_READY_STATES", Value: result.InjectionProviderAuthReadyStates},
		shellproto.Field{Key: "INJECTION_SHARED_AUTH_READY_STATES", Value: result.InjectionSharedAuthReadyStates},
		shellproto.Field{Key: "INJECTION_EXTRA_ENDPOINTS", Value: result.InjectionExtraEndpoints},
		shellproto.Field{Key: "INJECTION_SSH_ENABLED", Value: result.InjectionSSHEnabled},
		shellproto.Field{Key: "INJECTION_SSH_CONFIG_ASSURANCE", Value: result.InjectionSSHConfigAssurance},
		shellproto.Field{Key: "INJECTION_SECRET_COPY_TARGETS", Value: result.InjectionSecretCopyTargets},
	)
	var b strings.Builder
	if err := shellproto.WriteFields(&b, fields); err != nil {
		// Drop the partial buffer on the floor: the bash shim
		// MUST NOT see a half-emitted plan.
		return "", fmt.Errorf("format bundle result for shell: %w", err)
	}
	return b.String(), nil
}
