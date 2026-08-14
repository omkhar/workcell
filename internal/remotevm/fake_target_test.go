// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/pathutil"
	"golang.org/x/sys/unix"
)

type failingWorkspaceReservation struct {
	calls []string
	err   error
}

func (r *failingWorkspaceReservation) reserve(path string) error {
	r.calls = append(r.calls, path)
	return r.err
}

func TestFakeTargetMaterializeWorkspaceCopiesFixtureAndExcludesDotGit(t *testing.T) {
	t.Parallel()

	contract := DefaultContract()
	target, err := NewFakeTarget(contract)
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join("testdata", "source-workspace")
	tempWorkspace := t.TempDir()
	if _, err := copyWorkspaceTree(sourceRoot, tempWorkspace, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tempWorkspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tempWorkspace, ".git", "config"), []byte("[core]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         t.TempDir(),
		TargetID:          "fake-remote-target",
		MaterializationID: "fixture-materialization",
		SourceWorkspace:   tempWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(result.MaterializedWorkspace, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git unexpectedly materialized: %v", err)
	}
	if got, want := len(result.Manifest.Entries), 3; got != want {
		t.Fatalf("len(result.Manifest.Entries) = %d, want %d", got, want)
	}
}

func TestCopyWorkspaceTreeRejectsUnicodeDestinationCollision(t *testing.T) {
	for _, pair := range [][2]string{{"café", "cafe\u0301"}, {"straße", "STRASSE"}, {"Σ", "ς"}, {"ﬀ", "ff"}, {"µ", "Μ"}, {"ś", "ſ\u0301"}} {
		state := newWorkspaceDestinationState()
		if err := state.reserve(pair[0]); err != nil {
			t.Fatal(err)
		}
		if err := state.reserve(pair[1]); err == nil {
			t.Fatalf("Unicode collision accepted: %q and %q", pair[0], pair[1])
		}
	}
}

func TestRemoteWorkspaceExclusionsUseUnicodePathIdentity(t *testing.T) {
	for _, pair := range [][2]string{{".git", ".GIT"}, {"café", "cafe\u0301"}, {"straße", "STRASSE"}, {"Σ", "ς"}, {"ﬀ", "ff"}, {"µ", "Μ"}, {"ś", "ſ\u0301"}} {
		excluded, err := isExcludedPath(filepath.Join(pair[1], "child"), []string{pair[0]})
		if err != nil || !excluded {
			t.Fatalf("excluded %q by %q = %v, %v", pair[1], pair[0], excluded, err)
		}
		excluded, err = isExcludedPath(pair[1]+"-sibling", []string{pair[0]})
		if err != nil || excluded {
			t.Fatalf("sibling %q excluded by %q = %v, %v", pair[1], pair[0], excluded, err)
		}
	}
}

func TestRemoteWorkspaceIdentityRejectsInvalidUTF8(t *testing.T) {
	invalid := "secret-prefix-" + string([]byte{0xff})
	if _, err := isExcludedPath(invalid, nil); !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("exclusion error = %v", err)
	}
	if err := newWorkspaceDestinationState().reserve(invalid); !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("reservation error = %v", err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, invalid), []byte("approved"), 0o600); err != nil {
		return
	}
	destination := t.TempDir()
	if _, err := copyWorkspaceTree(source, destination, nil); !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("copy error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, invalid)); !os.IsNotExist(err) {
		t.Fatalf("destination was written: %v", err)
	}
	stateRoot := t.TempDir()
	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.MaterializeWorkspace(context.Background(), MaterializeRequest{StateRoot: stateRoot, TargetID: "target", MaterializationID: "invalid", SourceWorkspace: source})
	if !errors.Is(err, pathutil.ErrInvalidUTF8Path) || strings.Contains(err.Error(), "secret-prefix") {
		t.Fatalf("materialize error = %v", err)
	}
	manifest := filepath.Join(targetRoot(stateRoot, CanonicalProvider, "target"), "materializations", "invalid", MaterializationFile)
	if _, err := os.Lstat(manifest); !os.IsNotExist(err) {
		t.Fatalf("manifest exists after invalid input: %v", err)
	}
}

func TestCopyWorkspaceTreeRejectsUnsafeDescendantBeforeReadOnlyDestination(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value string
	}{
		{name: "newline", value: "secret-prefix-\n"},
		{name: "escape", value: "secret-prefix-\x1b"},
		{name: "line separator", value: "secret-prefix-\u2028"},
		{name: "paragraph separator", value: "secret-prefix-\u2029"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, testCase.value), []byte("approved"), 0o600); err != nil {
				t.Fatal(err)
			}
			destination := t.TempDir()
			if err := os.Chmod(destination, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(destination, 0o700) })
			_, err := copyWorkspaceTree(source, destination, nil)
			if !errors.Is(err, pathutil.ErrUnsafePathControl) || strings.Contains(err.Error(), "secret-prefix") {
				t.Fatalf("unsafe descendant error = %v", err)
			}
		})
	}
}

