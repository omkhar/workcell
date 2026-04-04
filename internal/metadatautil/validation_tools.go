// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9]{40,}`),
	regexp.MustCompile(`AIza[A-Za-z0-9\-_]{35}`),
	regexp.MustCompile(`ya29\.[A-Za-z0-9\-_]+`),
}

func ValidateJSONFiles(paths []string) error {
	for _, path := range paths {
		var decoded any
		if err := readJSONFile(path, &decoded); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

func ValidateTOMLFiles(paths []string) error {
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var decoded any
		if err := toml.Unmarshal(content, &decoded); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

func ScanCredentialPatterns(rootDir string) error {
	scanRoots := []string{
		filepath.Join(rootDir, "tests"),
		filepath.Join(rootDir, "docs", "examples"),
	}

	var findings []string
	for _, scanRoot := range scanRoots {
		info, err := os.Stat(scanRoot)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !info.IsDir() {
			continue
		}

		err = filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			text := string(content)
			for _, pattern := range credentialPatterns {
				if pattern.MatchString(text) {
					findings = append(findings, fmt.Sprintf("Possible credential in %s", path))
					break
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if len(findings) == 0 {
		return nil
	}

	sort.Strings(findings)
	return fmt.Errorf("%s\nFound %d possible credential(s) in tests/ or docs/examples/", strings.Join(findings, "\n"), len(findings))
}
