// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const detachedFIFOPath = "/state/tmp/workcell/session-stdin"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case filepath.Clean(os.Args[0]) == "/bin/sh":
		return runDetachedShell(args)
	case len(args) == 1 && args[0] == "attach":
		return runAttachFixture()
	case len(args) == 2 && args[0] == "lifecycle":
		return runLifecycleFixture(args[1])
	default:
		return fmt.Errorf("usage: %s <attach|lifecycle OUTPUT_PATH>", filepath.Base(os.Args[0]))
	}
}

func runAttachFixture() error {
	time.Sleep(8 * time.Second)
	fmt.Println("attached-from-container")
	time.Sleep(1 * time.Second)
	return nil
}

func runLifecycleFixture(outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(detachedFIFOPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(detachedFIFOPath)
	if err := syscall.Mkfifo(detachedFIFOPath, 0o666); err != nil {
		return err
	}
	if err := os.Chmod(detachedFIFOPath, 0o666); err != nil {
		return err
	}

	out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	go func() {
		for {
			fifo, err := os.OpenFile(detachedFIFOPath, os.O_RDONLY, 0)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return
				}
				time.Sleep(50 * time.Millisecond)
				continue
			}
			_, _ = io.Copy(out, fifo)
			_ = fifo.Close()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	<-sigCh
	_ = os.Remove(detachedFIFOPath)
	return nil
}

func runDetachedShell(args []string) error {
	if len(args) != 4 || args[0] != "-lc" || args[2] != "sh" {
		return fmt.Errorf("unsupported detached shell invocation: %q", strings.Join(args, " "))
	}
	stdinPath := os.Getenv("WORKCELL_DETACHED_STDIN_PATH")
	if stdinPath == "" {
		stdinPath = detachedFIFOPath
	}
	info, err := os.Stat(stdinPath)
	if err != nil {
		return fmt.Errorf("detached session stdin transport is unavailable: %s", stdinPath)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return fmt.Errorf("detached session stdin transport is unavailable: %s", stdinPath)
	}
	fifo, err := os.OpenFile(stdinPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("detached session stdin transport is not writable: %s", stdinPath)
	}
	defer fifo.Close()
	_, err = io.WriteString(fifo, args[3])
	return err
}