func TestRemoteWorkspaceExclusionRejectsUnsafeControls(t *testing.T) {
	for _, path := range []string{"secret-prefix-\n", "secret-prefix-\x1b", "secret-prefix-\u2028", "secret-prefix-\u2029"} {
		if _, err := isExcludedPath(path, nil); !errors.Is(err, pathutil.ErrUnsafePathControl) || strings.Contains(err.Error(), "secret-prefix") {
			t.Fatalf("unsafe exclusion error = %v", err)
		}
	}
}

func TestRemoteWorkspaceDiagnosticsQuoteControlCharacters(t *testing.T) {
	source := t.TempDir()
	target := "/tmp/target\n\x1b"
	if err := os.Symlink(target, filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := copyWorkspaceTree(source, t.TempDir(), nil)
	if err == nil {
		t.Fatal("absolute control-character symlink was accepted")
	}
	assertQuotedWorkspaceDiagnostic(t, err.Error())
}

func assertQuotedWorkspaceDiagnostic(t *testing.T, message string) {
	t.Helper()
	if strings.Contains(message, "\n") || strings.Contains(message, "\x1b") {
		t.Fatalf("diagnostic contains a raw control character: %q", message)
	}
	if !strings.Contains(message, `\n`) || !strings.Contains(message, `\x1b`) {
		t.Fatalf("diagnostic does not quote control characters: %q", message)
	}
}

func TestCopyWorkspaceTreeReservesBeforeWrite(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "first"), []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "destination")
	reservation := &failingWorkspaceReservation{err: errors.New("reserve failed")}
	if _, err := copyWorkspaceTreeWithReservation(source, destination, nil, reservation); !errors.Is(err, reservation.err) {
		t.Fatalf("copy error = %v", err)
	}
	if len(reservation.calls) != 1 || reservation.calls[0] != "first" {
		t.Fatalf("reservation calls = %#v", reservation.calls)
	}
	if _, err := os.Stat(filepath.Join(destination, "first")); !os.IsNotExist(err) {
		t.Fatalf("destination was written before reservation: %v", err)
	}
}

func TestCopyWorkspaceTreePreservesFirstContentAfterReservation(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "first"), []byte("approved"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	entries, err := copyWorkspaceTree(source, destination, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "first"))
	if err != nil || string(data) != "approved" || len(entries) != 1 || entries[0].SHA256 == "" {
		t.Fatalf("copy result = %q, %#v, %v", data, entries, err)
	}
}

func TestFakeTargetDoesNotWriteManifestAfterWorkspaceFailure(t *testing.T) {
	source := t.TempDir()
	if err := unix.Mkfifo(filepath.Join(source, "pipe"), 0o600); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}
	state := t.TempDir()
	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.MaterializeWorkspace(context.Background(), MaterializeRequest{StateRoot: state, TargetID: "target", MaterializationID: "failure", SourceWorkspace: source})
	if err == nil {
		t.Fatal("unsafe workspace entry was accepted")
	}
	manifest := filepath.Join(targetRoot(state, CanonicalProvider, "target"), "materializations", "failure", MaterializationFile)
	if _, err := os.Lstat(manifest); !os.IsNotExist(err) {
		t.Fatalf("manifest exists after failure: %v", err)
	}
}

