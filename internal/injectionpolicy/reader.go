// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injectionpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// PolicyFileMaxBytes is the maximum size of one policy file read.
const PolicyFileMaxBytes int64 = 16 << 20

// PolicyBundleMaxBytes is the maximum size of one policy bundle.
const PolicyBundleMaxBytes int64 = 64 << 20

// PolicyBundleMaxFiles is the maximum files in one policy bundle.
const PolicyBundleMaxFiles = 4096

// PolicyFile contains one accepted descriptor snapshot.
type PolicyFile struct {
	Bytes  []byte
	Sha256 string
}

// BundleReader bounds one policy bundle.
type BundleReader struct {
	bytes                             int64
	files                             int
	fileLimit, bundleLimit            int64
	fileLimitCount                    int
	beforeOpenLeaf                    func()
	beforeFinalStat, afterConfirmRead func()
	beforeConfirmRead                 func(*os.File)
	snapshots                         map[string]PolicyFile
}

// NewBundleReader returns a reader with a fresh bundle budget.
func NewBundleReader() *BundleReader {
	return NewBundleReaderWithLimits(PolicyFileMaxBytes, PolicyBundleMaxBytes, PolicyBundleMaxFiles)
}

// NewBundleReaderWithLimits creates a reader for test limits.
func NewBundleReaderWithLimits(fileLimit, bundleLimit int64, fileLimitCount int) *BundleReader {
	return &BundleReader{fileLimit: fileLimit, bundleLimit: bundleLimit, fileLimitCount: fileLimitCount}
}

// Read reads one policy file and accounts for its bundle limits.
func (r *BundleReader) Read(path string) (PolicyFile, error) {
	if r == nil {
		return PolicyFile{}, fmt.Errorf("injection policy reader is nil")
	}
	if snapshot, ok := r.snapshots[path]; ok {
		delete(r.snapshots, path)
		return snapshot, nil
	}
	if r.fileLimit <= 0 || r.bundleLimit <= 0 || r.fileLimitCount <= 0 || r.fileLimit > PolicyFileMaxBytes || r.bundleLimit > PolicyBundleMaxBytes || r.fileLimitCount > PolicyBundleMaxFiles {
		return PolicyFile{}, errors.New("injection policy reader has invalid limits")
	}
	if r.files >= r.fileLimitCount {
		return PolicyFile{}, fmt.Errorf("injection policy bundle exceeds the include/file count limit of %d: %s", r.fileLimitCount, path)
	}
	remaining := r.bundleLimit - r.bytes
	if remaining < 0 {
		remaining = 0
	}
	limit := r.fileLimit
	limitName := "per-file limit"
	if remaining < limit {
		limit = remaining
		limitName = "aggregate bundle limit"
	}

	file, err := openPolicyFileNoFollow(path, r.beforeOpenLeaf)
	if err != nil {
		return PolicyFile{}, err
	}
	before, err := statTrustedPolicyFile(file, path)
	if err != nil {
		return PolicyFile{}, closePolicyFile(err, file, path)
	}

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("read injection policy %s: %w", path, err), file, path)
	}
	after, err := statTrustedPolicyFile(file, path)
	if err != nil {
		return PolicyFile{}, closePolicyFile(err, file, path)
	}
	if !samePolicyFile(before, after) {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("injection policy changed while it was read: %s", path), file, path)
	}
	if int64(len(data)) > limit {
		max := limit
		if limitName == "aggregate bundle limit" {
			max = r.bundleLimit
		}
		return PolicyFile{}, closePolicyFile(fmt.Errorf("injection policy %s exceeds the %s of %d bytes", path, limitName, max), file, path)
	}
	if after.size != int64(len(data)) {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("injection policy byte count changed while it was read: %s", path), file, path)
	}
	if !utf8.Valid(data) {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("injection policy must contain valid UTF-8: %s", path), file, path)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("rewind injection policy %s: %w", path, err), file, path)
	}
	if r.beforeConfirmRead != nil {
		r.beforeConfirmRead(file)
	}
	confirmed, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("confirm injection policy %s: %w", path, err), file, path)
	}
	if len(confirmed) != len(data) || sha256.Sum256(confirmed) != sha256.Sum256(data) {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("injection policy changed while it was read: %s", path), file, path)
	}
	if r.afterConfirmRead != nil {
		r.afterConfirmRead()
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("rewind accepted injection policy %s: %w", path, err), file, path)
	}
	accepted, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("read accepted injection policy %s: %w", path, err), file, path)
	}
	if len(accepted) != len(data) || sha256.Sum256(accepted) != sha256.Sum256(data) {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("injection policy changed while it was read: %s", path), file, path)
	}
	if r.beforeFinalStat != nil {
		r.beforeFinalStat()
	}
	acceptedStat, err := statTrustedPolicyFile(file, path)
	if err != nil {
		return PolicyFile{}, closePolicyFile(err, file, path)
	}
	if !samePolicyFile(before, acceptedStat) {
		return PolicyFile{}, closePolicyFile(fmt.Errorf("injection policy changed while it was read: %s", path), file, path)
	}
	if err := file.Close(); err != nil {
		return PolicyFile{}, fmt.Errorf("close injection policy %s: %w", path, err)
	}

	r.bytes += int64(len(accepted))
	r.files++
	sum := sha256.Sum256(accepted)
	return PolicyFile{Bytes: accepted, Sha256: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

type policyFileStat struct {
	dev, ino, mode, uid, gid, nlink uint64
	size, modTime, changeTime       int64
}

// ReadAndPin pins a file snapshot for one following Read.
func (r *BundleReader) ReadAndPin(path string) (PolicyFile, error) {
	file, err := r.Read(path)
	if err != nil {
		return PolicyFile{}, err
	}
	if r.snapshots == nil {
		r.snapshots = make(map[string]PolicyFile)
	}
	r.snapshots[path] = file
	return file, nil
}

func statTrustedPolicyFile(file *os.File, path string) (policyFileStat, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return policyFileStat{}, fmt.Errorf("fstat injection policy %s: %w", path, err)
	}
	mode := uint64(stat.Mode)
	if mode&unix.S_IFMT != unix.S_IFREG || stat.Ino == 0 || stat.Nlink != 1 {
		return policyFileStat{}, fmt.Errorf("injection policy %s must be a regular file with exactly one link", path)
	}
	if stat.Uid != policyCurrentEUID() || mode&0o022 != 0 {
		return policyFileStat{}, fmt.Errorf("injection policy %s must be owned by the current user and not writable by group or other users", path)
	}
	if err := rejectPolicyACL(int(file.Fd())); err != nil {
		return policyFileStat{}, fmt.Errorf("injection policy file has an extended ACL: %s: %w", path, err)
	}
	modTime, changeTime := policyFileTimes(stat)
	return policyFileStat{
		dev: uint64(stat.Dev), ino: uint64(stat.Ino), mode: mode, uid: uint64(stat.Uid), gid: uint64(stat.Gid),
		nlink: uint64(stat.Nlink), size: stat.Size, modTime: modTime, changeTime: changeTime,
	}, nil
}

