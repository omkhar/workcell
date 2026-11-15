// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
)

// policy/tool-pins.toml records the reviewed CI and release tool pins, and
// scripts/ci/load-tool-pins.sh is the loader the workflows are to run as a
// `pins` step to read them at run time. The checks here hold the policy side:
//   - every key of the fixed table is set, with the value format of its key;
//   - the Go parse and the bash loader read the same pins from the policy.

const toolPinsLoaderRel = "scripts/ci/load-tool-pins.sh"

var (
	// Version parts have no leading zero, so one version has one spelling.
	toolPinReleaseTagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	toolPinBareSemverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	toolPinSHA256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// Each image pin must name its one reviewed repository on Docker Hub, with
	// no registry host or other prefix: repository:tag@sha256:digest. The loader
	// applies the same per-key patterns.
	toolPinBuildkitPattern = toolPinImagePattern(toolPinBuildkitRepository)
	toolPinQEMUPattern     = toolPinImagePattern(toolPinQEMURepository)
)

const (
	toolPinBuildkitRepository = "moby/buildkit"
	toolPinQEMURepository     = "tonistiigi/binfmt"
)

// toolPinImagePattern is the format of an image pin for one repository.
func toolPinImagePattern(repository string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(repository) + `:[A-Za-z0-9_][A-Za-z0-9_.-]*@sha256:[0-9a-f]{64}$`)
}

// toolPinField is one row of the fixed key table: the policy key, the step env
// name a consumer binds it to, the value format, and the parsed value.
type toolPinField struct {
	key, envName string
	format       *regexp.Regexp
	dst          *string
}

// fields is the fixed key table, in the loader's output order.
func (pins *toolPins) fields() []toolPinField {
	return []toolPinField{
		{"cosign", "WORKCELL_COSIGN_VERSION", toolPinReleaseTagPattern, &pins.Cosign},
		{"buildx", "WORKCELL_BUILDX_VERSION", toolPinReleaseTagPattern, &pins.Buildx},
		{"buildkit", "WORKCELL_BUILDKIT_IMAGE", toolPinBuildkitPattern, &pins.Buildkit},
		{"qemu", "WORKCELL_QEMU_IMAGE", toolPinQEMUPattern, &pins.QEMU},
		{"syft", "WORKCELL_SYFT_VERSION", toolPinReleaseTagPattern, &pins.Syft},
		{"actionlint_version", "ACTIONLINT_VERSION", toolPinBareSemverPattern, &pins.ActionlintVersion},
		{"actionlint_sha256", "ACTIONLINT_SHA256", toolPinSHA256Pattern, &pins.ActionlintSHA256},
		{"zizmor_version", "ZIZMOR_VERSION", toolPinBareSemverPattern, &pins.ZizmorVersion},
		{"zizmor_sha256", "ZIZMOR_SHA256", toolPinSHA256Pattern, &pins.ZizmorSHA256},
		{"oras_version", "WORKCELL_ORAS_VERSION", toolPinBareSemverPattern, &pins.OrasVersion},
		{"oras_linux_amd64_sha256", "WORKCELL_ORAS_LINUX_AMD64_SHA256", toolPinSHA256Pattern, &pins.OrasLinuxAMD64SHA256},
	}
}

// loaderOutput renders the pins exactly as scripts/ci/load-tool-pins.sh writes
// them to GITHUB_OUTPUT.
func (pins toolPins) loaderOutput() string {
	var output strings.Builder
	for _, field := range pins.fields() {
		fmt.Fprintf(&output, "%s=%s\n", field.key, *field.dst)
	}
	return output.String()
}

// validateToolPinLoaderAgreement runs the bash loader that the workflows run
// against the same policy file and requires its output to equal the Go parse
// byte for byte. A policy line one parser reads and the other does not, or a
// value the two read differently, fails the check.
func (check *pinnedInputsCheck) validateToolPinLoaderAgreement() error {
	loader := filepath.Join(check.repoRoot, filepath.FromSlash(toolPinsLoaderRel))
	policy := filepath.Join(check.repoRoot, "policy", "tool-pins.toml")
	got, err := runToolPinLoader(loader, policy)
	if err != nil {
		return err
	}
	if want := check.pins.loaderOutput(); got != want {
		return fmt.Errorf("%s output does not match the Go parse of policy/tool-pins.toml\nloader: %q\nparse:  %q", toolPinsLoaderRel, got, want)
	}
	return nil
}

func runToolPinLoader(loader, policy string) (string, error) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		return "", fmt.Errorf("bash is required to run %s: %w", toolPinsLoaderRel, err)
	}
	outputDir, err := os.MkdirTemp("", "workcell-tool-pins-")
	if err != nil {
		return "", err
	}
	scratchParent, cleaned, err := rootio.OpenParentDirectoryNoFollow(outputDir)
	if err != nil {
		return "", err
	}
	defer scratchParent.Close()
	defer rootio.RemoveAllAtNoFollow(scratchParent, filepath.Base(cleaned)) //nolint:errcheck // scratch directory
	scratch, err := rootio.OpenDirAtNoFollow(scratchParent, filepath.Base(cleaned))
	if err != nil {
		return "", err
	}
	defer scratch.Close()
	const outputName = "github-output"
	cmd := exec.Command(bash, "--noprofile", "--norc", loader, policy)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "GITHUB_OUTPUT=" + filepath.Join(cleaned, outputName)}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s rejected policy/tool-pins.toml: %w: %s", toolPinsLoaderRel, err, strings.TrimSpace(stderr.String()))
	}
	output, err := rootio.ReadFileAtNoFollow(scratch, outputName, toolPinsLoaderRel+" output", 1<<16)
	if err != nil {
		return "", err
	}
	return string(output), nil
}
