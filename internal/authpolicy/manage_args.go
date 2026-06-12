// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/omkhar/workcell/internal/pathutil"
)

// newCmdFlagSet constructs a flag.FlagSet whose error output and usage
// callback are wired identically across every parse* helper below.
// Each helper used to repeat this four-line preamble verbatim.
func newCmdFlagSet(program, command string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, usage(program)) }
	return fs
}

// requireNonEmptyFlags emits the canonical "missing required flags"
// usage+error when fs has positional args, or any of the provided
// pointer values is the empty string.  Callers list the *string values
// that the subcommand requires; the error is identical to what every
// hand-rolled parser produced before, preserving the bash exit-code
// contract.
func requireNonEmptyFlags(fs *flag.FlagSet, required ...*string) error {
	if fs.NArg() != 0 {
		fs.Usage()
		return fmt.Errorf("missing required flags")
	}
	for _, ptr := range required {
		if ptr == nil || *ptr == "" {
			fs.Usage()
			return fmt.Errorf("missing required flags")
		}
	}
	return nil
}

func parsePolicyPathArgs(program string, command string, args []string, stderr io.Writer) (string, error) {
	fs := newCmdFlagSet(program, command, stderr)
	policy := fs.String("policy", "", "")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if err := requireNonEmptyFlags(fs, policy); err != nil {
		return "", err
	}
	return resolveInputPath(*policy), nil
}

func parseInitArgs(program string, args []string, stderr io.Writer) (string, string, error) {
	fs := newCmdFlagSet(program, "init", stderr)
	policy := fs.String("policy", "", "")
	managedRoot := fs.String("managed-root", "", "")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	// init uses a bespoke error string ("--policy and --managed-root are
	// required") to match the bash predecessor's exact message; cannot
	// share requireNonEmptyFlags's "missing required flags" wording.
	if fs.NArg() != 0 || *policy == "" || *managedRoot == "" {
		fs.Usage()
		return "", "", fmt.Errorf("--policy and --managed-root are required")
	}
	return resolveInputPath(*policy), resolveInputPath(*managedRoot), nil
}

func parseSetArgs(program string, args []string, stderr io.Writer) (setOptions, error) {
	fs := newCmdFlagSet(program, "set", stderr)
	var opts setOptions
	fs.StringVar(&opts.policyPath, "policy", "", "")
	fs.StringVar(&opts.managedRoot, "managed-root", "", "")
	fs.StringVar(&opts.agent, "agent", "", "")
	fs.StringVar(&opts.credential, "credential", "", "")
	fs.StringVar(&opts.sourceRaw, "source", "", "")
	fs.StringVar(&opts.sourceBaseRaw, "source-base", "", "")
	fs.StringVar(&opts.resolver, "resolver", "", "")
	fs.BoolVar(&opts.ackHostResolver, "ack-host-resolver", false, "")
	if err := fs.Parse(args); err != nil {
		return setOptions{}, err
	}
	if err := requireNonEmptyFlags(fs, &opts.policyPath, &opts.managedRoot, &opts.agent, &opts.credential); err != nil {
		return setOptions{}, err
	}
	if _, ok := SupportedAgents[opts.agent]; !ok {
		return setOptions{}, fmt.Errorf("invalid agent: %s", opts.agent)
	}
	if _, ok := CredentialKeys[opts.credential]; !ok {
		return setOptions{}, fmt.Errorf("invalid credential: %s", opts.credential)
	}
	opts.policyPath = resolveInputPath(opts.policyPath)
	opts.managedRoot = resolveInputPath(opts.managedRoot)
	return opts, nil
}

func parseUnsetArgs(program string, args []string, stderr io.Writer) (string, string, string, error) {
	fs := newCmdFlagSet(program, "unset", stderr)
	policy := fs.String("policy", "", "")
	managedRoot := fs.String("managed-root", "", "")
	credential := fs.String("credential", "", "")
	if err := fs.Parse(args); err != nil {
		return "", "", "", err
	}
	if err := requireNonEmptyFlags(fs, policy, managedRoot, credential); err != nil {
		return "", "", "", err
	}
	if _, ok := CredentialKeys[*credential]; !ok {
		return "", "", "", fmt.Errorf("invalid credential: %s", *credential)
	}
	return resolveInputPath(*policy), resolveInputPath(*managedRoot), *credential, nil
}

func parseStatusArgs(program string, args []string, stderr io.Writer) (statusOptions, error) {
	fs := newCmdFlagSet(program, "status", stderr)
	var opts statusOptions
	fs.StringVar(&opts.policyPath, "policy", "", "")
	fs.StringVar(&opts.agent, "agent", "", "")
	fs.StringVar(&opts.mode, "mode", "strict", "")
	if err := fs.Parse(args); err != nil {
		return statusOptions{}, err
	}
	if err := requireNonEmptyFlags(fs, &opts.policyPath); err != nil {
		return statusOptions{}, err
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

func parseWhyArgs(program string, args []string, stderr io.Writer) (whyOptions, error) {
	fs := newCmdFlagSet(program, "why", stderr)
	var opts whyOptions
	fs.StringVar(&opts.policyPath, "policy", "", "")
	fs.StringVar(&opts.agent, "agent", "", "")
	fs.StringVar(&opts.mode, "mode", "", "")
	fs.StringVar(&opts.credential, "credential", "", "")
	if err := fs.Parse(args); err != nil {
		return whyOptions{}, err
	}
	if err := requireNonEmptyFlags(fs, &opts.policyPath, &opts.agent, &opts.mode, &opts.credential); err != nil {
		return whyOptions{}, err
	}
	if _, ok := SupportedAgents[opts.agent]; !ok {
		return whyOptions{}, fmt.Errorf("invalid agent: %s", opts.agent)
	}
	if _, ok := SupportedModes[opts.mode]; !ok {
		return whyOptions{}, fmt.Errorf("invalid mode: %s", opts.mode)
	}
	if _, ok := CredentialKeys[opts.credential]; !ok {
		return whyOptions{}, fmt.Errorf("invalid credential: %s", opts.credential)
	}
	opts.policyPath = resolveInputPath(opts.policyPath)
	return opts, nil
}

func parseBootstrapSummaryArgs(program string, args []string, stderr io.Writer) (bootstrapSummaryOptions, error) {
	fs := newCmdFlagSet(program, "bootstrap-summary", stderr)
	var opts bootstrapSummaryOptions
	fs.StringVar(&opts.agent, "agent", "", "")
	fs.StringVar(&opts.inputKinds, "input-kinds", "none", "")
	fs.StringVar(&opts.resolvers, "resolvers", "none", "")
	fs.StringVar(&opts.resolutionStates, "resolution-states", "none", "")
	fs.StringVar(&opts.providerReadyStates, "provider-ready-states", "none", "")
	if err := fs.Parse(args); err != nil {
		return bootstrapSummaryOptions{}, err
	}
	if err := requireNonEmptyFlags(fs, &opts.agent); err != nil {
		return bootstrapSummaryOptions{}, err
	}
	if _, ok := SupportedAgents[opts.agent]; !ok {
		return bootstrapSummaryOptions{}, fmt.Errorf("invalid agent: %s", opts.agent)
	}
	return opts, nil
}

func resolveInputPath(raw string) string {
	expanded, err := pathutil.ExpandUserPathStrictRequireNonEmpty(raw)
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
