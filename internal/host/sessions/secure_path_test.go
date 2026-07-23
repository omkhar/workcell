// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenAbsoluteDirectoryPositiveNestedExistingAndCreate(t *testing.T) {
	root := physicalTempDir(t)
	existing := filepath.Join(root, "existing", "leaf")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	fd, err := openAbsoluteDirectory(existing, false, defaultDescriptorFS(), requireOwnerOnlyDirectory)
	if err != nil {
		t.Fatalf("open existing nested directory: %v", err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}

	created := filepath.Join(root, "created", "leaf")
	fd, err = openAbsoluteDirectory(created, true, defaultDescriptorFS(), requireOwnerOnlyDirectory)
	if err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(created), created} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %o", path, info.Mode().Perm())
		}
	}
}

func TestOpenAbsoluteDirectoryRejectsNonCanonicalRawPathsBeforeIO(t *testing.T) {
	root := physicalTempDir(t)
	paths := []string{
		"relative/path",
		string(filepath.Separator),
		" " + filepath.Join(root, "leaf"),
		filepath.Join(root, "leaf") + " ",
		root + "//leaf",
		root + "/./leaf",
		root + "/../leaf",
		root + "/leaf/",
		root + "/ leaf",
		root + "/leaf\x00suffix",
		"//tmp/leaf",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			fs := defaultDescriptorFS()
			opens := 0
			fs.openat = func(int, string, int, uint32) (int, error) {
				opens++
				return -1, errors.New("unexpected open")
			}
			if fd, err := openAbsoluteDirectory(path, true, fs, requireOwnerOnlyDirectory); err == nil {
				unix.Close(fd)
				t.Fatal("unsafe raw path was accepted")
			}
			if opens != 0 {
				t.Fatalf("unsafe raw path performed %d opens", opens)
			}
		})
	}
}

func TestOpenAbsoluteDirectoryRejectsSymlinkAncestorAndLeaf(t *testing.T) {
	for _, create := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "missing"}[create], func(t *testing.T) {
			root := physicalTempDir(t)
			realParent := filepath.Join(root, "real")
			if err := os.Mkdir(realParent, 0o700); err != nil {
				t.Fatal(err)
			}
			if !create {
				if err := os.Mkdir(filepath.Join(realParent, "target"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(realParent, filepath.Join(root, "redirect")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "redirect", "target")
			if fd, err := openAbsoluteDirectory(path, create, defaultDescriptorFS(), requireOwnerOnlyDirectory); err == nil {
				unix.Close(fd)
				t.Fatal("symlink ancestor was accepted")
			}
			if create {
				if _, err := os.Lstat(filepath.Join(realParent, "target")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("walker created through symlink ancestor: %v", err)
				}
			}
		})
	}

	root := physicalTempDir(t)
	realLeaf := filepath.Join(root, "real-leaf")
	if err := os.Mkdir(realLeaf, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkLeaf := filepath.Join(root, "leaf")
	if err := os.Symlink(realLeaf, symlinkLeaf); err != nil {
		t.Fatal(err)
	}
	if fd, err := openAbsoluteDirectory(symlinkLeaf, false, defaultDescriptorFS(), requireOwnerOnlyDirectory); err == nil {
		unix.Close(fd)
		t.Fatal("symlink leaf was accepted")
	}
}

func TestOpenAbsoluteDirectoryRejectsUnsafeDirectoryBelowAnchor(t *testing.T) {
	root := physicalTempDir(t)
	unsafe := filepath.Join(root, "writable")
	leaf := filepath.Join(unsafe, "leaf")
	if err := os.MkdirAll(leaf, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0o777); err != nil {
		t.Fatal(err)
	}
	if fd, err := openAbsoluteDirectory(leaf, false, defaultDescriptorFS(), requireOwnerOnlyDirectory); err == nil {
		unix.Close(fd)
		t.Fatal("writable directory below owner-controlled anchor was accepted")
	}
	if err := os.Chmod(unsafe, 0o700); err != nil {
		t.Fatal(err)
	}
	var unsafeStat unix.Stat_t
	if err := unix.Stat(unsafe, &unsafeStat); err != nil {
		t.Fatal(err)
	}
	fs := defaultDescriptorFS()
	originalFstat := fs.fstat
	fs.fstat = func(fd int, stat *unix.Stat_t) error {
		if err := originalFstat(fd, stat); err != nil {
			return err
		}
		if sameFileIdentity(*stat, unsafeStat) {
			stat.Uid = uint32(os.Geteuid()) + 1
		}
		return nil
	}
	if fd, err := openAbsoluteDirectory(leaf, false, fs, requireOwnerOnlyDirectory); err == nil {
		unix.Close(fd)
		t.Fatal("foreign-owned directory below owner-controlled anchor was accepted")
	}
}

func TestValidateWalkDirectoryEstablishesRootOnlyAnchor(t *testing.T) {
	rootSystem := unix.Stat_t{Mode: unix.S_IFDIR | 0o755, Uid: 0, Ino: 1}
	rootSticky := unix.Stat_t{Mode: unix.S_IFDIR | unix.S_ISVTX | 0o777, Uid: 0, Ino: 1}
	rootOnly := unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: 0, Ino: 1}

	anchored, err := validateWalkDirectoryForUser("/", rootSystem, false, 0)
	if err != nil || anchored {
		t.Fatalf("root system directory: anchored = %v, error = %v", anchored, err)
	}
	anchored, err = validateWalkDirectoryForUser("/tmp", rootSticky, false, 0)
	if err != nil || anchored {
		t.Fatalf("root sticky transit directory: anchored = %v, error = %v", anchored, err)
	}
	anchored, err = validateWalkDirectoryForUser("/root", rootOnly, false, 0)
	if err != nil || !anchored {
		t.Fatalf("root-only directory: anchored = %v, error = %v", anchored, err)
	}
	if _, err := validateWalkDirectoryForUser("/root/writable", rootSticky, anchored, 0); err == nil {
		t.Fatal("root-owned sticky writable directory below the anchor was accepted")
	}
	if stillAnchored, err := validateWalkDirectoryForUser("/root/private", rootOnly, anchored, 0); err != nil || !stillAnchored {
		t.Fatalf("root-only directory below anchor: anchored = %v, error = %v", stillAnchored, err)
	}
}