func TestFakeTargetUsesProviderSpecificStateRoots(t *testing.T) {
	t.Parallel()

	target, err := NewAWSEC2SSMTarget()
	if err != nil {
		t.Fatal(err)
	}
	result, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         t.TempDir(),
		TargetID:          "i-1234567890abcdef0",
		MaterializationID: "fixture-materialization",
		SourceWorkspace:   filepath.Join("testdata", "source-workspace"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(filepath.Dir(result.TargetRoot)); got != AWSEC2SSMProvider {
		t.Fatalf("provider state root = %q, want %q", got, AWSEC2SSMProvider)
	}
	if got := filepath.Base(result.TargetRoot); got != "i-1234567890abcdef0" {
		t.Fatalf("target root leaf = %q, want %q", got, "i-1234567890abcdef0")
	}
}

func TestFakeTargetRejectsPathTraversalIdentifiers(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join("testdata", "source-workspace")
	for _, tc := range []struct {
		name string
		req  MaterializeRequest
		want string
	}{
		{
			name: "target",
			req: MaterializeRequest{
				StateRoot:         t.TempDir(),
				TargetID:          "../escape",
				MaterializationID: "fixture-materialization",
				SourceWorkspace:   sourceRoot,
			},
			want: "target id must be a single path segment",
		},
		{
			name: "materialization",
			req: MaterializeRequest{
				StateRoot:         t.TempDir(),
				TargetID:          "fake-remote-target",
				MaterializationID: "../escape",
				SourceWorkspace:   sourceRoot,
			},
			want: "materialization id must be a single path segment",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := target.MaterializeWorkspace(context.Background(), tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("MaterializeWorkspace() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFakeTargetRejectsPathTraversalProvider(t *testing.T) {
	t.Parallel()

	contract := DefaultContractForProvider("../provider")
	target, err := NewFakeTarget(contract)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         t.TempDir(),
		TargetID:          "fake-remote-target",
		MaterializationID: "fixture-materialization",
		SourceWorkspace:   filepath.Join("testdata", "source-workspace"),
	})
	if err == nil || !strings.Contains(err.Error(), "target provider must be a single path segment") {
		t.Fatalf("MaterializeWorkspace() error = %v, want target provider rejection", err)
	}
}

func TestFakeTargetRejectsPathTraversalSessionID(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	stateRoot := t.TempDir()
	materialized, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         stateRoot,
		TargetID:          "fake-remote-target",
		MaterializationID: "fixture-materialization",
		SourceWorkspace:   filepath.Join("testdata", "source-workspace"),
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrapped, err := target.BootstrapTarget(context.Background(), BootstrapRequest{
		StateRoot:   stateRoot,
		TargetID:    "fake-remote-target",
		BootstrapID: "fixture-bootstrap",
		ImageRef:    "workcell:local",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = target.StartSession(context.Background(), StartSessionRequest{
		SessionID:       "../escape",
		Agent:           "codex",
		Mode:            "strict",
		StartedAt:       "2026-04-24T00:00:00Z",
		Materialization: materialized,
		Bootstrap:       bootstrapped,
	})
	if err == nil || !strings.Contains(err.Error(), "session id must be a single path segment") {
		t.Fatalf("StartSession() error = %v, want session id rejection", err)
	}
}

func TestFakeTargetMaterializeWorkspaceRejectsEscapingSymlinks(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}

	for name, linkTarget := range map[string]string{
		"absolute": "/etc/passwd",
		"relative": "../../outside",
	} {
		tempWorkspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(tempWorkspace, "keep.txt"), []byte("ok\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(linkTarget, filepath.Join(tempWorkspace, "escape")); err != nil {
			t.Fatal(err)
		}
		_, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
			StateRoot:         t.TempDir(),
			TargetID:          "fake-remote-target",
			MaterializationID: "symlink-escape-" + name,
			SourceWorkspace:   tempWorkspace,
		})
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("%s: expected symlink escape rejection, got %v", name, err)
		}
	}
}

func TestFakeTargetMaterializeWorkspaceKeepsInternalSymlinks(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	tempWorkspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempWorkspace, "real.txt"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(tempWorkspace, "alias")); err != nil {
		t.Fatal(err)
	}
	result, err := target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         t.TempDir(),
		TargetID:          "fake-remote-target",
		MaterializationID: "symlink-internal",
		SourceWorkspace:   tempWorkspace,
	})
	if err != nil {
		t.Fatalf("internal symlink unexpectedly rejected: %v", err)
	}
	linked, err := os.Readlink(filepath.Join(result.MaterializedWorkspace, "alias"))
	if err != nil || linked != "real.txt" {
		t.Fatalf("alias = %q, %v; want real.txt", linked, err)
	}
}

func TestFakeTargetMaterializeWorkspaceRejectsSymlinkChainEscape(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	workspace := filepath.Join(parent, "ws")
	if err := os.MkdirAll(filepath.Join(workspace, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// dir/up resolves to the workspace root (legal on its own), but a second
	// link routed through it re-routes ".." past the lexical check; the
	// kernel resolves escape to parent/secret.txt, outside the workspace.
	if err := os.Symlink("..", filepath.Join(workspace, "dir", "up")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("dir/up/../secret.txt", filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}

	_, err = target.MaterializeWorkspace(context.Background(), MaterializeRequest{
		StateRoot:         t.TempDir(),
		TargetID:          "fake-remote-target",
		MaterializationID: "symlink-chain-escape",
		SourceWorkspace:   workspace,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink chain escape rejection, got %v", err)
	}
}
