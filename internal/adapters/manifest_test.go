// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/providerid"
)

const repoRoot = "../.."

func loadRepoManifests(t *testing.T) []Manifest {
	t.Helper()
	manifests, err := LoadManifests(filepath.Join(repoRoot, "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	return manifests
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func manifestIDs(manifests []Manifest, keep func(Manifest) bool) []string {
	var out []string
	for _, m := range manifests {
		if keep(m) {
			out = append(out, m.ID)
		}
	}
	return out
}

func sorted(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}

func TestManifestsMatchProviderIDLists(t *testing.T) {
	manifests := loadRepoManifests(t)

	certified := manifestIDs(manifests, func(m Manifest) bool { return m.Tier == "certified" })
	if !slices.Equal(certified, providerid.AllProviders) {
		t.Errorf("certified manifests = %v, want providerid.AllProviders %v", certified, providerid.AllProviders)
	}
	withCredentials := manifestIDs(manifests, func(m Manifest) bool { return len(m.Credentials) > 0 })
	if !slices.Equal(withCredentials, providerid.CredentialMetadataProviders) {
		t.Errorf("manifests with credentials = %v, want providerid.CredentialMetadataProviders %v", withCredentials, providerid.CredentialMetadataProviders)
	}
	planned := manifestIDs(manifests, func(m Manifest) bool { return m.Tier == "planned" })
	if !slices.Equal(planned, []string{providerid.Antigravity}) {
		t.Errorf("planned manifests = %v, want [%s]", planned, providerid.Antigravity)
	}
	documents := append([]string{providerid.CommonDocument}, manifestIDs(manifests, func(m Manifest) bool { return m.ManagedDocument })...)
	if !slices.Equal(sorted(documents), sorted(providerid.DocumentKeys)) {
		t.Errorf("managed document keys = %v, want providerid.DocumentKeys %v", documents, providerid.DocumentKeys)
	}
}

func TestManifestsMatchProviderRegistry(t *testing.T) {
	var got []providerDefinition
	for _, m := range loadRepoManifests(t) {
		if m.Tier == "planned" {
			continue
		}
		def := providerDefinition{
			id:                       m.ID,
			sharedCredentialsEnabled: m.SharedCredentials,
			tables: providerTables{
				credentialContainerPaths: map[string]string{},
				reservedTargets:          m.ReservedTargets,
			},
		}
		for _, c := range m.Credentials {
			def.tables.credentialKeys = append(def.tables.credentialKeys, c.Key)
			def.tables.credentialContainerPaths[c.Key] = c.ContainerPath
		}
		got = append(got, def)
	}
	if !reflect.DeepEqual(got, providers) {
		t.Fatalf("manifests differ from data.go providers:\n got  %+v\n want %+v", got, providers)
	}
}

func TestManifestsMatchLauncherProviderEndpoints(t *testing.T) {
	for _, m := range loadRepoManifests(t) {
		cmd := exec.Command("bash", "--noprofile", "--norc", "-c",
			`source scripts/lib/launcher/egress-endpoints.sh && provider_endpoints "$1"`, "bash", m.ID)
		cmd.Dir = repoRoot
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		out, err := cmd.Output()
		if m.Tier == "planned" {
			// An absent row is the function's own "return 1" with no output.
			// Any other failure or output is not proof of absence.
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || len(out) != 0 || len(m.EgressEndpoints) != 0 {
				t.Errorf("%s: planned adapter must have no provider_endpoints row and no egress endpoints (out %q, err %v)", m.ID, out, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: provider_endpoints: %v", m.ID, err)
		}
		if got := strings.Fields(string(out)); !slices.Equal(got, m.EgressEndpoints) {
			t.Errorf("%s: provider_endpoints = %v, manifest egress.endpoints = %v", m.ID, got, m.EgressEndpoints)
		}
	}
}

// TestManifestsMatchLauncherAgentDispatch runs the launcher instead of reading
// its source, so a decoy in a comment or heredoc cannot satisfy it.
func TestManifestsMatchLauncherAgentDispatch(t *testing.T) {
	probe := func(agent string) (string, int) {
		cmd := exec.Command("bash", "--noprofile", "--norc", "scripts/workcell", "--auth-status", "--agent", agent, "--workspace", ".")
		cmd.Dir = repoRoot
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("launcher probe for %s: %v", agent, err)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}
	const unsupported, planned = "Unsupported agent: ", "is a planned Workcell provider adapter"
	if out, code := probe("no-such-agent"); code != 2 || !strings.Contains(out, unsupported+"no-such-agent") {
		t.Fatalf("launcher probe cannot detect an unsupported agent (exit %d): %s", code, out)
	}
	for _, m := range loadRepoManifests(t) {
		out, code := probe(m.ID)
		switch m.Tier {
		case "certified":
			if code != 0 || strings.Contains(out, unsupported) || strings.Contains(out, planned) {
				t.Errorf("%s: certified manifest but the launcher rejects --agent (exit %d): %s", m.ID, code, out)
			}
		case "planned":
			if code != 2 || !strings.Contains(out, planned) {
				t.Errorf("%s: planned manifest but the launcher does not report a planned adapter (exit %d): %s", m.ID, code, out)
			}
		}
	}
}

func TestManifestsMatchRustLaunchTargets(t *testing.T) {
	source := readRepoFile(t, "runtime/container/rust/src/bin/workcell-launcher.rs")
	targets := regexp.MustCompile(`name: "([a-z-]+)",\s*script_path: "/usr/local/libexec/workcell/provider-wrapper.sh",\s*approved_invocations: &\[\s*"([^"]+)",\s*"([^"]+)",\s*\]`).
		FindAllStringSubmatch(source, -1)
	var names []string
	for _, target := range targets {
		name := target[1]
		names = append(names, name)
		if target[2] != "/usr/local/bin/"+name || target[3] != "/usr/local/libexec/workcell/core/"+name {
			t.Errorf("LaunchTarget %s invocations = %v", name, target[2:])
		}
	}
	var binaries []string
	for _, m := range loadRepoManifests(t) {
		if m.Tier == "certified" {
			binaries = append(binaries, m.Binary)
		}
	}
	if !slices.Equal(sorted(names), sorted(binaries)) {
		t.Errorf("Rust provider LaunchTargets = %v, certified manifest binaries = %v", names, binaries)
	}
}

func TestManifestInstallPinsExist(t *testing.T) {
	dockerfile := readRepoFile(t, "runtime/container/Dockerfile")
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "runtime/container/providers/package.json")), &pkg); err != nil {
		t.Fatal(err)
	}
	for _, m := range loadRepoManifests(t) {
		if m.Tier == "planned" {
			continue
		}
		if m.Install.VersionArg != "" && !regexp.MustCompile(`(?m)^ARG `+regexp.QuoteMeta(m.Install.VersionArg)+`=\S+$`).MatchString(dockerfile) {
			t.Errorf("%s: Dockerfile has no pinned ARG %s", m.ID, m.Install.VersionArg)
		}
		if m.Install.Package != "" && pkg.Dependencies[m.Install.Package] == "" {
			t.Errorf("%s: providers/package.json has no dependency %s", m.ID, m.Install.Package)
		}
		if _, err := os.Stat(filepath.Join(repoRoot, m.Install.Provenance)); err != nil {
			t.Errorf("%s: provenance script: %v", m.ID, err)
		}
	}
}

func TestParseManifestRejectsInvalidInput(t *testing.T) {
	const valid = `schema = 1
id = "demo"
tier = "certified"
binary = "demo"

[install]
method = "binary"
version_arg = "DEMO_VERSION"
provenance = "scripts/verify-demo.sh"

[credentials.demo_auth]
container_path = "/opt/demo.json"
`
	cases := map[string]string{
		"unknown key":             strings.Replace(valid, `binary = "demo"`, "binary = \"demo\"\nextra = true", 1),
		"unknown table":           valid + "\n[flags]\nmode = \"allowlist\"\n",
		"unknown install key":     strings.Replace(valid, `method = "binary"`, "method = \"binary\"\nsha = \"x\"", 1),
		"bare credentials table":  valid + "\n[credentials]\ncontainer_path = \"/x\"\n",
		"nested credentials key":  valid + "\n[credentials.a.b]\ncontainer_path = \"/x\"\n",
		"unknown credential key":  valid + "env = \"X\"\n",
		"wrong type":              strings.Replace(valid, `tier = "certified"`, "tier = 1", 1),
		"wrong list type":         valid + "\n[home]\nreserved_targets = [1]\n",
		"wrong schema":            strings.Replace(valid, "schema = 1", "schema = 2", 1),
		"missing schema":          strings.Replace(valid, "schema = 1\n", "", 1),
		"invalid id":              strings.Replace(valid, `id = "demo"`, `id = "Demo"`, 1),
		"invalid tier":            strings.Replace(valid, `"certified"`, `"trusted"`, 1),
		"missing binary":          strings.Replace(valid, "binary = \"demo\"\n", "", 1),
		"invalid method":          strings.Replace(valid, `method = "binary"`, `method = "curl"`, 1),
		"binary without arg":      strings.Replace(valid, "version_arg = \"DEMO_VERSION\"\n", "", 1),
		"npm without package":     strings.Replace(valid, `method = "binary"`, `method = "npm"`, 1),
		"missing provenance":      strings.Replace(valid, "provenance = \"scripts/verify-demo.sh\"\n", "", 1),
		"relative container path": strings.Replace(valid, `"/opt/demo.json"`, `"demo.json"`, 1),
		"subset parser rejection": valid + "\n[[install]]\n",
	}
	if _, err := parseManifest("valid.toml", []byte(valid)); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	for name, content := range cases {
		if _, err := parseManifest("case.toml", []byte(content)); err == nil {
			t.Errorf("%s: parseManifest accepted invalid input", name)
		}
	}
}

func TestLoadManifestsRejectsIDDirectoryMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "other"), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "schema = 1\nid = \"demo\"\ntier = \"planned\"\n"
	if err := os.WriteFile(filepath.Join(root, "other", "adapter.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifests(root); err == nil {
		t.Fatal("LoadManifests accepted an id that does not match its directory")
	}
}

func TestLoadManifestsFailsClosed(t *testing.T) {
	const planned = "schema = 1\nid = \"demo\"\ntier = \"planned\"\n"
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "demo", "adapter.toml"), []byte(planned), 0o600); err != nil {
		t.Fatal(err)
	}
	setup := map[string]func(root string){
		"symlinked adapter directory": func(root string) {
			os.Symlink(filepath.Join(outside, "demo"), filepath.Join(root, "demo"))
		},
		"symlinked manifest": func(root string) {
			os.Mkdir(filepath.Join(root, "demo"), 0o700)
			os.Symlink(filepath.Join(outside, "demo", "adapter.toml"), filepath.Join(root, "demo", "adapter.toml"))
		},
		"directory without manifest": func(root string) {
			os.Mkdir(filepath.Join(root, "demo"), 0o700)
		},
		"manifest is a directory": func(root string) {
			os.MkdirAll(filepath.Join(root, "demo", "adapter.toml"), 0o700)
		},
	}
	for name, prepare := range setup {
		root := t.TempDir()
		prepare(root)
		if _, err := LoadManifests(root); err == nil {
			t.Errorf("%s: LoadManifests accepted the tree", name)
		}
	}
	if _, err := LoadManifests(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("LoadManifests accepted a missing root")
	}
}
