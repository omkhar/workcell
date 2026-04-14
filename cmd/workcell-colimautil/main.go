// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"fmt"
	"os"

	"github.com/omkhar/workcell/internal/colimautil"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}

	switch args[0] {
	case "validate-runtime-mounts":
		if len(args) != 4 {
			return usage()
		}
		return colimautil.ValidateRuntimeMounts(args[1], args[2], args[3])
	case "validate-profile-config":
		if len(args) != 6 {
			return usage()
		}
		return colimautil.ValidateProfileConfig(args[1], args[2], args[3], args[4], args[5])
	default:
		return usage()
	}
}

func usage() error {
	return fmt.Errorf("usage: workcell-colimautil <validate-runtime-mounts|validate-profile-config> [args...]")
}
