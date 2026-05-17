// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/omkhar/workcell/internal/host/launcher"
)

// PolicyMain implements `workcell policy {show,validate,diff}`, the Go
// translation of the bash policy_main function in scripts/workcell.
//
// The bash version dispatches one of three host-only, read-only
// subcommands against an entrypoint policy file (defaulting to
// ~/.config/workcell/injection-policy.toml).  Behavior mirrors bash
// exactly:
//   - `policy help|-h|--help` prints PolicyUsageText() and returns nil.
//   - `policy show|validate|diff [--injection-policy PATH]` resolves the
//     policy path relative to the caller's working directory and
//     dispatches to commandShow/commandValidate/commandDiff.
//   - missing/unsupported subcommand or unknown option returns a
//     usageError; the helper wrapper translates that into exit 2 to
//     match scripts/workcell's `exit 2` on the same input.
//
// Stdout and stderr go to os.Stdout/os.Stderr; runPolicyMain is the
// io.Writer-aware seam used by the unit tests.
func PolicyMain(args []string) error {
	return runPolicyMain(args, os.Stdout, os.Stderr)
}

func runPolicyMain(args []string, stdout, stderr io.Writer) error {
	base, args := consumeBaseArg(args)
	if len(args) == 0 {
		fmt.Fprint(stderr, PolicyUsageText())
		return usageError{}
	}
	subcommand := args[0]
	switch subcommand {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, PolicyUsageText())
		return nil
	}
	rest := args[1:]
	switch subcommand {
	case "show", "validate", "diff":
		policyPath, helpRequested, err := parsePolicyMainArgs(subcommand, rest, stdout, stderr)
		if err != nil {
			return err
		}
		if helpRequested {
			return nil
		}
		resolved, err := launcher.CanonicalizePathFrom(policyPath, base)
		if err != nil {
			return err
		}
		switch subcommand {
		case "show":
			return commandShow(resolved, stdout)
		case "validate":
			return commandValidate(resolved, stdout)
		case "diff":
			return commandDiff(resolved, stdout)
		}
	}
	fmt.Fprintf(stderr, "Unsupported workcell policy command: %s\n", subcommand)
	fmt.Fprint(stderr, PolicyUsageText())
	return usageError{}
}

// parsePolicyMainArgs mirrors the bash `policy {show|validate|diff}`
// option loop: --injection-policy PATH, -h/--help, anything else is a
// usage error.  The returned policyPath defaults to
// defaultInjectionPolicyPath() when the flag is omitted; helpRequested
// is true when the caller hit -h/--help inside the option loop, in
// which case PolicyUsageText has already been written to stdout.
func parsePolicyMainArgs(subcommand string, args []string, stdout, stderr io.Writer) (policyPath string, helpRequested bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--injection-policy":
			if i+1 >= len(args) {
				return "", false, exit2("Option --injection-policy requires a value.")
			}
			value, valueErr := rawOptionValueOrDie("--injection-policy", args[i+1])
			if valueErr != nil {
				return "", false, valueErr
			}
			policyPath = value
			i++
		case "-h", "--help":
			fmt.Fprint(stdout, PolicyUsageText())
			return "", true, nil
		default:
			fmt.Fprintf(stderr, "Unsupported workcell policy %s option: %s\n", subcommand, args[i])
			fmt.Fprint(stderr, PolicyUsageText())
			return "", false, usageError{}
		}
	}
	if policyPath == "" {
		policyPath = defaultInjectionPolicyPath()
	}
	return policyPath, false, nil
}

// usageError marks errors that should produce a usage-style (exit 2)
// exit from workcell-hostutil.  It carries no message because callers
// have already written the appropriate stderr text.
type usageError struct{}

func (usageError) Error() string { return "usage error" }

// IsPolicyMainUsageError reports whether err originated from PolicyMain
// as a usage-style error.  cmd/workcell-hostutil translates this into
// exit code 2 to preserve scripts/workcell's policy_main contract.
func IsPolicyMainUsageError(err error) bool {
	var ue usageError
	return errors.As(err, &ue)
}
