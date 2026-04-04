// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package rootio

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func RelativePathWithin(root, target, label string) (string, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s must stay within %s: %s", label, root, target)
	}
	return rel, nil
}

func WriteFileAtomic(root *os.Root, relativePath string, data []byte, mode os.FileMode, tempPrefix string) error {
	return WriteFileAtomicFromReader(root, relativePath, bytes.NewReader(data), mode, tempPrefix)
}

func WriteFileAtomicFromReader(root *os.Root, relativePath string, source io.Reader, mode os.FileMode, tempPrefix string) error {
	cleanPath, err := normalizeRelativePath(relativePath)
	if err != nil {
		return err
	}
	parent := filepath.Dir(cleanPath)
	if parent != "." {
		if err := root.MkdirAll(parent, 0o700); err != nil {
			return err
		}
	}
	tempFile, tempPath, err := createTempFile(root, parent, tempPrefix, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = tempFile.Close()
		_ = root.Remove(tempPath)
	}()

	if _, err := io.Copy(tempFile, source); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := root.Rename(tempPath, cleanPath); err != nil {
		return err
	}
	return root.Chmod(cleanPath, mode)
}

func normalizeRelativePath(relativePath string) (string, error) {
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("path must be relative to the opened root: %s", relativePath)
	}
	cleanPath := filepath.Clean(relativePath)
	if cleanPath == "." || cleanPath == string(filepath.Separator) {
		return "", fmt.Errorf("path must name a file within the opened root: %s", relativePath)
	}
	return cleanPath, nil
}

func createTempFile(root *os.Root, parent, tempPrefix string, mode os.FileMode) (*os.File, string, error) {
	if tempPrefix == "" {
		tempPrefix = ".workcell-tmp-"
	}
	parent = filepath.Clean(parent)
	if parent == "." {
		parent = ""
	}
	for attempt := 0; attempt < 32; attempt++ {
		suffix, err := randomSuffix()
		if err != nil {
			return nil, "", err
		}
		name := tempPrefix + suffix + ".tmp"
		tempPath := name
		if parent != "" {
			tempPath = filepath.Join(parent, name)
		}
		file, err := root.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err == nil {
			return file, tempPath, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("unable to allocate temporary file under %s", root.Name())
}

func randomSuffix() (string, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", data[:]), nil
}
