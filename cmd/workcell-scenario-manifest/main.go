// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"os"

	"github.com/omkhar/workcell/internal/scenarios"
)

func main() {
	os.Exit(scenarios.Run(os.Args[0], os.Args[1:], os.Stdout, os.Stderr))
}
