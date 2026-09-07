// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/aptbroker"
)

const sudoCompatHelperPath = aptbroker.DefaultHelperPath

type sudoCompatOptions struct {
	preserveCSV string
	preserveSet bool
}

func runSudoCompat(args []string) int {
	command, preserve, status := parseSudoCompatArguments(args)
	if status != 0 {
		writeSudoCompatError(status)
		return status
	}
	return runClient(aptbroker.DefaultSocketPath, command, preserve)
}

func parseSudoCompatArguments(args []string) ([]string, []string, int) {
	options, index, status := parseSudoCompatOptions(args)
	if status != 0 {
		return nil, nil, status
	}
	if index >= len(args) {
		return nil, nil, 2
	}
	if args[index] != sudoCompatHelperPath {
		return nil, nil, 1
	}
	return args[index+1:], preservedNames(options.preserveCSV), 0
}

func parseSudoCompatOptions(args []string) (sudoCompatOptions, int, int) {
	var options sudoCompatOptions
	index := 0
	for index < len(args) {
		if args[index] == "--" {
			return options, index + 1, 0
		}
		next, handled, status := options.consume(args, index)
		if status != 0 {
			return sudoCompatOptions{}, 0, status
		}
		if !handled {
			return options, index, 0
		}
		index = next
	}
	return options, index, 0
}

func (options *sudoCompatOptions) consume(args []string, index int) (int, bool, int) {
	argument := args[index]
	switch {
	case argument == "-n":
		return index + 1, true, 0
	case argument == "--preserve-env":
		return options.consumeSeparatePreserve(args, index)
	case strings.HasPrefix(argument, "--preserve-env="):
		return options.consumePreserveValue(strings.TrimPrefix(argument, "--preserve-env="), index+1)
	default:
		return index, false, 0
	}
}

func (options *sudoCompatOptions) consumeSeparatePreserve(args []string, index int) (int, bool, int) {
	if index+1 >= len(args) {
		return 0, true, 2
	}
	return options.consumePreserveValue(args[index+1], index+2)
}

func (options *sudoCompatOptions) consumePreserveValue(value string, next int) (int, bool, int) {
	if options.preserveSet || value == "" || strings.HasPrefix(value, "-") {
		return 0, true, 2
	}
	options.preserveSet = true
	options.preserveCSV = value
	return next, true, 0
}

func writeSudoCompatError(status int) {
	if status == 2 {
		fmt.Fprintln(os.Stderr, "Workcell blocked malformed sudo compatibility request.")
		return
	}
	fmt.Fprintln(os.Stderr, "Workcell sudo compatibility mode only permits the package helper.")
}
