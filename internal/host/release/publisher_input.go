// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	finalTagPattern   = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	rcTagPattern      = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)-rc\.([1-9][0-9]*)$`)
	repositoryPattern = regexp.MustCompile(
		`^([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9-]{0,37}[A-Za-z0-9])/[A-Za-z0-9._-]{1,100}$`,
	)
	toolchainPattern = regexp.MustCompile(`^go(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	gitObjectPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// WorkcellAssetManifestSchemaV1 identifies the exact shipped asset inventory.
const WorkcellAssetManifestSchemaV1 = "workcell.release-assets.v1"

// TagPolicy is the only release-class mapping accepted by Workcell.
type TagPolicy struct {
	Kind       string `json:"kind"`
	Prerelease bool   `json:"prerelease"`
	MakeLatest bool   `json:"make_latest"`
}

// GitHubTagBinding is the exact annotated tag identity observed at GitHub.
type GitHubTagBinding struct {
	Ref             string
	ObjectType      string
	ObjectSHA       string
	PeeledCommitSHA string
}

// TagExpectation binds publication to the independently reviewed tag object
// and the commit reached by peeling that annotated tag.
type TagExpectation struct {
	ObjectSHA       string
	PeeledCommitSHA string
}

// ClassifyTag accepts only final and numbered release-candidate tags.
func ClassifyTag(tag string) (TagPolicy, error) {
	switch {
	case finalTagPattern.MatchString(tag):
		return TagPolicy{Kind: "final", MakeLatest: true}, nil
	case rcTagPattern.MatchString(tag):
		return TagPolicy{Kind: "rc", Prerelease: true}, nil
	default:
		return TagPolicy{}, inputErrorf("unsupported release tag %q; expected vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N with no leading zeroes and N >= 1", tag)
	}
}

func validateRepository(repository string) error {
	if !repositoryPattern.MatchString(repository) {
		return inputErrorf("GitHub repository must be one safe OWNER/REPO identifier, got %q", repository)
	}
	_, name, _ := strings.Cut(repository, "/")
	if name == "." || name == ".." {
		return inputErrorf("GitHub repository must be one safe OWNER/REPO identifier, got %q", repository)
	}
	return nil
}

func validateToolchain(toolchain string) error {
	if !toolchainPattern.MatchString(toolchain) {
		return inputErrorf("toolchain must be exact goX.Y.Z, got %q", toolchain)
	}
	return nil
}

func validateTagExpectation(expected TagExpectation) error {
	if !gitObjectPattern.MatchString(expected.ObjectSHA) {
		return inputErrorf("expected annotated tag object SHA must be exactly 40 lowercase hexadecimal characters")
	}
	if !gitObjectPattern.MatchString(expected.PeeledCommitSHA) {
		return inputErrorf("expected peeled tag commit SHA must be exactly 40 lowercase hexadecimal characters")
	}
	if expected.ObjectSHA == expected.PeeledCommitSHA {
		return inputErrorf("expected annotated tag object SHA and peeled tag commit SHA must differ")
	}
	return nil
}

func validateTagBinding(tag string, expected TagExpectation, observed GitHubTagBinding) error {
	if _, err := ClassifyTag(tag); err != nil {
		return err
	}
	if err := validateTagExpectation(expected); err != nil {
		return err
	}
	wantRef := "refs/tags/" + tag
	if observed.Ref != wantRef {
		return fmt.Errorf("tag ref = %q, want %q", observed.Ref, wantRef)
	}
	if observed.ObjectType != "tag" {
		return fmt.Errorf("tag object type = %q, want annotated tag object type tag", observed.ObjectType)
	}
	if observed.ObjectSHA != expected.ObjectSHA {
		return fmt.Errorf("annotated tag object SHA = %q, want %q", observed.ObjectSHA, expected.ObjectSHA)
	}
	if observed.PeeledCommitSHA != expected.PeeledCommitSHA {
		return fmt.Errorf("peeled tag commit SHA = %q, want %q", observed.PeeledCommitSHA, expected.PeeledCommitSHA)
	}
	return nil
}

func expectedWorkcellAssetNames(tag string) []string {
	bundle := "workcell-" + tag + ".tar.gz"
	return []string{
		bundle,
		bundle + ".sigstore.json",
		"workcell.rb",
		"workcell.rb.sigstore.json",
		"workcell-image.digest",
		"workcell-image.digest.sigstore.json",
		"workcell-build-inputs.json",
		"workcell-build-inputs.sigstore.json",
		"workcell-control-plane.json",
		"workcell-control-plane.sigstore.json",
		"workcell-builder-environment.json",
		"workcell-builder-environment.sigstore.json",
		"SHA256SUMS",
		"SHA256SUMS.sigstore.json",
		"workcell-source.spdx.json",
		"workcell-source.spdx.sigstore.json",
		"workcell-image.spdx.json",
		"workcell-image.spdx.sigstore.json",
	}
}

func orderWorkcellAssetPaths(tag string, paths []string) ([]string, error) {
	if _, err := ClassifyTag(tag); err != nil {
		return nil, err
	}
	expectedNames := expectedWorkcellAssetNames(tag)
	if len(expectedNames) != maxReleaseAssetCount {
		return nil, fmt.Errorf("release asset inventory %s contains %d names, want %d", WorkcellAssetManifestSchemaV1, len(expectedNames), maxReleaseAssetCount)
	}
	expected := make(map[string]struct{}, len(expectedNames))
	for _, name := range expectedNames {
		expected[name] = struct{}{}
	}
	byName := make(map[string]string, len(paths))
	for _, path := range paths {
		name := filepath.Base(path)
		if !assetNamePattern.MatchString(name) || name == "." || name == ".." {
			return nil, inputErrorf("release asset %q basename %q is not GitHub-safe; use 1-255 ASCII letters, digits, dots, underscores, or hyphens and begin with a letter or digit", path, name)
		}
		if _, ok := expected[name]; !ok {
			return nil, inputErrorf("release asset inventory %s contains unexpected basename %q", WorkcellAssetManifestSchemaV1, name)
		}
		if _, duplicate := byName[name]; duplicate {
			return nil, inputErrorf("duplicate release asset basename %q; every upload name must be unique", name)
		}
		byName[name] = path
	}
	ordered := make([]string, 0, len(expectedNames))
	for _, name := range expectedNames {
		path, ok := byName[name]
		if !ok {
			return nil, inputErrorf("release asset inventory %s is missing required basename %q", WorkcellAssetManifestSchemaV1, name)
		}
		ordered = append(ordered, path)
	}
	return ordered, nil
}

func inspectWorkcellAssets(tag string, paths []string) ([]localAsset, error) {
	return inspectWorkcellAssetsWithOpener(tag, paths, openatAssetSourceOpener{})
}

func inspectWorkcellAssetsWithOpener(tag string, paths []string, opener assetSourceOpener) ([]localAsset, error) {
	ordered, err := orderWorkcellAssetPaths(tag, paths)
	if err != nil {
		return nil, err
	}
	return inspectLocalAssetsWithOpener(ordered, opener)
}
