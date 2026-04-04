// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"os"

	"github.com/omkhar/workcell/internal/transcript"
)

func main() {
	os.Exit(transcript.Run("pty_transcript", os.Stdin, os.Stdout, os.Stderr, os.Args[1:]))
}
