// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
)

// fixturePatchPaths are the paths that refresh-action-inputs and
// flag-inventory restamp write.
var fixturePatchPaths = []string{"tests/fixtures/actions/inputs.tsv", "tests/fixtures/flags"}

// FixturePatch returns a git patch of the fixture paths in the work tree at
// rootDir against its index: changed, deleted and new files. New files are
// marked intent-to-add first, since a plain git diff leaves them out and a
// missing fixture would then pass as clean. It fails when git status sees a
// fixture change that the patch does not carry.
func FixturePatch(rootDir string) ([]byte, error) {
	untracked, err := fixtureGit(rootDir, nil, "ls-files", "-z", "--others", "--")
	if err != nil {
		return nil, err
	}
	if len(untracked) > 0 {
		// -f: a bump-tree .gitignore must not hide a regenerated fixture.
		if _, err := fixtureGit(rootDir, untracked, "add", "--intent-to-add", "--force", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			return nil, err
		}
	}
	patch, err := fixtureGit(rootDir, nil, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--")
	if err != nil {
		return nil, err
	}
	status, err := fixtureGit(rootDir, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--")
	if err != nil {
		return nil, err
	}
	if len(status) > 0 && len(patch) == 0 {
		return nil, errors.New("the regenerated fixtures differ from the bump tree, but the patch is empty")
	}
	return patch, nil
}

// fixtureGit runs git in rootDir with the fixture paths appended after
// args, which must end in "--" unless stdin carries the paths.
func fixtureGit(rootDir string, stdin []byte, args ...string) ([]byte, error) {
	full := append([]string{"-C", rootDir, "-c", "core.quotePath=false"}, args...)
	if stdin == nil {
		full = append(full, fixturePatchPaths...)
	}
	cmd := exec.Command("git", full...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, bytes.TrimSpace(stderr.Bytes()))
	}
	return out, nil
}
