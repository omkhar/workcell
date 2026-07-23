// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessions

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func openTestDirectory(t testing.TB, path string) int {
	t.Helper()
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Close(fd); err != nil {
			t.Errorf("close test directory: %v", err)
		}
	})
	return fd
}

func writeOwnerTestFile(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadOwnerFileAtRejectsUnsafeBasenamesBeforeIO(t *testing.T) {
	names := []string{"", ".", "..", "/absolute", "../leaf", "nested/leaf", "nested//leaf", "leaf/", " leaf", "leaf ", "leaf\x00suffix"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			fs := defaultDescriptorFS()
			opens := 0
			fs.openat = func(int, string, int, uint32) (int, error) {
				opens++
				return -1, errors.New("unexpected open")
			}
			if _, err := readOwnerFileAtFS(fs, -1, name, name, "test file", 8, false, 1); err == nil {
				t.Fatal("unsafe basename was accepted")
			}
			if opens != 0 {
				t.Fatalf("unsafe basename performed %d opens", opens)
			}
		})
	}
}

func TestReadOwnerFileAtBoundariesAndPositiveRead(t *testing.T) {
	root := physicalTempDir(t)
	dirFD := openTestDirectory(t, root)
	path := filepath.Join(root, "record")
	writeOwnerTestFile(t, path, []byte("four"))

	for _, limit := range []int64{4, math.MaxInt64 - 1} {
		file, err := readOwnerFileAt(dirFD, "record", path, "record", limit, false, 1)
		if err != nil {
			t.Fatalf("limit %d rejected exact file: %v", limit, err)
		}
		if string(file.data) != "four" || file.stat.Size != 4 {
			t.Fatalf("read result = %q, size = %d", file.data, file.stat.Size)
		}
	}
	if _, err := readOwnerFileAt(dirFD, "record", path, "record", 3, false, 1); err == nil {
		t.Fatal("file larger than limit was accepted")
	}
	for _, limit := range []int64{-1, 0, math.MaxInt64} {
		fs := defaultDescriptorFS()
		opens := 0
		fs.openat = func(int, string, int, uint32) (int, error) {
			opens++
			return -1, errors.New("unexpected open")
		}
		if _, err := readOwnerFileAtFS(fs, dirFD, "record", path, "record", limit, false, 1); err == nil || opens != 0 {
			t.Fatalf("limit %d: error = %v, opens = %d", limit, err, opens)
		}
	}

	emptyPath := filepath.Join(root, "empty")
	writeOwnerTestFile(t, emptyPath, nil)
	if file, err := readOwnerFileAt(dirFD, "empty", emptyPath, "empty file", 1, true, 1); err != nil || len(file.data) != 0 {
		t.Fatalf("empty writable read = %#v, %v", file, err)
	}
}

func TestReadOwnerFileAtRejectsSymlinkFIFODeviceAndDirectory(t *testing.T) {
	root := physicalTempDir(t)
	dirFD := openTestDirectory(t, root)
	regular := filepath.Join(root, "regular")
	writeOwnerTestFile(t, regular, []byte("data"))
	if err := os.Symlink(regular, filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"symlink", "fifo", "directory"} {
		fs := defaultDescriptorFS()
		opens := 0
		fs.openat = func(int, string, int, uint32) (int, error) {
			opens++
			return -1, errors.New("unexpected open")
		}
		if _, err := readOwnerFileAtFS(fs, dirFD, name, filepath.Join(root, name), name, 16, false, 1); err == nil {
			t.Fatalf("%s was accepted as a regular file", name)
		}
		if opens != 0 {
			t.Fatalf("%s performed %d opens before type rejection", name, opens)
		}
	}

	deviceDirFD := openTestDirectory(t, "/dev")
	fs := defaultDescriptorFS()
	opens := 0
	fs.openat = func(int, string, int, uint32) (int, error) {
		opens++
		return -1, errors.New("unexpected open")
	}
	if _, err := readOwnerFileAtFS(fs, deviceDirFD, "null", "/dev/null", "device", 1, false, 1); err == nil {
		t.Fatal("device was accepted as a regular file")
	}
	if opens != 0 {
		t.Fatalf("device performed %d opens before type rejection", opens)
	}
}

