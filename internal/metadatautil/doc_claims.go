// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
	"golang.org/x/sys/unix"
)

// DocClaimHits reads one "doc<TAB>path" line per path a doc cites and writes
// "doc<TAB>rule<TAB>path" for each path that anchors nothing: escaping-path
// for a .. or symlink component, and missing-path for a path that does not
// exist. The probe walks descriptors from rootDir without following a link, so
// a component swapped for a symlink during the walk fails rather than passes.
func DocClaimHits(rootDir string, cited io.Reader, hits io.Writer) error {
	root, err := os.Open(rootDir)
	if err != nil {
		return err
	}
	defer root.Close()
	lines := bufio.NewScanner(cited)
	for lines.Scan() {
		doc, path, found := strings.Cut(lines.Text(), "\t")
		if !found {
			return fmt.Errorf("doc claim line has no tab: %q", lines.Text())
		}
		rule, err := docClaimRule(root, path)
		if err != nil {
			return fmt.Errorf("probe %s cited in %s: %w", path, doc, err)
		}
		if rule != "" {
			if _, err := fmt.Fprintf(hits, "%s\t%s\t%s\n", doc, rule, path); err != nil {
				return err
			}
		}
	}
	return lines.Err()
}

// docClaimRule returns the rule path breaks, or the empty string. A ..
// component fails before any probe, since it may leave the repository.
func docClaimRule(root *os.File, path string) (string, error) {
	if slices.Contains(strings.Split(path, "/"), "..") {
		return "escaping-path", nil
	}
	info, err := rootio.LstatAtNoFollow(root, path)
	switch {
	case errors.Is(err, unix.ELOOP) || (err == nil && info.Mode&unix.S_IFMT == unix.S_IFLNK):
		return "escaping-path", nil
	case errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR):
		return "missing-path", nil
	case err != nil:
		return "", err
	}
	return "", nil
}
