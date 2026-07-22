// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCanonicalAssetSourcePathRejectsRetargetableRawForms(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		"", "\x00", ".", "..", "./asset", "dir/../asset", "dir//asset", "dir/",
		"/dir//asset", "//dir/asset", "/dir/./asset", "/dir/../asset",
		" leading/asset", "trailing /asset", "dir/ leading", "dir/trailing ",
	} {
		t.Run(fmt.Sprintf("%q", path), func(t *testing.T) {
			t.Parallel()
			_, err := canonicalAssetSourcePath(path, "linux")
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("canonicalAssetSourcePath(%q) error = %v", path, err)
			}
		})
	}
	for _, path := range []string{"asset", "dir/asset", "/dir/asset"} {
		if _, err := canonicalAssetSourcePath(path, "linux"); err != nil {
			t.Fatalf("canonicalAssetSourcePath(%q) error = %v", path, err)
		}
	}
}

func TestCanonicalAssetSourcePathRewritesDarwinSystemSymlinks(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		"/tmp/release": "/private/tmp/release", "/var/folders/release": "/private/var/folders/release",
		"/private/tmp/release": "/private/tmp/release",
	} {
		got, err := canonicalAssetSourcePath(path, "darwin")
		if err != nil || got != want {
			t.Fatalf("canonicalAssetSourcePath(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestInspectLocalAssetsStagesStableUnlinkedContent(t *testing.T) {
	t.Parallel()
	content := []byte("trusted release content\n")
	path := writeAssetFile(t, "asset.bin", content)
	assets, err := inspectLocalAssets([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	closeFoundationAssetsAtCleanup(t, assets)
	wantDigest := fmt.Sprintf("%x", sha256.Sum256(content))
	if len(assets) != 1 || assets[0].name != "asset.bin" || assets[0].size != int64(len(content)) || assets[0].sha256 != wantDigest {
		t.Fatalf("inspectLocalAssets() = %#v", assets)
	}
	stage, ok := assets[0].content.(*os.File)
	if !ok {
		t.Fatalf("staged content type = %T", assets[0].content)
	}
	if _, err := os.Stat(stage.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage remains linked at %q: %v", stage.Name(), err)
	}
	if _, err := os.Stat(filepath.Dir(stage.Name())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private staging directory remains at %q: %v", filepath.Dir(stage.Name()), err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(stage.Fd()), &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || int(stat.Uid) != os.Geteuid() || stat.Nlink != 0 {
		t.Fatalf("staged fd mode=%o uid=%d nlink=%d", stat.Mode, stat.Uid, stat.Nlink)
	}
	flags, err := unix.FcntlInt(stage.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_ACCMODE != unix.O_RDONLY {
		t.Fatalf("staged fd access mode = %d, want read-only", flags&unix.O_ACCMODE)
	}
	if got := readFoundationAsset(t, &assets[0]); !bytes.Equal(got, content) {
		t.Fatalf("staged content = %q, want %q", got, content)
	}
	if _, err := stage.WriteAt([]byte("X"), 0); err == nil {
		t.Fatal("read-only staged content allowed a same-length overwrite")
	}
	if err := stage.Truncate(1); err == nil {
		t.Fatal("read-only staged content allowed truncation")
	}
	if got := readFoundationAsset(t, &assets[0]); !bytes.Equal(got, content) {
		t.Fatalf("sealed staged content = %q, want %q", got, content)
	}
}

func TestSealStagedAssetContentRejectsPathSwap(t *testing.T) {
	stage, err := createStagedAssetContent()
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(stage.directoryPath, "moved")
	t.Cleanup(func() { _ = os.Remove(moved); _ = os.Remove(stage.directoryPath) })
	if _, err := stage.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := stage.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(stage.directoryPath, stage.name), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage.directoryPath, stage.name), []byte("evil"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reader, err := sealStagedAssetContent(stage, "asset.bin", 4); err == nil {
		_ = reader.Close()
		t.Fatal("path-swapped stage was sealed")
	}
}

func TestInspectLocalAssetsRejectsUnsafeLinksTypesAndMissingSource(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string) string
		input  bool
		want   string
	}{
		{name: "empty", mutate: func(t *testing.T, path string) string { return replaceAsset(t, path, nil, false) }, input: true, want: "non-empty regular"},
		{name: "symlink", mutate: func(t *testing.T, path string) string { return replaceAsset(t, path, []byte("target"), true) }, input: true, want: "symlink"},
		{name: "ancestor symlink", mutate: symlinkAssetAncestor, input: true, want: "symbolic-link"},
		{name: "fifo", mutate: func(t *testing.T, path string) string {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}, input: true, want: "must be a regular file"},
		{name: "device", mutate: func(*testing.T, string) string { return "/dev/null" }, input: true, want: "current-user-controlled directory"},
		{name: "hardlink", mutate: func(t *testing.T, path string) string {
			if err := os.Link(path, filepath.Join(t.TempDir(), "hardlink")); err != nil {
				t.Fatal(err)
			}
			return path
		}, input: true, want: "exactly one link"},
		{name: "directory", mutate: func(t *testing.T, path string) string {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			return path
		}, input: true, want: "must be a regular file"},
		{name: "missing", mutate: func(t *testing.T, path string) string {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			return path
		}, want: "inspect release asset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := tc.mutate(t, writeAssetFile(t, "asset.bin", []byte("content")))
			assets, err := inspectLocalAssets([]string{path})
			if err == nil {
				_ = closeLocalAssets(assets)
				t.Fatal("inspectLocalAssets() unexpectedly succeeded")
			}
			if errors.Is(err, ErrInvalidInput) != tc.input || !strings.Contains(err.Error(), tc.want) || (!tc.input && !errors.Is(err, os.ErrNotExist)) {
				t.Fatalf("inspectLocalAssets() error = %v", err)
			}
		})
	}
}

func TestInspectLocalAssetsRejectsUntrustedPathsAndWritableFiles(t *testing.T) {
	t.Parallel()
	t.Run("no owner-controlled directory anchor", func(t *testing.T) {
		path, err := os.CreateTemp(string(filepath.Separator)+"tmp", "workcell-release-anchor-*")
		if err != nil {
			t.Fatal(err)
		}
		name := path.Name()
		if _, err := path.WriteString("content"); err != nil {
			t.Fatal(err)
		}
		if err := path.Close(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(name) })
		if _, err := inspectLocalAssets([]string{name}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "current-user-controlled directory") {
			t.Fatalf("inspectLocalAssets() error = %v", err)
		}
	})
	t.Run("writable directory below anchor", func(t *testing.T) {
		root := t.TempDir()
		writable := filepath.Join(root, "writable")
		if err := os.Mkdir(writable, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(writable, 0o777); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(writable, "asset.bin")
		if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := inspectLocalAssets([]string{path}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "below its owner-controlled anchor") {
			t.Fatalf("inspectLocalAssets() error = %v", err)
		}
	})
	t.Run("file writable by another user", func(t *testing.T) {
		path := writeAssetFile(t, "asset.bin", []byte("content"))
		if err := os.Chmod(path, 0o622); err != nil {
			t.Fatal(err)
		}
		if _, err := inspectLocalAssets([]string{path}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "not writable by another user") {
			t.Fatalf("inspectLocalAssets() error = %v", err)
		}
	})
	t.Run("foreign-owned source metadata", func(t *testing.T) {
		stat := validFoundationStat(1)
		stat.UID++
		source := &foundationInjectedSource{reader: bytes.NewReader([]byte("x")), stat: stat}
		_, err := inspectLocalAssetsWithOpener([]string{"/asset.bin"}, foundationOpenerFunc(func(string) (assetSource, error) { return source, nil }))
		if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "owned by the current user") || source.closes != 1 {
			t.Fatalf("inspectLocalAssetsWithOpener() error=%v closes=%d", err, source.closes)
		}
	})
}

func TestStagedContentSurvivesSameMetadataMutationAndPathSwap(t *testing.T) {
	t.Parallel()
	t.Run("post inspection mutation", func(t *testing.T) {
		original := []byte("original")
		path := writeAssetFile(t, "asset.bin", original)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		assets, err := inspectLocalAssets([]string{path})
		if err != nil {
			t.Fatal(err)
		}
		closeFoundationAssetsAtCleanup(t, assets)
		if err := os.WriteFile(path, []byte("mutated!"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		if got := readFoundationAsset(t, &assets[0]); !bytes.Equal(got, original) {
			t.Fatalf("staged content changed: got %q, want %q", got, original)
		}
	})
	t.Run("swap after open", func(t *testing.T) {
		original := []byte("opened source")
		path := writeAssetFile(t, "asset.bin", original)
		base := openatAssetSourceOpener{}
		opener := foundationOpenerFunc(func(candidate string) (assetSource, error) {
			source, err := base.Open(candidate)
			if err != nil {
				return nil, err
			}
			if err := os.Rename(candidate, candidate+".opened"); err != nil {
				return nil, errors.Join(err, source.Close())
			}
			if err := os.WriteFile(candidate, []byte("replacement"), 0o600); err != nil {
				return nil, errors.Join(err, source.Close())
			}
			return source, nil
		})
		assets, err := inspectLocalAssetsWithOpener([]string{path}, opener)
		if err != nil {
			t.Fatal(err)
		}
		closeFoundationAssetsAtCleanup(t, assets)
		if got := readFoundationAsset(t, &assets[0]); !bytes.Equal(got, original) {
			t.Fatalf("path swap changed opened stream: got %q, want %q", got, original)
		}
	})
}

func TestInspectionPreservesOperationalStatReadAndSourceCloseErrors(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"stat", "restat", "read", "close"} {
		t.Run(phase, func(t *testing.T) {
			injected := errors.New("injected " + phase + " failure")
			source := &foundationInjectedSource{reader: bytes.NewReader([]byte("x")), stat: validFoundationStat(1)}
			switch phase {
			case "stat":
				source.statErr = injected
				source.statErrAt = 1
			case "restat":
				source.statErr = injected
				source.statErrAt = 2
			case "read":
				source.readErr = injected
			case "close":
				source.closeErr = injected
			}
			_, err := inspectLocalAssetsWithOpener([]string{"/asset.bin"}, foundationOpenerFunc(func(string) (assetSource, error) { return source, nil }))
			if !errors.Is(err, injected) || errors.Is(err, ErrInvalidInput) || source.closes != 1 {
				t.Fatalf("%s error=%v closes=%d", phase, err, source.closes)
			}
		})
	}
}

func TestInspectionBoundsGrowingSourceAtObservedSize(t *testing.T) {
	t.Parallel()
	source := &foundationInjectedSource{
		reader: bytes.NewReader([]byte("keeps growing")),
		stat:   validFoundationStat(1),
	}
	_, err := inspectLocalAssetsWithOpener([]string{"/asset.bin"}, foundationOpenerFunc(func(string) (assetSource, error) { return source, nil }))
	if err == nil || !strings.Contains(err.Error(), "copied size 2 differs from reported size 1") || source.reader.Len() != len("keeps growing")-2 || source.closes != 1 {
		t.Fatalf("inspectLocalAssetsWithOpener() error=%v remaining=%d closes=%d", err, source.reader.Len(), source.closes)
	}
}

func TestInspectionRejectsOversizedAndAggregateBudgets(t *testing.T) {
	t.Parallel()
	source := &foundationInjectedSource{
		reader: bytes.NewReader(nil),
		stat:   validFoundationStat(maxReleaseAssetBytes + 1),
	}
	_, err := inspectLocalAssetsWithOpener([]string{"/asset.bin"}, foundationOpenerFunc(func(string) (assetSource, error) { return source, nil }))
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "per-asset staging limit") || source.closes != 1 {
		t.Fatalf("inspectLocalAssetsWithOpener() error=%v closes=%d", err, source.closes)
	}
	if total, err := reserveReleaseAssetBytes(maxReleaseAssetStagedBytes-maxReleaseAssetBytes, maxReleaseAssetBytes); err != nil || total != maxReleaseAssetStagedBytes {
		t.Fatalf("aggregate boundary = %d, %v", total, err)
	}
	for _, tc := range []struct{ total, size int64 }{{0, 0}, {0, maxReleaseAssetBytes + 1}, {-1, 1}, {maxReleaseAssetStagedBytes, 1}} {
		if _, err := reserveReleaseAssetBytes(tc.total, tc.size); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("reserveReleaseAssetBytes(%d, %d) error = %v", tc.total, tc.size, err)
		}
	}
}