func samePolicyFile(before, after policyFileStat) bool {
	return before.dev == after.dev && before.ino == after.ino && before.mode == after.mode && before.uid == after.uid && before.gid == after.gid && before.nlink == after.nlink && before.size == after.size && before.modTime == after.modTime && before.changeTime == after.changeTime
}

func closePolicyFile(cause error, file *os.File, path string) error {
	if err := file.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("close injection policy %s: %w", path, err))
	}
	return cause
}

// ReadFile reads one policy file with default limits.
func ReadFile(path string) ([]byte, error) {
	reader := NewBundleReader()
	file, err := reader.Read(path)
	if err != nil {
		return nil, err
	}
	return file.Bytes, nil
}

func openPolicyFileNoFollow(path string, beforeOpenLeaf func()) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("make injection policy path absolute %s: %w", path, err)
	}
	abs = canonicalPolicySystemPath(filepath.Clean(abs))
	if !filepath.IsAbs(abs) || abs == string(filepath.Separator) {
		return nil, fmt.Errorf("injection policy path must name a file: %s", path)
	}

	components := strings.Split(strings.TrimPrefix(abs, string(filepath.Separator)), string(filepath.Separator))
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open injection policy root for %s: %w", path, err)
	}
	var rootStat unix.Stat_t
	if err := unix.Fstat(fd, &rootStat); err != nil {
		return nil, errors.Join(fmt.Errorf("stat injection policy root for %s: %w", path, err), closeUnixError(unix.Close(fd), "root", path))
	}
	anchored, err := validatePolicyDirectory(fd, string(filepath.Separator), rootStat, false)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("validate injection policy root for %s: %w", path, err), closeUnixError(unix.Close(fd), "root", path))
	}
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			return nil, errors.Join(fmt.Errorf("injection policy path has an unsafe component: %s", path), closeUnixError(unix.Close(fd), "parent", path))
		}
		leaf := index == len(components)-1
		if leaf && beforeOpenLeaf != nil {
			beforeOpenLeaf()
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if !leaf {
			flags |= unix.O_DIRECTORY
		}
		nextFD, openErr := unix.Openat(fd, component, flags, 0)
		closeErr := unix.Close(fd)
		if openErr != nil {
			if errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, unix.ENOTDIR) {
				return nil, errors.Join(fmt.Errorf("injection policy path contains a symbolic link or non-directory component: %s", path), closeUnixError(closeErr, "parent", path))
			}
			return nil, errors.Join(fmt.Errorf("open injection policy component %q in %s: %w", component, abs, openErr), closeUnixError(closeErr, "parent", path))
		}
		if closeErr != nil {
			return nil, errors.Join(fmt.Errorf("close injection policy parent for %s: %w", path, closeErr), closeUnixError(unix.Close(nextFD), "component", path))
		}
		if !leaf {
			var stat unix.Stat_t
			if err := unix.Fstat(nextFD, &stat); err != nil {
				return nil, errors.Join(fmt.Errorf("stat injection policy directory %s: %w", path, err), closeUnixError(unix.Close(nextFD), "directory", path))
			}
			currentPath := string(filepath.Separator) + strings.Join(components[:index+1], string(filepath.Separator))
			nextAnchored, err := validatePolicyDirectory(nextFD, currentPath, stat, anchored)
			if err != nil {
				return nil, errors.Join(err, closeUnixError(unix.Close(nextFD), "directory", path))
			}
			anchored = nextAnchored
		} else if !anchored {
			return nil, errors.Join(fmt.Errorf("injection policy must be beneath a current-user-controlled directory: %s", path), closeUnixError(unix.Close(nextFD), "file", path))
		}
		fd = nextFD
	}

	if fd < 0 {
		return nil, fmt.Errorf("open injection policy returned an invalid descriptor: %s", path)
	}
	// #nosec G115 -- successful unix.Openat calls return a nonnegative int file descriptor.
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		return nil, errors.Join(fmt.Errorf("open injection policy: %s", path), closeUnixError(unix.Close(fd), "file", path))
	}
	return file, nil
}

