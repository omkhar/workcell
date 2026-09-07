// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !linux

package main

import "errors"

func startServer([]string) error {
	return errors.New("apt broker startup is unsupported on this platform")
}

func startupHandshake(readyFD, acknowledgeFD uintptr) (func() error, error) {
	if readyFD == 0 && acknowledgeFD == 0 {
		return nil, nil
	}
	return nil, errors.New("apt broker startup is unsupported on this platform")
}
