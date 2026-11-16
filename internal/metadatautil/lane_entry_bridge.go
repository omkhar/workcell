package metadatautil

// This file is the lane reader's publish bridge. The reader of the layers
// before lint-ratchets-3i cannot read main's entry guard, nor the reader
// before lint-ratchets-3a main's go-run-env.sh. The bridge reads main's
// guarded lanes and their libraries only at the bytes laneBridgePins holds,
// and pins by digest the main files that only a later layer reads. Each
// piece leaves in the merge its comment names.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// laneBridgePins holds "PATH SHA256" for each lane of this tree that names
// workcell_entry_root, each of which opens with the entry guard, and for each
// repository library such a lane sources, at the bytes this tree holds. The
// reader of lint-ratchets-3i reads each of these lanes with its root pinned:
// past the guard, its stop and laneBridgeRoot, nothing in the lane or a
// library it sources writes ROOT_DIR or workcell_entry_root, by any name.
// The merge of lint-ratchets-3i removes this bridge.
var laneBridgePins = map[string]bool{
	"scripts/ci/job-docs.sh 5551be29a025f8250e714ce48185eef9aa461e8cfc68b88e93d4ea44d9741971":                true,
	"scripts/ci/job-fuzz.sh 01c5d910b5fb60a8c35736fad82bedfb2b2a1c1506e3cd4f7e4ec1d2a1520f3c":                true,
	"scripts/ci/job-mutation.sh 92cf9d88765c1d8756f972701829d091e07bead86e7ad66c32f694f5d1877da3":            true,
	"scripts/ci/job-pin-hygiene.sh 62fefe077f16e97929227e5887f2972c065f8d239d83732623009a098758b1cd":         true,
	"scripts/ci/job-pr-shape.sh 9e954e229370cae48a74a78f5bf1be7efe19ec5c3a07c0742c9a00a0a38e79f4":            true,
	"scripts/ci/job-release-asset-acl.sh 7440b8be87bf2b7a553e18dcd229927216d3e495ca633c842239e8ccb13e8114":   true,
	"scripts/ci/job-validate.sh d7556f6981a936142e87e21691244edd5313f99033401a35a6d20b64c5ccbac7":            true,
	"scripts/ci/lib/local-docker-parity.sh 4ce035e24b8b85123af3695a1e4732cbf8b39faad3e81c3fb23e17887f9f35d9": true,
	"scripts/lib/canonical-build-env.sh 7adee61eddf064dc025b5d168bfbf88e187f95a7b8ea6b3db642683b6d877fbc":    true,
	"scripts/lib/go-run-env.sh 6d8fac16daf282c210e3283e590f344c0800dc2383861d1a2b54d5948815a27c":             true,
	"scripts/lib/owned-root.sh cda88920e315acdb6cc34fa8df8f484752b2bf3e92cc85bd0500c18016e35d74":             true,
	"scripts/lib/trusted-docker-client.sh fe74966fb062c3e3ddf3b1cb5557c94ea76f48793bd97fe8ece6588562320a95":  true,
	"scripts/validate-repo.sh 970268c89193dd174501622d86c46931df5994c9a87690e01dad078a291b9ae6":              true,
}

// laneBridgeGuard opens the entry guard's line, laneBridgeRoot is the line
// that assigns ROOT_DIR the root the guard checked, and laneBridgeMark is the
// line laneEntryBridged writes into a lane it reads.
const (
	laneBridgeGuard = `workcell_entry_root="$(`
	laneBridgeRoot  = `ROOT_DIR="${workcell_entry_root}"`
	laneBridgeMark  = "# workcell-lane-bridge: a pinned guarded lane"
)

// laneEntryBridged returns the text this reader reads for the lane script
// rel. A lane that does not name workcell_entry_root is read as written. Any
// other lane is unreadable unless laneBridgePins holds its bytes, which are
// read by laneBridgeRead with the root line in the canonical form for rel's
// directory, a readonly ROOT_DIR, which holds for these bytes, and
// laneBridgeMark. The guard's root is the tree whose rel is the script, as
// the canonical assignment's is.
func laneEntryBridged(text, rel string) (string, error) {
	if !strings.Contains(text, "workcell_entry_root") {
		return text, nil
	}
	if !laneBridgePins[rel+" "+laneBridgeDigest(text)] {
		return "", fmt.Errorf("lane wiring is unreadable: %s names workcell_entry_root, which this reader reads only at the bytes the publish bridge pins", rel)
	}
	ups := strings.TrimSuffix(strings.Repeat("../", strings.Count(rel, "/")), "/")
	return laneBridgeRead(text, `ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/`+ups+`" && pwd)"`+"\nreadonly ROOT_DIR\n"+laneBridgeMark), nil
}

// laneGoRunEnvPin is the sha256 of main's scripts/lib/go-run-env.sh, whose
// cache lock this reader cannot read, and laneBridgePins holds it only at
// that path. A guarded lane reads it as an empty library, since reading it as
// lint-ratchets-3a does is too large to bring back. That widens nothing:
// each tree this pin is in calls its functions from the same lines, and for
// the trees of the merges of lint-ratchets-1f and 1g, the reader of
// lint-ratchets-3i lists the same scripts in the same classes with these
// bytes as with empty text; it reads no earlier tree's validate-repo.sh either
// way. The merge of lint-ratchets-3a removes this pin.
const laneGoRunEnvPin = "6d8fac16daf282c210e3283e590f344c0800dc2383861d1a2b54d5948815a27c"

// laneEntryLibrary returns the text this reader reads for a repository
// library that the lane text lane sources. The library of a lane that
// laneEntryBridged did not read, which holds no laneBridgeMark line, is read
// as written. Any other library is unreadable unless laneBridgePins holds its
// bytes, which are read by laneBridgeRead, and main's go-run-env.sh as empty text.
func laneEntryLibrary(text, library, lane string) (string, error) {
	if !strings.Contains(lane, "\n"+laneBridgeMark+"\n") {
		return text, nil
	}
	if !laneBridgePins[library+" "+laneBridgeDigest(text)] {
		return "", fmt.Errorf("lane wiring is unreadable: a guarded lane sources %s, which this reader reads only at the bytes the publish bridge pins", library)
	}
	if laneBridgeDigest(text) == laneGoRunEnvPin {
		return "", nil
	}
	return laneBridgeRead(text, laneBridgeRoot), nil
}

// laneBridgeRead returns pinned text with the entry guard's line and the
// four lines of its stop blank, where it opens with them, its laneBridgeRoot
// line replaced by root, and workcell_entry_root spelled ROOT_DIR. Past the
// stop, the guard's root is set or the shell has ended.
func laneBridgeRead(text, root string) string {
	lines := strings.Split(text, "\n")
	if at := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, laneBridgeGuard) }); at >= 0 {
		for line := at; line <= at+4; line++ {
			lines[line] = ""
		}
	}
	if at := slices.Index(lines, laneBridgeRoot); at >= 0 {
		lines[at] = root
	}
	return strings.ReplaceAll(strings.Join(lines, "\n"), "workcell_entry_root", "ROOT_DIR")
}

// laneBridgeDigest returns the hex sha256 of text.
func laneBridgeDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
