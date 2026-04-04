// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package paritytree

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

type Entry struct {
	Path       string
	Mode       fs.FileMode
	Kind       string
	LinkTarget string
	SHA256     string
}

func Snapshot(root string) ([]Entry, error) {
	entries := make([]Entry, 0)
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}

		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := Entry{
			Path: filepath.ToSlash(rel),
			Mode: info.Mode(),
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry.Kind = "symlink"
			entry.LinkTarget = target
		case info.Mode().IsDir():
			entry.Kind = "dir"
		case info.Mode().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(content)
			entry.Kind = "file"
			entry.SHA256 = hex.EncodeToString(sum[:])
		default:
			entry.Kind = "other"
		}
		entries = append(entries, entry)
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

func CompareDirectoryTrees(leftRoot, rightRoot string) error {
	leftSnapshot, err := Snapshot(leftRoot)
	if err != nil {
		return err
	}
	rightSnapshot, err := Snapshot(rightRoot)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(leftSnapshot, rightSnapshot) {
		return nil
	}
	return fmt.Errorf(
		"tree mismatch between %s and %s\nleft=%#v\nright=%#v",
		leftRoot,
		rightRoot,
		leftSnapshot,
		rightSnapshot,
	)
}
