// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

var (
	assetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)

	// ErrInvalidInput identifies caller-controlled release-input failures.
	ErrInvalidInput = errors.New("invalid release publisher input")
)

const (
	maxReleaseAssetCount             = 18
	maxReleaseAssetBytes       int64 = 64 * 1024 * 1024
	maxReleaseAssetStagedBytes int64 = 256 * 1024 * 1024
)

type localAsset struct {
	name    string
	size    int64
	sha256  string
	content assetReadCloser
}

type assetReadCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type assetSourceStat struct {
	Mode           uint32
	Size           int64
	Nlink          uint64
	UID            uint32
	GID            uint32
	Dev            uint64
	Ino            uint64
	ModTimeSec     int64
	ModTimeNsec    int64
	ChangeTimeSec  int64
	ChangeTimeNsec int64
}

type assetSource interface {
	io.Reader
	Stat() (assetSourceStat, error)
	Close() error
}

type assetSourceOpener interface {
	Open(string) (assetSource, error)
}

type openatAssetSourceOpener struct{}

type openatAssetSource struct {
	file *os.File
}

type stagedAssetWriter struct {
	file          *os.File
	directory     *os.File
	directoryPath string
	directoryDev  uint64
	directoryIno  uint64
	name          string
	linked        bool
}

func (stage *stagedAssetWriter) Write(buffer []byte) (int, error) { return stage.file.Write(buffer) }
func (stage *stagedAssetWriter) Sync() error                      { return stage.file.Sync() }

func inputErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

func (openatAssetSourceOpener) Open(path string) (assetSource, error) {
	cleanPath, err := canonicalAssetSourcePath(path, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	components := strings.Split(strings.TrimPrefix(cleanPath, string(filepath.Separator)), string(filepath.Separator))
	currentFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open filesystem root for release asset %q: %w", path, err)
	}
	var rootStat unix.Stat_t
	if err := unix.Fstat(currentFD, &rootStat); err != nil {
		return nil, errors.Join(fmt.Errorf("stat filesystem root for release asset %q: %w", path, err), closeUnixFD(currentFD, "release asset path parent"))
	}
	anchored, err := validateAssetDirectory(string(filepath.Separator), rootStat, false)
	if err != nil {
		return nil, errors.Join(err, closeUnixFD(currentFD, "release asset path parent"))
	}
	for i, component := range components {
		final := i == len(components)-1
		var expected unix.Stat_t
		if final {
			if !anchored {
				return nil, errors.Join(inputErrorf("release asset %q must be beneath a current-user-controlled directory", path), closeUnixFD(currentFD, "release asset path parent"))
			}
			if err := unix.Fstatat(currentFD, component, &expected, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return nil, errors.Join(fmt.Errorf("inspect release asset %q before opening: %w", path, err), closeUnixFD(currentFD, "release asset path parent"))
			}
			if uint32(expected.Mode)&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) {
				return nil, errors.Join(inputErrorf("release asset %q must be a regular file, not a symlink, directory, FIFO, or device", path), closeUnixFD(currentFD, "release asset path parent"))
			}
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if final {
			flags |= unix.O_NONBLOCK
		} else {
			flags |= unix.O_DIRECTORY
		}
		nextFD, openErr := unix.Openat(currentFD, component, flags, 0)
		if openErr != nil {
			classified := classifyOpenatError(currentFD, component, path, final, openErr)
			return nil, errors.Join(classified, closeUnixFD(currentFD, "release asset path parent"))
		}
		if final {
			var opened unix.Stat_t
			if err := unix.Fstat(nextFD, &opened); err != nil {
				return nil, errors.Join(fmt.Errorf("stat opened release asset %q: %w", path, err), closeUnixFD(nextFD, "release asset"), closeUnixFD(currentFD, "release asset path parent"))
			}
			if opened.Dev != expected.Dev || opened.Ino != expected.Ino || uint32(opened.Mode)&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) {
				return nil, errors.Join(inputErrorf("release asset %q changed between inspection and open", path), closeUnixFD(nextFD, "release asset"), closeUnixFD(currentFD, "release asset path parent"))
			}
			if closeErr := closeUnixFD(currentFD, "release asset path parent"); closeErr != nil {
				return nil, errors.Join(closeErr, closeUnixFD(nextFD, "release asset"))
			}
			file := os.NewFile(uintptr(nextFD), cleanPath)
			if file == nil {
				return nil, errors.Join(errors.New("create release asset file handle"), closeUnixFD(nextFD, "release asset"))
			}
			return &openatAssetSource{file: file}, nil
		}
		var directoryStat unix.Stat_t
		if err := unix.Fstat(nextFD, &directoryStat); err != nil {
			return nil, errors.Join(fmt.Errorf("stat release asset %q directory component %q: %w", path, component, err), closeUnixFD(nextFD, "release asset path component"), closeUnixFD(currentFD, "release asset path parent"))
		}
		nextAnchored, err := validateAssetDirectory(cleanPath, directoryStat, anchored)
		if err != nil {
			return nil, errors.Join(err, closeUnixFD(nextFD, "release asset path component"), closeUnixFD(currentFD, "release asset path parent"))
		}
		if closeErr := closeUnixFD(currentFD, "release asset path parent"); closeErr != nil {
			return nil, errors.Join(closeErr, closeUnixFD(nextFD, "release asset path component"))
		}
		currentFD = nextFD
		anchored = nextAnchored
	}
	return nil, errors.Join(errors.New("release asset path traversal ended without a file"), closeUnixFD(currentFD, "release asset path parent"))
}

