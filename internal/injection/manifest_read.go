// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"github.com/omkhar/workcell/internal/rootio"
)

// maxInjectionManifestBytes bounds each generated manifest read. It matches
// the maximum selected injection file size.
const maxInjectionManifestBytes = rootio.MaxManifestBytes

// readInjectionManifest opens the full path through descriptors. It rejects
// leaf and parent symlinks before it reads one regular file.
func readInjectionManifest(path string) ([]byte, error) {
	return rootio.ReadFileNoFollow(path, "injection manifest", maxInjectionManifestBytes)
}
