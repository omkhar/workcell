// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessions

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

var (
	dockerContainerNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]+$`)
	workcellContainerNamePattern = regexp.MustCompile(`^workcell-[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// ProveContainerAbsentForDelete validates a successful `docker ps -a
// --format {{.Names}}` inventory and proves that it does not contain the
// Workcell-managed container name. Unsupported record identifiers and
// malformed inventories fail closed.
func ProveContainerAbsentForDelete(containerName, inventory string) error {
	normalizedName := strings.TrimPrefix(containerName, "/")
	if !workcellContainerNamePattern.MatchString(normalizedName) {
		return fmt.Errorf("unsupported Workcell container name: %q", containerName)
	}

	scanner := bufio.NewScanner(strings.NewReader(inventory))
	for scanner.Scan() {
		observedName := scanner.Text()
		if !dockerContainerNamePattern.MatchString(observedName) {
			return fmt.Errorf("malformed Docker container inventory")
		}
		if observedName == normalizedName {
			return fmt.Errorf("container is still present: %s", normalizedName)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read Docker container inventory: %w", err)
	}
	return nil
}
