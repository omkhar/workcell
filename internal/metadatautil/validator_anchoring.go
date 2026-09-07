// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// anchoringCheckerPrefix names this file and its test. The check skips both so
// that the needles they carry as text are not counted as call sites.
const anchoringCheckerPrefix = "validator_anchoring"

// CheckValidatorAnchoring requires each validator that anchors on the shared
// shell-invocation parser to run the shared evasion corpus. A validator that
// reads a command out of file content is bypassed by a comment, a heredoc body
// or a longer option unless a negative fixture proves otherwise, so the two
// counts must stay equal.
//
// The check counts call sites with a text scan rather than reading the syntax
// tree, the same choice the other checks in this package record: the call sites
// are few, and a scan avoids go/ast for one caller.
func CheckValidatorAnchoring(rootDir string) error {
	anchors, err := countCallSites(rootDir, "ShellInvocations(", false)
	if err != nil {
		return err
	}
	corpus, err := countCallSites(rootDir, "RequireRejectsAllEvasions(", true)
	if err != nil {
		return err
	}
	if anchors == 0 {
		return errors.New("no validator anchors on the shared shell-invocation parser; the anchoring check has lost its subject")
	}
	if anchors != corpus {
		return fmt.Errorf("validator anchoring parity: %d call(s) of ShellInvocations but %d run(s) of the shared evasion corpus; every anchored validator needs one RequireRejectsAllEvasions run", anchors, corpus)
	}
	return nil
}

// countCallSites returns the number of lines under internal/ that call needle.
// It reads test sources when inTests is set and non-test sources otherwise, and
// it skips declaration lines so that a function is never counted as its own
// caller.
func countCallSites(rootDir, needle string, inTests bool) (int, error) {
	count := 0
	root := filepath.Join(rootDir, "internal")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, anchoringCheckerPrefix) {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") != inTests {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for line := range strings.Lines(string(content)) {
			if strings.Contains(line, needle) && !strings.HasPrefix(line, "func ") {
				count++
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan %s for %s: %w", root, needle, err)
	}
	return count, nil
}