func TestReadOwnerFileAtRejectsIdentitySwapBetweenInspectionAndOpen(t *testing.T) {
	root := physicalTempDir(t)
	dirFD := openTestDirectory(t, root)
	path := filepath.Join(root, "record")
	oldPath := filepath.Join(root, "record-opened")
	writeOwnerTestFile(t, path, []byte("old!"))

	fs := defaultDescriptorFS()
	originalOpen := fs.openat
	var swapErr error
	fs.openat = func(dirFD int, name string, flags int, mode uint32) (int, error) {
		if swapErr == nil {
			if err := os.Rename(path, oldPath); err != nil {
				swapErr = err
			} else if err := os.WriteFile(path, []byte("new!"), 0o600); err != nil {
				swapErr = err
			} else if err := os.Chmod(path, 0o600); err != nil {
				swapErr = err
			}
		}
		return originalOpen(dirFD, name, flags, mode)
	}
	if _, err := readOwnerFileAtFS(fs, dirFD, "record", path, "record", 4, false, 1); err == nil || !strings.Contains(err.Error(), "changed between inspection and open") {
		t.Fatalf("identity swap error = %v", err)
	}
	if swapErr != nil {
		t.Fatal(swapErr)
	}
}

func TestReadOwnerFileAtRejectsUnsafeModeOwnerAndLinkCount(t *testing.T) {
	root := physicalTempDir(t)
	dirFD := openTestDirectory(t, root)
	path := filepath.Join(root, "record")
	writeOwnerTestFile(t, path, []byte("data"))
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerFileAt(dirFD, "record", path, "record", 4, false, 1); err == nil {
		t.Fatal("group-readable file was accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	linkPath := filepath.Join(root, "record-link")
	if err := os.Link(path, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerFileAt(dirFD, "record", path, "record", 4, false, 1); err == nil {
		t.Fatal("unexpected two-link file was accepted")
	}
	if file, err := readOwnerFileAt(dirFD, "record", path, "record", 4, false, 2); err != nil || string(file.data) != "data" {
		t.Fatalf("explicit two-link read = %q, %v", file.data, err)
	}

	fs := defaultDescriptorFS()
	originalFstat := fs.fstat
	fs.fstat = func(fd int, stat *unix.Stat_t) error {
		if err := originalFstat(fd, stat); err != nil {
			return err
		}
		stat.Uid = uint32(os.Geteuid()) + 1
		return nil
	}
	if _, err := readOwnerFileAtFS(fs, dirFD, "record", path, "record", 4, false, 2); err == nil {
		t.Fatal("foreign-owned file was accepted")
	}
}

func TestValidateOwnerFileStatRejectsMetadataBoundaries(t *testing.T) {
	valid := unix.Stat_t{Mode: unix.S_IFREG | 0o600, Uid: uint32(os.Geteuid()), Nlink: 1, Size: 4, Ino: 1}
	tests := []struct {
		name   string
		mutate func(*unix.Stat_t)
		links  []uint64
	}{
		{name: "zero inode", mutate: func(stat *unix.Stat_t) { stat.Ino = 0 }, links: []uint64{1}},
		{name: "special", mutate: func(stat *unix.Stat_t) { stat.Mode = unix.S_IFIFO | 0o600 }, links: []uint64{1}},
		{name: "owner", mutate: func(stat *unix.Stat_t) { stat.Uid++ }, links: []uint64{1}},
		{name: "mode", mutate: func(stat *unix.Stat_t) { stat.Mode |= 0o004 }, links: []uint64{1}},
		{name: "no allowed links", mutate: func(*unix.Stat_t) {}},
		{name: "zero allowed link", mutate: func(*unix.Stat_t) {}, links: []uint64{0}},
		{name: "wrong link", mutate: func(stat *unix.Stat_t) { stat.Nlink = 2 }, links: []uint64{1}},
		{name: "negative size", mutate: func(stat *unix.Stat_t) { stat.Size = -1 }, links: []uint64{1}},
		{name: "over size", mutate: func(stat *unix.Stat_t) { stat.Size = 5 }, links: []uint64{1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stat := valid
			test.mutate(&stat)
			if err := validateOwnerFileStat("record", "record", stat, 4, test.links...); err == nil {
				t.Fatal("unsafe metadata was accepted")
			}
		})
	}
}

func TestReadOwnerFileAtDetectsPostReadMutation(t *testing.T) {
	root := physicalTempDir(t)
	dirFD := openTestDirectory(t, root)
	path := filepath.Join(root, "record")
	writeOwnerTestFile(t, path, []byte("four"))
	fs := defaultDescriptorFS()
	originalRead := fs.read
	mutated := false
	var mutationErr error
	fs.read = func(fd int, buffer []byte) (int, error) {
		count, err := originalRead(fd, buffer)
		if count > 0 && !mutated {
			mutated = true
			mutationErr = os.WriteFile(path, []byte("expanded"), 0o600)
		}
		return count, err
	}
	if _, err := readOwnerFileAtFS(fs, dirFD, "record", path, "record", 16, false, 1); err == nil {
		t.Fatal("post-read size mutation was accepted")
	}
	if mutationErr != nil {
		t.Fatal(mutationErr)
	}

	writeOwnerTestFile(t, path, []byte("four"))
	fs = defaultDescriptorFS()
	originalFstat := fs.fstat
	fstatCalls := 0
	fs.fstat = func(fd int, stat *unix.Stat_t) error {
		if err := originalFstat(fd, stat); err != nil {
			return err
		}
		fstatCalls++
		if fstatCalls == 2 {
			stat.Ino++
		}
		return nil
	}
	if _, err := readOwnerFileAtFS(fs, dirFD, "record", path, "record", 4, false, 1); err == nil {
		t.Fatal("post-read identity mutation was accepted")
	}
}

func TestReadOwnerFileAtJoinsOperationAndCloseFailuresWithoutRetry(t *testing.T) {
	for _, operation := range []string{"first fstat", "second fstat", "read", "close"} {
		t.Run(operation, func(t *testing.T) {
			root := physicalTempDir(t)
			dirFD := openTestDirectory(t, root)
			path := filepath.Join(root, "record")
			writeOwnerTestFile(t, path, []byte("data"))
			operationFailure := errors.New("operation failure")
			closeFailure := errors.New("close failure")
			fs := defaultDescriptorFS()
			originalFstat, originalRead, originalClose := fs.fstat, fs.read, fs.close
			fstatCalls, closeCalls := 0, 0
			fs.fstat = func(fd int, stat *unix.Stat_t) error {
				fstatCalls++
				if (operation == "first fstat" && fstatCalls == 1) || (operation == "second fstat" && fstatCalls == 2) {
					return operationFailure
				}
				return originalFstat(fd, stat)
			}
			fs.read = func(fd int, buffer []byte) (int, error) {
				if operation == "read" {
					return 0, operationFailure
				}
				return originalRead(fd, buffer)
			}
			fs.close = func(fd int) error {
				closeCalls++
				return errors.Join(originalClose(fd), closeFailure)
			}
			file, err := readOwnerFileAtFS(fs, dirFD, "record", path, "record", 4, false, 1)
			if operation != "close" && !errors.Is(err, operationFailure) {
				t.Fatalf("operation error = %v", err)
			}
			if !errors.Is(err, closeFailure) || closeCalls != 1 {
				t.Fatalf("close error = %v, calls = %d", err, closeCalls)
			}
			if len(file.data) != 0 {
				t.Fatalf("data returned after failure: %q", file.data)
			}
		})
	}
}
