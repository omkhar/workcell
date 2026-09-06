// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/aptbroker"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--sudo-compat" {
		os.Exit(runSudoCompat(os.Args[2:]))
	}
	socketPath := flag.String("socket", aptbroker.DefaultSocketPath, "")
	preserveCSV := flag.String("preserve-env", "", "")
	flag.Parse()
	args := commandArguments(flag.Args())
	preserve := preservedNames(*preserveCSV)
	os.Exit(runClient(*socketPath, args, preserve))
}

func runClient(socketPath string, args, preserve []string) int {
	response, status, err := aptbroker.RunClient(context.Background(), socketPath, args, preserve, os.LookupEnv)
	if err != nil {
		writeClientError(status)
		return status
	}
	_, _ = os.Stdout.Write(response.Stdout)
	_, _ = os.Stderr.Write(response.Stderr)
	return status
}

func commandArguments(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

func preservedNames(value string) []string {
	if value == "" {
		return nil
	}
	names := strings.Split(value, ",")
	result := names[:0]
	for _, name := range names {
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}

func writeClientError(status int) {
	switch status {
	case 2:
		fmt.Fprintln(os.Stderr, "Workcell blocked malformed privileged package request.")
	case 1:
		fmt.Fprintln(os.Stderr, "Workcell apt broker is unavailable.")
	}
}
