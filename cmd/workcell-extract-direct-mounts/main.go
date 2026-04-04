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
	fs := flag.NewFlagSet("workcell-extract-direct-mounts", flag.ContinueOnError)
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
