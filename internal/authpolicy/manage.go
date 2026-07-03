// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"fmt"
	"io"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/providerid"
)

var (
	canonicalCredentialDestinations = map[string][2]string{
		"codex_auth":           {providerid.Codex, "auth.json"},
		"copilot_github_token": {providerid.Copilot, "github-token.txt"},
		"claude_api_key":       {providerid.Claude, "api-key.txt"},
		"claude_mcp":           {providerid.Claude, "mcp.json"},
		"gemini_env":           {providerid.Gemini, "gemini.env"},
		"gemini_oauth":         {providerid.Gemini, "oauth_creds.json"},
		"gemini_projects":      {providerid.Gemini, "projects.json"},
		"gcloud_adc":           {providerid.Gemini, "gcloud-adc.json"},
		"github_hosts":         {"shared", "github-hosts.yml"},
		"github_config":        {"shared", "github-config.yml"},
	}
	statusOrder = map[string][]string{
		providerid.Codex:   {"codex_auth"},
		providerid.Copilot: {"copilot_github_token"},
		providerid.Claude:  {"claude_api_key", "claude_auth"},
		providerid.Gemini:  {"gemini_env", "gemini_oauth"},
	}
	entryAllowedKeys = map[string]struct{}{
		"source":          {},
		"resolver":        {},
		"materialization": {},
		"providers":       {},
		"modes":           {},
	}
)

// Run is the byte-for-byte translation of the bash
// workcell-manage-injection-policy entry point.  It writes diagnostics
// to stderr exactly as the original did and returns a
// *cliexit.ExitCodeError encoding the bash exit-code contract; the
// caller (cmd/workcell-hostutil/main.go) recovers Code via errors.As
// and propagates it to os.Exit.  Returning the typed error rather than
// an int keeps the package on the canonical error-returning idiom
// every other Go CLI translation in this repo uses.
func Run(program string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage(program))
		return &cliexit.ExitCodeError{Code: 2}
	}
	switch args[0] {
	case "init":
		policyPath, managedRoot, err := parseInitArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandInit(policyPath, managedRoot); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		fmt.Fprintln(stdout, "policy_path="+resolveOutputPath(policyPath))
		fmt.Fprintln(stdout, "managed_root="+resolveOutputPath(managedRoot))
		return nil
	case "set":
		opts, err := parseSetArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandSet(opts); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		printSetOutput(stdout, opts)
		return nil
	case "unset":
		policyPath, managedRoot, credential, err := parseUnsetArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		removed, err := commandUnset(policyPath, managedRoot, credential)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		fmt.Fprintln(stdout, "policy_path="+policyPath)
		fmt.Fprintln(stdout, "credential="+credential)
		fmt.Fprintf(stdout, "removed=%d\n", removed)
		return nil
	case "status":
		opts, err := parseStatusArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandStatus(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "show":
		policyPath, err := parsePolicyPathArgs(program, "show", args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandShow(policyPath, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "validate":
		policyPath, err := parsePolicyPathArgs(program, "validate", args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandValidate(policyPath, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "diff":
		policyPath, err := parsePolicyPathArgs(program, "diff", args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandDiff(policyPath, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "why":
		opts, err := parseWhyArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandWhy(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "bootstrap-summary":
		opts, err := parseBootstrapSummaryArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandBootstrapSummary(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	default:
		fmt.Fprintln(stderr, usage(program))
		fmt.Fprintf(stderr, "%s: unsupported command: %s\n", program, args[0])
		return &cliexit.ExitCodeError{Code: 2}
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

type whyOptions struct {
	policyPath string
	agent      string
	mode       string
	credential string
}

type bootstrapSummaryOptions struct {
	agent               string
	inputKinds          string
	resolvers           string
	resolutionStates    string
	providerReadyStates string
}

type credentialSelectionReport struct {
	selected  bool
	reason    string
	readiness string
	inputKind string
	providers []string
	modes     []string
	resolver  string
}

type bootstrapSummary struct {
	state    string
	path     string
	support  string
	handoff  string
	doc      string
	nextStep string
}

func usage(program string) string {
	if program == "" {
		program = "workcell-manage-injection-policy"
	}
	return fmt.Sprintf(
		"Usage: %s {init,set,unset,status,show,validate,diff,why} ...",
		program,
	)
}
