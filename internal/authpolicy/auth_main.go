// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/launcher"
)

func exit2(format string, args ...any) error {
	return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf(format, args...)}
}

// AuthMain implements `workcell auth <subcommand>`, the Go translation
// of the bash auth_main function in scripts/workcell. The user-visible
// CLI surface is kept byte-identical: argument parsing, the four
// subcommand verbs (init, set, unset, status), and the underlying
// invocation of the in-process manage_injection_policy command
// (authpolicy.Run) all mirror the bash behaviour exactly.
//
// The first arg may be `--base=PATH`; when present it is used as the
// resolver base for relative --injection-policy / --managed-root /
// --source paths, replicating the bash `resolve_host_path` helper that
// passed `--base "$(pwd -P)"` through every host-path lookup.
func AuthMain(args []string) error {
	return authMain(args, os.Stdout, os.Stderr)
}

func authMain(args []string, stdout, stderr io.Writer) error {
	base, rest := consumeBaseArg(args)

	subcommand := ""
	if len(rest) > 0 {
		subcommand = rest[0]
	}

	switch subcommand {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, AuthUsageText())
		return nil
	case "":
		fmt.Fprint(stderr, AuthUsageText())
		return &cliexit.ExitCodeError{Code: 2, Message: ""}
	}
	rest = rest[1:]

	policyPath := defaultInjectionPolicyPath()
	managedRoot := defaultManagedCredentialsRoot()

	switch subcommand {
	case "init":
		return authInit(rest, base, policyPath, managedRoot, stdout, stderr)
	case "set":
		return authSet(rest, base, policyPath, managedRoot, stdout, stderr)
	case "unset":
		return authUnset(rest, base, policyPath, managedRoot, stdout, stderr)
	case "status":
		return authStatus(rest, base, policyPath, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Unsupported workcell auth command: %s\n", subcommand)
		fmt.Fprint(stderr, AuthUsageText())
		return &cliexit.ExitCodeError{Code: 2, Message: ""}
	}
}

func consumeBaseArg(args []string) (base string, rest []string) {
	if len(args) > 0 && strings.HasPrefix(args[0], "--base=") {
		base = strings.TrimPrefix(args[0], "--base=")
		return base, args[1:]
	}
	return "", args
}

func configHome() string {
	home, err := launcher.RealHome()
	if err != nil || home == "" {
		home, _ = os.UserHomeDir()
	}
	return home + "/.config/workcell"
}

func defaultInjectionPolicyPath() string {
	return configHome() + "/injection-policy.toml"
}

func defaultManagedCredentialsRoot() string {
	return configHome() + "/credentials"
}

// rawOptionValueOrDie mirrors scripts/workcell raw_option_value_or_die:
// the value may not be empty.
func rawOptionValueOrDie(option, value string) (string, error) {
	if value == "" {
		return "", exit2("Option %s requires a value.", option)
	}
	return value, nil
}

// optionValueOrDie mirrors scripts/workcell option_value_or_die: the
// value may not be empty and may not start with `--`.
func optionValueOrDie(option, value string) (string, error) {
	if value == "" || strings.HasPrefix(value, "--") {
		return "", exit2("Option %s requires a value.", option)
	}
	return value, nil
}

func resolveHostPath(raw, base string) (string, error) {
	return launcher.CanonicalizePathFrom(raw, base)
}

func authInit(args []string, base, policyPath, managedRoot string, stdout, stderr io.Writer) error {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--injection-policy":
			v, err := nextValue(args, i, false)
			if err != nil {
				return err
			}
			policyPath = v
			i++
		case "--managed-root":
			v, err := nextValue(args, i, false)
			if err != nil {
				return err
			}
			managedRoot = v
			i++
		case "-h", "--help":
			fmt.Fprint(stdout, AuthUsageText())
			return nil
		default:
			fmt.Fprintf(stderr, "Unsupported workcell auth init option: %s\n", args[i])
			fmt.Fprint(stderr, AuthUsageText())
			return &cliexit.ExitCodeError{Code: 2, Message: ""}
		}
	}
	resolvedPolicy, err := resolveHostPath(policyPath, base)
	if err != nil {
		return exit2("%s", err.Error())
	}
	resolvedRoot, err := resolveHostPath(managedRoot, base)
	if err != nil {
		return exit2("%s", err.Error())
	}
	return runManagePolicy([]string{
		"init",
		"--policy", resolvedPolicy,
		"--managed-root", resolvedRoot,
	}, stdout, stderr)
}

