// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build linux

package release

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReleaseAssetACLLinux(t *testing.T) {
	requireLinuxACL(t)
	foreignUID := strconv.FormatUint(uint64(os.Geteuid())+1, 10)

	t.Run("source file", func(t *testing.T) {
		path := writeAssetFile(t, "asset", []byte("asset"))
		addLinuxACL(t, path, "u:"+foreignUID+":r--")
		assertLinuxMode(t, path, 0o600)
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
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		path := directory + "/asset"
		if err := os.WriteFile(path, []byte("asset"), 0o600); err != nil {
			t.Fatal(err)
		}
		addLinuxACL(t, directory, "u:"+foreignUID+":rwx")
		assertLinuxMode(t, directory, 0o700)
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
					return addLinuxACLError(candidate, "u:"+foreignUID+":r--")
				},
			}, nil
		})
		if _, err := inspectLocalAssetsWithOpener([]string{path}, opener); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("inspectLocalAssetsWithOpener() = %v, want ErrInvalidInput", err)
		}
		assertLinuxMode(t, path, 0o600)
	})

	t.Run("staging directory inheritance", func(t *testing.T) {
		tempRoot := t.TempDir()
		if err := os.Chmod(tempRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		addLinuxACL(t, tempRoot, "d:u:"+foreignUID+":rwx")
		assertLinuxMode(t, tempRoot, 0o700)
		t.Setenv("TMPDIR", tempRoot)
		if stage, err := createStagedAssetContent(); !errors.Is(err, ErrInvalidInput) {
			if stage != nil {
				_ = discardStagedAssetContent(stage, "asset")
			}
			t.Fatalf("createStagedAssetContent() = %v, want ErrInvalidInput", err)
		}
		assertLinuxMode(t, tempRoot, 0o700)
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
		addLinuxACL(t, stage.file.Name(), "u:"+foreignUID+":r--")
		assertLinuxMode(t, stage.file.Name(), 0o600)
		if reader, err := sealStagedAssetContent(stage, "asset", 5); !errors.Is(err, ErrInvalidInput) {
			if reader != nil {
				_ = reader.Close()
			}
			t.Fatalf("sealStagedAssetContent() = %v, want ErrInvalidInput", err)
		}
	})
}

func requireLinuxACL(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("setfacl"); err == nil {
		return
	} else if os.Getenv("WORKCELL_REQUIRE_NATIVE_ACL_TESTS") == "1" {
		t.Fatalf("setfacl is required for native Linux ACL tests: %v", err)
	}
	t.Skip("setfacl is not installed")
}

func addLinuxACL(t *testing.T, path, entry string) {
	t.Helper()
	if err := addLinuxACLError(path, entry); err != nil {
		t.Fatal(err)
	}
}

func addLinuxACLError(path, entry string) error {
	output, err := exec.Command("setfacl", "-n", "-m", entry, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("add Linux ACL: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func assertLinuxMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode for %q = %o, want %o", path, got, want)
	}
}
