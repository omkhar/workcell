// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package launcher

import (
	"os/exec"
	"testing"
)

func TestProcessGenerationDistinguishesSameSecondStarts(t *testing.T) {
	for attempt := 0; attempt < 3; attempt++ {
		first := exec.Command("/bin/sleep", "2")
		second := exec.Command("/bin/sleep", "2")
		if err := first.Start(); err != nil {
			t.Fatal(err)
		}
		if err := second.Start(); err != nil {
			_ = first.Process.Kill()
			_ = first.Wait()
			t.Fatal(err)
		}

		firstLegacy, firstLegacyErr := ProcessStartTime(first.Process.Pid)
		secondLegacy, secondLegacyErr := ProcessStartTime(second.Process.Pid)
		firstGeneration, firstGenerationErr := processGeneration(first.Process.Pid)
		secondGeneration, secondGenerationErr := processGeneration(second.Process.Pid)
		_ = first.Process.Kill()
		_ = second.Process.Kill()
		_ = first.Wait()
		_ = second.Wait()

		if firstLegacyErr != nil || secondLegacyErr != nil || firstGenerationErr != nil || secondGenerationErr != nil {
			t.Fatalf(
				"process identities: legacy errors=(%v, %v) generation errors=(%v, %v)",
				firstLegacyErr, secondLegacyErr, firstGenerationErr, secondGenerationErr,
			)
		}
		if firstLegacy != secondLegacy {
			continue
		}
		if firstGeneration == secondGeneration {
			t.Fatalf("same-second processes share kernel generation %q", firstGeneration)
		}
		return
	}
	t.Fatal("could not create two processes within the same lstart second")
}
