// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import "errors"

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

// ValidateContainerSmokeFlagInventory requires scripts/container-smoke.sh to
// run scripts/check-flag-inventory.sh exactly once with the built image tag and
// no other arguments; the environment and the executable are exact command words.
func ValidateContainerSmokeFlagInventory(script string) error {
	checks := 0
	for _, invocation := range ShellInvocations(script, "WORKCELL_GO_BIN=${GO_BIN} WORKCELL_IMAGE_TAG=${IMAGE_TAG} WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT=${DOCKER_CONTEXT_NAME} ${ROOT_DIR}/scripts/check-flag-inventory.sh") {
		if len(invocation.Args) == 0 {
			checks++
		}
	}
	if checks != 1 {
		return errors.New("Expected the smoke lane to run the flag inventory once against the built image tag")
	}
	return nil
}
