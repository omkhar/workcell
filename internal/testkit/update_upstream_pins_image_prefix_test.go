// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestUpdateUpstreamPinsRefusesImageOutsideItsRepository proves the updater
// checks each image pin it would write before it writes any file. A base image
// whose track names another registry, or a digest that is not sha256 hex, stops
// the refresh with the tree unchanged.
func TestUpdateUpstreamPinsRefusesImageOutsideItsRepository(t *testing.T) {
	t.Parallel()

	pins := readUpdaterFixturePins(t)
	plan := updaterTargetDebianPlan()
	toolsRoot := t.TempDir()
	citoolsPath := buildUpdaterFixtureCITools(t, toolsRoot)
	goWrapperPath := writeUpdaterFixtureGoWrapper(t, toolsRoot)
	for _, tc := range []struct {
		name, want string
		evilTrack  bool
		env        []string
	}{
		{name: "runtime base on another registry", evilTrack: true, want: "runtime base image must be node:<tag>@sha256:<digest>, found ghcr.io/evil/"},
		{name: "BuildKit digest is not sha256 hex", env: []string{"WORKCELL_FIXTURE_BUILDKIT_DIGEST=" + strings.Repeat("A", 64)}, want: "BuildKit image must be moby/buildkit:<tag>@sha256:<digest>"},
		{name: "QEMU digest is too short", env: []string{"WORKCELL_FIXTURE_QEMU_DIGEST=abc"}, want: "QEMU binfmt image must be tonistiigi/binfmt:<tag>@sha256:<digest>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := writeUpdaterFixture(t, updaterDriftedManifest(t, plan.Snapshot), 0o640)
			options := defaultUpdaterFixtureOptions()
			options.ExtraArgs = []string{"--toolchain-only"}
			options.ExtraEnv = tc.env
			if tc.evilTrack {
				dockerfile := filepath.Join(root, "runtime", "container", "Dockerfile")
				data, err := os.ReadFile(dockerfile)
				if err != nil {
					t.Fatal(err)
				}
				track, _ := splitUpdaterFixtureImage(t, pins.RuntimeBase)
				edited := strings.Replace(string(data), "ARG NODE_BASE_IMAGE="+track, "ARG NODE_BASE_IMAGE=ghcr.io/evil/"+track, 1)
				if edited == string(data) {
					t.Fatal("fixture has no NODE_BASE_IMAGE line to move")
				}
				writeUpdaterFixtureFile(t, root, "runtime/container/Dockerfile", edited, 0o644)
				options.ExtraEnv = append(options.ExtraEnv, "WORKCELL_FIXTURE_RUNTIME_BASE_TRACK=ghcr.io/evil/"+track)
			}
			before := snapshotUpdaterFixtureFiles(t, root)
			run := runUpdaterFixtureWithOptions(t, root, t.TempDir(), citoolsPath, goWrapperPath, pins, plan, "--apply", options)
			if run.Code != 1 || !strings.Contains(run.Output, tc.want) {
				t.Fatalf("--apply exit code = %d, output = %q, want 1 and %q", run.Code, run.Output, tc.want)
			}
			if !reflect.DeepEqual(snapshotUpdaterFixtureFiles(t, root), before) {
				t.Fatal("the updater wrote a file before it refused the image pin")
			}
		})
	}
}