func validateAssetDirectory(path string, stat unix.Stat_t, anchored bool) (bool, error) {
	mode := uint32(stat.Mode)
	if mode&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) || stat.Ino == 0 {
		return false, inputErrorf("release asset %q contains a non-directory component", path)
	}
	ownedAndControlled := stat.Uid == uint32(os.Geteuid()) && mode&0o022 == 0
	if anchored {
		if !ownedAndControlled {
			return false, inputErrorf("release asset %q contains a directory below its owner-controlled anchor that is foreign-owned or writable by another user", path)
		}
		return true, nil
	}
	if stat.Uid == 0 && (mode&0o022 == 0 || mode&uint32(unix.S_ISVTX) != 0) {
		return os.Geteuid() == 0 && mode&0o077 == 0 && mode&uint32(unix.S_ISVTX) == 0, nil
	}
	if ownedAndControlled {
		return true, nil
	}
	return false, inputErrorf("release asset %q contains an untrusted directory ancestor", path)
}

func canonicalAssetSourcePath(path, goos string) (string, error) {
	if err := validateRawAssetSourcePath(path); err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("make release asset path %q absolute: %w", path, err)
	}
	if goos == "darwin" {
		switch {
		case absolute == "/var" || strings.HasPrefix(absolute, "/var/"):
			absolute = filepath.Join("/private", strings.TrimPrefix(absolute, string(filepath.Separator)))
		case absolute == "/tmp" || strings.HasPrefix(absolute, "/tmp/"):
			absolute = filepath.Join("/private", strings.TrimPrefix(absolute, string(filepath.Separator)))
		}
	}
	return absolute, nil
}

func validateRawAssetSourcePath(path string) error {
	if path == "" {
		return inputErrorf("release asset path must not be empty")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return inputErrorf("release asset path contains a NUL byte")
	}
	components := strings.Split(path, string(filepath.Separator))
	start := 0
	if filepath.IsAbs(path) {
		start = 1
	}
	for _, component := range components[start:] {
		switch {
		case component == "":
			return inputErrorf("release asset path %q contains duplicate or trailing separators", path)
		case component == "." || component == "..":
			return inputErrorf("release asset path %q contains a dot component", path)
		case strings.TrimSpace(component) != component:
			return inputErrorf("release asset path %q contains a component with leading or trailing whitespace", path)
		}
	}
	return nil
}

func classifyOpenatError(parentFD int, component, path string, final bool, openErr error) error {
	var stat unix.Stat_t
	statErr := unix.Fstatat(parentFD, component, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if statErr == nil {
		kind := stat.Mode & unix.S_IFMT
		switch {
		case kind == unix.S_IFLNK:
			return inputErrorf("release asset %q must not contain symbolic-link components", path)
		case !final && kind != unix.S_IFDIR:
			return inputErrorf("release asset %q ancestor component %q must be a directory", path, component)
		case final && kind != unix.S_IFREG:
			return inputErrorf("release asset %q must be a non-empty regular file with exactly one link", path)
		}
	}
	wrapped := fmt.Errorf("open release asset %q component %q: %w", path, component, openErr)
	if statErr != nil && !errors.Is(statErr, unix.ENOENT) {
		return errors.Join(wrapped, fmt.Errorf("classify release asset %q component %q: %w", path, component, statErr))
	}
	return wrapped
}

func closeUnixFD(fd int, label string) error {
	if fd < 0 {
		return nil
	}
	if err := unix.Close(fd); err != nil {
		return fmt.Errorf("close %s: %w", label, err)
	}
	return nil
}

func (source *openatAssetSource) Read(buffer []byte) (int, error) { return source.file.Read(buffer) }

func (source *openatAssetSource) Stat() (assetSourceStat, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(source.file.Fd()), &stat); err != nil {
		return assetSourceStat{}, err
	}
	return assetSourceStatFromUnix(stat), nil
}