func TestInspectionRejectsSourceMutation(t *testing.T) {
	t.Parallel()
	mutations := map[string]func(*assetSourceStat){
		"mode": func(stat *assetSourceStat) { stat.Mode++ }, "size": func(stat *assetSourceStat) { stat.Size++ },
		"link": func(stat *assetSourceStat) { stat.Nlink++ }, "uid": func(stat *assetSourceStat) { stat.UID++ },
		"gid": func(stat *assetSourceStat) { stat.GID++ }, "device": func(stat *assetSourceStat) { stat.Dev++ },
		"inode": func(stat *assetSourceStat) { stat.Ino++ }, "mtime-sec": func(stat *assetSourceStat) { stat.ModTimeSec++ },
		"mtime-nsec": func(stat *assetSourceStat) { stat.ModTimeNsec++ }, "ctime-sec": func(stat *assetSourceStat) { stat.ChangeTimeSec++ },
		"ctime-nsec": func(stat *assetSourceStat) { stat.ChangeTimeNsec++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			before := validFoundationStat(4)
			after := before
			mutate(&after)
			source := &foundationInjectedSource{reader: bytes.NewReader([]byte("data")), stats: []assetSourceStat{before, after}}
			_, err := inspectLocalAssetsWithOpener([]string{"/asset.bin"}, foundationOpenerFunc(func(string) (assetSource, error) { return source, nil }))
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "changed while its content was staged") || source.closes != 1 {
				t.Fatalf("inspectLocalAssetsWithOpener() error=%v closes=%d", err, source.closes)
			}
		})
	}
}