func TestOpenAbsoluteDirectoryHandlesMkdirEEXISTRaces(t *testing.T) {
	tests := []struct {
		name    string
		install func(string) error
		wantOK  bool
	}{
		{name: "owner-only directory", install: func(path string) error { return os.Mkdir(path, 0o700) }, wantOK: true},
		{name: "symlink", install: func(path string) error { return os.Symlink(filepath.Dir(path), path) }},
		{name: "writable directory", install: func(path string) error {
			if err := os.Mkdir(path, 0o700); err != nil {
				return err
			}
			return os.Chmod(path, 0o777)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := physicalTempDir(t)
			path := filepath.Join(root, "target")
			fs := defaultDescriptorFS()
			mkdirCalls, chmodCalls := 0, 0
			fs.mkdirat = func(int, string, uint32) error {
				mkdirCalls++
				if err := test.install(path); err != nil {
					return err
				}
				return unix.EEXIST
			}
			originalChmod := fs.fchmod
			fs.fchmod = func(fd int, mode uint32) error {
				chmodCalls++
				return originalChmod(fd, mode)
			}
			fd, err := openAbsoluteDirectory(path, true, fs, requireOwnerOnlyDirectory)
			if test.wantOK {
				if err != nil {
					t.Fatalf("benign EEXIST race failed: %v", err)
				}
				unix.Close(fd)
			} else if err == nil {
				unix.Close(fd)
				t.Fatal("unsafe EEXIST race was accepted")
			}
			if mkdirCalls != 1 || chmodCalls != 0 {
				t.Fatalf("mkdir calls = %d, chmod calls = %d", mkdirCalls, chmodCalls)
			}
		})
	}
}

func TestOpenAbsoluteDirectoryCreateRetryRepairsSkippedParentSync(t *testing.T) {
	parent := physicalTempDir(t)
	path := filepath.Join(parent, "created")
	injected := errors.New("injected parent sync failure")
	fs := defaultDescriptorFS()
	originalOpen, originalMkdir, originalSync, originalClose := fs.openat, fs.mkdirat, fs.fsync, fs.close
	created, failed, targetFD, targetCloses := false, false, -1, 0
	fs.openat = func(dirFD int, name string, flags int, mode uint32) (int, error) {
		fd, err := originalOpen(dirFD, name, flags, mode)
		if err == nil && name == filepath.Base(path) {
			targetFD = fd
		}
		return fd, err
	}
	fs.mkdirat = func(fd int, name string, mode uint32) error {
		err := originalMkdir(fd, name, mode)
		if err == nil && name == filepath.Base(path) {
			created = true
		}
		return err
	}
	fs.fsync = func(fd int) error {
		if created && !failed {
			failed = true
			return injected // Deliberately skip the real fsync.
		}
		return originalSync(fd)
	}
	fs.close = func(fd int) error {
		if fd == targetFD {
			targetCloses++
		}
		return originalClose(fd)
	}
	if fd, err := openAbsoluteDirectory(path, true, fs, requireOwnerOnlyDirectory); !errors.Is(err, injected) {
		if fd >= 0 {
			unix.Close(fd)
		}
		t.Fatalf("first create error = %v", err)
	}
	if targetCloses != 1 {
		t.Fatalf("target closes after fsync failure = %d", targetCloses)
	}

	var parentStat unix.Stat_t
	if err := unix.Stat(parent, &parentStat); err != nil {
		t.Fatal(err)
	}
	retryFS := defaultDescriptorFS()
	originalRetrySync := retryFS.fsync
	parentSyncs := 0
	retryFS.fsync = func(fd int) error {
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			return err
		}
		if sameFileIdentity(parentStat, stat) {
			parentSyncs++
		}
		return originalRetrySync(fd)
	}
	fd, err := openAbsoluteDirectory(path, true, retryFS, requireOwnerOnlyDirectory)
	if err != nil {
		t.Fatalf("create retry failed: %v", err)
	}
	unix.Close(fd)
	if parentSyncs == 0 {
		t.Fatal("create retry did not repair the containing-directory sync")
	}
}

