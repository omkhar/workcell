package metadatautil_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

const toolPinsStepText = "      - id: pins\n        name: Load tool pins\n        run: ./scripts/ci/load-tool-pins.sh\n"

// fixtureToolPin reads one value of the fixture's policy/tool-pins.toml, so a
// routine pin bump does not require editing these tests.
func fixtureToolPin(t *testing.T, cfg metadatautil.PinnedInputsConfig, key string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(fixtureRepoRoot(cfg), "policy", "tool-pins.toml"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + ` = "([^"]+)"$`).FindStringSubmatch(string(content))
	if match == nil {
		t.Fatalf("fixture policy/tool-pins.toml has no %s pin", key)
	}
	return match[1]
}

func fixtureRepoRoot(cfg metadatautil.PinnedInputsConfig) string {
	return filepath.Dir(filepath.Dir(cfg.WorkflowsDir))
}

// TestCheckPinnedInputsRejectsWorkflowToolPinBypass covers the ways a workflow
// could carry its own tool pin, drift from policy/tool-pins.toml, or use a pin
// the pins step did not load.
func TestCheckPinnedInputsRejectsWorkflowToolPinBypass(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		workflow string
		old      string
		new      func(cfg metadatautil.PinnedInputsConfig) string
		want     string
	}{
		{
			name:     "docs buildx version drift",
			workflow: "docs.yml",
			old:      "version: ${{ steps.pins.outputs.buildx }}",
			new:      func(metadatautil.PinnedInputsConfig) string { return "version: v9.0.0" },
			want:     "must set version: ${{ steps.pins.outputs.buildx }}",
		},
		{
			name:     "docs buildx version copied inline",
			workflow: "docs.yml",
			old:      "version: ${{ steps.pins.outputs.buildx }}",
			new: func(cfg metadatautil.PinnedInputsConfig) string {
				return "version: " + fixtureToolPin(t, cfg, "buildx")
			},
			want: "holds the policy/tool-pins.toml buildx value",
		},
		{
			name:     "docs BuildKit image drift",
			workflow: "docs.yml",
			old:      "WORKCELL_BUILDKIT_IMAGE: ${{ steps.pins.outputs.buildkit }}",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "WORKCELL_BUILDKIT_IMAGE: moby/buildkit:buildx-stable-1@sha256:" + strings.Repeat("0", 64)
			},
			want: "names the tool-pin image moby/buildkit inline",
		},
		{
			name:     "security zizmor version drift",
			workflow: "security.yml",
			old:      "ZIZMOR_VERSION: ${{ steps.pins.outputs.zizmor_version }}",
			new:      func(metadatautil.PinnedInputsConfig) string { return "ZIZMOR_VERSION: 0.0.0" },
			want:     "env ZIZMOR_VERSION must be exactly ${{ steps.pins.outputs.zizmor_version }}",
		},
		{
			name:     "security zizmor digest bound to another pin",
			workflow: "security.yml",
			old:      "ZIZMOR_SHA256: ${{ steps.pins.outputs.zizmor_sha256 }}",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "ZIZMOR_SHA256: ${{ steps.pins.outputs.actionlint_sha256 }}"
			},
			want: "env ZIZMOR_SHA256 must be exactly ${{ steps.pins.outputs.zizmor_sha256 }}",
		},
		{
			name:     "release zizmor version copied inline",
			workflow: "release.yml",
			old:      "ZIZMOR_VERSION: ${{ steps.pins.outputs.zizmor_version }}",
			new: func(cfg metadatautil.PinnedInputsConfig) string {
				return "ZIZMOR_VERSION: " + fixtureToolPin(t, cfg, "zizmor_version")
			},
			want: "holds the policy/tool-pins.toml zizmor_version value",
		},
		{
			name:     "release zizmor digest copied inline",
			workflow: "release.yml",
			old:      "ZIZMOR_SHA256: ${{ steps.pins.outputs.zizmor_sha256 }}",
			new: func(cfg metadatautil.PinnedInputsConfig) string {
				return "ZIZMOR_SHA256: " + fixtureToolPin(t, cfg, "zizmor_sha256")
			},
			want: "holds the policy/tool-pins.toml zizmor_sha256 value",
		},
		{
			name:     "workflow env defines a pin",
			workflow: "ci.yml",
			old:      "\njobs:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "\nenv:\n  WORKCELL_COSIGN_VERSION: v1.0.0\njobs:\n"
			},
			want: "must not define WORKCELL_COSIGN_VERSION",
		},
		{
			name:     "pins step skipped",
			workflow: "security.yml",
			old:      toolPinsStepText,
			new: func(metadatautil.PinnedInputsConfig) string {
				return toolPinsStepText + "        if: false\n"
			},
			want: "pins step must not set if",
		},
		{
			name:     "pins step allowed to fail",
			workflow: "security.yml",
			old:      toolPinsStepText,
			new: func(metadatautil.PinnedInputsConfig) string {
				return toolPinsStepText + "        continue-on-error: true\n"
			},
			want: "pins step must not set continue-on-error",
		},
		{
			name:     "pins step reads another policy file",
			workflow: "security.yml",
			old:      "run: ./scripts/ci/load-tool-pins.sh\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "run: ./scripts/ci/load-tool-pins.sh policy/other.toml\n"
			},
			want: "pins step must run exactly ./scripts/ci/load-tool-pins.sh",
		},
		{
			name:     "pins step removed",
			workflow: "security.yml",
			old:      toolPinsStepText,
			new:      func(metadatautil.PinnedInputsConfig) string { return "" },
			want:     "must run the pins step",
		},
		{
			name:     "pin interpolated into run text",
			workflow: "security.yml",
			old:      toolPinsStepText,
			new: func(metadatautil.PinnedInputsConfig) string {
				return toolPinsStepText + "      - run: echo \"${{ steps.pins.outputs.zizmor_version }}\"\n"
			},
			want: "binds a tool pin in run:",
		},
		{
			name:     "unknown pin output",
			workflow: "security.yml",
			old:      "ZIZMOR_VERSION: ${{ steps.pins.outputs.zizmor_version }}",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "ZIZMOR_VERSION: ${{ steps.pins.outputs.zizmor }}"
			},
			want: "names unknown tool pin output \"zizmor\"",
		},
		{
			name:     "bracket reference to the pins step",
			workflow: "security.yml",
			old:      "ZIZMOR_VERSION: ${{ steps.pins.outputs.zizmor_version }}",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "ZIZMOR_VERSION: ${{ steps['pins'].outputs.zizmor_version }}"
			},
			want: "must reference tool pins only as ${{ steps.pins.outputs.<key> }}",
		},
		{
			name:     "consumer runs after a failed pins step",
			workflow: "docs.yml",
			old:      "      - name: Check docs spelling and manpage\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "      - name: Check docs spelling and manpage\n        if: ${{ !cancelled() }}\n"
			},
			want: "must not use always(), cancelled(), failure() or a negated success()",
		},
		{
			name:     "setup-buildx without the pinned version",
			workflow: "ci.yml",
			old:      "          version: ${{ steps.pins.outputs.buildx }}\n",
			new:      func(metadatautil.PinnedInputsConfig) string { return "" },
			want:     "must set version: ${{ steps.pins.outputs.buildx }}",
		},
		{
			name:     "workflow env sets BASH_ENV",
			workflow: "ci.yml",
			old:      "\njobs:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "\nenv:\n  BASH_ENV: ./attacker.sh\njobs:\n"
			},
			want: "workflow env must not define BASH_ENV",
		},
		{
			name:     "workflow env sets ENV",
			workflow: "release.yml",
			old:      "\nenv:\n",
			new:      func(metadatautil.PinnedInputsConfig) string { return "\nenv:\n  ENV: ./attacker.sh\n" },
			want:     "workflow env must not define ENV",
		},
		{
			name:     "job env sets BASH_ENV",
			workflow: "security.yml",
			old:      "  actionlint:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "  actionlint:\n    env:\n      BASH_ENV: ./attacker.sh\n"
			},
			want: "job actionlint env must not define BASH_ENV",
		},
		{
			name:     "job env sets bash_env in lower case",
			workflow: "security.yml",
			old:      "  actionlint:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "  actionlint:\n    env:\n      bash_env: ./attacker.sh\n"
			},
			want: "job actionlint env must not define bash_env",
		},
		{
			name:     "job env exports a bash function",
			workflow: "security.yml",
			old:      "  actionlint:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "  actionlint:\n    env:\n      BASH_FUNC_wc%%: \"() { echo 0; }\"\n"
			},
			want: "job actionlint env must not define BASH_FUNC_wc%%",
		},
		{
			name:     "job env moves PATH",
			workflow: "security.yml",
			old:      "  actionlint:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "  actionlint:\n    env:\n      PATH: ./attacker\n"
			},
			want: "job actionlint env must not define PATH",
		},
		{
			name:     "job env redirects GITHUB_OUTPUT",
			workflow: "security.yml",
			old:      "  actionlint:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "  actionlint:\n    env:\n      GITHUB_OUTPUT: /tmp/elsewhere\n"
			},
			want: "job actionlint env must not define GITHUB_OUTPUT",
		},
		{
			name:     "job env preloads a library",
			workflow: "security.yml",
			old:      "  actionlint:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "  actionlint:\n    env:\n      LD_PRELOAD: ./attacker.so\n"
			},
			want: "job actionlint env must not define LD_PRELOAD",
		},
		{
			name:     "workflow env sets ld_library_path in lower case",
			workflow: "release.yml",
			old:      "\nenv:\n",
			new:      func(metadatautil.PinnedInputsConfig) string { return "\nenv:\n  ld_library_path: ./attacker\n" },
			want:     "workflow env must not define ld_library_path",
		},
		{
			name:     "workflow env merges names in",
			workflow: "ci.yml",
			old:      "\njobs:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "\nenv:\n  <<: {BASH_ENV: ./attacker.sh}\njobs:\n"
			},
			want: "workflow env must not use a YAML merge key (<<)",
		},
		{
			name:     "job env merges names in",
			workflow: "security.yml",
			old:      "  actionlint:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "  actionlint:\n    env:\n      <<: {LD_PRELOAD: ./attacker.so}\n"
			},
			want: "job actionlint env must not use a YAML merge key (<<)",
		},
		{
			name:     "step env merges a pin literal in",
			workflow: "security.yml",
			old:      "          ACTIONLINT_VERSION: ${{ steps.pins.outputs.actionlint_version }}\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "          ACTIONLINT_VERSION: ${{ steps.pins.outputs.actionlint_version }}\n          <<: {ACTIONLINT_SHA256: \"0000\"}\n"
			},
			want: "env must not use a YAML merge key (<<)",
		},
		{
			name:     "step env given as a YAML alias",
			workflow: "security.yml",
			old:      "      - name: Install actionlint\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "      - env: &attacker\n          DECOY: \"0000\"\n        run: echo decoy\n      - env: *attacker\n        run: echo decoy\n      - name: Install actionlint\n"
			},
			want: "job actionlint step 4 env must be a plain mapping, not a YAML alias or an expression",
		},
		{
			name:     "step env from an expression",
			workflow: "security.yml",
			old:      "      - name: Install actionlint\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "      - env: ${{ fromJSON(vars.ATTACKER_ENV) }}\n        run: echo decoy\n      - name: Install actionlint\n"
			},
			want: "env must be a plain mapping, not a YAML alias or an expression",
		},
		{
			name:     "job env given as a YAML alias in a job without a pins step",
			workflow: "ci.yml",
			old:      "\njobs:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "\njobs:\n  decoy:\n    runs-on: ubuntu-latest\n    steps:\n      - env: &attacker\n          DECOY: v9.9.9\n        run: echo decoy\n  decoy2:\n    runs-on: ubuntu-latest\n    env: *attacker\n    steps:\n      - run: echo decoy\n"
			},
			want: "job decoy2 env must be a plain mapping, not a YAML alias or an expression",
		},
		{
			name:     "job env from an expression",
			workflow: "security.yml",
			old:      "  actionlint:\n",
			new: func(metadatautil.PinnedInputsConfig) string {
				return "  actionlint:\n    env: ${{ fromJSON(vars.ATTACKER_ENV) }}\n"
			},
			want: "job actionlint env must be a plain mapping",
		},
		{
			name:     "pins step sets BASH_ENV",
			workflow: "security.yml",
			old:      toolPinsStepText,
			new: func(metadatautil.PinnedInputsConfig) string {
				return toolPinsStepText + "        env:\n          BASH_ENV: ./attacker.sh\n"
			},
			want: "pins step must hold only id, name and run, found env",
		},
		{
			name:     "step before the pins step can write GITHUB_ENV",
			workflow: "security.yml",
			old:      toolPinsStepText,
			new: func(metadatautil.PinnedInputsConfig) string {
				return "      - run: echo BASH_ENV=./attacker.sh >> \"${GITHUB_ENV}\"\n" + toolPinsStepText
			},
			want: "must check out the repository as its first step and run the pins step right after it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := writePinnedInputsFixture(t)
			rewriteFile(t, filepath.Join(cfg.WorkflowsDir, tc.workflow), func(content string) string {
				if !strings.Contains(content, tc.old) {
					t.Fatalf("fixture %s does not contain %q", tc.workflow, tc.old)
				}
				return strings.Replace(content, tc.old, tc.new(cfg), 1)
			})
			requirePinnedInputsErrorContains(t, cfg, tc.want)
		})
	}
}

