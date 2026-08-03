// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/omkhar/workcell/internal/host/c3certify"
)

func main() {
	root := flag.String("root", "", "Workcell control-plane root")
	workspace := flag.String("workspace", "", "clean workload git workspace")
	tree := flag.String("precommit-control-tree", "", "full indexed control-tree SHA")
	flag.Parse()
	if flag.NArg() != 0 || *root == "" || *workspace == "" {
		fmt.Fprintln(os.Stderr, "Usage: workcell-c3-certify --root PATH --workspace PATH [--precommit-control-tree SHA]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	options := c3certify.Options{Root: *root, Workspace: *workspace, PrecommitControlTree: *tree}
	if err := c3certify.Run(ctx, options, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