func TestOpenAbsoluteDirectoryCleansUpAfterFchmodAndFstatFailures(t *testing.T) {
	for _, operation := range []string{"fchmod", "fstat"} {
		t.Run(operation, func(t *testing.T) {
			root := physicalTempDir(t)
			path := filepath.Join(root, "target")
			injected := errors.New("injected " + operation + " failure")
			fs := defaultDescriptorFS()
			originalOpen, originalClose := fs.openat, fs.close
			targetFD, targetCloses := -1, 0
			fs.openat = func(dirFD int, name string, flags int, mode uint32) (int, error) {
				fd, err := originalOpen(dirFD, name, flags, mode)
				if err == nil && name == "target" {
					targetFD = fd
				}
				return fd, err
			}
			if operation == "fchmod" {
				fs.fchmod = func(int, uint32) error { return injected }
			} else {
				originalFstat := fs.fstat
				fs.fstat = func(fd int, stat *unix.Stat_t) error {
					if fd == targetFD {
						return injected
					}
					return originalFstat(fd, stat)
				}
			}
			fs.close = func(fd int) error {
				if fd == targetFD {
					targetCloses++
				}
				return originalClose(fd)
			}
			if fd, err := openAbsoluteDirectory(path, true, fs, requireOwnerOnlyDirectory); !errors.Is(err, injected) {
				if fd >= 0 {
					unix.Close(fd)
				}
				t.Fatalf("error = %v", err)
			}
			if targetCloses != 1 {
				t.Fatalf("target closes = %d", targetCloses)
			}
		})
	}
}

func TestOpenAbsoluteDirectoryJoinsCloseFailuresWithoutRetry(t *testing.T) {
	root := physicalTempDir(t)
	path := filepath.Join(root, "target")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	parentFailure := errors.New("parent close failure")
	targetFailure := errors.New("target cleanup failure")
	fs := defaultDescriptorFS()
	originalOpen, originalClose := fs.openat, fs.close
	parentFD, targetFD := -1, -1
	parentCloses, targetCloses := 0, 0
	fs.openat = func(dirFD int, name string, flags int, mode uint32) (int, error) {
		fd, err := originalOpen(dirFD, name, flags, mode)
		if err == nil && name == "target" {
			parentFD, targetFD = dirFD, fd
		}
		return fd, err
	}
	fs.close = func(fd int) error {
		realErr := originalClose(fd)
		switch fd {
		case parentFD:
			parentCloses++
			return errors.Join(realErr, parentFailure)
		case targetFD:
			targetCloses++
			return errors.Join(realErr, targetFailure)
		default:
			return realErr
		}
	}
	fd, err := openAbsoluteDirectory(path, false, fs, requireOwnerOnlyDirectory)
	if fd >= 0 {
		unix.Close(fd)
	}
	if !errors.Is(err, parentFailure) || !errors.Is(err, targetFailure) {
		t.Fatalf("joined close error = %v", err)
	}
	if parentCloses != 1 || targetCloses != 1 {
		t.Fatalf("parent closes = %d, target closes = %d", parentCloses, targetCloses)
	}
}

func TestOpenAbsoluteDirectoryAppliesFinalRootPolicies(t *testing.T) {
	root := physicalTempDir(t)
	tests := []struct {
		name   string
		mode   os.FileMode
		policy directoryPolicy
		wantOK bool
	}{
		{name: "owner-only accepted", mode: 0o700, policy: requireOwnerOnlyDirectory, wantOK: true},
		{name: "owner-controlled accepted", mode: 0o750, policy: requireOwnerControlledDirectory, wantOK: true},
		{name: "owner-only rejects read bits", mode: 0o750, policy: requireOwnerOnlyDirectory},
		{name: "owner-controlled rejects writes", mode: 0o770, policy: requireOwnerControlledDirectory},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, "policy-"+string(rune('a'+index)))
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			fd, err := openAbsoluteDirectory(path, false, defaultDescriptorFS(), test.policy)
			if test.wantOK {
				if err != nil {
					t.Fatalf("valid root rejected: %v", err)
				}
				unix.Close(fd)
			} else if err == nil {
				unix.Close(fd)
				t.Fatal("invalid root accepted")
			}
		})
	}
}
