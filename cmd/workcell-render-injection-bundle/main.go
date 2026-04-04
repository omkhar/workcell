// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/omkhar/workcell/internal/injection"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("workcell-render-injection-bundle", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	policyPath := fs.String("policy", "", "")
	agent := fs.String("agent", "", "")
	mode := fs.String("mode", "", "")
	outputRoot := fs.String("output-root", "", "")
	policyMetadata := fs.String("policy-metadata", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *policyPath == "" || *agent == "" || *mode == "" || *outputRoot == "" {
		fmt.Fprintln(stderr, "policy, agent, mode, and output-root are required")
		return 2
	}
	if err := injection.RunRenderInjectionBundle(*policyPath, *agent, *mode, *outputRoot, *policyMetadata); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
