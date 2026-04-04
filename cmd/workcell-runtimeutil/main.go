// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"fmt"
	"io"
	"os"

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
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}

	fmt.Fprintln(stderr, err)
	return 1
}
