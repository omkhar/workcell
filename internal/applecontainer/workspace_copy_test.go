// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/pathutil"
	"golang.org/x/sys/unix"
)

func workspaceTestParent(t *testing.T) (string, int) {
	t.Helper()
	parent := t.TempDir()
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	mustNil(t, err)
	t.Cleanup(func() { mustNil(t, unix.Close(fd)) })
	return parent, fd
}

func copyWorkspaceForTest(t *testing.T, source string, excluded []string, limits workspaceCopyLimits, ops workspaceOps) ([]WorkspaceEntry, error) {
	_, parentFD := workspaceTestParent(t)
	return copyWorkspaceTreeWithOps(source, parentFD, "out", excluded, limits, ops)
}

func TestWorkspaceCopyUsesPinnedParent(t *testing.T) {
	source, container := t.TempDir(), t.TempDir()
	mustNil(t, os.WriteFile(filepath.Join(source, "file"), []byte("x"), 0o600))
	parent, moved := filepath.Join(container, "parent"), filepath.Join(container, "moved")
	mustNil(t, os.Mkdir(parent, 0o700))
	parentFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	mustNil(t, err)
	defer unix.Close(parentFD)
	mustNil(t, errors.Join(os.Rename(parent, moved), os.Mkdir(parent, 0o700)))
	_, err = copyWorkspaceTreeDescriptor(source, parentFD, "out", nil)
	if _, replacementErr := os.Lstat(filepath.Join(parent, "out")); err != nil || !os.IsNotExist(replacementErr) {
		t.Fatalf("pinned destination parent result: copy=%v replacement=%v", err, replacementErr)
	}
	_, err = os.Stat(filepath.Join(moved, "out", "file"))
	mustNil(t, err)
	if _, err := copyWorkspaceTreeDescriptor(source, parentFD, "../escape", nil); err == nil {
		t.Fatal("non-leaf destination name succeeded")
	}
}

func TestWorkspaceCopyRejectsSourceRaces(t *testing.T) {
	for _, kind := range []string{"replacement", "symlink-follow", "special", "in-place-file", "ctime-only", "nlink-only", "in-place-directory"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			victim := filepath.Join(source, "victim")
			mustNil(t, os.WriteFile(victim, []byte("original"), 0o600))
			ops, sourceFlagsOK := systemWorkspaceOps(), true
			if kind == "replacement" || kind == "symlink-follow" || kind == "special" {
				original, swapped := ops.openat, false
				ops.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
					if name == "victim" && flags&unix.O_CREAT == 0 && !swapped {
						sourceFlagsOK = flags&(unix.O_NOFOLLOW|unix.O_NONBLOCK) == unix.O_NOFOLLOW|unix.O_NONBLOCK
						swapped = true
						if kind == "symlink-follow" {
							mustNil(t, os.Rename(victim, victim+".saved"))
							mustNil(t, os.Symlink("victim.saved", victim))
						} else if mustNil(t, os.Remove(victim)); kind == "special" {
							mustNil(t, unix.Mkfifo(victim, 0o600))
						} else {
							mustNil(t, os.WriteFile(victim, []byte("replacement"), 0o600))
						}
					}
					return original(fd, name, flags, mode)
				}
			} else {
				watched := source
				if kind == "in-place-file" || kind == "ctime-only" || kind == "nlink-only" {
					watched = victim
				}
				var watchedStat unix.Stat_t
				mustNil(t, unix.Stat(watched, &watchedStat))
				original, observations := ops.fstat, 0
				ops.fstat = func(fd int, stat *unix.Stat_t) error {
					if err := original(fd, stat); err != nil {
						return err
					}
					if stat.Ino == watchedStat.Ino && uint64(stat.Dev) == uint64(watchedStat.Dev) {
						observations++
						if observations == 2 {
							if kind == "ctime-only" {
								stat.Ctim.Nsec++
							} else if kind == "nlink-only" {
								stat.Nlink++
							} else if kind == "in-place-file" {
								mustNil(t, os.WriteFile(victim, []byte("modified"), 0o600))
							} else {
								mustNil(t, os.WriteFile(filepath.Join(source, "late"), []byte("x"), 0o600))
							}
							if kind != "ctime-only" && kind != "nlink-only" {
								return original(fd, stat)
							}
						}
					}
					return nil
				}
			}
			if _, err := copyWorkspaceForTest(t, source, nil, defaultWorkspaceCopyLimits(), ops); err == nil || !sourceFlagsOK {
				t.Fatalf("%s source mutation result: flagsOK=%t error=%v", kind, sourceFlagsOK, err)
			}
		})
	}
}

