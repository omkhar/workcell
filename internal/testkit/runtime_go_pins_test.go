// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpstreamRefreshIncludesRuntimeGoPins(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts/update-upstream-pins.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []struct{ arg, current, target string }{
		{"GO_VERSION", "current_runtime_go_version", "target_go_toolchain"},
		{"GO_LINUX_X86_64_SHA256", "current_runtime_go_sha_amd64", "target_go_sha_amd64"},
		{"GO_LINUX_ARM64_SHA256", "current_runtime_go_sha_arm64", "target_go_sha_arm64"},
	} {
		for _, want := range []string{
			fmt.Sprintf(`%s="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" %s)"`, pin.current, pin.arg),
			fmt.Sprintf(`"${%s}|${%s}"`, pin.current, pin.target),
			fmt.Sprintf(`replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG %s=' "ARG %s=${%s}"`, pin.arg, pin.arg, pin.target),
		} {
			if !strings.Contains(string(data), want) {
				t.Errorf("upstream refresh lacks runtime Go pin operation: %s", want)
			}
		}
	}
}