func TestCloseLocalAssetsJoinsContentCloseErrors(t *testing.T) {
	t.Parallel()
	firstErr, secondErr := errors.New("first close"), errors.New("second close")
	first := &foundationTrackedContent{Reader: bytes.NewReader(nil), closeErr: firstErr}
	second := &foundationTrackedContent{Reader: bytes.NewReader(nil), closeErr: secondErr}
	assets := []localAsset{{name: "first", content: first}, {name: "second", content: second}}
	err := closeLocalAssets(assets)
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) || first.closes != 1 || second.closes != 1 || assets[0].content != nil || assets[1].content != nil {
		t.Fatalf("closeLocalAssets() error=%v first=%d second=%d", err, first.closes, second.closes)
	}
}

func TestInspectLocalAssetsRetainsLexicalAndDuplicateChecks(t *testing.T) {
	first := writeAssetFile(t, "asset.bin", []byte("one"))
	for name, paths := range map[string][]string{"none": nil, "unsafe": {writeAssetFile(t, "unsafe name", []byte("unsafe"))}, "duplicate": {first, writeAssetFile(t, "asset.bin", []byte("two"))}} {
		_, err := inspectLocalAssets(paths)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	paths := strings.Fields("/a /b /c /d /e /f /g /h /i /j /k /l /m /n /o /p /q /r /s")
	injected, opens := errors.New("injected open"), 0
	opener := foundationOpenerFunc(func(string) (assetSource, error) { opens++; return nil, injected })
	if _, err := inspectLocalAssetsWithOpener(paths, opener); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "between 1 and 18 assets") || opens != 0 {
		t.Fatalf("over-count error=%v opens=%d", err, opens)
	}
	if _, err := inspectLocalAssetsWithOpener(paths[:maxReleaseAssetCount], opener); !errors.Is(err, injected) || opens != 1 {
		t.Fatalf("exact-count error=%v opens=%d", err, opens)
	}
}

