// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// TestGeneratedFilesMatchGolden pins the committed generated files as the
// golden output of the renderers over the repository manifests.
func TestGeneratedFilesMatchGolden(t *testing.T) {
	manifests := loadRepoManifests(t)
	for golden, render := range map[string]func([]Manifest) ([]byte, error){
		"internal/adapters/data_gen.go":         RenderDataGo,
		"internal/providerid/providerid_gen.go": RenderProviderIDGo,
	} {
		got, err := render(manifests)
		if err != nil {
			t.Fatal(err)
		}
		if want := readRepoFile(t, golden); !bytes.Equal(got, []byte(want)) {
			t.Errorf("%s is stale; run scripts/generate-adapters-*.sh:\n%s", golden, got)
		}
	}
}

// TestRenderersFollowManifests is the negative control for the golden test:
// a planned manifest, a manifest without credentials, and a changed path must
// each change the output in the expected way.
func TestRenderersFollowManifests(t *testing.T) {
	manifests := slices.Clone(loadRepoManifests(t))
	committed, err := RenderDataGo(manifests)
	if err != nil {
		t.Fatal(err)
	}
	manifests = append(manifests, Manifest{ID: "zz-planned", Tier: "planned"},
		Manifest{ID: "zz-bare", Tier: "uncertified", Binary: "zz"})
	claude := slices.IndexFunc(manifests, func(m Manifest) bool { return m.ID == "claude" })
	manifests[claude].Credentials = slices.Clone(manifests[claude].Credentials)
	manifests[claude].Credentials[0].ContainerPath = "/changed"

	data, err := RenderDataGo(manifests)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(data, committed) || !strings.Contains(string(data), `"/changed"`) {
		t.Error("data render ignores a changed credential path")
	}
	if strings.Contains(string(data), "zz-planned") || !strings.Contains(string(data), `id:                       "zz-bare"`) {
		t.Error("data render must skip planned manifests and keep the others")
	}
	ids, err := RenderProviderIDGo(manifests)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`var AllProviders = []string{"claude", "codex", "copilot", "gemini", "zz-bare"}`,
		`var CredentialMetadataProviders = []string{"claude", "codex", "copilot", "gemini"}`,
	} {
		if !strings.Contains(string(ids), want) {
			t.Errorf("providerid render lacks %s:\n%s", want, ids)
		}
	}
}
