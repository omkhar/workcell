// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseHelpFlags(t *testing.T) {
	t.Parallel()

	help := strings.Join([]string{
		// clap
		"Options:",
		"  -c, --config <key=value>",
		"          Examples: - `-c model=\"o3\"` and --not-a-flag",
		"      --enable <FEATURE>",
		"          - on-request: The model decides",
		// commander
		"  --allowedTools, --allowed-tools <tools...>",
		"  -d, --debug [filter]                  Enable debug mode",
		"                                        --append-system-prompt[-file], --add-dir",
		"  --yes       Skip the picker.",
		"              --yes=<digest> from the preview.",
		// yargs
		"  -y, --yolo                      Automatically accept  [boolean]",
		"  -C <directory>",
		"  --first <value> --second <value>",
		"  -u --unsafe",
		"Commands:",
		"  exec              Run Codex non-interactively [aliases: e]",
		"  $ copilot -p \"Fix the bug\"",
	}, "\n")
	want := []string{"--allowed-tools", "--allowedTools", "--config", "--debug", "--enable", "--first", "--second", "--unsafe", "--yes", "--yolo", "-C", "-c", "-d", "-u", "-y"}
	if got, err := ParseHelpFlags(help); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseHelpFlags() = %q, %v, want %q", got, err, want)
	}
	// An option-looking line with unread syntax fails instead of truncating.
	for _, line := range []string{"  --model_name <M>", "  --model.foo", "  --model:x", "  --first <v> --[no-]x", "  -?, --help", "  --[no-]color", "  -h -?", "--new-mode"} {
		if got, err := ParseHelpFlags("Options:\n" + line + "\n"); err == nil {
			t.Errorf("ParseHelpFlags(%q) = %q, want an error", line, got)
		}
	}
}

func TestParseHelpCommands(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ help, want string }{
		"commander": {"Commands:\n  agents [options]   Manage\n  attach <id>   Open a\n                terminal\n  stop|kill <id>   Stop\n\nOptions:\n  -h, --help\n", "agents attach stop kill"},
		"yargs":     {"Commands:\n  tool mcp   Manage\n  tool skills <command>  Manage  [aliases: skill, skillz]\n  tool [query..]   Launch  [default]\n\nOptions:\n  -d\n", "mcp skills skill skillz"},
		"clap":      {"Commands:\n  exec  Run [aliases: e]\n  apply  Apply the diff to your\n         working tree [aliases: a]\n  help  Print\n\nOptions:\n", "exec e apply a help"},
		"later":     {"Options:\n  -h\nCommands:\n  exec  Run\nOptions:\nCommands:\n  late  Run\n", "exec"},
		"none":      {"Options:\n  -h\n", ""},
	}
	for name, c := range cases {
		if got, err := ParseHelpCommands(c.help, "tool"); err != nil || strings.Join(got, " ") != c.want {
			t.Errorf("%s: ParseHelpCommands() = %q, %v, want %q", name, got, err, c.want)
		}
	}
	// An alias list the parser cannot read fails.
	if got, err := ParseHelpCommands("Commands:\n  exec  Run [aliases: new_cmd]\n", "tool"); err == nil {
		t.Errorf("ParseHelpCommands() = %q, want an error", got)
	}
	// A command line with a name the parser cannot read fails.
	if got, err := ParseHelpCommands("Commands:\n  exec  Run\n  new_cmd  New\n", "tool"); err == nil {
		t.Errorf("ParseHelpCommands() = %q, want an error", got)
	}
}

const flagInventoryDemoManifest = `schema = 1
id = "demo"
tier = "certified"
binary = "demo"

[install]
method = "binary"
version_arg = "DEMO_VERSION"
provenance = "scripts/verify-demo.sh"

[flags]
subcommands = ["exec"]
other_commands = ["help"]
allow = ["--model"]
deny = ["--yolo"]
`

func writeFlagInventoryRepo(t *testing.T, fixture string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"adapters/demo", "adapters/planned", "runtime/container", "tests/fixtures/flags"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteText(t, filepath.Join(root, "adapters", "demo", "adapter.toml"), flagInventoryDemoManifest)
	mustWriteText(t, filepath.Join(root, "adapters", "planned", "adapter.toml"), "schema = 1\nid = \"planned\"\ntier = \"planned\"\n")
	mustWriteText(t, filepath.Join(root, "runtime", "container", "Dockerfile"), "FROM scratch\nARG DEMO_VERSION=1.2.3\n")
	if fixture != "" {
		mustWriteText(t, FlagFixturePath(root, "demo"), fixture)
	}
	return root
}

