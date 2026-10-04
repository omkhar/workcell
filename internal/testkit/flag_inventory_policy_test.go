// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"

	"github.com/omkhar/workcell/internal/adapters"
)

const flagInventoryProbeValue = "workcell-flag-probe"

// yargsProviders lists providers whose CLI parser accepts --flag=value on a
// boolean option (yargs reads --yolo=true as --yolo). The clap and commander
// CLIs reject an attached value on a boolean option, so those spellings never
// reach the provider.
var yargsProviders = map[string]bool{"gemini": true}

// camelCaseFlag returns --allowedTools for --allowed-tools.
func camelCaseFlag(flag string) string {
	parts := strings.Split(strings.TrimPrefix(flag, "--"), "-")
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return "--" + strings.Join(parts, "")
}

// policyExitCodes runs reject_unsafe_<id>_args once per argv in its own bash
// process, with each argv element as one argument, and returns each exit code
// (0 accepted, 2 rejected by workcell_die).
func policyExitCodes(t *testing.T, id string, argvs [][]string) []string {
	t.Helper()
	const script = `source "$1"; fn="$2"; shift 2; "${fn}" "$@"`
	policy := filepath.Join(repoRoot(t), "runtime", "container", "provider-policy.sh")
	codes := make([]string, len(argvs))
	for i, argv := range argvs {
		args := append([]string{"-c", script, "bash", policy, "reject_unsafe_" + id + "_args"}, argv...)
		err := exec.Command("bash", args...).Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			codes[i] = "0"
		case errors.As(err, &exit):
			codes[i] = fmt.Sprint(exit.ExitCode())
		default:
			t.Fatalf("%s policy harness: %v", id, err)
		}
	}
	return codes
}

