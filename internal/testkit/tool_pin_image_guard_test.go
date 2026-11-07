package testkit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestToolPinImageConsumersRejectOtherRepositories proves the scripts that use
// the BuildKit and QEMU image pins refuse an image outside the reviewed
// repository before they run Docker, even when the value arrives from the
// environment rather than from the pins step.
func TestToolPinImageConsumersRejectOtherRepositories(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, script, variable, value, want string
	}{
		{"validator BuildKit from another registry", "scripts/ci/build-validator-image.sh", "WORKCELL_BUILDKIT_IMAGE", "ghcr.io/evil/moby/buildkit:buildx-stable-1@sha256:" + digest, "WORKCELL_BUILDKIT_IMAGE must be moby/buildkit:"},
		{"validator BuildKit with a registry host", "scripts/ci/build-validator-image.sh", "WORKCELL_BUILDKIT_IMAGE", "docker.io/moby/buildkit:buildx-stable-1@sha256:" + digest, "WORKCELL_BUILDKIT_IMAGE must be moby/buildkit:"},
		{"validator BuildKit lookalike repository", "scripts/ci/build-validator-image.sh", "WORKCELL_BUILDKIT_IMAGE", "moby/buildkitx:buildx-stable-1@sha256:" + digest, "WORKCELL_BUILDKIT_IMAGE must be moby/buildkit:"},
		{"validator BuildKit without a digest", "scripts/ci/build-validator-image.sh", "WORKCELL_BUILDKIT_IMAGE", "moby/buildkit:buildx-stable-1", "WORKCELL_BUILDKIT_IMAGE must be moby/buildkit:"},
		{"builder manifest BuildKit from another registry", "scripts/generate-builder-environment-manifest.sh", "WORKCELL_BUILDKIT_IMAGE", "ghcr.io/evil/moby/buildkit:buildx-stable-1@sha256:" + digest, "WORKCELL_BUILDKIT_IMAGE must be moby/buildkit:"},
		{"builder manifest QEMU from another registry", "scripts/generate-builder-environment-manifest.sh", "WORKCELL_QEMU_IMAGE", "ghcr.io/evil/tonistiigi/binfmt:qemu-v10@sha256:" + digest, "WORKCELL_QEMU_IMAGE must be tonistiigi/binfmt:"},
		{"builder manifest QEMU uppercase digest", "scripts/generate-builder-environment-manifest.sh", "WORKCELL_QEMU_IMAGE", "tonistiigi/binfmt:qemu-v10@sha256:" + strings.Repeat("A", 64), "WORKCELL_QEMU_IMAGE must be tonistiigi/binfmt:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := filepath.Join(repoRoot(t), filepath.FromSlash(tc.script))
			out := filepath.Join(t.TempDir(), "out.json")
			output, err := execRetryETXTBSY(func() *exec.Cmd {
				cmd := exec.Command(script, out)
				cmd.Env = append(os.Environ(), tc.variable+"="+tc.value)
				return cmd
			})
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("%s with %s=%q: err = %v, want exit 1\n%s", tc.script, tc.variable, tc.value, err, output)
			}
			if !strings.Contains(string(output), tc.want) {
				t.Fatalf("%s output = %q, want %q", tc.script, output, tc.want)
			}
		})
	}
}