// TestCheckPinnedInputsRequiresToolPinLoaderAgreement proves the check runs
// the bash loader the workflows run and requires it to read the policy file
// exactly as the Go parser does.
func TestCheckPinnedInputsRequiresToolPinLoaderAgreement(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		file string
		edit func(string) string
		want string
	}{
		{
			name: "policy line only the loader rejects",
			file: "policy/tool-pins.toml",
			edit: func(content string) string {
				return regexp.MustCompile(`(?m)^(cosign = "[^"]+")$`).ReplaceAllString(content, "${1} ")
			},
			want: "scripts/ci/load-tool-pins.sh rejected policy/tool-pins.toml",
		},
		{
			name: "loader writes a different value",
			file: "scripts/ci/load-tool-pins.sh",
			edit: func(content string) string {
				return strings.Replace(content, `output+="${key}=${values[index]}"`, `output+="${key}=x${values[index]}"`, 1)
			},
			want: "output does not match the Go parse of policy/tool-pins.toml",
		},
		{
			name: "loader drops a key",
			file: "scripts/ci/load-tool-pins.sh",
			edit: func(content string) string {
				return strings.Replace(content, "  oras_linux_amd64_sha256\n)", ")", 1)
			},
			want: "unknown key oras_linux_amd64_sha256",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := writePinnedInputsFixture(t)
			rewriteFile(t, filepath.Join(fixtureRepoRoot(cfg), filepath.FromSlash(tc.file)), func(content string) string {
				updated := tc.edit(content)
				if updated == content {
					t.Fatalf("edit did not change %s", tc.file)
				}
				return updated
			})
			requirePinnedInputsErrorContains(t, cfg, tc.want)
		})
	}
}

