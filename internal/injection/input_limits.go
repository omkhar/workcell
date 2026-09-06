// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"fmt"
	"io"
	"os"
)

// Injection inputs are operator-selected host material. These limits bound
// memory, disk, and metadata work when a selected file or directory is staged.
const (
	maxInjectionFileBytes      int64 = 16 * 1024 * 1024
	maxInjectionTreeBytes      int64 = 64 * 1024 * 1024
	maxInjectionTreeEntries          = 4096
	maxInjectionMountPathBytes       = 4096
)

// injectionTreeBudget accumulates the bytes and entries consumed by every
// selected input in one bundle render or one direct-mount staging pass, so a
// set of individually legal inputs cannot add up to an unbounded copy.
type injectionTreeBudget struct {
	bytes   int64
	entries int
}

func newInjectionTreeBudget() *injectionTreeBudget {
	return &injectionTreeBudget{}
}

func (budget *injectionTreeBudget) remainingBytes() int64 {
	return maxInjectionTreeBytes - budget.bytes
}

func (budget *injectionTreeBudget) remainingEntries() int {
	return maxInjectionTreeEntries - budget.entries
}

func (budget *injectionTreeBudget) addEntries(source string, count int) error {
	if count > budget.remainingEntries() {
		return fmt.Errorf("injection tree %s exceeds the aggregate entry limit of %d", source, maxInjectionTreeEntries)
	}
	budget.entries += count
	return nil
}

// accountInjectionFileSize charges a known file size against the per-file and
// aggregate limits without reading the file. Copy paths that stream bytes use
// this instead of buffering the whole input.
func accountInjectionFileSize(size int64, source string, budget *injectionTreeBudget) error {
	if size < 0 || size > maxInjectionFileBytes {
		return fmt.Errorf("injection input %s exceeds the per-file limit of %d bytes", source, maxInjectionFileBytes)
	}
	if budget == nil {
		return nil
	}
	if size > budget.remainingBytes() {
		return fmt.Errorf("injection input %s exceeds the aggregate tree limit of %d bytes", source, maxInjectionTreeBytes)
	}
	budget.bytes += size
	return nil
}

// accountInjectionSourceSize charges one already-validated regular file that is
// bind-mounted rather than copied. Direct mounts skip the read paths, so this is
// where their material enters the shared budget.
func accountInjectionSourceSize(source Path, budget *injectionTreeBudget) error {
	info, err := os.Stat(source.String())
	if err != nil {
		return err
	}
	if err := budget.addEntries(source.String(), 1); err != nil {
		return err
	}
	return accountInjectionFileSize(info.Size(), source.String(), budget)
}

// readInjectionFile reads an already-opened injection input under the smaller
// of the per-file limit and the budget's remaining bytes. The read stops one
// byte past that limit, so an oversize input is refused without being buffered
// whole.
func readInjectionFile(reader io.Reader, source string, budget *injectionTreeBudget) ([]byte, error) {
	limit := maxInjectionFileBytes
	limitDescription := fmt.Sprintf("the per-file limit of %d bytes", maxInjectionFileBytes)
	if budget != nil && budget.remainingBytes() < limit {
		limit = budget.remainingBytes()
		limitDescription = fmt.Sprintf("the aggregate tree limit of %d bytes", maxInjectionTreeBytes)
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read injection input %s: %w", source, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("injection input %s exceeds %s", source, limitDescription)
	}
	if budget != nil {
		budget.bytes += int64(len(data))
	}
	return data, nil
}

// readInjectionPath opens one regular file through the no-follow direct-mount
// opener and reads it under the injection input limits.
func readInjectionPath(source Path, budget *injectionTreeBudget) ([]byte, error) {
	file, _, kind, err := openDirectMountSource(source.String())
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if kind != directMountSourceRegular {
		return nil, fmt.Errorf("injection source must be a regular file: %s", source)
	}
	return readInjectionFile(file, source.String(), budget)
}