func TestWorkspaceCopyRejectsStableSpecialAndHardlink(t *testing.T) {
	source := t.TempDir()
	mustNil(t, unix.Mkfifo(filepath.Join(source, "pipe"), 0o600))
	ops, opened := systemWorkspaceOps(), false
	original := ops.openat
	ops.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
		if name == "pipe" && flags&unix.O_CREAT == 0 {
			opened = true
		}
		return original(fd, name, flags, mode)
	}
	if _, err := copyWorkspaceForTest(t, source, nil, defaultWorkspaceCopyLimits(), ops); err == nil || opened {
		t.Fatalf("stable FIFO result: opened=%t error=%v", opened, err)
	}
	source = t.TempDir()
	mustNil(t, os.WriteFile(filepath.Join(source, "a"), []byte("x"), 0o600))
	mustNil(t, os.Link(filepath.Join(source, "a"), filepath.Join(source, "b")))
	_, parentFD := workspaceTestParent(t)
	if _, err := copyWorkspaceTreeDescriptor(source, parentFD, "out", nil); err == nil || !strings.Contains(err.Error(), "multiply linked") {
		t.Fatalf("source hard link accepted: %v", err)
	}
}

func TestWorkspaceCopyRejectsDestinationNameRaces(t *testing.T) {
	for _, kind := range []string{"root", "directory", "file", "symlink", "symlink-snapshot", "directory-preopen", "file-precreate", "directory-adopt", "final-stability", "final-extra", "final-missing", "final-kind", "final-mode", "final-dir-mode", "final-nlink", "final-size", "final-hash", "final-link", "final-alias", "final-root-mode", "final-special"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			parent, parentFD := workspaceTestParent(t)
			destination := filepath.Join(parent, "out")
			mustNil(t, os.Mkdir(filepath.Join(source, "nested"), 0o700))
			mustNil(t, os.WriteFile(filepath.Join(source, "nested", "copied"), []byte("x"), 0o400))
			mustNil(t, os.WriteFile(filepath.Join(source, "file"), []byte("source"), 0o600))
			mustNil(t, os.Symlink("file", filepath.Join(source, "link")))
			ops, swapped, flagsOK := systemWorkspaceOps(), false, true
			swap := func(target string, directory bool) {
				mustNil(t, os.Rename(target, target+".saved"))
				if directory {
					mustNil(t, os.Mkdir(target, 0o700))
				} else {
					mustNil(t, os.WriteFile(target, []byte("replacement"), 0o600))
				}
				swapped = true
			}
			switch kind {
			case "root", "directory", "file":
				wanted := map[string]uint32{"root": 0o755, "directory": 0o700, "file": 0o600}[kind]
				target := map[string]string{"root": destination, "directory": filepath.Join(destination, "nested"), "file": filepath.Join(destination, "file")}[kind]
				original := ops.fchmod
				ops.fchmod = func(fd int, mode uint32) error {
					err := original(fd, mode)
					if err == nil && mode == wanted && !swapped {
						swap(target, kind != "file")
					}
					return err
				}
			case "symlink":
				original := ops.symlinkat
				ops.symlinkat = func(target string, fd int, name string) error {
					if err := original(target, fd, name); err != nil {
						return err
					}
					mustNil(t, os.Remove(filepath.Join(destination, name)))
					return os.Symlink("../../outside", filepath.Join(destination, name))
				}
			case "symlink-snapshot":
				original, reads := ops.readlink, 0
				ops.readlink = func(fd int, name string, buffer []byte) (int, error) {
					reads++
					if reads == 2 {
						mustNil(t, errors.Join(os.Remove(filepath.Join(destination, name)), os.Symlink("file", filepath.Join(destination, name))))
					}
					return original(fd, name, buffer)
				}
			case "directory-preopen":
				original, opens := ops.openat, 0
				ops.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
					if name == "nested" && flags&unix.O_DIRECTORY != 0 {
						opens++
						if opens == 2 {
							flagsOK = flags&unix.O_NOFOLLOW != 0
							swap(filepath.Join(destination, name), true)
						}
					}
					return original(fd, name, flags, mode)
				}
			case "file-precreate":
				original := ops.openat
				ops.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
					if name == "file" && flags&unix.O_CREAT != 0 {
						flagsOK = flags&unix.O_EXCL != 0
						mustNil(t, os.WriteFile(filepath.Join(destination, name), []byte("precreated"), 0o600))
					}
					return original(fd, name, flags, mode)
				}
			case "directory-adopt":
				original := ops.fstatat
				ops.fstatat = func(fd int, name string, stat *unix.Stat_t, flags int) error {
					if _, err := os.Lstat(filepath.Join(destination, name)); name == "nested" && !swapped && err == nil {
						swap(filepath.Join(destination, name), true)
						mustNil(t, os.WriteFile(filepath.Join(destination, name, "extra"), []byte("x"), 0o600))
					}
					return original(fd, name, stat, flags)
				}
			case "final-stability":
				ops.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
					opened, err := unix.Openat(fd, name, flags, mode)
					if _, statErr := os.Lstat(filepath.Join(destination, name)); err == nil && statErr == nil && name == "file" && flags&unix.O_CREAT == 0 && !swapped {
						swap(filepath.Join(destination, name), false)
					}
					return opened, err
				}
			default:
				original := ops.fchmod
				ops.fchmod = func(fd int, mode uint32) error {
					err := original(fd, mode)
					if err != nil || mode != 0o755 || swapped {
						return err
					}
					file, link := filepath.Join(destination, "file"), filepath.Join(destination, "link")
					mutations := map[string]func() error{
						"final-extra":     func() error { return os.WriteFile(filepath.Join(destination, "extra"), []byte("x"), 0o600) },
						"final-missing":   func() error { return os.Remove(file) },
						"final-kind":      func() error { return errors.Join(os.Remove(file), os.Mkdir(file, 0o401)) },
						"final-mode":      func() error { return os.Chmod(file, 0o400) },
						"final-dir-mode":  func() error { return os.Chmod(filepath.Join(destination, "nested"), 0o777) },
						"final-nlink":     func() error { return os.Link(file, filepath.Join(parent, "hardlink")) },
						"final-size":      func() error { return os.WriteFile(file, []byte("source!"), 0o401) },
						"final-hash":      func() error { return os.WriteFile(file, []byte("tamper"), 0o401) },
						"final-link":      func() error { return errors.Join(os.Remove(link), os.Symlink("./file", link)) },
						"final-alias":     func() error { return os.Rename(file, filepath.Join(destination, "FILE")) },
						"final-root-mode": func() error { return os.Chmod(destination, 0o777) },
						"final-special":   func() error { return unix.Chmod(file, 0o4600) },
					}
					mustNil(t, mutations[kind]())
					swapped = true
					return nil
				}
			}
			_, err := copyWorkspaceTreeWithOps(source, parentFD, "out", nil, defaultWorkspaceCopyLimits(), ops)
			if _, copiedErr := os.Lstat(filepath.Join(destination, "nested", "copied")); err == nil || !flagsOK || kind == "directory-adopt" && copiedErr == nil || strings.HasPrefix(kind, "final-") && !swapped {
				t.Fatalf("%s destination replacement result: flagsOK=%t error=%v", kind, flagsOK, err)
			}
		})
	}
}