func authSet(args []string, base, policyPath, managedRoot string, stdout, stderr io.Writer) error {
	var (
		agent           string
		credential      string
		sourcePath      string
		resolverName    string
		ackHostResolver bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--injection-policy":
			v, err := nextValue(args, i, false)
			if err != nil {
				return err
			}
			policyPath = v
			i++
		case "--managed-root":
			v, err := nextValue(args, i, false)
			if err != nil {
				return err
			}
			managedRoot = v
			i++
		case "--agent":
			v, err := nextValue(args, i, true)
			if err != nil {
				return err
			}
			agent = v
			i++
		case "--credential":
			v, err := nextValue(args, i, true)
			if err != nil {
				return err
			}
			credential = v
			i++
		case "--source":
			v, err := nextValue(args, i, false)
			if err != nil {
				return err
			}
			sourcePath = v
			i++
		case "--resolver":
			v, err := nextValue(args, i, true)
			if err != nil {
				return err
			}
			resolverName = v
			i++
		case "--ack-host-resolver":
			ackHostResolver = true
		case "-h", "--help":
			fmt.Fprint(stdout, AuthUsageText())
			return nil
		default:
			fmt.Fprintf(stderr, "Unsupported workcell auth set option: %s\n", args[i])
			fmt.Fprint(stderr, AuthUsageText())
			return &cliexit.ExitCodeError{Code: 2, Message: ""}
		}
	}
	if agent == "" {
		return exit2("workcell auth set requires --agent.")
	}
	if credential == "" {
		return exit2("workcell auth set requires --credential.")
	}
	if resolverName != "" && !ackHostResolver {
		return exit2("workcell auth set requires --ack-host-resolver with --resolver.")
	}
	resolvedPolicy, err := resolveHostPath(policyPath, base)
	if err != nil {
		return exit2("%s", err.Error())
	}
	resolvedRoot, err := resolveHostPath(managedRoot, base)
	if err != nil {
		return exit2("%s", err.Error())
	}
	authCmd := []string{
		"set",
		"--policy", resolvedPolicy,
		"--managed-root", resolvedRoot,
		"--agent", agent,
		"--credential", credential,
	}
	if sourcePath != "" {
		sourceBase := base
		if sourceBase == "" {
			if cwd, err := os.Getwd(); err == nil {
				sourceBase = cwd
			}
		}
		authCmd = append(authCmd, "--source", sourcePath, "--source-base", sourceBase)
	}
	if resolverName != "" {
		authCmd = append(authCmd, "--resolver", resolverName, "--ack-host-resolver")
	}
	return runManagePolicy(authCmd, stdout, stderr)
}

func authUnset(args []string, base, policyPath, managedRoot string, stdout, stderr io.Writer) error {
	var credential string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--injection-policy":
			v, err := nextValue(args, i, false)
			if err != nil {
				return err
			}
			policyPath = v
			i++
		case "--managed-root":
			v, err := nextValue(args, i, false)
			if err != nil {
				return err
			}
			managedRoot = v
			i++
		case "--credential":
			v, err := nextValue(args, i, true)
			if err != nil {
				return err
			}
			credential = v
			i++
		case "-h", "--help":
			fmt.Fprint(stdout, AuthUsageText())
			return nil
		default:
			fmt.Fprintf(stderr, "Unsupported workcell auth unset option: %s\n", args[i])
			fmt.Fprint(stderr, AuthUsageText())
			return &cliexit.ExitCodeError{Code: 2, Message: ""}
		}
	}
	if credential == "" {
		return exit2("workcell auth unset requires --credential.")
	}
	resolvedPolicy, err := resolveHostPath(policyPath, base)
	if err != nil {
		return exit2("%s", err.Error())
	}
	resolvedRoot, err := resolveHostPath(managedRoot, base)
	if err != nil {
		return exit2("%s", err.Error())
	}
	return runManagePolicy([]string{
		"unset",
		"--policy", resolvedPolicy,
		"--managed-root", resolvedRoot,
		"--credential", credential,
	}, stdout, stderr)
}

func authStatus(args []string, base, policyPath string, stdout, stderr io.Writer) error {
	var (
		agent              string
		mode               = "strict"
		policyPathExplicit bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--injection-policy":
			v, err := nextValue(args, i, false)
			if err != nil {
				return err
			}
			policyPath = v
			policyPathExplicit = true
			i++
		case "--agent":
			v, err := nextValue(args, i, true)
			if err != nil {
				return err
			}
			agent = v
			i++
		case "--mode":
			v, err := nextValue(args, i, true)
			if err != nil {
				return err
			}
			mode = v
			i++
		case "--workspace":
			// Accepted for CLI symmetry; ignored by host status.
			if _, err := nextValue(args, i, false); err != nil {
				return err
			}
			i++
		case "-h", "--help":
			fmt.Fprint(stdout, AuthUsageText())
			return nil
		default:
			fmt.Fprintf(stderr, "Unsupported workcell auth status option: %s\n", args[i])
			fmt.Fprint(stderr, AuthUsageText())
			return &cliexit.ExitCodeError{Code: 2, Message: ""}
		}
	}
	resolvedPolicy, err := resolveHostPath(policyPath, base)
	if err != nil {
		return exit2("%s", err.Error())
	}
	if policyPathExplicit {
		if info, statErr := os.Stat(resolvedPolicy); statErr != nil || info.IsDir() {
			return exit2("Injection policy file does not exist: %s", resolvedPolicy)
		}
	}
	authCmd := []string{
		"status",
		"--policy", resolvedPolicy,
		"--mode", mode,
	}
	if agent != "" {
		authCmd = append(authCmd, "--agent", agent)
	}
	return runManagePolicy(authCmd, stdout, stderr)
}

// nextValue returns the value following args[i]. When strict is true it
// rejects values starting with `--` (option_value_or_die parity); when
// strict is false only emptiness is rejected (raw_option_value_or_die).
func nextValue(args []string, i int, strict bool) (string, error) {
	option := args[i]
	value := ""
	if i+1 < len(args) {
		value = args[i+1]
	}
	if strict {
		return optionValueOrDie(option, value)
	}
	return rawOptionValueOrDie(option, value)
}

// runManagePolicy invokes the in-process manage_injection_policy
// command directly, mirroring scripts/workcell's run_clean_host_command
// invocation of scripts/lib/manage_injection_policy. The cwd-based home
// isolation that run_clean_host_command provides is unnecessary here
// because Run does not exec subprocesses; it only reads files via paths
// the caller already canonicalised.  Run returns a *cliexit.ExitCodeError
// directly, so we propagate it untouched.
func runManagePolicy(args []string, stdout, stderr io.Writer) error {
	return Run("workcell-manage-injection-policy", args, stdout, stderr)
}

// authMainBuffer is used by tests that need stdout captured separately
// from stderr without writing to os.Stdout.
func authMainBuffer(args []string) (string, string, error) {
	var out, errBuf bytes.Buffer
	err := authMain(args, &out, &errBuf)
	return out.String(), errBuf.String(), err
}
