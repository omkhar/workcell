// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"github.com/omkhar/workcell/internal/rootio"
)

const (
	// maxInjectionManifestBytes bounds each generated manifest read and write.
	maxInjectionManifestBytes = rootio.MaxManifestBytes
	// maxInjectionMountSpecBytes bounds each generated direct-mount specification.
	maxInjectionMountSpecBytes = rootio.MaxDirectMountSpecBytes
)

// readInjectionManifest opens the full path through descriptors. It rejects
// leaf and parent symlinks before it reads one regular file.
func readInjectionManifest(path string) ([]byte, error) {
	return rootio.ReadFileNoFollow(path, "injection manifest", maxInjectionManifestBytes)
}

func marshalInjectionManifest(value any) ([]byte, error) {
	return rootio.MarshalCompactJSON(value, "injection manifest", maxInjectionManifestBytes)
}

func marshalInjectionMountSpec(value any) ([]byte, error) {
	return rootio.MarshalCompactJSON(value, "direct mount specification", maxInjectionMountSpecBytes)
}
