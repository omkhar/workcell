package metadatautil

import (
	"path/filepath"
	"strings"
	"testing"
)

// TEMPORARY: while the repository's workflows still hold their tool pins
// inline, these tests run the binding checks on a small workflow that already
// uses the pins step. The cleanup change (PR 4 of the tool-pin split) replaces
// them with the same cases run on the repository's own workflows.

const bindingTestWorkflow = `name: binding test
on: push
permissions: {}
defaults:
  run:
    shell: bash --noprofile --norc -euo pipefail {0}
env:
  KEEP: "1"
jobs:
  build:
    runs-on: ubuntu-latest
    env:
      KEEP: "1"
    steps:
      - uses: actions/checkout@0000000000000000000000000000000000000000
        with:
          persist-credentials: false
      - id: pins
        name: Load tool pins
        run: ./scripts/ci/load-tool-pins.sh
      - uses: sigstore/cosign-installer@0000000000000000000000000000000000000000
        with:
          cosign-release: ${{ steps.pins.outputs.cosign }}
      - uses: docker/setup-buildx-action@0000000000000000000000000000000000000000
        with:
          driver-opts: image=${{ steps.pins.outputs.buildkit }}
          version: ${{ steps.pins.outputs.buildx }}
      - name: Install actionlint
        env:
          ACTIONLINT_SHA256: ${{ steps.pins.outputs.actionlint_sha256 }}
          ACTIONLINT_VERSION: ${{ steps.pins.outputs.actionlint_version }}
        run: echo "${ACTIONLINT_VERSION}"
`

const bindingTestPinsStep = "      - id: pins\n        name: Load tool pins\n        run: ./scripts/ci/load-tool-pins.sh\n"