func TestFlagInventoryRenderCheckAndRestamp(t *testing.T) {
	t.Parallel()

	root := writeFlagInventoryRepo(t, "")
	plan, err := FlagInventoryPlan(root)
	if err != nil || plan != "demo demo exec\n" {
		t.Fatalf("FlagInventoryPlan() = %q, %v", plan, err)
	}
	help := filepath.Join(root, "help.txt")
	mustWriteText(t, help, "Commands:\n  exec  Run\n  help  Print help\n\nOptions:\n      --yolo\n  -m, --model <M>\n")
	if _, err := RenderFlagFixtureFromHelp(root, "planned", []string{help}); err == nil {
		t.Fatal("rendered a fixture for a planned adapter")
	}
	// A command in neither subcommands nor other_commands fails the render.
	mustWriteText(t, help+".new", "Commands:\n  exec  Run\n  help  Print help\n  install  New\n\nOptions:\n      --yolo\n")
	if _, err := RenderFlagFixtureFromHelp(root, "demo", []string{help + ".new"}); err == nil || !strings.Contains(err.Error(), `command "install"`) {
		t.Fatalf("RenderFlagFixtureFromHelp() = %v, want unclassified install", err)
	}
	// Help with no command list fails closed when the manifest names commands.
	mustWriteText(t, help+".none", "Options:\n      --yolo\n")
	if _, err := RenderFlagFixtureFromHelp(root, "demo", []string{help + ".none"}); err == nil || !strings.Contains(err.Error(), "lists no commands") {
		t.Fatalf("RenderFlagFixtureFromHelp() = %v, want layout failure", err)
	}
	fixture, err := RenderFlagFixtureFromHelp(root, "demo", []string{help})
	if err != nil || !strings.Contains(string(fixture), "# demo-version: 1.2.3\n") || !strings.HasSuffix(string(fixture), "\n--model\n--yolo\n-m\n") {
		t.Fatalf("fixture = %q, %v", fixture, err)
	}
	mustWriteText(t, FlagFixturePath(root, "demo"), string(fixture))
	// -m is in the help but not in the manifest.
	if err := CheckFlagInventory(root); err == nil || !strings.Contains(err.Error(), "flag -m is not classified") {
		t.Fatalf("CheckFlagInventory() = %v, want unclassified -m", err)
	}

	classified := strings.Replace(string(fixture), "-m\n", "", 1)
	mustWriteText(t, FlagFixturePath(root, "demo"), classified)
	if err := CheckFlagInventory(root); err != nil {
		t.Fatalf("CheckFlagInventory() = %v, want nil", err)
	}

	mustWriteText(t, filepath.Join(root, "runtime", "container", "Dockerfile"), "FROM scratch\nARG DEMO_VERSION=1.3.0\n")
	if err := CheckFlagInventory(root); err == nil || !strings.Contains(err.Error(), "stamp 1.2.3 does not match the demo pin 1.3.0") {
		t.Fatalf("CheckFlagInventory() = %v, want stale stamp", err)
	}
	if err := RestampFlagFixtures(root); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(FlagFixturePath(root, "demo"))
	if want := strings.Replace(classified, "1.2.3", "1.3.0", 1); string(got) != want {
		t.Fatalf("restamped fixture = %q, want %q", got, want)
	}
	if err := CheckFlagInventory(root); err != nil {
		t.Fatalf("CheckFlagInventory() after restamp = %v, want nil", err)
	}
}

func TestCheckFlagInventoryRejectsMalformedFixtures(t *testing.T) {
	t.Parallel()

	const good = "# demo-version: 1.2.3\n--model\n--yolo\n"
	cases := map[string]string{
		"missing fixture":     "",
		"no stamp":            "--model\n",
		"two stamps":          "# demo-version: 1.2.3\n# demo-version: 1.2.3\n--model\n",
		"stamp for other id":  strings.Replace(good, "demo-version", "codex-version", 1),
		"no flags":            "# demo-version: 1.2.3\n",
		"flag with value":     good + "--model=x\n",
		"blank line":          good + "\n--yolo\n",
		"comment-only prefix": "# demo-version: 1.2.3\nmodel\n",
	}
	for name, fixture := range cases {
		root := writeFlagInventoryRepo(t, fixture)
		if err := CheckFlagInventory(root); err == nil {
			t.Errorf("%s: CheckFlagInventory() = nil, want error", name)
		}
		if fixture != "" {
			if err := RestampFlagFixtures(root); err == nil {
				t.Errorf("%s: RestampFlagFixtures() = nil, want error", name)
			}
		}
	}
	if err := CheckFlagInventory(writeFlagInventoryRepo(t, good)); err != nil {
		t.Fatalf("good fixture: %v", err)
	}
	root := writeFlagInventoryRepo(t, "")
	secret := filepath.Join(root, "secret.txt")
	mustWriteText(t, secret, good)
	if err := os.Symlink(secret, FlagFixturePath(root, "demo")); err != nil {
		t.Fatal(err)
	}
	if err := CheckFlagInventory(root); err == nil {
		t.Fatal("symlinked fixture: CheckFlagInventory() = nil, want error")
	}
	help := filepath.Join(root, "help.txt")
	mustWriteText(t, help, "Options:\n  --model <M>\n  --yolo\n")
	if err := CompareFlagFixture(root, "demo", []string{help}); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("symlinked fixture: CompareFlagFixture() = %v, want an error that does not name the target", err)
	}
}