func (source *openatAssetSource) Close() error { return source.file.Close() }

func inspectLocalAssets(paths []string) ([]localAsset, error) {
	return inspectLocalAssetsWithOpener(paths, openatAssetSourceOpener{})
}

func inspectLocalAssetsWithOpener(paths []string, opener assetSourceOpener) ([]localAsset, error) {
	if len(paths) == 0 || len(paths) > maxReleaseAssetCount {
		return nil, inputErrorf("release publication requires between 1 and %d assets", maxReleaseAssetCount)
	}
	if opener == nil {
		return nil, errors.New("release asset source opener must not be nil")
	}
	assets := make([]localAsset, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	var stagedBytes int64
	for _, path := range paths {
		name := filepath.Base(path)
		if !assetNamePattern.MatchString(name) || name == "." || name == ".." {
			return nil, abortAssetInspection(assets, inputErrorf("release asset %q basename %q is not GitHub-safe; use 1-255 ASCII letters, digits, dots, underscores, or hyphens and begin with a letter or digit", path, name))
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, abortAssetInspection(assets, inputErrorf("duplicate release asset basename %q; every upload name must be unique", name))
		}
		seen[name] = struct{}{}

		source, err := opener.Open(path)
		if err != nil {
			return nil, abortAssetInspection(assets, fmt.Errorf("open release asset %q: %w", path, err))
		}
		stat, statErr := source.Stat()
		if statErr != nil {
			return nil, abortAssetInspection(assets, errors.Join(fmt.Errorf("stat opened release asset %q: %w", path, statErr), closeAssetSource(source, name)))
		}
		if stat.Mode&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) || stat.Size <= 0 || stat.Nlink != 1 || stat.Ino == 0 {
			return nil, abortAssetInspection(assets, errors.Join(inputErrorf("release asset %q must be a non-empty regular file with exactly one link", path), closeAssetSource(source, name)))
		}
		if stat.UID != uint32(os.Geteuid()) || stat.Mode&0o022 != 0 {
			return nil, abortAssetInspection(assets, errors.Join(inputErrorf("release asset %q must be owned by the current user and not writable by another user", path), closeAssetSource(source, name)))
		}
		stagedBytes, err = reserveReleaseAssetBytes(stagedBytes, stat.Size)
		if err != nil {
			return nil, abortAssetInspection(assets, errors.Join(fmt.Errorf("release asset %q: %w", path, err), closeAssetSource(source, name)))
		}

		stage, err := createStagedAssetContent()
		if err != nil {
			return nil, abortAssetInspection(assets, errors.Join(fmt.Errorf("create private content stage for release asset %q: %w", name, err), closeAssetSource(source, name)))
		}
		written, copyErr := io.Copy(stage, io.LimitReader(source, stat.Size))
		if copyErr == nil {
			var probed int64
			probed, copyErr = io.Copy(io.Discard, io.LimitReader(source, 1))
			written += probed
		}
		after, afterStatErr := source.Stat()
		sourceCloseErr := closeAssetSource(source, name)
		if copyErr != nil || afterStatErr != nil || sourceCloseErr != nil {
			var wrappedCopyErr error
			if copyErr != nil {
				wrappedCopyErr = fmt.Errorf("stage release asset %q content: %w", name, copyErr)
			}
			var wrappedStatErr error
			if afterStatErr != nil {
				wrappedStatErr = fmt.Errorf("restat staged release asset %q source: %w", name, afterStatErr)
			}
			return nil, abortAssetInspection(assets, errors.Join(wrappedCopyErr, wrappedStatErr, sourceCloseErr, discardStagedAssetContent(stage, name)))
		}
		if stat != after {
			return nil, abortAssetInspection(assets, errors.Join(inputErrorf("release asset %q changed while its content was staged", path), discardStagedAssetContent(stage, name)))
		}
		if written != stat.Size {
			return nil, abortAssetInspection(assets, errors.Join(fmt.Errorf("stage release asset %q content: copied size %d differs from reported size %d", name, written, stat.Size), discardStagedAssetContent(stage, name)))
		}
		if err := stage.Sync(); err != nil {
			return nil, abortAssetInspection(assets, errors.Join(fmt.Errorf("sync staged release asset %q content: %w", name, err), discardStagedAssetContent(stage, name)))
		}
		sealed, err := sealStagedAssetContent(stage, name, written)
		if err != nil {
			return nil, abortAssetInspection(assets, err)
		}
		digest, err := hashStagedAssetContent(sealed, name, written)
		if err != nil {
			return nil, abortAssetInspection(assets, errors.Join(err, closeAssetContent(sealed, name)))
		}
		assets = append(assets, localAsset{name: name, size: written, sha256: digest, content: sealed})
	}
	return assets, nil
}

