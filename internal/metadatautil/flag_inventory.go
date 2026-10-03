// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/adapters"
)

// The flag inventory binds each certified adapter's CLI options to its manifest.
// scripts/check-flag-inventory.sh renders tests/fixtures/flags/<id>.txt from the
// CLI help in the runtime image; CheckFlagInventory fails on unclassified flags.

var (
	// An option line starts with at most six spaces of indentation, so the
	// deeper-indented description text that names other flags is not read.
	helpOptionLinePattern = regexp.MustCompile(`^ {1,6}(-{1,2}[A-Za-z0-9][A-Za-z0-9-]*(?:, *-{1,2}[A-Za-z0-9][A-Za-z0-9-]*)*)(?:[ =<\[]|$)`)
	// helpOptionLikePattern matches a line that starts like an option line. One
	// that helpOptionLinePattern does not match uses syntax the parser does not
	// read, so the inventory fails instead of recording a truncated name.
	helpOptionLikePattern = regexp.MustCompile(`^ {1,6}-{1,2}[A-Za-z0-9]`)
	// helpSecondOptionPattern matches text after an option list that starts
	// another option, as in "  -u --unsafe".
	helpSecondOptionPattern = regexp.MustCompile(`^ +-{1,2}[A-Za-z0-9]`)
	flagFixtureStampRE      = regexp.MustCompile(`(?m)^# ([a-z][a-z0-9-]*)-version: ([0-9]+\.[0-9]+\.[0-9]+)$`)
)

// FlagFixturePath is the checked-in inventory of one adapter.
func FlagFixturePath(root, id string) string {
	return filepath.Join(root, "tests", "fixtures", "flags", id+".txt")
}

// ParseHelpFlags returns the sorted unique option tokens that CLI help text
// declares (clap, commander, and yargs layouts).
func ParseHelpFlags(help string) ([]string, error) {
	var flags []string
	for _, line := range strings.Split(help, "\n") {
		match := helpOptionLinePattern.FindStringSubmatch(line)
		if match == nil {
			if helpOptionLikePattern.MatchString(line) {
				return nil, fmt.Errorf("help option line %q uses syntax the inventory does not read", line)
			}
			continue
		}
		if helpSecondOptionPattern.MatchString(line[len(match[0])-1:]) {
			return nil, fmt.Errorf("help option line %q lists an option that the inventory does not read", line)
		}
		for _, token := range strings.Split(match[1], ",") {
			flags = append(flags, strings.TrimSpace(token))
		}
	}
	slices.Sort(flags)
	return slices.Compact(flags), nil
}

// RenderFlagFixture renders the fixture for adapter m at the pinned version.
func RenderFlagFixture(m adapters.Manifest, version string, flags []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Option inventory for the PINNED runtime %s CLI. The stamp must equal the pin.\n", m.Binary)
	fmt.Fprintf(&b, "# %s-version: %s\n", m.ID, version)
	fmt.Fprintf(&b, "# Regenerate: scripts/check-flag-inventory.sh --write. Classify: adapters/%s/adapter.toml [flags].\n", m.ID)
	for _, flag := range flags {
		b.WriteString(flag + "\n")
	}
	return []byte(b.String())
}

// FlagInventoryPlan prints one line per certified adapter:
// "<id> <binary> [<subcommand>...]".
func FlagInventoryPlan(root string) (string, error) {
	manifests, err := certifiedManifests(root)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, m := range manifests {
		b.WriteString(strings.Join(append([]string{m.ID, m.Binary}, m.Flags.Subcommands...), " ") + "\n")
	}
	return b.String(), nil
}

// RenderFlagFixtureFromHelp renders the fixture for adapter id from its help
// output files, stamped with the current pin.
func RenderFlagFixtureFromHelp(root, id string, helpPaths []string) ([]byte, error) {
	manifests, err := certifiedManifests(root)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(manifests, func(m adapters.Manifest) bool { return m.ID == id })
	if i < 0 {
		return nil, fmt.Errorf("no certified adapter %q", id)
	}
	m := manifests[i]
	version, err := adapterPin(root, m)
	if err != nil {
		return nil, err
	}
	var help strings.Builder
	for _, path := range helpPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		help.Write(data)
		help.WriteByte('\n')
	}
	flags, err := ParseHelpFlags(help.String())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	if len(flags) == 0 {
		return nil, fmt.Errorf("%s help declares no flags", id)
	}
	return RenderFlagFixture(m, version, flags), nil
}

// CheckFlagInventory fails when a certified adapter has no fixture, a fixture
// stamp differs from the provider pin, or a fixture flag is in neither the
// manifest's [flags] allow nor deny list.
func CheckFlagInventory(root string) error {
	manifests, err := certifiedManifests(root)
	if err != nil {
		return err
	}
	var problems []string
	for _, m := range manifests {
		path := FlagFixturePath(root, m.ID)
		version, flags, err := readFlagFixture(path, m.ID)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		pin, err := adapterPin(root, m)
		if err != nil {
			return err
		}
		if version != pin {
			problems = append(problems, fmt.Sprintf("%s: stamp %s does not match the %s pin %s; run scripts/check-flag-inventory.sh --write", path, version, m.ID, pin))
		}
		for _, flag := range flags {
			if !slices.Contains(m.Flags.Allow, flag) && !slices.Contains(m.Flags.Deny, flag) {
				problems = append(problems, fmt.Sprintf("%s: flag %s is not classified; add it to [flags] allow or deny in adapters/%s/adapter.toml", path, flag, m.ID))
			}
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	return nil
}

func readFlagFixture(path, id string) (string, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	stamps := flagFixtureStampRE.FindAllStringSubmatch(string(data), -1)
	if len(stamps) != 1 || stamps[0][1] != id {
		return "", nil, fmt.Errorf("%s: want exactly one \"# %s-version: X.Y.Z\" stamp", path, id)
	}
	var flags []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !adapters.FlagPattern.MatchString(line) {
			return "", nil, fmt.Errorf("%s: invalid flag line %q", path, line)
		}
		flags = append(flags, line)
	}
	if len(flags) == 0 {
		return "", nil, fmt.Errorf("%s: no flags", path)
	}
	return stamps[0][2], flags, nil
}

func certifiedManifests(root string) ([]adapters.Manifest, error) {
	all, err := adapters.LoadManifests(filepath.Join(root, "adapters"))
	if err != nil {
		return nil, err
	}
	var out []adapters.Manifest
	for _, m := range all {
		if m.Tier == "certified" {
			out = append(out, m)
		}
	}
	return out, nil
}

// adapterPin reads the version the existing pin owners hold for adapter m.
func adapterPin(root string, m adapters.Manifest) (string, error) {
	if m.Install.Method == "npm" {
		var pkg providersPackageJSON
		path := filepath.Join(root, "runtime", "container", "providers", "package.json")
		if err := readJSONFile(path, &pkg); err != nil {
			return "", err
		}
		if version := pkg.Dependencies[m.Install.Package]; version != "" {
			return version, nil
		}
		return "", fmt.Errorf("%s does not pin %s", path, m.Install.Package)
	}
	return ExtractDockerfileArg(filepath.Join(root, "runtime", "container", "Dockerfile"), m.Install.VersionArg)
}
