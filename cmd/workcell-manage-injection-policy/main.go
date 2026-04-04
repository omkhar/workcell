// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"os"

	"github.com/omkhar/workcell/internal/authpolicy"
)

func main() {
	os.Exit(authpolicy.Run(os.Args[0], os.Args[1:], os.Stdout, os.Stderr))
}
