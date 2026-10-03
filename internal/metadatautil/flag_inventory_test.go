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
		"Commands:",
		"  exec              Run Codex non-interactively [aliases: e]",
		"  $ copilot -p \"Fix the bug\"",
	}, "\n")
	want := []string{"--allowed-tools", "--allowedTools", "--config", "--debug", "--enable", "--yes", "--yolo", "-C", "-c", "-d", "-y"}
	if got, err := ParseHelpFlags(help); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseHelpFlags() = %q, %v, want %q", got, err, want)
	}
	// An option-looking line with unread syntax fails instead of truncating.
	for _, line := range []string{"  --model_name <M>", "  --model.foo", "  --model:x", "  -u --unsafe"} {
		if got, err := ParseHelpFlags("Options:\n" + line + "\n"); err == nil {
			t.Errorf("ParseHelpFlags(%q) = %q, want an error", line, got)
		}
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

func TestFlagInventoryRenderAndCheck(t *testing.T) {
	t.Parallel()

	root := writeFlagInventoryRepo(t, "")
	plan, err := FlagInventoryPlan(root)
	if err != nil || plan != "demo demo exec\n" {
		t.Fatalf("FlagInventoryPlan() = %q, %v", plan, err)
	}
	help := filepath.Join(root, "help.txt")
	mustWriteText(t, help, "Commands:\n  exec  Run\n  help  Print help\n\nOptions:\n      --yolo\n  -m, --model <M>\n")
	if _, err := RenderFlagFixtureFromHelp(root, "demo", []string{help}); err != nil {
		t.Fatal(err)
	}
	if _, err := RenderFlagFixtureFromHelp(root, "planned", []string{help}); err == nil {
		t.Fatal("rendered a fixture for a planned adapter")
	}
	fixture, _ := RenderFlagFixtureFromHelp(root, "demo", []string{help})
	if !strings.Contains(string(fixture), "# demo-version: 1.2.3\n") || !strings.HasSuffix(string(fixture), "\n--model\n--yolo\n-m\n") {
		t.Fatalf("fixture = %q", fixture)
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
	}
	if err := CheckFlagInventory(writeFlagInventoryRepo(t, good)); err != nil {
		t.Fatalf("good fixture: %v", err)
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
