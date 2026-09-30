// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"encoding/json"
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
			if err == nil || len(m.EgressEndpoints) != 0 {
				t.Errorf("%s: planned adapter must have no provider_endpoints row and no egress endpoints", m.ID)
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

func TestManifestsMatchLauncherAgentCase(t *testing.T) {
	manifests := loadRepoManifests(t)
	match := regexp.MustCompile(`(?m)^if \[\[ -n "\$\{AGENT\}" \]\]; then\n  case "\$\{AGENT\}" in\n    ([a-z| ]+)\) ;;\n    ([a-z]+)\)\n`).
		FindStringSubmatch(readRepoFile(t, "scripts/workcell"))
	if match == nil {
		t.Fatal("scripts/workcell --agent case block not found")
	}
	supported := strings.Fields(strings.ReplaceAll(match[1], "|", " "))
	certified := manifestIDs(manifests, func(m Manifest) bool { return m.Tier == "certified" })
	if !slices.Equal(sorted(supported), certified) {
		t.Errorf("scripts/workcell supported agents = %v, certified manifests = %v", supported, certified)
	}
	planned := manifestIDs(manifests, func(m Manifest) bool { return m.Tier == "planned" })
	if !slices.Equal([]string{match[2]}, planned) {
		t.Errorf("scripts/workcell planned agent = %s, planned manifests = %v", match[2], planned)
	}
}

func TestManifestsMatchLauncherSupportedCredentialKeys(t *testing.T) {
	match := regexp.MustCompile(`supported_credential_keys=([a-z_,]+)\\n`).
		FindStringSubmatch(readRepoFile(t, "scripts/workcell"))
	if match == nil {
		t.Fatal("scripts/workcell supported_credential_keys line not found")
	}
	want := slices.Clone(sharedCredentialKeys)
	for _, m := range loadRepoManifests(t) {
		for _, c := range m.Credentials {
			want = append(want, c.Key)
		}
	}
	if got := strings.Split(match[1], ","); !slices.Equal(sorted(got), sorted(want)) {
		t.Errorf("supported_credential_keys = %v, manifests plus shared keys = %v", got, want)
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

func TestLoadManifestRejectsInvalidInput(t *testing.T) {
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
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if _, err := LoadManifest(write("valid.toml", valid)); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	for name, content := range cases {
		if _, err := LoadManifest(write("case.toml", content)); err == nil {
			t.Errorf("%s: LoadManifest accepted invalid input", name)
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
