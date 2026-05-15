// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package main is the workcell-runtimeutil umbrella binary.
//
// Calling convention: subcommands here preserve the calling shape of
// their bash predecessors. Subcommands absorbed from binaries that
// used the Go `flag` package keep flag-style arguments
// (extract-direct-mounts --manifest=..., render-injection-bundle
// --agent=...); subcommands absorbed from bash functions or positional
// CLIs keep positional argv (canonicalize-path PATH, resolve-ips,
// rewrite-bundle-credential-source, list-direct-mounts). Mixed-shape
// dispatch is intentional and matches the bash callers in
// scripts/workcell / scripts/lib/*.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/omkhar/workcell/internal/injection"
	"github.com/omkhar/workcell/internal/runtimeutil"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workcell-runtimeutil <command> [args...]")
		return 2
	}

	var err error
	switch args[0] {
	case "canonicalize-path":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: workcell-runtimeutil canonicalize-path PATH")
			return 2
		}
		var value string
		value, err = runtimeutil.CanonicalizePath(args[1])
		if err == nil {
			fmt.Fprintln(stdout, value)
			return 0
		}
	case "resolve-ips":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: workcell-runtimeutil resolve-ips HOST")
			return 2
		}
		var values []string
		values, err = runtimeutil.ResolveIPs(args[1])
		if err == nil {
			for _, value := range values {
				fmt.Fprintln(stdout, value)
			}
			return 0
		}
	case "rewrite-bundle-credential-source":
		if len(args) != 5 {
			fmt.Fprintln(stderr, "usage: workcell-runtimeutil rewrite-bundle-credential-source MANIFEST_PATH MOUNT_SPEC_PATH CREDENTIAL_KEY OVERRIDE_SOURCE")
			return 2
		}
		err = runtimeutil.RewriteBundleCredentialOverride(args[1], args[2], args[3], args[4])
		if err == nil {
			return 0
		}
	case "list-direct-mounts":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: workcell-runtimeutil list-direct-mounts MOUNT_SPEC_PATH")
			return 2
		}
		var mounts []runtimeutil.DirectMount
		mounts, err = runtimeutil.ListDirectMounts(args[1])
		if err == nil {
			for _, mount := range mounts {
				fmt.Fprintf(stdout, "%s\t%s\n", mount.Source, mount.MountPath)
			}
			return 0
		}
	case "extract-direct-mounts":
		return runExtractDirectMounts(args[1:], stderr)
	case "render-injection-bundle":
		return runRenderInjectionBundle(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}

	fmt.Fprintln(stderr, err)
	return 1
}

// runExtractDirectMounts absorbs the former workcell-extract-direct-mounts
// binary.  The flag set name carries through to error messages so
// callers see the same surface they did before; only the program
// prefix changes.
func runExtractDirectMounts(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("workcell-runtimeutil extract-direct-mounts", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	manifestPath := fs.String("manifest", "", "")
	mountSpecPath := fs.String("mount-spec", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *manifestPath == "" || *mountSpecPath == "" {
		fmt.Fprintln(stderr, "both --manifest and --mount-spec are required")
		return 2
	}
	if err := injection.RunExtractDirectMounts(*manifestPath, *mountSpecPath); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// runRenderInjectionBundle absorbs the former
// workcell-render-injection-bundle binary.  The agent/mode
// validation is delegated to injection.ValidateRenderAgentMode so
// the bash contract of "exit 2 on bad flags, exit 1 on render
// failure" is preserved.
func runRenderInjectionBundle(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("workcell-runtimeutil render-injection-bundle", flag.ContinueOnError)
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
	if err := injection.ValidateRenderAgentMode(*agent, *mode); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := injection.RunRenderInjectionBundle(*policyPath, *agent, *mode, *outputRoot, *policyMetadata); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
