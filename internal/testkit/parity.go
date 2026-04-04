// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// CommandSpec describes a command invocation for parity checks.
type CommandSpec struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}

// CommandResult captures the observable surface of a command invocation.
type CommandResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

// TreeEntry describes one file-system entry in a snapshot.
type TreeEntry struct {
	Path       string
	Mode       os.FileMode
	Kind       string
	SHA256     string
	LinkTarget string
}

// TreePair identifies two roots that should contain the same tree.
type TreePair struct {
	LeftRoot  string
	RightRoot string
}

// ParityCase compares two command invocations and optional output trees.
type ParityCase struct {
	Name      string
	Left      CommandSpec
	Right     CommandSpec
	TreePairs []TreePair
}

// RunCommand executes a command and captures stdout, stderr, exit code, and duration.
func RunCommand(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	if spec.Env != nil {
		cmd.Env = append([]string(nil), spec.Env...)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now()
	err := cmd.Run()
	result := CommandResult{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Duration: time.Since(startedAt),
	}

	if err == nil {
		result.ExitCode = 0
		return result, nil
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return result, err
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return result, err
	}
	return result, err
}

// CompareCommandResults reports the first mismatch between two command results.
func CompareCommandResults(leftLabel, rightLabel string, left, right CommandResult) error {
	if left.ExitCode != right.ExitCode {
		return fmt.Errorf("%s exit code %d != %s exit code %d", leftLabel, left.ExitCode, rightLabel, right.ExitCode)
	}
	if !bytes.Equal(left.Stdout, right.Stdout) {
		return fmt.Errorf("%s stdout mismatch with %s", leftLabel, rightLabel)
	}
	if !bytes.Equal(left.Stderr, right.Stderr) {
		return fmt.Errorf("%s stderr mismatch with %s", leftLabel, rightLabel)
	}
	return nil
}

// SnapshotTree records a deterministic snapshot of a directory tree.
func SnapshotTree(root string) ([]TreeEntry, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	var entries []TreeEntry
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}

		entry := TreeEntry{
			Path: rel,
			Mode: info.Mode(),
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			entry.Kind = "symlink"
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry.LinkTarget = target
		case info.IsDir():
			entry.Kind = "dir"
		case info.Mode().IsRegular():
			entry.Kind = "file"
			digest, err := hashFile(path)
			if err != nil {
				return err
			}
			entry.SHA256 = digest
		default:
			entry.Kind = "other"
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// CompareTreeSnapshots reports the first mismatch between two tree snapshots.
func CompareTreeSnapshots(leftLabel string, left []TreeEntry, rightLabel string, right []TreeEntry) error {
	if len(left) != len(right) {
		return fmt.Errorf("%s has %d entries, %s has %d entries", leftLabel, len(left), rightLabel, len(right))
	}
	for i := range left {
		l := left[i]
		r := right[i]
		if l.Path != r.Path {
			return fmt.Errorf("tree path mismatch at index %d: %s != %s", i, l.Path, r.Path)
		}
		if l.Kind != r.Kind {
			return fmt.Errorf("tree kind mismatch for %s: %s != %s", l.Path, l.Kind, r.Kind)
		}
		if l.Mode != r.Mode {
			return fmt.Errorf("tree mode mismatch for %s: %s != %s", l.Path, l.Mode, r.Mode)
		}
		if l.SHA256 != r.SHA256 {
			return fmt.Errorf("tree sha256 mismatch for %s", l.Path)
		}
		if l.LinkTarget != r.LinkTarget {
			return fmt.Errorf("tree symlink target mismatch for %s: %s != %s", l.Path, l.LinkTarget, r.LinkTarget)
		}
	}
	return nil
}

// CompareDirectoryTrees snapshots and compares two directories.
func CompareDirectoryTrees(leftRoot, rightRoot string) error {
	left, err := SnapshotTree(leftRoot)
	if err != nil {
		return fmt.Errorf("snapshot %s: %w", leftRoot, err)
	}
	right, err := SnapshotTree(rightRoot)
	if err != nil {
		return fmt.Errorf("snapshot %s: %w", rightRoot, err)
	}
	return CompareTreeSnapshots(leftRoot, left, rightRoot, right)
}

// RunParityCase executes both sides of a parity case and compares outputs and trees.
func RunParityCase(ctx context.Context, c ParityCase) error {
	left, err := RunCommand(ctx, c.Left)
	if err != nil {
		return fmt.Errorf("%s left command: %w", c.Name, err)
	}
	right, err := RunCommand(ctx, c.Right)
	if err != nil {
		return fmt.Errorf("%s right command: %w", c.Name, err)
	}
	if err := CompareCommandResults(c.Name+" left", c.Name+" right", left, right); err != nil {
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	for _, pair := range c.TreePairs {
		if err := CompareDirectoryTrees(pair.LeftRoot, pair.RightRoot); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
	}
	return nil
}

func hashFile(path string) (string, error) {
	handle, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer handle.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, handle); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
