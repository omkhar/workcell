// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"strings"
)

// ValidateContainerSmokeBuildEpoch requires scripts/container-smoke.sh to run
// the fixed default epoch assignment once. A per-commit epoch reaches every
// runtime-image RUN layer and defeats the BuildKit cache. A bare assignment is
// a command with no name, so the shared shell-invocation parser sees it with no
// arguments; a comment, a heredoc body, a quoted decoy or an assignment that
// prefixes another command does not count.
func ValidateContainerSmokeBuildEpoch(script string) error {
	count := 0
	for _, invocation := range ShellInvocations(script, `SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-0}`) {
		if len(invocation.Args) == 0 {
			count++
		}
	}
	if count != 1 {
		return errors.New("Expected the smoke build epoch to default to a fixed value, not the HEAD commit time")
	}
	return nil
}

func ValidateContainerSmokeFlagInventory(script string) error {
	build := -1
	for _, invocation := range ShellInvocations(script, "SOURCE_DATE_EPOCH=${BUILD_SOURCE_DATE_EPOCH} buildx_cmd") {
		if hasPair(invocation.Args, "-f", "${ROOT_DIR}/runtime/container/Dockerfile") && hasPair(invocation.Args, "-t", "${IMAGE_TAG}") && strings.Contains("\x00"+strings.Join(invocation.Args, "\x00")+"\x00", "\x00--load\x00") {
			build = invocation.Position
		}
	}
	checks := 0
	for _, invocation := range ShellInvocations(script, "WORKCELL_GO_BIN=${GO_BIN} WORKCELL_IMAGE_TAG=${IMAGE_TAG} WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT=${DOCKER_CONTEXT_NAME} ${ROOT_DIR}/scripts/check-flag-inventory.sh") {
		if len(invocation.Args) == 0 && build >= 0 && invocation.Position > build {
			checks++
		}
	}
	if checks != 1 {
		return errors.New("Expected the smoke lane to run the flag inventory once against the built image tag after the image build")
	}
	return nil
}

func hasPair(args []string, flag, value string) bool { // adjacent argv elements; a word cannot contain NUL
	return strings.Contains("\x00"+strings.Join(args, "\x00")+"\x00", "\x00"+flag+"\x00"+value+"\x00")
}