// TestCheckPinnedInputsRejectsToolPinImageFromAnotherRepository proves each
// image pin must name its own reviewed repository, with no registry host or
// other prefix, in both the Go parse and the loader.
func TestCheckPinnedInputsRejectsToolPinImageFromAnotherRepository(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, key string
		value     func(current string) string
	}{
		{"buildkit from another registry", "buildkit", func(current string) string { return "ghcr.io/evil/" + current }},
		{"buildkit with the Docker Hub host", "buildkit", func(current string) string { return "docker.io/" + current }},
		{"buildkit lookalike repository", "buildkit", func(current string) string {
			return strings.Replace(current, "moby/buildkit:", "moby/buildkitx:", 1)
		}},
		{"buildkit set to the QEMU repository", "buildkit", func(current string) string {
			return strings.Replace(current, "moby/buildkit:", "tonistiigi/binfmt:", 1)
		}},
		{"qemu from another registry", "qemu", func(current string) string { return "ghcr.io/evil/" + current }},
		{"qemu set to the BuildKit repository", "qemu", func(current string) string {
			return strings.Replace(current, "tonistiigi/binfmt:", "moby/buildkit:", 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := writePinnedInputsFixture(t)
			current := fixtureToolPin(t, cfg, tc.key)
			value := tc.value(current)
			if value == current {
				t.Fatalf("mutation did not change the %s pin", tc.key)
			}
			rewriteFile(t, toolPinsFixturePath(cfg), func(content string) string {
				return strings.Replace(content, tc.key+` = "`+current+`"`, tc.key+` = "`+value+`"`, 1)
			})
			requirePinnedInputsErrorContains(t, cfg, "[tool_pins] "+tc.key+" must match ^"+regexp.QuoteMeta(map[string]string{"buildkit": "moby/buildkit", "qemu": "tonistiigi/binfmt"}[tc.key])+":")
		})
	}
}

