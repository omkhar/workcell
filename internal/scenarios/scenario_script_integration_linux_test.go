// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package scenarios

import (
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// TestRunScenarioTestsStopsAScenarioWhenTheRunnerCannotWriteFiles stops the
// runner when it can no longer write a file, as when a scenario has filled
// TMPDIR: the test sets a file size limit of 0 on the runner just before the
// signal. The runner must still stop the scenario and its child, and print
// the scenario's captured tail.
func TestRunScenarioTestsStopsAScenarioWhenTheRunnerCannotWriteFiles(t *testing.T) {
	t.Parallel()

	for _, jobs := range []string{"1", "2"} {
		t.Run("jobs "+jobs, func(t *testing.T) {
			t.Parallel()
			runStoppedScenario(t, jobs, syscall.SIGTERM, "SIGTERM", 143, func(pid int) {
				var limit unix.Rlimit
				if err := unix.Prlimit(pid, unix.RLIMIT_FSIZE, nil, &limit); err != nil {
					t.Fatal(err)
				}
				limit.Cur = 0
				if err := unix.Prlimit(pid, unix.RLIMIT_FSIZE, &limit, nil); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
