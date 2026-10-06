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

// effectiveDockerfile is the Dockerfile buildx would use: the last of -f X,
// --file X, --file=X or -fX wins, so a later decoy cannot hide behind the
// required pair.
func effectiveDockerfile(args []string) string {
	file := ""
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "-f" || arg == "--file":
			if i+1 < len(args) {
				i++
				file = args[i]
			}
		case strings.HasPrefix(arg, "--file="):
			file = strings.TrimPrefix(arg, "--file=")
		case strings.HasPrefix(arg, "-f") && !strings.HasPrefix(arg, "--"):
			file = strings.TrimPrefix(arg[2:], "=")
		}
	}
	return file
}

func ValidateContainerSmokeFlagInventory(script string) error {
	build := -1
	for _, invocation := range ShellInvocations(script, "SOURCE_DATE_EPOCH=${BUILD_SOURCE_DATE_EPOCH} buildx_cmd") {
		// NUL-joined argv makes each check exact: a word cannot contain NUL, and
		// the last --load spelling wins, so a later --load=false is not a load.
		args := "\x00" + strings.Join(invocation.Args, "\x00") + "\x00"
		// The last build that tags ${IMAGE_TAG} decides the image, so a later
		// retagging build must itself comply.
		if !strings.Contains(args, "${IMAGE_TAG}") {
			continue
		}
		build = -1
		if effectiveDockerfile(invocation.Args) == "${ROOT_DIR}/runtime/container/Dockerfile" && strings.Contains(args, "\x00-t\x00${IMAGE_TAG}\x00") && strings.LastIndex(args, "\x00--load\x00") > strings.LastIndex(args, "\x00--load=") {
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
