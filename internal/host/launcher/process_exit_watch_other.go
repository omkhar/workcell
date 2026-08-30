// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !darwin && !linux

package launcher

import "errors"

func validateProcessExitWatchSupport() error {
	return errors.New("process-group supervision requires Darwin or Linux exit observation")
}

func startProcessExitWatch(int) (processExitWatcher, error) {
	return nil, validateProcessExitWatchSupport()
}