// TestCheckPinnedInputsRejectsToolPinVersionWithLeadingZero proves a version
// part with a leading zero, a second spelling of the same version, fails in
// both the Go parse and the loader.
func TestCheckPinnedInputsRejectsToolPinVersionWithLeadingZero(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ key, value string }{
		{"oras_version", "01.3.3"},
		{"actionlint_version", "1.07.12"},
		{"zizmor_version", "1.31.01"},
		{"cosign", "v03.1.4"},
		{"syft", "v1.056.0"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Parallel()
			cfg := writePinnedInputsFixture(t)
			current := fixtureToolPin(t, cfg, tc.key)
			rewriteFile(t, toolPinsFixturePath(cfg), func(content string) string {
				return strings.Replace(content, tc.key+` = "`+current+`"`, tc.key+` = "`+tc.value+`"`, 1)
			})
			requirePinnedInputsErrorContains(t, cfg, "[tool_pins] "+tc.key+" must match ")
		})
	}
}

// TestCheckPinnedInputsRejectsValidatorBuildkitFallbackFromAnotherRepository
// proves the build-validator-image.sh fallback must be the policy value.
func TestCheckPinnedInputsRejectsValidatorBuildkitFallbackFromAnotherRepository(t *testing.T) {
	t.Parallel()

	cfg := writePinnedInputsFixture(t)
	rewriteFile(t, filepath.Join(fixtureRepoRoot(cfg), "scripts", "ci", "build-validator-image.sh"), func(content string) string {
		updated := strings.Replace(content, "${WORKCELL_BUILDKIT_IMAGE:-moby/buildkit:", "${WORKCELL_BUILDKIT_IMAGE:-ghcr.io/evil/moby/buildkit:", 1)
		if updated == content {
			t.Fatal("fixture build-validator-image.sh has no BuildKit fallback to mutate")
		}
		return updated
	})
	requirePinnedInputsErrorContains(t, cfg, "BuildKit image")
}

// TestCheckPinnedInputsBindsOrasPinsToTheSignerContract proves an ORAS pin
// change in policy/tool-pins.toml alone fails the reviewed signer contract.
func TestCheckPinnedInputsBindsOrasPinsToTheSignerContract(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ key, value string }{
		{"oras_version", "9.9.9"},
		{"oras_linux_amd64_sha256", strings.Repeat("0", 64)},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			cfg := writePinnedInputsFixture(t)
			current := fixtureToolPin(t, cfg, tc.key)
			rewriteFile(t, toolPinsFixturePath(cfg), func(content string) string {
				return strings.Replace(content, tc.key+` = "`+current+`"`, tc.key+` = "`+tc.value+`"`, 1)
			})
			requirePinnedInputsErrorContains(t, cfg, "sign-release must match the exact privileged step contract")
		})
	}
}