func writeAssetFile(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func replaceAsset(t *testing.T, path string, content []byte, symlink bool) string {
	t.Helper()
	if !symlink {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	target := writeAssetFile(t, "target", content)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	return path
}

func symlinkAssetAncestor(t *testing.T, path string) string {
	t.Helper()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, filepath.Base(path)), []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(link, filepath.Base(path))
}

type foundationOpenerFunc func(string) (assetSource, error)

func (open foundationOpenerFunc) Open(path string) (assetSource, error) { return open(path) }

type foundationInjectedSource struct {
	reader                     *bytes.Reader
	stat                       assetSourceStat
	stats                      []assetSourceStat
	statErr, readErr, closeErr error
	statErrAt                  int
	statCalls                  int
	closes                     int
}

func (source *foundationInjectedSource) Read(buffer []byte) (int, error) {
	if source.readErr != nil {
		return 0, source.readErr
	}
	return source.reader.Read(buffer)
}
func (source *foundationInjectedSource) Stat() (assetSourceStat, error) {
	source.statCalls++
	if source.statErr != nil && (source.statErrAt == 0 || source.statErrAt == source.statCalls) {
		return assetSourceStat{}, source.statErr
	}
	if source.statCalls <= len(source.stats) {
		return source.stats[source.statCalls-1], nil
	}
	return source.stat, nil
}
func (source *foundationInjectedSource) Close() error { source.closes++; return source.closeErr }

type foundationTrackedContent struct {
	*bytes.Reader
	closeErr error
	closes   int
}

func (content *foundationTrackedContent) Close() error { content.closes++; return content.closeErr }

func readFoundationAsset(t *testing.T, asset *localAsset) []byte {
	t.Helper()
	reader, err := rewindLocalAssetReader(asset)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func closeFoundationAssetsAtCleanup(t *testing.T, assets []localAsset) {
	t.Helper()
	t.Cleanup(func() {
		if err := closeLocalAssets(assets); err != nil {
			t.Errorf("closeLocalAssets() error = %v", err)
		}
	})
}

func validFoundationStat(size int64) assetSourceStat {
	return assetSourceStat{
		Mode:  uint32(unix.S_IFREG | 0o600),
		Size:  size,
		Nlink: 1,
		UID:   uint32(os.Geteuid()),
		GID:   uint32(os.Getegid()),
		Dev:   1,
		Ino:   1,
	}
}
