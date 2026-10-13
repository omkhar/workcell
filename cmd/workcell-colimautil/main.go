// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/colimautil"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		// Honor the canonical cliexit.ExitCodeError so usage errors exit
		// 2 (matching workcell-citools/-hostutil/-runtimeutil) instead of
		// collapsing every failure to 1.
		var ec *cliexit.ExitCodeError
		if errors.As(err, &ec) {
			if msg := ec.Error(); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			os.Exit(ec.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}

	switch args[0] {
	case "-h", "--help":
		fmt.Println("usage: workcell-colimautil <validate-runtime-mounts|validate-profile-config> [args...]")
		return nil
	case "validate-runtime-mounts":
		if len(args) != 5 {
			return usage()
		}
		return colimautil.ValidateRuntimeMounts(args[1], args[2], args[3], args[4])
	case "validate-profile-config":
		if len(args) != 8 {
			return usage()
		}
		return colimautil.ValidateProfileConfig(args[1], args[2], args[3], args[4], args[5], args[6], args[7])
	default:
		return usage()
	}
}

func usage() error {
	return &cliexit.ExitCodeError{Code: 2, Message: "usage: workcell-colimautil <validate-runtime-mounts|validate-profile-config> [args...]"}
}
