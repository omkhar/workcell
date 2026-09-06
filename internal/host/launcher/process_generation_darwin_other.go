// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !darwin

package launcher

import (
	"fmt"
	"runtime"
)

func observeValidDarwinProcessGeneration(int, string) (string, error) {
	return "", fmt.Errorf("darwin process generation does not match %s host", runtime.GOOS)
}