func closeUnixError(err error, label, path string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close injection policy %s for %s: %w", label, path, err)
}

func validatePolicyDirectory(fd int, path string, stat unix.Stat_t, anchored bool) (bool, error) {
	mode := uint64(stat.Mode)
	if mode&unix.S_IFMT != unix.S_IFDIR || stat.Ino == 0 {
		return false, fmt.Errorf("injection policy path contains a non-directory component: %s", path)
	}
	ownedAndControlled := stat.Uid == policyCurrentEUID() && mode&0o022 == 0
	if anchored {
		if !ownedAndControlled {
			return false, fmt.Errorf("injection policy directory below the current-user-controlled anchor is unsafe: %s", path)
		}
		if err := rejectPolicyACL(fd); err != nil {
			return false, fmt.Errorf("injection policy directory has an extended ACL: %s: %w", path, err)
		}
		return true, nil
	}
	if stat.Uid == 0 && (mode&0o022 == 0 || mode&unix.S_ISVTX != 0) {
		anchored = policyCurrentEUID() == 0 && mode&0o077 == 0 && mode&unix.S_ISVTX == 0
	} else if ownedAndControlled {
		anchored = true
	} else {
		return false, fmt.Errorf("injection policy directory ancestor is not trusted: %s", path)
	}
	if err := rejectPolicyACL(fd); err != nil {
		return false, fmt.Errorf("injection policy directory has an extended ACL: %s: %w", path, err)
	}
	return anchored, nil
}

var policyCurrentEUID = func() uint32 { return uint32(os.Geteuid()) }

func canonicalPolicySystemPath(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	for _, prefix := range []string{"/var", "/etc", "/tmp"} {
		if path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator)) {
			return filepath.Join("/private", strings.TrimPrefix(path, string(filepath.Separator)))
		}
	}
	return path
}