func bindingTestPins(t *testing.T) toolPins {
	t.Helper()
	pins, err := loadToolPins(filepath.Join("..", "..", "policy", "tool-pins.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return pins
}

func TestToolPinBindingChecksAcceptTheBoundWorkflow(t *testing.T) {
	t.Parallel()

	if err := validateWorkflowToolPinBindingsText(bindingTestWorkflow, "binding.yml", bindingTestPins(t)); err != nil {
		t.Fatalf("validateWorkflowToolPinBindingsText() error = %v", err)
	}
}

func TestToolPinBindingChecksRejectBypasses(t *testing.T) {
	t.Parallel()

	pins := bindingTestPins(t)
	for _, tc := range []struct {
		name, old, new, want string
	}{
		{"inline pin value", "ACTIONLINT_VERSION: ${{ steps.pins.outputs.actionlint_version }}", "ACTIONLINT_VERSION: " + pins.ActionlintVersion, "holds the policy/tool-pins.toml actionlint_version value"},
		{"inline image repository", "image=${{ steps.pins.outputs.buildkit }}", "image=moby/buildkit:latest", "names the tool-pin image moby/buildkit inline"},
		{"bracket reference", "${{ steps.pins.outputs.cosign }}", "${{ steps['pins'].outputs.cosign }}", "must reference tool pins only as"},
		{"binding in run", `run: echo "${ACTIONLINT_VERSION}"`, "run: echo ${{ steps.pins.outputs.actionlint_version }}", "binds a tool pin in run:"},
		{"unknown output", "${{ steps.pins.outputs.cosign }}", "${{ steps.pins.outputs.cosign_next }}", "names unknown tool pin output"},
		{"env bound to the wrong key", "ACTIONLINT_SHA256: ${{ steps.pins.outputs.actionlint_sha256 }}", "ACTIONLINT_SHA256: ${{ steps.pins.outputs.zizmor_sha256 }}", "env ACTIONLINT_SHA256 must be exactly"},
		{"action input literal", "cosign-release: ${{ steps.pins.outputs.cosign }}", "cosign-release: v9.9.9", "must set cosign-release: ${{ steps.pins.outputs.cosign }}"},
		{"setup-buildx without version", "          version: ${{ steps.pins.outputs.buildx }}\n", "", "must set version: ${{ steps.pins.outputs.buildx }}"},
		{"pins step with if", bindingTestPinsStep, bindingTestPinsStep + "        if: always()\n", "pins step must not set if"},
		{"pins step runs another script", "run: ./scripts/ci/load-tool-pins.sh", "run: ./scripts/ci/load-tool-pins.sh || true", "pins step must run exactly"},
		{"pins step env", bindingTestPinsStep, bindingTestPinsStep + "        env:\n          BASH_ENV: ./attacker.sh\n", "pins step must hold only id, name and run, found env"},
		{"second pins step", bindingTestPinsStep, bindingTestPinsStep + bindingTestPinsStep, "must have one pins step"},
		{"step before the pins step", bindingTestPinsStep, "      - run: echo BASH_ENV=./attacker.sh >> \"${GITHUB_ENV}\"\n" + bindingTestPinsStep, "must check out the repository as its first step and run the pins step right after it"},
		{"consumer under always()", "      - name: Install actionlint\n", "      - name: Install actionlint\n        if: always()\n", "binds a tool pin, so its if: must not use"},
		{"job defaults", "    runs-on: ubuntu-latest\n", "    runs-on: ubuntu-latest\n    defaults:\n      run:\n        shell: sh\n", "must not set job defaults or a container"},
		{"workflow shell", "shell: bash --noprofile --norc -euo pipefail {0}", "shell: bash {0}", "defaults.run must hold only shell"},
		{"job field reference", "    runs-on: ubuntu-latest\n", "    runs-on: ubuntu-latest\n    timeout-minutes: ${{ steps.pins.outputs.cosign }}\n", "must not reference the pins step"},
		{"pin name in job env", "    env:\n      KEEP: \"1\"\n", "    env:\n      ACTIONLINT_VERSION: \"1\"\n", "must not define ACTIONLINT_VERSION; bind"},
		{"workflow env BASH_ENV", "env:\n  KEEP: \"1\"\njobs:", "env:\n  BASH_ENV: ./attacker.sh\njobs:", "workflow env must not define BASH_ENV"},
		{"job env bash_env in lower case", "    env:\n      KEEP: \"1\"\n", "    env:\n      bash_env: ./attacker.sh\n", "job build env must not define bash_env"},
		{"job env LD_PRELOAD", "    env:\n      KEEP: \"1\"\n", "    env:\n      LD_PRELOAD: ./attacker.so\n", "job build env must not define LD_PRELOAD"},
		{"job env GITHUB_OUTPUT", "    env:\n      KEEP: \"1\"\n", "    env:\n      GITHUB_OUTPUT: /tmp/elsewhere\n", "job build env must not define GITHUB_OUTPUT"},
		{"job env BASH_FUNC", "    env:\n      KEEP: \"1\"\n", "    env:\n      BASH_FUNC_wc%%: \"() { echo 0; }\"\n", "job build env must not define BASH_FUNC_wc%%"},
		{"job env expression", "    env:\n      KEEP: \"1\"\n", "    env: ${{ fromJSON(vars.ATTACKER_ENV) }}\n", "job build env must be a plain mapping"},
		{"job env merge key", "    env:\n      KEEP: \"1\"\n", "    env:\n      <<: {LD_PRELOAD: ./attacker.so}\n", "job build env must not use a YAML merge key (<<)"},
		{"step env merge key", "          ACTIONLINT_VERSION: ${{ steps.pins.outputs.actionlint_version }}\n", "          ACTIONLINT_VERSION: ${{ steps.pins.outputs.actionlint_version }}\n          <<: {ACTIONLINT_SHA256: \"0000\"}\n", "env must not use a YAML merge key (<<)"},
		{"step env alias", "      - name: Install actionlint\n", "      - env: &decoy\n          DECOY: \"0000\"\n        run: echo decoy\n      - env: *decoy\n        run: echo decoy\n      - name: Install actionlint\n", "build step 6 env must be a plain mapping, not a YAML alias or an expression"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(bindingTestWorkflow, tc.old) {
				t.Fatalf("test workflow does not contain %q", tc.old)
			}
			workflow := strings.Replace(bindingTestWorkflow, tc.old, tc.new, 1)
			err := validateWorkflowToolPinBindingsText(workflow, "binding.yml", pins)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateWorkflowToolPinBindingsText() error = %v, want %q", err, tc.want)
			}
		})
	}
}
