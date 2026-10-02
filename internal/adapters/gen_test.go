// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// TestGeneratedFilesMatchGolden pins the committed generated files as the
// golden output of the renderers over the repository manifests.
func TestGeneratedFilesMatchGolden(t *testing.T) {
	manifests := loadRepoManifests(t)
	for golden, render := range map[string]func([]Manifest) ([]byte, error){
		"internal/adapters/data_gen.go":              RenderDataGo,
		"internal/providerid/providerid_gen.go":      RenderProviderIDGo,
		"scripts/lib/launcher/generated-adapters.sh": RenderLauncherShell,
		"runtime/container/generated-adapters.sh":    RenderRuntimeShell,
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
	manifests[claude].EgressEndpoints = []string{"changed.example:443"}

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
	launcher, err := RenderLauncherShell(manifests)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"    claude | codex | copilot | gemini | zz-bare) ;;\n",
		"    claude)\n      echo \"changed.example:443\"\n",
		"    zz-bare)\n      echo \"\"\n",
	} {
		if !strings.Contains(string(launcher), want) {
			t.Errorf("launcher shell render lacks %q:\n%s", want, launcher)
		}
	}
	runtime, err := RenderRuntimeShell(manifests)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(launcher)+string(runtime), "zz-planned") || !strings.Contains(string(runtime), "| zz-bare) ;;") {
		t.Error("shell renders must skip planned manifests and keep the others")
	}
}

// TestGeneratedShellPerAgent sources each committed generated shell file and
// asks it about every manifest id, so the golden bytes also act right in bash.
func TestGeneratedShellPerAgent(t *testing.T) {
	manifests := loadRepoManifests(t)
	var keys []string
	for _, m := range manifests {
		for _, c := range m.Credentials {
			keys = append(keys, c.Key)
		}
	}
	run := func(script, fn, arg string) (string, int) {
		cmd := exec.Command("bash", "--noprofile", "--norc", "-c", `source "$1" && "$2" "$3"`, "bash", script, fn, arg)
		cmd.Dir = repoRoot
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		out, err := cmd.Output()
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}
	for _, script := range []string{"scripts/lib/launcher/generated-adapters.sh", "runtime/container/generated-adapters.sh"} {
		for _, id := range append(manifestIDs(manifests, func(Manifest) bool { return true }), "no-such-agent", "", "claude | codex", "*") {
			want := slices.ContainsFunc(manifests, func(m Manifest) bool { return m.ID == id && m.Tier != "planned" })
			if _, code := run(script, "workcell_supported_agent", id); (code == 0) != want {
				t.Errorf("%s: workcell_supported_agent %q exit %d, want supported=%v", script, id, code, want)
			}
		}
	}
	if out, code := run("scripts/lib/launcher/generated-adapters.sh", "workcell_adapter_credential_keys", ""); code != 0 || out != strings.Join(keys, ",")+"\n" {
		t.Errorf("workcell_adapter_credential_keys = %q (exit %d), want %q", out, code, strings.Join(keys, ","))
	}
}