func reserveReleaseAssetBytes(total, size int64) (int64, error) {
	if size <= 0 || size > maxReleaseAssetBytes {
		return total, inputErrorf("asset size %d exceeds the per-asset staging limit of %d bytes", size, maxReleaseAssetBytes)
	}
	if total < 0 || total > maxReleaseAssetStagedBytes-size {
		return total, inputErrorf("release assets exceed the aggregate staging limit of %d bytes", maxReleaseAssetStagedBytes)
	}
	return total + size, nil
}

func createStagedAssetContent() (*stagedAssetWriter, error) {
	directoryPath, err := os.MkdirTemp("", ".workcell-release-stage-*")
	if err != nil {
		return nil, fmt.Errorf("create private release asset staging directory: %w", err)
	}
	directoryFD, err := unix.Open(directoryPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open private release asset staging directory: %w", err), removeStagedAssetDirectoryPath(directoryPath))
	}
	directory := os.NewFile(uintptr(directoryFD), directoryPath)
	if directory == nil {
		return nil, errors.Join(errors.New("create private release asset staging directory handle"), closeUnixFD(directoryFD, "release asset staging directory"), removeStagedAssetDirectoryPath(directoryPath))
	}
	var directoryStat unix.Stat_t
	if err := unix.Fstat(directoryFD, &directoryStat); err != nil {
		return nil, errors.Join(fmt.Errorf("stat private release asset staging directory: %w", err), closeAssetContent(directory, "staging directory"), removeStagedAssetDirectoryPath(directoryPath))
	}
	if err := validateStagedAssetDirectoryStat(directoryStat); err != nil {
		return nil, errors.Join(err, closeAssetContent(directory, "staging directory"), removeStagedAssetDirectoryPath(directoryPath))
	}
	stage := &stagedAssetWriter{
		directory: directory, directoryPath: directoryPath,
		directoryDev: uint64(directoryStat.Dev), directoryIno: directoryStat.Ino,
	}
	for attempts := 0; attempts < 8; attempts++ {
		var randomName [16]byte
		if _, err := rand.Read(randomName[:]); err != nil {
			return nil, errors.Join(fmt.Errorf("generate release asset staging name: %w", err), discardStagedAssetContent(stage, "staging file"))
		}
		stage.name = ".workcell-release-asset-" + hex.EncodeToString(randomName[:])
		fileFD, openErr := unix.Openat(directoryFD, stage.name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if errors.Is(openErr, unix.EEXIST) {
			continue
		}
		if openErr != nil {
			return nil, errors.Join(fmt.Errorf("create random release asset content stage: %w", openErr), discardStagedAssetContent(stage, "staging file"))
		}
		stage.linked = true
		stage.file = os.NewFile(uintptr(fileFD), filepath.Join(directoryPath, stage.name))
		if stage.file == nil {
			return nil, errors.Join(errors.New("create release asset content stage handle"), closeUnixFD(fileFD, "release asset content stage"), discardStagedAssetContent(stage, "staging file"))
		}
		break
	}
	if stage.file == nil {
		return nil, errors.Join(errors.New("allocate unique release asset content stage name"), discardStagedAssetContent(stage, "staging file"))
	}
	if err := unix.Fchmod(int(stage.file.Fd()), 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("set private mode on release asset content stage: %w", err), discardStagedAssetContent(stage, "staging file"))
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(stage.file.Fd()), &stat); err != nil {
		return nil, errors.Join(fmt.Errorf("stat release asset content stage: %w", err), discardStagedAssetContent(stage, "staging file"))
	}
	if err := validateStagedAssetStat(stat, 0, 1); err != nil {
		return nil, errors.Join(err, discardStagedAssetContent(stage, "staging file"))
	}
	return stage, nil
}