// TestFlagInventoryEntriesMatchProviderPolicy runs every [flags] allow, deny,
// and subcommands entry of each certified manifest through
// reject_unsafe_<id>_args, so a manifest classification cannot drift from the
// runtime policy.
func TestFlagInventoryEntriesMatchProviderPolicy(t *testing.T) {
	t.Parallel()

	if out, err := exec.Command("bash", "-c", "((BASH_VERSINFO[0] >= 4))").CombinedOutput(); err != nil {
		t.Fatalf("provider-policy.sh needs bash 4 or later on PATH: %v %s", err, out)
	}
	manifests, err := adapters.LoadManifests(filepath.Join(repoRoot(t), "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	certified := 0
	for _, m := range manifests {
		if m.Tier != "certified" {
			continue
		}
		certified++
		if len(m.Flags.Allow) == 0 || len(m.Flags.Deny) == 0 {
			t.Errorf("%s: certified manifest needs [flags] allow and deny", m.ID)
			continue
		}
		type probe struct {
			argv []string
			want string
		}
		var probes []probe
		for _, flag := range m.Flags.Allow {
			probes = append(probes, probe{[]string{flag, flagInventoryProbeValue}, "0"})
		}
		for _, flag := range m.Flags.Deny {
			forms := [][]string{{flag, flagInventoryProbeValue}}
			if len(flag) == 2 {
				forms = append(forms, []string{flag + flagInventoryProbeValue})
				if yargsProviders[m.ID] {
					// yargs reads -d<letter> as -d plus the short option.
					forms = append(forms, []string{"-d" + flag[1:]})
				}
			} else {
				// Every parser takes the attached value form of a long option.
				forms = append(forms, []string{flag + "=" + flagInventoryProbeValue})
			}
			if len(flag) > 2 && yargsProviders[m.ID] {
				// yargs also accepts the camel-case spelling of a dashed option.
				camel := camelCaseFlag(flag)
				forms = append(forms, []string{camel, flagInventoryProbeValue}, []string{camel + "=" + flagInventoryProbeValue})
			}
			for _, form := range forms {
				probes = append(probes, probe{form, "2"})
				// A later option must not hide an earlier one, or the reverse.
				probes = append(probes, probe{append([]string{m.Flags.Allow[0]}, form...), "2"})
			}
		}
		if yargsProviders[m.ID] {
			// A value-taking short option owns the rest of its group.
			probes = append(probes, probe{[]string{"-pyes"}, "0"}, probe{[]string{"-dl"}, "0"},
				// Text after -- is prompt text, not options.
				probe{[]string{"--", "-yellow"}, "0"}, probe{[]string{"--", "-safe"}, "0"}, probe{[]string{"-d", "--", "-yellow"}, "0"},
				probe{[]string{"-y", "--", "text"}, "2"}, probe{[]string{"---", "-y"}, "2"}) // --- is a positional
		}
		if m.ID == "codex" {
			// A config override reaches the same setting as --approve-for-me.
			probes = append(probes, probe{[]string{"-c", "approvals_reviewer=auto_review"}, "2"},
				probe{[]string{"--config=profiles.x.approvals_reviewer=auto_review"}, "2"})
		}
		for _, sub := range m.Flags.Subcommands {
			probes = append(probes, probe{[]string{sub}, "0"})
		}
		argvs := make([][]string, len(probes))
		for i, p := range probes {
			argvs[i] = p.argv
		}
		for i, got := range policyExitCodes(t, m.ID, argvs) {
			if got != probes[i].want {
				t.Errorf("%s: reject_unsafe_%s_args %s exited %s, want %s", m.ID, m.ID, strings.Join(probes[i].argv, " "), got, probes[i].want)
			}
		}
	}
	if certified == 0 {
		t.Fatal("no certified manifests")
	}
}

// TestFlagInventoryPolicyHarnessNegativeControl proves the harness tells an
// accepted flag from a rejected one, so a broken harness cannot pass the test
// above vacuously.
func TestFlagInventoryPolicyHarnessNegativeControl(t *testing.T) {
	t.Parallel()

	codes := policyExitCodes(t, "copilot", [][]string{
		{"--model", flagInventoryProbeValue},
		{"--yolo", flagInventoryProbeValue},
		{"-c" + flagInventoryProbeValue},
		{"login"},
	})
	if got := fmt.Sprint(codes); got != "[0 2 2 2]" {
		t.Fatalf("copilot probe exit codes = %s, want [0 2 2 2]", got)
	}
}

// TestContainerSmokeRunsFlagInventory keeps the inventory in the
// container-smoke lane, after the image build and against the built tag.
func TestContainerSmokeRunsFlagInventory(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "container-smoke.sh"))
	if err != nil {
		t.Fatal(err)
	}
	smoke := string(data)
	// The guard reads parsed invocations, so the same check as a comment does not satisfy it.
	commented := strings.ReplaceAll(smoke, "\n  \"${ROOT_DIR}/scripts/check-flag-inventory.sh\"", "\n  # \"${ROOT_DIR}/scripts/check-flag-inventory.sh\"")
	if commented == smoke || !smokeRunsInventoryAfterBuild(smoke) || smokeRunsInventoryAfterBuild(commented) {
		t.Fatal("container-smoke.sh must run scripts/check-flag-inventory.sh on WORKCELL_IMAGE_TAG after it builds the image, as a parsed invocation")
	}
}

// smokeRunsInventoryAfterBuild anchors on the commands as they run: the parser
// reads a leading environment assignment as the first command word and drops quotes.
func smokeRunsInventoryAfterBuild(smoke string) bool {
	build := -1
	for _, inv := range metadatautil.ShellInvocations(smoke, "SOURCE_DATE_EPOCH=${BUILD_SOURCE_DATE_EPOCH} buildx_cmd") {
		if strings.Contains(strings.Join(inv.Args, " "), "runtime/container/Dockerfile") {
			build = inv.Position
		}
	}
	for _, inv := range metadatautil.ShellInvocations(smoke, "WORKCELL_GO_BIN=${GO_BIN}") {
		args := strings.Join(inv.Args, " ")
		if build >= 0 && inv.Position > build && strings.Contains(args, "WORKCELL_IMAGE_TAG=${IMAGE_TAG}") && strings.Contains(args, "scripts/check-flag-inventory.sh") {
			return true
		}
	}
	return false
}
