// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin

package release

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReleaseAssetACLDarwin(t *testing.T) {
	t.Run("source file", func(t *testing.T) {
		path := writeAssetFile(t, "asset", []byte("asset"))
		addDarwinACL(t, path)
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = unix.Close(fd) })
		if err := rejectExtendedACL(fd); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("rejectExtendedACL() = %v, want ErrInvalidInput", err)
		}
		if _, err := inspectLocalAssets([]string{path}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("inspectLocalAssets() = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("source ancestor", func(t *testing.T) {
		directory := t.TempDir()
		path := directory + "/asset"
		if err := os.WriteFile(path, []byte("asset"), 0o600); err != nil {
			t.Fatal(err)
		}
		addDarwinACL(t, directory)
		if _, err := inspectLocalAssets([]string{path}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "directory") {
			t.Fatalf("inspectLocalAssets() = %v, want directory ErrInvalidInput", err)
		}
	})
	t.Run("ACL mutation during read", func(t *testing.T) {
		path := writeAssetFile(t, "asset", []byte("asset"))
		base := openatAssetSourceOpener{}
		opener := foundationOpenerFunc(func(candidate string) (assetSource, error) {
			source, err := base.Open(candidate)
			if err != nil {
				return nil, err
			}
			return &aclMutationSource{
				assetSource: source,
				mutate: func() error {
					return addDarwinACLError(candidate)
				},
			}, nil
		})
		if _, err := inspectLocalAssetsWithOpener([]string{path}, opener); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("inspectLocalAssetsWithOpener() = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("staging directory inheritance", func(t *testing.T) {
		tempRoot := t.TempDir()
		addDarwinInheritedACL(t, tempRoot)
		t.Setenv("TMPDIR", tempRoot)
		if stage, err := createStagedAssetContent(); !errors.Is(err, ErrInvalidInput) {
			if stage != nil {
				_ = discardStagedAssetContent(stage, "asset")
			}
			t.Fatalf("createStagedAssetContent() = %v, want ErrInvalidInput", err)
		}
	})
	t.Run("linked staging handle", func(t *testing.T) {
		stage, err := createStagedAssetContent()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = discardStagedAssetContent(stage, "asset") })
		if _, err := stage.Write([]byte("asset")); err != nil {
			t.Fatal(err)
		}
		if err := stage.Sync(); err != nil {
			t.Fatal(err)
		}
		addDarwinACL(t, stage.file.Name())
		if reader, err := sealStagedAssetContent(stage, "asset", 5); !errors.Is(err, ErrInvalidInput) {
			if reader != nil {
				_ = reader.Close()
			}
			t.Fatalf("sealStagedAssetContent() = %v, want ErrInvalidInput", err)
		}
	})
}

func addDarwinACL(t *testing.T, path string) {
	t.Helper()
	if err := addDarwinACLCall(path, "everyone deny write"); err != nil {
		t.Fatal(err)
	}
}

func addDarwinACLError(path string) error { return addDarwinACLCall(path, "everyone deny write") }

func addDarwinInheritedACL(t *testing.T, path string) {
	t.Helper()
	if err := addDarwinACLCall(path, "everyone deny write,file_inherit,directory_inherit"); err != nil {
		t.Fatal(err)
	}
}

func addDarwinACLCall(path, acl string) error {
	output, err := exec.Command("/bin/chmod", "+a", acl, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("add Darwin ACL: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