func sealStagedAssetContent(stage *stagedAssetWriter, name string, size int64) (*os.File, error) {
	if stage == nil || stage.file == nil || stage.directory == nil || !stage.linked {
		return nil, errors.New("release asset content stage is incomplete")
	}
	var writerStat unix.Stat_t
	if err := unix.Fstat(int(stage.file.Fd()), &writerStat); err != nil {
		return nil, errors.Join(fmt.Errorf("stat completed release asset %q content stage: %w", name, err), discardStagedAssetContent(stage, name))
	}
	if err := validateStagedAssetStat(writerStat, size, 1); err != nil {
		return nil, errors.Join(err, discardStagedAssetContent(stage, name))
	}

	readerFD, err := unix.Openat(int(stage.directory.Fd()), stage.name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open release asset %q content stage read-only: %w", name, err), discardStagedAssetContent(stage, name))
	}
	reader := os.NewFile(uintptr(readerFD), filepath.Join(stage.directoryPath, stage.name))
	if reader == nil {
		return nil, errors.Join(fmt.Errorf("create release asset %q read-only content handle", name), closeUnixFD(readerFD, "release asset read-only content"), discardStagedAssetContent(stage, name))
	}
	var readerStat unix.Stat_t
	if err := unix.Fstat(readerFD, &readerStat); err != nil {
		return nil, errors.Join(fmt.Errorf("stat release asset %q read-only content stage: %w", name, err), closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	if err := validateStagedAssetStat(readerStat, size, 1); err != nil {
		return nil, errors.Join(err, closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	if writerStat.Dev != readerStat.Dev || writerStat.Ino != readerStat.Ino {
		return nil, errors.Join(fmt.Errorf("release asset %q content stage path changed before read-only sealing", name), closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	flags, err := unix.FcntlInt(uintptr(readerFD), unix.F_GETFL, 0)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("inspect release asset %q read-only content flags: %w", name, err), closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	if flags&unix.O_ACCMODE != unix.O_RDONLY {
		return nil, errors.Join(fmt.Errorf("release asset %q content stage did not seal read-only", name), closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	if err := unix.Unlinkat(int(stage.directory.Fd()), stage.name, 0); err != nil {
		return nil, errors.Join(fmt.Errorf("unlink release asset %q content stage: %w", name, err), closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	stage.linked = false
	var sealedStat unix.Stat_t
	if err := unix.Fstat(readerFD, &sealedStat); err != nil {
		return nil, errors.Join(fmt.Errorf("stat sealed release asset %q content: %w", name, err), closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	if err := validateStagedAssetStat(sealedStat, size, 0); err != nil {
		return nil, errors.Join(err, closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	if writerStat.Dev != sealedStat.Dev || writerStat.Ino != sealedStat.Ino {
		return nil, errors.Join(fmt.Errorf("release asset %q content stage identity changed while sealing", name), closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	if err := closeStagedAssetWriter(stage, name); err != nil {
		return nil, errors.Join(err, closeAssetContent(reader, name), discardStagedAssetContent(stage, name))
	}
	if err := closeStagedAssetDirectory(stage); err != nil {
		return nil, errors.Join(err, closeAssetContent(reader, name))
	}
	return reader, nil
}

func hashStagedAssetContent(reader *os.File, name string, size int64) (string, error) {
	var before unix.Stat_t
	if err := unix.Fstat(int(reader.Fd()), &before); err != nil {
		return "", fmt.Errorf("stat sealed release asset %q before hashing: %w", name, err)
	}
	if err := validateStagedAssetStat(before, size, 0); err != nil {
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(reader, size+1))
	var after unix.Stat_t
	afterErr := unix.Fstat(int(reader.Fd()), &after)
	if copyErr != nil || afterErr != nil {
		var wrappedCopyErr, wrappedAfterErr error
		if copyErr != nil {
			wrappedCopyErr = fmt.Errorf("hash sealed release asset %q: %w", name, copyErr)
		}
		if afterErr != nil {
			wrappedAfterErr = fmt.Errorf("stat sealed release asset %q after hashing: %w", name, afterErr)
		}
		return "", errors.Join(wrappedCopyErr, wrappedAfterErr)
	}
	if written != size {
		return "", fmt.Errorf("hash sealed release asset %q: read size %d differs from staged size %d", name, written, size)
	}
	if assetSourceStatFromUnix(before) != assetSourceStatFromUnix(after) {
		return "", fmt.Errorf("sealed release asset %q changed while it was hashed", name)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind sealed release asset %q after hashing: %w", name, err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateStagedAssetDirectoryStat(stat unix.Stat_t) error {
	if uint32(stat.Mode)&uint32(unix.S_IFMT) != uint32(unix.S_IFDIR) || uint32(stat.Mode)&0o777 != 0o700 || stat.Uid != uint32(os.Geteuid()) || stat.Ino == 0 {
		return errors.New("release asset staging directory failed owner, mode, directory, or identity validation")
	}
	return nil
}

func validateStagedAssetStat(stat unix.Stat_t, size int64, links uint64) error {
	if uint32(stat.Mode)&uint32(unix.S_IFMT) != uint32(unix.S_IFREG) || uint32(stat.Mode)&0o777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Size != size || uint64(stat.Nlink) != links || stat.Ino == 0 {
		return errors.New("release asset content stage failed owner, mode, size, regular-file, identity, or link-count validation")
	}
	return nil
}

func closeStagedAssetWriter(stage *stagedAssetWriter, name string) error {
	if stage == nil || stage.file == nil {
		return nil
	}
	file := stage.file
	stage.file = nil
	if err := file.Close(); err != nil {
		return fmt.Errorf("close release asset %q content stage writer: %w", name, err)
	}
	return nil
}

func closeStagedAssetDirectory(stage *stagedAssetWriter) error {
	if stage == nil || stage.directory == nil {
		return nil
	}
	var pathStat unix.Stat_t
	pathErr := unix.Fstatat(unix.AT_FDCWD, stage.directoryPath, &pathStat, unix.AT_SYMLINK_NOFOLLOW)
	if pathErr == nil && (uint64(pathStat.Dev) != stage.directoryDev || pathStat.Ino != stage.directoryIno) {
		pathErr = errors.New("release asset staging directory path changed before cleanup")
	}
	if pathErr == nil {
		pathErr = os.Remove(stage.directoryPath)
	}
	directory := stage.directory
	stage.directory = nil
	closeErr := directory.Close()
	if pathErr != nil {
		pathErr = fmt.Errorf("remove release asset staging directory: %w", pathErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close release asset staging directory: %w", closeErr)
	}
	return errors.Join(pathErr, closeErr)
}

func removeStagedAssetDirectoryPath(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove release asset staging directory: %w", err)
	}
	return nil
}

func discardStagedAssetContent(stage *stagedAssetWriter, name string) error {
	if stage == nil {
		return nil
	}
	var unlinkErr error
	if stage.linked && stage.directory != nil {
		if err := unix.Unlinkat(int(stage.directory.Fd()), stage.name, 0); err != nil {
			unlinkErr = fmt.Errorf("remove release asset %q content stage: %w", name, err)
		} else {
			stage.linked = false
		}
	}
	return errors.Join(unlinkErr, closeStagedAssetWriter(stage, name), closeStagedAssetDirectory(stage))
}

func rewindLocalAssetReader(asset *localAsset) (io.Reader, error) {
	if asset == nil || asset.content == nil {
		return nil, errors.New("release asset has no staged content")
	}
	if _, err := asset.content.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind release asset %q content: %w", asset.name, err)
	}
	return io.LimitReader(asset.content, asset.size), nil
}

func closeLocalAssets(assets []localAsset) error {
	var closeErrors []error
	for i := range assets {
		if assets[i].content == nil {
			continue
		}
		if err := assets[i].content.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close staged release asset %q content: %w", assets[i].name, err))
		}
		assets[i].content = nil
	}
	return errors.Join(closeErrors...)
}

func abortAssetInspection(assets []localAsset, err error) error {
	return errors.Join(err, closeLocalAssets(assets))
}

func closeAssetSource(source assetSource, name string) error {
	if source == nil {
		return nil
	}
	if err := source.Close(); err != nil {
		return fmt.Errorf("close release asset %q source: %w", name, err)
	}
	return nil
}

func closeAssetContent(content io.Closer, name string) error {
	if content == nil {
		return nil
	}
	if err := content.Close(); err != nil {
		return fmt.Errorf("close staged release asset %q content: %w", name, err)
	}
	return nil
}
