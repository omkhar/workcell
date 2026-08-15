// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const workspaceTestStage = ".workcell-stage-11111111111111111111111111111111"

type workspacePublishFixture struct {
	target                          AppleContainerTarget
	request                         MaterializeRequest
	finalParent, final, stageParent string
}

func newWorkspacePublishFixture(t *testing.T) workspacePublishFixture {
	t.Helper()
	target, err := NewAppleContainerTarget(Contract{})
	mustNil(t, err)
	state, source := t.TempDir(), t.TempDir()
	mustNil(t, os.WriteFile(filepath.Join(source, "file"), []byte("original"), 0o600))
	root := targetRoot(state, target.Contract.TargetKind, target.Contract.TargetProvider, "tid")
	return workspacePublishFixture{target, MaterializeRequest{StateRoot: state, TargetID: "tid", MaterializationID: "mid", SourceWorkspace: source}, filepath.Join(root, "materializations"), filepath.Join(root, "materializations", "mid"), filepath.Join(root, workspaceStagingName)}
}
func workspaceTestMaterializeOps() workspaceMaterializeOps {
	ops := systemWorkspaceMaterializeOps()
	ops.random = bytes.NewReader(bytes.Repeat([]byte{0x11}, 16)).Read
	return ops
}
func workspacePathMatchesFD(path string, fd int) bool {
	var named, opened unix.Stat_t
	return unix.Lstat(path, &named) == nil && unix.Fstat(fd, &opened) == nil && named.Dev == opened.Dev && named.Ino == opened.Ino
}
func workspaceMustNotExist(t *testing.T, label, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists or failed inspection: %v", label, err)
	}
}
func TestWorkspaceManagedDirectoryPolicyRequiresBothViews(t *testing.T) {
	for _, kind := range []string{"secure", "opened-uid", "named-uid", "opened-write", "named-special"} {
		base := unix.Stat_t{Dev: 1, Ino: 2, Mode: unix.S_IFDIR | 0o700, Uid: uint32(os.Geteuid())}
		opened, named := base, base
		changed := map[bool]*unix.Stat_t{true: &opened, false: &named}[strings.HasPrefix(kind, "opened")]
		changed.Uid += map[bool]uint32{true: 1}[strings.HasSuffix(kind, "uid")]
		changed.Mode |= map[string]unix.Stat_t{"opened-write": {Mode: 0o022}, "named-special": {Mode: 0o7000}}[kind].Mode
		ops := systemWorkspaceOps()
		ops.fstat = func(_ int, stat *unix.Stat_t) error { *stat = opened; return nil }
		ops.fstatat = func(_ int, _ string, stat *unix.Stat_t, _ int) error { *stat = named; return nil }
		_, err := verifyWorkspaceDirectoryNameAt(1, "materializations", 2, workspaceObjectID{1, 2}, ops)
		if rejected := err != nil; rejected != (kind != "secure") {
			t.Fatalf("%s rejected=%t error=%v", kind, rejected, err)
		}
	}
}
func TestWorkspacePublicationSuccessAndManifestFormat(t *testing.T) {
	fixture := newWorkspacePublishFixture(t)
	mustNil(t, os.Remove(filepath.Join(fixture.request.SourceWorkspace, "file")))
	ops, manifestFlags := workspaceTestMaterializeOps(), false
	originalOpen := ops.workspace.openat
	ops.workspace.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
		if name == fixture.target.Contract.WorkspaceMaterialization.ManifestName && flags&unix.O_CREAT != 0 {
			manifestFlags = flags&(unix.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC) == unix.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC
		}
		return originalOpen(fd, name, flags, mode)
	}
	result, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops)
	mustNil(t, err)
	content, err := os.ReadFile(result.ManifestPath)
	mustNil(t, err)
	want, err := marshalManifestBytes(result.Manifest)
	mustNil(t, err)
	if !manifestFlags || result.Manifest.Entries == nil || !bytes.Contains(content, []byte(`"entries":[]`)) || !bytes.Equal(content, want) || bytes.Contains(content, []byte("\n  \"")) {
		t.Fatalf("workspace manifest or create flags are invalid: flags=%t content=%q", manifestFlags, content)
	}
	pointer, pointerErr := marshalManifestBytes(&result.Manifest)
	bootstrap, bootstrapErr := marshalManifestBytes(BootstrapManifest{Version: 1, BootstrapID: "bid"})
	if pointerErr != nil || bootstrapErr != nil || !bytes.Equal(pointer, want) || !bytes.Contains(bootstrap, []byte("\n  \"")) {
		t.Fatalf("manifest encoding parity: pointer=%q bootstrap=%q error=%v", pointer, bootstrap, errors.Join(pointerErr, bootstrapErr))
	}
	info, statErr := os.Stat(result.MaterializationRoot)
	names, readErr := os.ReadDir(fixture.stageParent)
	if statErr != nil || info.Mode().Perm() != 0o755 || readErr != nil || len(names) != 0 {
		t.Fatalf("successful publication state: final=%v/%v stages=%v/%v", info, statErr, names, readErr)
	}
}
func TestWorkspacePublicationRejectsPhysicalOverlap(t *testing.T) {
	for _, kind := range []string{"equal", "state-inside-source", "source-inside-state"} {
		fixture := newWorkspacePublishFixture(t)
		switch kind {
		case "equal":
			fixture.request.StateRoot = fixture.request.SourceWorkspace
		case "state-inside-source":
			fixture.request.StateRoot = filepath.Join(fixture.request.SourceWorkspace, "state")
			mustNil(t, os.Mkdir(fixture.request.StateRoot, 0o700))
		case "source-inside-state":
			fixture.request.SourceWorkspace = filepath.Join(fixture.request.StateRoot, "source")
			mustNil(t, os.Mkdir(fixture.request.SourceWorkspace, 0o700))
		}
		if _, err := fixture.target.materializeWorkspaceWithOps(fixture.request, workspaceTestMaterializeOps()); err == nil || !strings.Contains(err.Error(), "overlap") {
			t.Fatalf("%s overlap result: %v", kind, err)
		}
		workspaceMustNotExist(t, kind+" overlap target state", filepath.Join(fixture.request.StateRoot, "targets"))
	}
}
func TestWorkspacePublicationRequiresPinnedStateRoot(t *testing.T) {
	for _, reject := range []bool{false, true} {
		fixture, ops, changed := newWorkspacePublishFixture(t), workspaceTestMaterializeOps(), false
		moved, original := fixture.request.StateRoot+".moved", ops.workspace.openat
		ops.workspace.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
			if name == filepath.Base(fixture.request.StateRoot) && !changed {
				changed = true
				if reject {
					mustNil(t, errors.Join(os.Rename(fixture.request.StateRoot, moved), os.Mkdir(fixture.request.StateRoot, 0o700)))
				} else {
					mustNil(t, os.Mkdir(filepath.Join(fixture.request.StateRoot, "peer"), 0o700))
				}
			}
			return original(fd, name, flags, mode)
		}
		if _, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops); !changed || reject != (err != nil) {
			t.Fatalf("state-root change: reject=%t changed=%t error=%v", reject, changed, err)
		}
		if reject {
			for _, root := range []string{fixture.request.StateRoot, moved} {
				workspaceMustNotExist(t, "state-root swap target state", filepath.Join(root, "targets"))
			}
		}
	}
	fixture := newWorkspacePublishFixture(t)
	fixture.request.StateRoot = filepath.Join(t.TempDir(), "missing")
	if _, err := fixture.target.materializeWorkspaceWithOps(fixture.request, workspaceTestMaterializeOps()); err == nil {
		t.Fatal("missing state root succeeded")
	}
	workspaceMustNotExist(t, "missing state root", fixture.request.StateRoot)
	fixture = newWorkspacePublishFixture(t)
	originalState, replacementState, link := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "state")
	mustNil(t, os.Symlink(originalState, link))
	fixture.request.StateRoot = link
	ops, swapped := workspaceTestMaterializeOps(), false
	original := ops.workspace.mkdirat
	ops.workspace.mkdirat = func(fd int, name string, mode uint32) error {
		if name == "targets" && !swapped {
			swapped = true
			mustNil(t, errors.Join(os.Remove(link), os.Symlink(replacementState, link)))
		}
		return original(fd, name, mode)
	}
	if _, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops); err == nil || !swapped {
		t.Fatalf("visible StateRoot swap: reached=%t error=%v", swapped, err)
	}
	if names, err := os.ReadDir(replacementState); err != nil || len(names) != 0 {
		t.Fatalf("visible StateRoot replacement changed: names=%v error=%v", names, err)
	}
}
func TestWorkspacePublicationExistingFinalUntouched(t *testing.T) {
	for _, kind := range []string{"directory", "file", "symlink"} {
		fixture := newWorkspacePublishFixture(t)
		mustNil(t, os.MkdirAll(fixture.finalParent, 0o755))
		mustNil(t, map[string]func() error{
			"directory": func() error { return os.Mkdir(fixture.final, 0o700) },
			"file":      func() error { return os.WriteFile(fixture.final, []byte("keep"), 0o600) },
			"symlink":   func() error { return os.Symlink("sentinel-target", fixture.final) },
		}[kind]())
		before, err := os.Lstat(fixture.final)
		mustNil(t, err)
		_, err = fixture.target.materializeWorkspaceWithOps(fixture.request, workspaceTestMaterializeOps())
		after, statErr := os.Lstat(fixture.final)
		if !errors.Is(err, unix.EEXIST) || statErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			t.Fatalf("existing %s changed: before=%v after=%v materialize=%v stat=%v", kind, before, after, err, statErr)
		}
		workspaceMustNotExist(t, "existing-final staging parent", fixture.stageParent)
	}
}
func TestWorkspaceNativePublishNeverReplaces(t *testing.T) {
	if !workspaceMaterializationSupported {
		t.Skip("native workspace publication is unsupported")
	}
	parent := t.TempDir()
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	mustNil(t, err)
	defer unix.Close(fd)
	mustNil(t, errors.Join(os.WriteFile(filepath.Join(parent, "stage"), []byte("stage"), 0o600), os.WriteFile(filepath.Join(parent, "final"), []byte("final"), 0o600)))
	if err := workspacePublish(fd, "stage", fd, "final"); !errors.Is(err, unix.EEXIST) {
		t.Fatalf("native no-replace returned %v, want EEXIST", err)
	}
	stage, stageErr := os.ReadFile(filepath.Join(parent, "stage"))
	final, finalErr := os.ReadFile(filepath.Join(parent, "final"))
	if stageErr != nil || finalErr != nil || string(stage) != "stage" || string(final) != "final" {
		t.Fatalf("files after exclusive rename: stage=%q/%v final=%q/%v", stage, stageErr, final, finalErr)
	}
}
func TestWorkspacePublicationDurability(t *testing.T) {
	for _, fail := range []string{"", "nested-file", "nested", "workspace"} {
		fixture, ops, events := newWorkspacePublishFixture(t), workspaceTestMaterializeOps(), []string{}
		nested := filepath.Join(fixture.request.SourceWorkspace, "nested")
		mustNil(t, errors.Join(os.Remove(filepath.Join(fixture.request.SourceWorkspace, "file")), os.Mkdir(nested, 0o700), os.WriteFile(filepath.Join(nested, "file"), []byte("data"), 0o600), os.Symlink("file", filepath.Join(nested, "link"))))
		stage := filepath.Join(fixture.stageParent, workspaceTestStage)
		workspace := filepath.Join(stage, fixture.target.Contract.WorkspaceMaterialization.WorkspaceDir)
		paths := []struct{ label, path string }{{"nested-file", filepath.Join(workspace, "nested", "file")}, {"link", filepath.Join(workspace, "nested", "link")}, {"nested", filepath.Join(workspace, "nested")}, {"workspace", workspace}, {"manifest", filepath.Join(stage, fixture.target.Contract.WorkspaceMaterialization.ManifestName)}, {"stage", stage}, {"staging", fixture.stageParent}, {"final", fixture.finalParent}}
		ready, sync, publish, chmod, symlink := map[string]bool{}, ops.workspace.fsync, ops.publish, ops.workspace.fchmod, ops.workspace.symlinkat
		ops.workspace.fchmod = func(fd int, mode uint32) error {
			ready["nested-file"] = ready["nested-file"] || workspacePathMatchesFD(paths[0].path, fd)
			ready["nested"] = ready["nested"] || workspacePathMatchesFD(paths[2].path, fd)
			ready["workspace"] = ready["workspace"] || workspacePathMatchesFD(paths[3].path, fd)
			return chmod(fd, mode)
		}
		ops.workspace.symlinkat = func(target string, fd int, name string) error {
			err := symlink(target, fd, name)
			ready["link"] = ready["link"] || err == nil
			return err
		}
		ops.workspace.fsync = func(fd int) error {
			for _, item := range paths {
				if workspacePathMatchesFD(item.path, fd) {
					if item.label == "nested" && !ready["link"] || (item.label == "nested-file" || item.label == "nested" || item.label == "workspace") && !ready[item.label] {
						t.Fatalf("%s synced before its required mode or symlink update", item.label)
					}
					events = append(events, item.label)
					if item.label == fail {
						return unix.EIO
					}
					break
				}
			}
			return sync(fd)
		}
		ops.publish = func(fromFD int, from string, toFD int, to string) error {
			events = append(events, "publish")
			return publish(fromFD, from, toFD, to)
		}
		_, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops)
		got, want := strings.Join(events, ","), "nested-file,nested,workspace,manifest,stage,staging,final,publish,staging,final"
		if fail == "" && (err != nil || got != want) {
			t.Fatalf("durability order: got=%q want=%q error=%v", got, want, err)
		}
		stageInfo, stageErr := os.Stat(stage)
		_, manifestErr := os.Lstat(filepath.Join(stage, fixture.target.Contract.WorkspaceMaterialization.ManifestName))
		_, finalErr := os.Lstat(fixture.final)
		if fail != "" && (err == nil || !strings.Contains(err.Error(), "sync workspace") || !strings.Contains(err.Error(), "pathname cleanup") || !strings.HasSuffix(got, fail) || strings.Contains(got, "publish") || !os.IsNotExist(manifestErr) || !os.IsNotExist(finalErr) || stageErr != nil || stageInfo.Mode().Perm() != 0o700) {
			t.Fatalf("%s durability failure: events=%q stage=%v/%v manifest=%v final=%v error=%v", fail, got, stageInfo, stageErr, manifestErr, finalErr, err)
		}
	}
}
func TestWorkspacePublicationFailureRetainsPrivateStage(t *testing.T) {
	for _, kind := range []string{"post-mkdir", "manifest-limit", "short-write", "manifest-validation", "publish-race", "source-change"} {
		fixture := newWorkspacePublishFixture(t)
		ops, reached := workspaceTestMaterializeOps(), false
		switch kind {
		case "post-mkdir":
			original := ops.workspace.fstatat
			ops.workspace.fstatat = func(fd int, name string, stat *unix.Stat_t, flags int) error {
				if name == workspaceTestStage && !reached {
					reached = true
					return unix.EIO
				}
				return original(fd, name, stat, flags)
			}
		case "manifest-limit":
			ops.manifest = 1
			reached = true
		case "short-write":
			original := ops.write
			ops.write = func(fd int, data []byte) (int, error) {
				reached = true
				n, err := original(fd, data)
				return n - 1, err
			}
		case "manifest-validation":
			original := ops.write
			ops.write = func(fd int, data []byte) (int, error) {
				n, err := original(fd, data)
				if err == nil {
					reached = true
					_, err = unix.Pwrite(fd, []byte("X"), 0)
				}
				return n, err
			}
		case "publish-race":
			original := ops.publish
			ops.publish = func(fromFD int, from string, toFD int, to string) error {
				reached = true
				mustNil(t, unix.Mkdirat(toFD, to, 0o700))
				return original(fromFD, from, toFD, to)
			}
		case "source-change":
			original := ops.workspace.fsync
			ops.workspace.fsync = func(fd int) error {
				err := original(fd)
				stage := filepath.Join(fixture.stageParent, workspaceTestStage)
				if err == nil && !reached && workspacePathMatchesFD(stage, fd) {
					reached = true
					mustNil(t, os.WriteFile(filepath.Join(fixture.request.SourceWorkspace, "late"), []byte("x"), 0o600))
				}
				return err
			}
		}
		_, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops)
		if err == nil || !reached || !strings.Contains(err.Error(), "pathname cleanup") {
			t.Fatalf("%s failure: reached=%t error=%v", kind, reached, err)
		}
		if kind == "publish-race" {
			if info, statErr := os.Stat(fixture.final); statErr != nil || !info.IsDir() {
				t.Fatalf("publish-race final changed: info=%v error=%v", info, statErr)
			}
		} else if _, statErr := os.Lstat(fixture.final); !os.IsNotExist(statErr) {
			t.Fatalf("%s published a final: %v", kind, statErr)
		}
		stage := filepath.Join(fixture.stageParent, workspaceTestStage)
		if info, statErr := os.Stat(stage); statErr != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s retained stage: info=%v error=%v", kind, info, statErr)
		}
	}
}
func TestWorkspacePublicationRetriesStageCollision(t *testing.T) {
	fixture := newWorkspacePublishFixture(t)
	mustNil(t, os.MkdirAll(fixture.stageParent, 0o700))
	mustNil(t, os.Mkdir(filepath.Join(fixture.stageParent, workspaceTestStage), 0o700))
	ops := systemWorkspaceMaterializeOps()
	ops.random = bytes.NewReader(append(bytes.Repeat([]byte{0x11}, 16), bytes.Repeat([]byte{0x22}, 16)...)).Read
	if _, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops); err != nil {
		t.Fatalf("stage collision retry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.stageParent, workspaceTestStage)); err != nil {
		t.Fatalf("colliding private stage changed: %v", err)
	}
}
func TestWorkspacePublicationRejectsDescriptorRaces(t *testing.T) {
	for _, kind := range []string{"state-root", "state-policy", "managed-parent", "parent-final-pre", "parent-stage-pre", "parent-final-post", "parent-stage-post", "stage-name", "final-name", "final-late"} {
		fixture := newWorkspacePublishFixture(t)
		ops, reached, publishCalled := workspaceTestMaterializeOps(), false, false
		publish := ops.publish
		ops.publish = func(fromFD int, from string, toFD int, to string) error {
			publishCalled = true
			return publish(fromFD, from, toFD, to)
		}
		switch kind {
		case "state-root":
			moved, original := fixture.request.StateRoot+".moved", ops.workspace.mkdirat
			ops.workspace.mkdirat = func(fd int, name string, mode uint32) error {
				if name == "targets" && !reached {
					reached = true
					mustNil(t, errors.Join(os.Rename(fixture.request.StateRoot, moved), os.Mkdir(fixture.request.StateRoot, 0o700)))
				}
				return original(fd, name, mode)
			}
		case "state-policy", "managed-parent", "parent-final-pre", "parent-stage-pre":
			original := ops.workspace.fsync
			ops.workspace.fsync = func(fd int) error {
				err := original(fd)
				stage := filepath.Join(fixture.stageParent, workspaceTestStage)
				if err == nil && !reached && workspacePathMatchesFD(stage, fd) {
					reached = true
					if kind == "state-policy" {
						mustNil(t, os.Chmod(fixture.request.StateRoot, 0o777))
						return err
					}
					if strings.HasPrefix(kind, "parent-") {
						parent := map[bool]string{true: fixture.stageParent, false: fixture.finalParent}[strings.Contains(kind, "stage")]
						mustNil(t, os.Chmod(parent, 0o777))
						return err
					}
					mustNil(t, errors.Join(os.Rename(fixture.finalParent, fixture.finalParent+".moved"), os.Mkdir(fixture.finalParent, 0o700)))
				}
				return err
			}
		case "stage-name":
			original := ops.workspace.fchmod
			ops.workspace.fchmod = func(fd int, mode uint32) error {
				err := original(fd, mode)
				stage := filepath.Join(fixture.stageParent, workspaceTestStage)
				if err == nil && mode == 0o755 && !reached && workspacePathMatchesFD(stage, fd) {
					reached = true
					mustNil(t, errors.Join(os.Rename(stage, stage+".moved"), os.Mkdir(stage, 0o700)))
				}
				return err
			}
		case "final-name", "parent-final-post", "parent-stage-post":
			original := ops.publish
			ops.publish = func(fromFD int, from string, toFD int, to string) error {
				if err := original(fromFD, from, toFD, to); err != nil {
					return err
				}
				reached = true
				if kind == "final-name" {
					mustNil(t, errors.Join(os.Rename(fixture.final, fixture.final+".moved"), os.Mkdir(fixture.final, 0o700)))
				} else {
					mustNil(t, os.Chmod(map[bool]string{true: fixture.stageParent, false: fixture.finalParent}[strings.Contains(kind, "stage")], 0o777))
				}
				return nil
			}
		case "final-late":
			originalPublish, originalSync, renamed := ops.publish, ops.workspace.fsync, false
			ops.publish = func(fromFD int, from string, toFD int, to string) error {
				if err := originalPublish(fromFD, from, toFD, to); err != nil {
					return err
				}
				renamed = true
				return io.ErrUnexpectedEOF
			}
			ops.workspace.fsync = func(fd int) error {
				if renamed && !reached {
					reached = true
					mustNil(t, errors.Join(os.Rename(fixture.final, fixture.final+".moved"), os.Mkdir(fixture.final, 0o700)))
				}
				return originalSync(fd)
			}
		}
		_, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops)
		if err == nil || !reached {
			t.Fatalf("%s race: reached=%t error=%v", kind, reached, err)
		}
		if wantPublish := strings.HasPrefix(kind, "final-") || strings.HasSuffix(kind, "-post"); publishCalled != wantPublish {
			t.Fatalf("%s race publication=%t, want %t", kind, publishCalled, wantPublish)
		}
		if kind == "stage-name" {
			if info, statErr := os.Stat(filepath.Join(fixture.stageParent, workspaceTestStage+".moved")); statErr != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("owned swapped stage mode: info=%v error=%v", info, statErr)
			}
		}
		if strings.HasPrefix(kind, "final-") {
			wantError := map[bool]string{true: "final binding changed", false: "ambiguous"}[kind == "final-late"]
			wantMode := map[bool]os.FileMode{true: 0o755, false: 0o700}[kind == "final-late"]
			finalInfo, finalErr := os.Stat(fixture.final)
			movedInfo, movedErr := os.Stat(fixture.final + ".moved")
			if !strings.Contains(err.Error(), wantError) || finalErr != nil || movedErr != nil || finalInfo.Mode().Perm() != 0o700 || movedInfo.Mode().Perm() != wantMode {
				t.Fatalf("post-publish state: final=%v/%v moved=%v/%v error=%v", finalInfo, finalErr, movedInfo, movedErr, err)
			}
		}
	}
}
func TestWorkspacePublicationCopiesFromPinnedSource(t *testing.T) {
	fixture := newWorkspacePublishFixture(t)
	real, decoy, link := fixture.request.SourceWorkspace, t.TempDir(), filepath.Join(t.TempDir(), "source")
	mustNil(t, os.WriteFile(filepath.Join(decoy, "file"), []byte("decoy"), 0o600))
	mustNil(t, os.Symlink(real, link))
	fixture.request.SourceWorkspace = link
	ops, swapped := workspaceTestMaterializeOps(), false
	original := ops.workspace.mkdirat
	ops.workspace.mkdirat = func(fd int, name string, mode uint32) error {
		if name == "targets" && !swapped {
			swapped = true
			mustNil(t, errors.Join(os.Remove(link), os.Symlink(decoy, link)))
		}
		return original(fd, name, mode)
	}
	result, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops)
	mustNil(t, err)
	data, err := os.ReadFile(filepath.Join(result.MaterializedWorkspace, "file"))
	if err != nil || string(data) != "original" || !swapped {
		t.Fatalf("pinned source result: data=%q swapped=%t error=%v", data, swapped, err)
	}
}
func TestWorkspacePublicationManagedOpenDoesNotFollowSwap(t *testing.T) {
	fixture := newWorkspacePublishFixture(t)
	out, originalTargets := t.TempDir(), filepath.Join(fixture.request.StateRoot, "targets")
	mustNil(t, os.Mkdir(originalTargets, 0o700))
	ops, reached, flagsOK := workspaceTestMaterializeOps(), false, false
	original := ops.workspace.openat
	ops.workspace.openat = func(fd int, name string, flags int, mode uint32) (int, error) {
		if name == "targets" && !reached {
			reached, flagsOK = true, flags&unix.O_NOFOLLOW != 0
			mustNil(t, errors.Join(os.Rename(originalTargets, originalTargets+".moved"), os.Symlink(out, originalTargets)))
		}
		return original(fd, name, flags, mode)
	}
	if _, err := fixture.target.materializeWorkspaceWithOps(fixture.request, ops); err == nil || !reached || !flagsOK {
		t.Fatalf("managed open swap: reached=%t flagsOK=%t error=%v", reached, flagsOK, err)
	}
	if names, err := os.ReadDir(out); err != nil || len(names) != 0 {
		t.Fatalf("managed open followed replacement: names=%v error=%v", names, err)
	}
}
func TestWorkspacePublicationConcurrentCreateOnce(t *testing.T) {
	fixture := newWorkspacePublishFixture(t)
	start, errs := make(chan struct{}), make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := fixture.target.MaterializeWorkspace(context.Background(), fixture.request)
			errs <- err
		}()
	}
	close(start)
	first, second := <-errs, <-errs
	if !(first == nil && errors.Is(second, unix.EEXIST) || second == nil && errors.Is(first, unix.EEXIST)) {
		t.Fatalf("concurrent results: first=%v second=%v", first, second)
	}
}
func TestWorkspaceManifestLimitExactAndOver(t *testing.T) {
	for _, delta := range []int64{0, -1} {
		fixture := newWorkspacePublishFixture(t)
		mustNil(t, os.Remove(filepath.Join(fixture.request.SourceWorkspace, "file")))
		manifest := WorkspaceManifest{Version: 1, TargetKind: fixture.target.Contract.TargetKind, TargetProvider: fixture.target.Contract.TargetProvider, TargetID: "tid", WorkspaceTransport: fixture.target.Contract.WorkspaceTransport, SourceWorkspace: fixture.request.SourceWorkspace, MaterializationID: "mid", MaterializedWorkspace: filepath.Join(fixture.final, fixture.target.Contract.WorkspaceMaterialization.WorkspaceDir), ExcludedPaths: append([]string(nil), fixture.target.Contract.WorkspaceMaterialization.ExcludedPaths...), Entries: []WorkspaceEntry{}}
		content, err := json.Marshal(manifest)
		mustNil(t, err)
		ops := workspaceTestMaterializeOps()
		ops.manifest = int64(len(content)+1) + delta
		_, err = fixture.target.materializeWorkspaceWithOps(fixture.request, ops)
		if (delta == 0) != (err == nil) {
			t.Fatalf("manifest limit delta %d: %v", delta, err)
		}
	}
}
