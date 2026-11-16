// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Command workcell-provider-policy runs the runtime provider argument policy.
// The container entrypoint runs validate-command over the full provider
// command. The provider wrapper runs check over the provider arguments after
// it fixes the launch target. The caller passes its WORKCELL_MODE, AGENT_UI
// and WORKCELL_WRAPPER_CONTEXT values. A refusal prints its message to stderr
// and exits 2. The provider arguments travel on the exec argument list, so an
// argument list over the kernel limit fails the exec with E2BIG; both callers
// end the call with || exit 2, so a very long argv fails closed.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/providerpolicy"
)

const usageMessage = "usage: workcell-provider-policy <check|validate-command> --mode=MODE --agent-ui=UI --wrapper-context=VALUE PROVIDER [ARG...]"

// main passes os.Getppid() == 1 as the caller check. The shell policy ran
// inside the entrypoint and tested $$ == 1, its own process. This binary is
// a child that the entrypoint execs, so the entrypoint is its parent: a
// parent PID of 1 is the exec'd-binary equivalent of that $$ check.
func main() {
	os.Exit(run(os.Args[1:], os.Getppid() == 1, os.Stdout, os.Stderr))
}

func run(args []string, callerIsPID1 bool, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, usageMessage)
		return 0
	}
	if len(args) < 5 || (args[0] != "check" && args[0] != "validate-command") {
		fmt.Fprintln(stderr, usageMessage)
		return 2
	}
	session := providerpolicy.Session{CallerIsPID1: callerIsPID1}
	// Each option name sits next to the field that it sets, in the order of
	// the usage line.
	options := [...]struct {
		prefix string
		field  *string
	}{
		{"--mode=", &session.Mode},
		{"--agent-ui=", &session.AgentUI},
		{"--wrapper-context=", &session.WrapperContext},
	}
	for i, opt := range options {
		value, ok := strings.CutPrefix(args[1+i], opt.prefix)
		if !ok {
			fmt.Fprintln(stderr, usageMessage)
			return 2
		}
		*opt.field = value
	}
	provider, rest := args[4], args[5:]
	var err error
	if args[0] == "check" {
		err = providerpolicy.Check(provider, rest, session, providerpolicy.Adapters)
	} else {
		err = providerpolicy.ValidateCommand(provider, rest, session, providerpolicy.Adapters)
	}
	var refusal *providerpolicy.Refusal
	switch {
	case err == nil:
		return 0
	case errors.As(err, &refusal):
		fmt.Fprintln(stderr, refusal.Message)
	default:
		fmt.Fprintln(stderr, err)
	}
	return 2
}