func TestWriteFlagFixtureRefusesSymlinkedParent(t *testing.T) {
	t.Parallel()

	root := writeFlagInventoryRepo(t, "")
	help := filepath.Join(root, "help.txt")
	mustWriteText(t, help, "Commands:\n  exec  Run\n  help  Print help\n\nOptions:\n  --model <M>\n")
	flags := filepath.Dir(FlagFixturePath(root, "demo"))
	// A missing fixture directory is created, and a second write replaces the file.
	if err := os.RemoveAll(flags); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := WriteFlagFixture(root, "demo", []string{help}); err != nil {
			t.Fatalf("WriteFlagFixture() = %v", err)
		}
	}
	info, err := os.Stat(FlagFixturePath(root, "demo"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("fixture stat = %v; want mode 0600", err)
	}
	if entries, _ := os.ReadDir(flags); len(entries) != 1 {
		t.Fatalf("staged sibling left behind: %v", entries)
	}
	if err := CompareFlagFixture(root, "demo", []string{help}); err != nil {
		t.Fatalf("CompareFlagFixture() after write = %v", err)
	}
	// An untrusted checkout can point the fixture directory outside itself.
	outside := t.TempDir()
	if err := os.RemoveAll(flags); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, flags); err != nil {
		t.Fatal(err)
	}
	if err := WriteFlagFixture(root, "demo", []string{help}); err == nil {
		t.Fatal("WriteFlagFixture() through a symlinked parent = nil, want error")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote %v outside the checkout", entries)
	}
}

// TestFlagInventoryRealRepo keeps the checked-in fixtures classified and
// stamped with the provider pins, so validate fails before container smoke.
func TestFlagInventoryRealRepo(t *testing.T) {
	t.Parallel()

	if err := CheckFlagInventory(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
}

func TestRestampFlagFixturesRefusesSymlinkedParent(t *testing.T) {
	t.Parallel()

	const good = "# demo-version: 1.2.3\n--model\n--yolo\n"
	root := writeFlagInventoryRepo(t, good)
	mustWriteText(t, filepath.Join(root, "runtime", "container", "Dockerfile"), "FROM scratch\nARG DEMO_VERSION=1.3.0\n")
	// An untrusted checkout can point tests/fixtures at another directory in
	// the checkout, which os.Root would follow.
	fixtures := filepath.Join(root, "tests", "fixtures")
	outside := filepath.Join(root, "decoy")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fixtures, filepath.Join(outside, "fixtures")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "decoy", "fixtures"), fixtures); err != nil {
		t.Fatal(err)
	}
	if err := RestampFlagFixtures(root); err == nil {
		t.Fatal("RestampFlagFixtures() = nil, want a refused symlinked parent")
	}
	if got, _ := os.ReadFile(filepath.Join(outside, "fixtures", "flags", "demo.txt")); string(got) != good {
		t.Fatalf("outside fixture = %q, want it unchanged", got)
	}
}

// An uncertified npm adapter has a fixture stamped with its pin in the
// uncertified provider package.json. The static check and the restamp cover
// it; the container probe plan does not, because the Tier 1 image lacks it.
func TestFlagInventoryCoversUncertifiedAdapter(t *testing.T) {
	t.Parallel()

	root := writeFlagInventoryRepo(t, "# demo-version: 1.2.3\n--model\n--yolo\n")
	for _, dir := range []string{"adapters/undemo", "runtime/container/providers-uncertified"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteText(t, filepath.Join(root, "adapters", "undemo", "adapter.toml"), `schema = 1
id = "undemo"
tier = "uncertified"
binary = "undemo"

[install]
method = "npm"
package = "undemo-cli"
provenance = "scripts/verify-undemo.sh"

[flags]
allow = ["--model"]
deny = ["--auto"]
`)
	mustWriteText(t, filepath.Join(root, "runtime", "container", "providers-uncertified", "package.json"), `{"dependencies":{"undemo-cli":"4.5.6"}}`)
	if plan, err := FlagInventoryPlan(root); err != nil || plan != "demo demo exec\n" {
		t.Fatalf("FlagInventoryPlan() = %q, %v, want only the certified adapter", plan, err)
	}
	if err := CheckFlagInventory(root); err == nil {
		t.Fatal("CheckFlagInventory() = nil, want the missing undemo fixture to fail")
	}
	mustWriteText(t, FlagFixturePath(root, "undemo"), "# undemo-version: 4.5.5\n--auto\n--model\n")
	if err := CheckFlagInventory(root); err == nil || !strings.Contains(err.Error(), "stamp 4.5.5 does not match the undemo pin 4.5.6") {
		t.Fatalf("CheckFlagInventory() = %v, want the stale undemo stamp", err)
	}
	if err := RestampFlagFixtures(root); err != nil {
		t.Fatal(err)
	}
	if err := CheckFlagInventory(root); err != nil {
		t.Fatalf("CheckFlagInventory() after restamp = %v, want nil", err)
	}
	mustWriteText(t, FlagFixturePath(root, "undemo"), "# undemo-version: 4.5.6\n--auto\n--model\n--port\n")
	if err := CheckFlagInventory(root); err == nil || !strings.Contains(err.Error(), "flag --port is not classified") {
		t.Fatalf("CheckFlagInventory() = %v, want unclassified --port", err)
	}
}
