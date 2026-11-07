package metadatautil_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

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
