//go:build !unix

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package secretfile

import (
	"fmt"
	"os"
)

func Open(path, label string, uid int) (*os.File, error) {
	return nil, fmt.Errorf("%s secure file validation is unsupported on this platform: %s", label, path)
}