func TestWorkspaceLinkResolutionUsesRawComponentOrder(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []WorkspaceEntry
		wantErr bool
	}{
		{"safe-repeat", []WorkspaceEntry{{Path: "dir", Kind: "dir"}, {Path: "dir/file", Kind: "file"}, {Path: "x", Kind: "symlink", LinkTarget: "dir"}, {Path: "y", Kind: "symlink", LinkTarget: "x/../x/file"}}, false},
		{"combined-dotdot-escape", []WorkspaceEntry{{Path: "dir", Kind: "dir"}, {Path: "dir/x", Kind: "symlink", LinkTarget: ".."}, {Path: "dir/outside", Kind: "file"}, {Path: "y", Kind: "symlink", LinkTarget: "dir/x/../outside"}}, true},
		{"dangling", []WorkspaceEntry{{Path: "x", Kind: "symlink", LinkTarget: "missing"}}, true},
		{"case-alias", []WorkspaceEntry{{Path: "file", Kind: "file"}, {Path: "link", Kind: "symlink", LinkTarget: "FILE"}}, true},
		{"cycle", []WorkspaceEntry{{Path: "x", Kind: "symlink", LinkTarget: "y"}, {Path: "y", Kind: "symlink", LinkTarget: "x"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateWorkspaceLinks(test.entries)
			if (err != nil) != test.wantErr {
				t.Fatalf("link validation error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
	for _, hops := range []int{40, 41} {
		entries := []WorkspaceEntry{{Path: "file", Kind: "file"}}
		for i := hops - 1; i >= 0; i-- {
			target := "file"
			if i+1 < hops {
				target = fmt.Sprintf("link%d", i+1)
			}
			entries = append(entries, WorkspaceEntry{Path: fmt.Sprintf("link%d", i), Kind: "symlink", LinkTarget: target})
		}
		if err := validateWorkspaceLinks(entries); (err != nil) != (hops > workspaceMaxSymlinkHops) {
			t.Fatalf("%d-hop chain error = %v", hops, err)
		}
	}
}

func TestWorkspaceCopyLimits(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents []string
		limits   workspaceCopyLimits
		wantErr  bool
	}{
		{"file-exact", []string{"abcd"}, workspaceCopyLimits{4, 10, 10}, false},
		{"file-over", []string{"abcde"}, workspaceCopyLimits{4, 10, 10}, true},
		{"aggregate-exact", []string{"ab", "cd"}, workspaceCopyLimits{4, 4, 10}, false},
		{"aggregate-over", []string{"ab", "cde"}, workspaceCopyLimits{4, 4, 10}, true},
		{"entries-exact", []string{"a", "b"}, workspaceCopyLimits{4, 10, 2}, false},
		{"entries-over", []string{"a", "b"}, workspaceCopyLimits{4, 10, 1}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := t.TempDir()
			for i, content := range test.contents {
				mustNil(t, os.WriteFile(filepath.Join(source, fmt.Sprintf("f%d", i)), []byte(content), 0o600))
			}
			_, err := copyWorkspaceForTest(t, source, nil, test.limits, systemWorkspaceOps())
			if (err != nil) != test.wantErr {
				t.Fatalf("copy error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
	source := t.TempDir()
	victim := filepath.Join(source, "victim")
	mustNil(t, os.WriteFile(victim, []byte("a"), 0o600))
	var watched unix.Stat_t
	mustNil(t, unix.Stat(victim, &watched))
	ops, grown := systemWorkspaceOps(), false
	original := ops.fstat
	ops.fstat = func(fd int, stat *unix.Stat_t) error {
		if err := original(fd, stat); err != nil {
			return err
		}
		if stat.Ino == watched.Ino && !grown {
			grown = true
			return os.WriteFile(victim, []byte("ab"), 0o600)
		}
		return nil
	}
	if _, err := copyWorkspaceForTest(t, source, nil, workspaceCopyLimits{1, 10, 10}, ops); err == nil {
		t.Fatal("file growth past the stream limit succeeded")
	}
	source = t.TempDir()
	mustNil(t, os.Mkdir(filepath.Join(source, ".git"), 0o700))
	_, err := copyWorkspaceForTest(t, source, []string{".git"}, workspaceCopyLimits{1, 1, 0}, systemWorkspaceOps())
	if err == nil {
		t.Fatal("excluded entry bypassed the entry limit")
	}
	for _, test := range []struct {
		path       string
		components int
		wantErr    bool
	}{{strings.Repeat("a", 4096), 256, false}, {strings.Repeat("a", 4097), 256, true}, {"a", 257, true}} {
		if err := validateWorkspacePath(test.path, test.components); (err != nil) != test.wantErr {
			t.Fatalf("path limit error = %v, wantErr %t", err, test.wantErr)
		}
	}
	for _, size := range []int{4096, 4097} {
		ops := systemWorkspaceOps()
		ops.readlink = func(_ int, _ string, buffer []byte) (int, error) {
			for i := 0; i < min(size, len(buffer)); i++ {
				buffer[i] = 'a'
			}
			return min(size, len(buffer)), nil
		}
		_, err := readWorkspaceLink(-1, "link", ops)
		if (err != nil) != (size > workspaceMaxLinkBytes) {
			t.Fatalf("%d-byte link error = %v", size, err)
		}
	}
	for _, remaining := range []int64{262, 261} {
		walk := workspaceWalk{reserved: make(map[string]WorkspaceEntry), manifestBytes: workspaceManifestMaxBytes - remaining}
		if err := walk.reserve("x"); (err != nil) != (remaining < 262) {
			t.Fatalf("manifest path budget remaining %d: %v", remaining, err)
		}
	}
	for _, remaining := range []int64{6, 5} {
		walk := workspaceWalk{reserved: make(map[string]WorkspaceEntry), manifestBytes: workspaceManifestMaxBytes - remaining}
		if err := walk.retain(WorkspaceEntry{LinkTarget: "<"}); (err != nil) != (remaining < 6) {
			t.Fatalf("manifest link budget remaining %d: %v", remaining, err)
		}
	}
}

func TestWorkspaceUnicodeIdentityAndEarlyValidation(t *testing.T) {
	for _, aliases := range [][2]string{{"Caf\u00e9", "Cafe\u0301"}, {"Stra\u00dfe", "STRASSE"}} {
		walk := workspaceWalk{reserved: make(map[string]WorkspaceEntry)}
		mustNil(t, walk.reserve(aliases[0]))
		if err := walk.reserve(aliases[1]); err == nil {
			t.Fatalf("Unicode aliases %q and %q did not collide", aliases[0], aliases[1])
		}
	}
	ops, opened := systemWorkspaceOps(), false
	ops.openat = func(int, string, int, uint32) (int, error) {
		opened = true
		return -1, unix.EPERM
	}
	_, err := copyWorkspaceTreeWithOps("secret\nevent=forged", -1, "out", nil, defaultWorkspaceCopyLimits(), ops)
	if err == nil || opened || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "forged") {
		t.Fatalf("unsafe source validation: opened=%t error=%v", opened, err)
	}
	for _, value := range []string{"bad\nname", string([]byte{'b', 0xff})} {
		if err := validateWorkspaceLeaf("workspace entry", value); err == nil || (!errors.Is(err, pathutil.ErrInvalidUTF8Path) && !errors.Is(err, pathutil.ErrUnsafePathControl)) {
			t.Fatalf("unsafe name accepted: error=%v", err)
		}
	}
	source := t.TempDir()
	bad := "excluded\nentry"
	mustNil(t, os.WriteFile(filepath.Join(source, bad), []byte("secret"), 0o600))
	ops, inspected := systemWorkspaceOps(), false
	original := ops.fstatat
	ops.fstatat = func(fd int, name string, stat *unix.Stat_t, flags int) error {
		if name == bad {
			inspected = true
		}
		return original(fd, name, stat, flags)
	}
	_, err = copyWorkspaceForTest(t, source, []string{bad}, defaultWorkspaceCopyLimits(), ops)
	if err == nil || inspected {
		t.Fatalf("unsafe excluded name result: inspected=%t error=%v", inspected, err)
	}
}
