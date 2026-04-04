// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"os"

	"github.com/omkhar/workcell/internal/authresolve"
)

func main() {
	os.Exit(authresolve.Run(os.Args[1:], os.Stdout, os.Stderr))
}
