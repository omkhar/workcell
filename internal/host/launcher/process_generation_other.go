// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !darwin && !linux

package launcher

// Colima cleanup is supported only on Darwin. Keep unsupported build targets
// compiling without claiming the second-resolution fallback as a safety gate.
func processGeneration(pid int) (string, error) {
	return ProcessStartTime(pid)
}
