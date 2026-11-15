// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProfileImageMarkerIsPublishedByStagedRename writes the prepared image
// marker through the staged, synced, owner-only publish helper. A direct write
// could leave a short marker after a kill or a power loss, and a short marker
// rebuilds a valid image, which can fail offline (issue 823). The copy is
// staged in the marker's own directory, never in TMPDIR, which may be shared
// or unwritable. A failed publish keeps the old marker and leaves no staged
// copy.
func TestProfileImageMarkerIsPublishedByStagedRename(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	for _, fail := range []string{"0", "1"} {
		t.Run("publish_fails="+fail, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "state"), 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "state", "image.env")
			if err := os.WriteFile(marker, []byte("old\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			body := launcherFunction(t, "write_profile_image_marker") + `
IMAGE_TAG=img SOURCE_DATE_EPOCH=7
profile_image_marker_path() { printf '%s\n' "$DIR/state/image.env"; }
go_hostutil() {
  printf '%s %s\n' "$2" "$(dirname "$3")" >>"$DIR/calls"
  [[ "$2" == publish-session-capture-file && "$FAIL" == 0 ]] && cp "$3" "$4"
}
if write_profile_image_marker prof sha256:abc "$DIGEST"; then echo written; else echo failed; fi
`
			cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+body)
			cmd.Env = append(os.Environ(), "DIR="+dir, "TMPDIR="+filepath.Join(dir, "no-such-tmp"), "FAIL="+fail, "DIGEST="+digest)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("probe failed: %v\n%s", err, out)
			}
			got, _ := os.ReadFile(marker)
			calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
			want, result := "image_tag=img\nimage_id=sha256:abc\nsource_date_epoch=7\nbuild_inputs_sha256="+digest+"\n", "written\n"
			if fail == "1" {
				want, result = "old\n", "Workcell could not record the prepared runtime image for profile prof.\nfailed\n"
			}
			if string(out) != result || string(got) != want || string(calls) != "publish-session-capture-file "+filepath.Join(dir, "state")+"\n" {
				t.Fatalf("output %q, marker %q, helper calls %q; want %q, %q and one publish staged beside the marker", out, got, calls, result, want)
			}
			if left, _ := os.ReadDir(filepath.Join(dir, "state")); len(left) != 1 {
				t.Fatalf("staged marker left behind: %v", left)
			}
		})
	}
}
