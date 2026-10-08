// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"slices"
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

// optionValues returns, in order, each value buildx reads for one option
// spelled -s X, --long X, --long=X, -sX or -s=X.
func optionValues(args []string, short, long string) []string {
	var values []string
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == short || arg == long:
			if i+1 < len(args) {
				i++
				values = append(values, args[i])
			}
		case strings.HasPrefix(arg, long+"="):
			values = append(values, strings.TrimPrefix(arg, long+"="))
		case strings.HasPrefix(arg, short) && !strings.HasPrefix(arg, "--"):
			values = append(values, strings.TrimPrefix(arg[len(short):], "="))
		}
	}
	return values
}

func ValidateContainerSmokeFlagInventory(script string) error {
	// Every buildx_cmd call counts, with or without the epoch prefix.
	var builds []Invocation
	for _, name := range []string{"buildx_cmd", "SOURCE_DATE_EPOCH=${BUILD_SOURCE_DATE_EPOCH} buildx_cmd"} {
		builds = append(builds, ShellInvocations(script, name)...)
	}
	slices.SortFunc(builds, func(a, b Invocation) int { return a.Position - b.Position })
	build := -1
	for _, invocation := range builds {
		// NUL-joined argv makes each check exact: a word cannot contain NUL, and
		// the last --load spelling wins, so a later --load=false is not a load.
		args := "\x00" + strings.Join(invocation.Args, "\x00") + "\x00"
		// The last build that names IMAGE_TAG in any spelling decides the
		// image, so a later retagging build must itself comply.
		if !strings.Contains(args, "IMAGE_TAG") {
			continue
		}
		build = -1
		// The last -f wins; every -t tags the image.
		files := optionValues(invocation.Args, "-f", "--file")
		tags := optionValues(invocation.Args, "-t", "--tag")
		if len(files) > 0 && files[len(files)-1] == "${ROOT_DIR}/runtime/container/Dockerfile" && (slices.Contains(tags, "${IMAGE_TAG}") || slices.Contains(tags, "$IMAGE_TAG")) && strings.LastIndex(args, "\x00--load\x00") > strings.LastIndex(args, "\x00--load=") {
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
