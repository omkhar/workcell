// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin || linux

package release

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	testTagObjectSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testTagCommitSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestClassifyTag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tag  string
		want TagPolicy
	}{
		{tag: "v0.0.0", want: TagPolicy{Kind: "final", MakeLatest: true}},
		{tag: "v1.0.0", want: TagPolicy{Kind: "final", MakeLatest: true}},
		{tag: "v1.0.0-rc.3", want: TagPolicy{Kind: "rc", Prerelease: true}},
		{tag: "v10.20.30-rc.40", want: TagPolicy{Kind: "rc", Prerelease: true}},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			t.Parallel()
			got, err := ClassifyTag(tc.tag)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("ClassifyTag() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestClassifyTagRejectsUnreviewedClasses(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{
		"", "1.0.0", "v01.0.0", "v1.00.0", "v1.0.00", "v1.0",
		"v1.0.0-beta.1", "v1.0.0+build", "v1.0.0-rc", "v1.0.0-rc.0",
		"v1.0.0-rc.01", "v1.0.0-rc.1.extra", " v1.0.0", "v1.0.0\n",
	} {
		t.Run(fmt.Sprintf("%q", tag), func(t *testing.T) {
			t.Parallel()
			_, err := ClassifyTag(tag)
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "expected vMAJOR.MINOR.PATCH") {
				t.Fatalf("ClassifyTag(%q) error = %v", tag, err)
			}
		})
	}
}

func TestValidateRepository(t *testing.T) {
	t.Parallel()
	for _, repository := range []string{
		"o/r",
		"example/workcell",
		"Example-Owner/workcell.release_v1",
		strings.Repeat("a", 39) + "/" + strings.Repeat("b", 100),
	} {
		t.Run(repository, func(t *testing.T) {
			t.Parallel()
			if err := validateRepository(repository); err != nil {
				t.Fatalf("validateRepository(%q) error = %v", repository, err)
			}
		})
	}
}

func TestValidateRepositoryRejectsUnsafeInput(t *testing.T) {
	t.Parallel()
	for _, repository := range []string{
		"", "owner", "/repo", "owner/", "owner/repo/extra", "-owner/repo",
		"owner-/repo", "owner/.", "owner/..", "owner/repo name", "owner/repo?x",
		strings.Repeat("a", 40) + "/repo",
		"owner/" + strings.Repeat("b", 101),
	} {
		t.Run(fmt.Sprintf("%q", repository), func(t *testing.T) {
			t.Parallel()
			err := validateRepository(repository)
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "safe OWNER/REPO") {
				t.Fatalf("validateRepository(%q) error = %v", repository, err)
			}
		})
	}
}

func TestValidateToolchain(t *testing.T) {
	t.Parallel()
	for _, toolchain := range []string{"go0.0.0", "go1.26.5", "go10.20.30"} {
		t.Run(toolchain, func(t *testing.T) {
			t.Parallel()
			if err := validateToolchain(toolchain); err != nil {
				t.Fatalf("validateToolchain(%q) error = %v", toolchain, err)
			}
		})
	}
}

func TestValidateToolchainRejectsInexactInput(t *testing.T) {
	t.Parallel()
	for _, toolchain := range []string{
		"", "auto", "1.26.5", "go1.26", "go01.26.5", "go1.026.5",
		"go1.26.05", "go1.26.5-rc.1", "go1.26.5 linux/arm64", " go1.26.5",
	} {
		t.Run(fmt.Sprintf("%q", toolchain), func(t *testing.T) {
			t.Parallel()
			err := validateToolchain(toolchain)
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "exact goX.Y.Z") {
				t.Fatalf("validateToolchain(%q) error = %v", toolchain, err)
			}
		})
	}
}

func TestValidateTagExpectation(t *testing.T) {
	t.Parallel()
	expected := TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}
	if err := validateTagExpectation(expected); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTagExpectationRejectsInvalidSHAs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*TagExpectation)
		want   string
	}{
		{name: "empty object", mutate: func(value *TagExpectation) { value.ObjectSHA = "" }, want: "annotated tag object SHA"},
		{name: "short object", mutate: func(value *TagExpectation) { value.ObjectSHA = strings.Repeat("a", 39) }, want: "annotated tag object SHA"},
		{name: "long object", mutate: func(value *TagExpectation) { value.ObjectSHA = strings.Repeat("a", 41) }, want: "annotated tag object SHA"},
		{name: "uppercase object", mutate: func(value *TagExpectation) { value.ObjectSHA = strings.Repeat("A", 40) }, want: "annotated tag object SHA"},
		{name: "nonhex object", mutate: func(value *TagExpectation) { value.ObjectSHA = strings.Repeat("g", 40) }, want: "annotated tag object SHA"},
		{name: "empty peel", mutate: func(value *TagExpectation) { value.PeeledCommitSHA = "" }, want: "peeled tag commit SHA"},
		{name: "short peel", mutate: func(value *TagExpectation) { value.PeeledCommitSHA = strings.Repeat("b", 39) }, want: "peeled tag commit SHA"},
		{name: "long peel", mutate: func(value *TagExpectation) { value.PeeledCommitSHA = strings.Repeat("b", 41) }, want: "peeled tag commit SHA"},
		{name: "uppercase peel", mutate: func(value *TagExpectation) { value.PeeledCommitSHA = strings.Repeat("B", 40) }, want: "peeled tag commit SHA"},
		{name: "nonhex peel", mutate: func(value *TagExpectation) { value.PeeledCommitSHA = strings.Repeat("z", 40) }, want: "peeled tag commit SHA"},
		{name: "object equals peel", mutate: func(value *TagExpectation) { value.PeeledCommitSHA = value.ObjectSHA }, want: "must differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expected := TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}
			tc.mutate(&expected)
			err := validateTagExpectation(expected)
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateTagExpectation() error = %v", err)
			}
		})
	}
}

func TestValidateTagBinding(t *testing.T) {
	t.Parallel()
	tag := "v1.0.0-rc.3"
	expected := TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}
	observed := GitHubTagBinding{
		Ref:             "refs/tags/" + tag,
		ObjectType:      "tag",
		ObjectSHA:       testTagObjectSHA,
		PeeledCommitSHA: testTagCommitSHA,
	}
	if err := validateTagBinding(tag, expected, observed); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTagBindingRejectsEveryMismatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*GitHubTagBinding)
		want   string
	}{
		{name: "missing ref", mutate: func(value *GitHubTagBinding) { value.Ref = "" }, want: "tag ref"},
		{name: "wrong ref", mutate: func(value *GitHubTagBinding) { value.Ref = "refs/tags/v1.0.1" }, want: "tag ref"},
		{name: "lightweight tag", mutate: func(value *GitHubTagBinding) { value.ObjectType = "commit" }, want: "annotated tag object type"},
		{name: "missing type", mutate: func(value *GitHubTagBinding) { value.ObjectType = "" }, want: "annotated tag object type"},
		{name: "wrong object", mutate: func(value *GitHubTagBinding) { value.ObjectSHA = strings.Repeat("c", 40) }, want: "annotated tag object SHA"},
		{name: "malformed object", mutate: func(value *GitHubTagBinding) { value.ObjectSHA = "bad" }, want: "annotated tag object SHA"},
		{name: "wrong peel", mutate: func(value *GitHubTagBinding) { value.PeeledCommitSHA = strings.Repeat("d", 40) }, want: "peeled tag commit SHA"},
		{name: "malformed peel", mutate: func(value *GitHubTagBinding) { value.PeeledCommitSHA = "bad" }, want: "peeled tag commit SHA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tag := "v1.0.0"
			expected := TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}
			observed := GitHubTagBinding{
				Ref:             "refs/tags/" + tag,
				ObjectType:      "tag",
				ObjectSHA:       testTagObjectSHA,
				PeeledCommitSHA: testTagCommitSHA,
			}
			tc.mutate(&observed)
			err := validateTagBinding(tag, expected, observed)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateTagBinding() error = %v, want %q", err, tc.want)
			}
			if errors.Is(err, ErrInvalidInput) {
				t.Fatalf("observed hosted-state mismatch was classified as caller input: %v", err)
			}
		})
	}
}

func TestValidateTagBindingRejectsInvalidExpectation(t *testing.T) {
	t.Parallel()
	err := validateTagBinding("v1.0.0", TagExpectation{}, GitHubTagBinding{})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "annotated tag object SHA") {
		t.Fatalf("validateTagBinding() error = %v", err)
	}
}

func TestValidateTagBindingRejectsInvalidTag(t *testing.T) {
	t.Parallel()
	tag := "v1.0.0-beta.1"
	err := validateTagBinding(tag, TagExpectation{ObjectSHA: testTagObjectSHA, PeeledCommitSHA: testTagCommitSHA}, GitHubTagBinding{
		Ref:             "refs/tags/" + tag,
		ObjectType:      "tag",
		ObjectSHA:       testTagObjectSHA,
		PeeledCommitSHA: testTagCommitSHA,
	})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "unsupported release tag") {
		t.Fatalf("validateTagBinding() error = %v", err)
	}
}

func TestWorkcellAssetManifestV1Inventory(t *testing.T) {
	t.Parallel()
	tag := "v1.0.0-rc.3"
	want := []string{
		"workcell-v1.0.0-rc.3.tar.gz", "workcell-v1.0.0-rc.3.tar.gz.sigstore.json",
		"workcell.rb", "workcell.rb.sigstore.json",
		"workcell-image.digest", "workcell-image.digest.sigstore.json",
		"workcell-build-inputs.json", "workcell-build-inputs.sigstore.json",
		"workcell-control-plane.json", "workcell-control-plane.sigstore.json",
		"workcell-builder-environment.json", "workcell-builder-environment.sigstore.json",
		"SHA256SUMS", "SHA256SUMS.sigstore.json",
		"workcell-source.spdx.json", "workcell-source.spdx.sigstore.json",
		"workcell-image.spdx.json", "workcell-image.spdx.sigstore.json",
	}
	if WorkcellAssetManifestSchemaV1 != "workcell.release-assets.v1" {
		t.Fatalf("asset manifest schema = %q", WorkcellAssetManifestSchemaV1)
	}
	if len(want) != maxReleaseAssetCount {
		t.Fatalf("manifest contains %d assets, want foundation limit %d", len(want), maxReleaseAssetCount)
	}
	if got := expectedWorkcellAssetNames(tag); !reflect.DeepEqual(got, want) {
		t.Fatalf("expectedWorkcellAssetNames() = %#v, want %#v", got, want)
	}
}

func TestAssetInventoryMatchesBothAuthoritativeWorkflowLists(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	tag := "v1.0.0-rc.3"
	want := expectedWorkcellAssetNames(tag)
	got, err := parseWorkflowAssetInventories(string(data), tag)
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"artifact", "publisher"} {
		if !reflect.DeepEqual(got[label], want) {
			t.Fatalf("%s release.yml inventory = %#v, want %#v", label, got[label], want)
		}
	}
}

func TestWorkflowInventoryParserAcceptsBothBundlePlaceholderForms(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "shell placeholder in artifact",
			old:  "            dist/${{ env.BUNDLE_NAME }}\n            dist/${{ env.BUNDLE_NAME }}.sigstore.json",
			new:  "            dist/${BUNDLE_NAME}\n            dist/${BUNDLE_NAME}.sigstore.json",
		},
		{
			name: "expression placeholder in publisher",
			old:  "            \"dist/${BUNDLE_NAME}\" \\\n            \"dist/${BUNDLE_NAME}.sigstore.json\" \\",
			new:  "            \"dist/${{ env.BUNDLE_NAME }}\" \\\n            \"dist/${{ env.BUNDLE_NAME }}.sigstore.json\" \\",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marker := "- name: Upload workflow artifacts"
			if tc.name == "expression placeholder in publisher" {
				marker = `./scripts/publish-github-release.sh "${GITHUB_REF_NAME}"`
			}
			mutated := replaceWorkflowSection(workflow, marker, tc.old, tc.new)
			if mutated == workflow {
				t.Fatal("test mutation did not change workflow")
			}
			if _, err := parseWorkflowAssetInventories(mutated, "v1.0.0"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkflowInventoryParserRejectsMutations(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	for _, tc := range []struct {
		name   string
		mutate func(string) string
		want   string
	}{
		{
			name: "missing asset",
			mutate: func(value string) string {
				return replaceWorkflowSection(value, "- name: Upload workflow artifacts", "            dist/workcell.rb\n", "")
			},
			want: "does not match",
		},
		{
			name: "extra asset",
			mutate: func(value string) string {
				return replaceWorkflowSection(value, "- name: Upload workflow artifacts", "          retention-days: 7", "            dist/unexpected.bin\n          retention-days: 7")
			},
			want: "does not match",
		},
		{
			name: "unresolved placeholder",
			mutate: func(value string) string {
				return replaceWorkflowSection(value, `./scripts/publish-github-release.sh "${GITHUB_REF_NAME}"`, "            \"dist/${BUNDLE_NAME}\" \\\n", "            \"dist/${UNRESOLVED_BUNDLE}\" \\\n")
			},
			want: "unresolved placeholder",
		},
		{
			name: "duplicate artifact step",
			mutate: func(value string) string {
				return value + "\n      - name: Upload workflow artifacts\n        with:\n          path: |\n            dist/workcell.rb\n          retention-days: 7\n"
			},
			want: "exactly one artifact inventory",
		},
		{
			name: "duplicate publisher invocation",
			mutate: func(value string) string {
				return value + "\n          ./scripts/publish-github-release.sh \"${GITHUB_REF_NAME}\" \\\n            dist/workcell.rb\n"
			},
			want: "exactly one publisher inventory",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mutate(workflow)
			if mutated == workflow {
				t.Fatal("test mutation did not change workflow")
			}
			_, err := parseWorkflowAssetInventories(mutated, "v1.0.0")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseWorkflowAssetInventories() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOrderWorkcellAssetPathsCanonicalizesCallerOrder(t *testing.T) {
	t.Parallel()
	tag := "v1.0.0"
	paths, _ := writeWorkcellAssetFixture(t, tag)
	want := append([]string(nil), paths...)
	reverseStrings(paths)
	input := append([]string(nil), paths...)

	got, err := orderWorkcellAssetPaths(tag, paths)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orderWorkcellAssetPaths() = %#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(paths, input) {
		t.Fatalf("orderWorkcellAssetPaths() mutated caller paths: got %#v, want %#v", paths, input)
	}
}

func TestOrderWorkcellAssetPathsRejectsUnsealedInventories(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, []string) []string
		want   string
	}{
		{name: "missing", mutate: func(_ *testing.T, paths []string) []string {
			return append([]string(nil), paths[:len(paths)-1]...)
		}, want: "missing required basename"},
		{name: "extra", mutate: func(t *testing.T, paths []string) []string {
			return append(append([]string(nil), paths...), filepath.Join(t.TempDir(), "unexpected.bin"))
		}, want: "unexpected basename"},
		{name: "duplicate", mutate: func(t *testing.T, paths []string) []string {
			duplicate := filepath.Join(t.TempDir(), filepath.Base(paths[0]))
			return append(append([]string(nil), paths...), duplicate)
		}, want: "duplicate release asset basename"},
		{name: "wrong", mutate: func(t *testing.T, paths []string) []string {
			changed := append([]string(nil), paths...)
			changed[0] = filepath.Join(t.TempDir(), "wrong-name.bin")
			return changed
		}, want: "unexpected basename"},
		{name: "unsafe", mutate: func(t *testing.T, paths []string) []string {
			changed := append([]string(nil), paths...)
			changed[0] = filepath.Join(t.TempDir(), "asset &=%#? +two.bin")
			return changed
		}, want: "not GitHub-safe"},
		{name: "empty list", mutate: func(_ *testing.T, _ []string) []string {
			return nil
		}, want: "missing required basename"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			paths, _ := writeWorkcellAssetFixture(t, "v1.0.0")
			errPaths := tc.mutate(t, paths)
			_, err := orderWorkcellAssetPaths("v1.0.0", errPaths)
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("orderWorkcellAssetPaths() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestInspectWorkcellAssetsRejectsInvalidInputBeforeOpening(t *testing.T) {
	t.Parallel()
	validPaths, _ := writeWorkcellAssetFixture(t, "v1.0.0")
	for _, tc := range []struct {
		name  string
		tag   string
		paths []string
	}{
		{name: "invalid tag", tag: "v1.0.0-beta.1", paths: validPaths},
		{name: "missing asset", tag: "v1.0.0", paths: validPaths[:len(validPaths)-1]},
		{name: "extra asset", tag: "v1.0.0", paths: append(append([]string(nil), validPaths...), filepath.Join(t.TempDir(), "extra.bin"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			_, err := inspectWorkcellAssetsWithOpener(tc.tag, tc.paths, foundationOpenerFunc(func(string) (assetSource, error) {
				opens++
				return nil, errors.New("unexpected open")
			}))
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("inspectWorkcellAssetsWithOpener() error = %v", err)
			}
			if opens != 0 {
				t.Fatalf("opener calls = %d, want 0", opens)
			}
		})
	}
}

func TestInspectWorkcellAssetsHashesCanonicalRegularFiles(t *testing.T) {
	t.Parallel()
	tag := "v1.0.0-rc.3"
	paths, contents := writeWorkcellAssetFixture(t, tag)
	reverseStrings(paths)

	assets, err := inspectWorkcellAssets(tag, paths)
	if err != nil {
		t.Fatal(err)
	}
	closeAssetsAtCleanup(t, assets)
	wantNames := expectedWorkcellAssetNames(tag)
	if len(assets) != len(wantNames) {
		t.Fatalf("inspectWorkcellAssets() returned %d assets, want %d", len(assets), len(wantNames))
	}
	for i, asset := range assets {
		name := wantNames[i]
		wantDigest := fmt.Sprintf("%x", sha256.Sum256(contents[name]))
		if asset.name != name || asset.size != int64(len(contents[name])) || asset.sha256 != wantDigest {
			t.Fatalf("asset %d = %#v, want name=%q size=%d sha256=%q", i, asset, name, len(contents[name]), wantDigest)
		}
		reader, err := rewindLocalAssetReader(&assets[i])
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, contents[name]) {
			t.Fatalf("asset %q staged content = %q, want %q", name, got, contents[name])
		}
		if stage, ok := asset.content.(*os.File); !ok {
			t.Fatalf("asset %q content type = %T, want *os.File", name, asset.content)
		} else if _, err := os.Stat(stage.Name()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("asset %q stage remains linked at %q: %v", name, stage.Name(), err)
		}
	}
}

func writeWorkcellAssetFixture(t *testing.T, tag string) ([]string, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	names := expectedWorkcellAssetNames(tag)
	paths := make([]string, 0, len(names))
	contents := make(map[string][]byte, len(names))
	for i, name := range names {
		content := []byte(fmt.Sprintf("asset-%02d-%s\n", i, name))
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		contents[name] = content
	}
	return paths, contents
}

func reverseStrings(values []string) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func replaceWorkflowSection(workflow, marker, old, replacement string) string {
	index := strings.Index(workflow, marker)
	if index < 0 {
		return workflow
	}
	return workflow[:index] + strings.Replace(workflow[index:], old, replacement, 1)
}

func parseWorkflowAssetInventories(workflow, tag string) (map[string][]string, error) {
	if _, err := ClassifyTag(tag); err != nil {
		return nil, err
	}
	inventories := map[string][]string{"artifact": {}, "publisher": {}}
	section := ""
	bundle := "workcell-" + tag + ".tar.gz"
	replacer := strings.NewReplacer(
		"${{ env.BUNDLE_NAME }}", bundle,
		"${BUNDLE_NAME}", bundle,
	)
	artifactInventories := 0
	publisherInventories := 0
	appendAsset := func(label, line string) error {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\\"))
		line = strings.Trim(line, "\"")
		line = strings.TrimPrefix(line, "dist/")
		line = replacer.Replace(line)
		if strings.Contains(line, "${") || strings.Contains(line, "}}") {
			return fmt.Errorf("%s release workflow inventory contains unresolved placeholder %q", label, line)
		}
		inventories[label] = append(inventories[label], line)
		return nil
	}
	for lineNumber, line := range strings.Split(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "- name: Upload workflow artifacts":
			artifactInventories++
			section = "artifact-pending"
		case section == "artifact-pending" && trimmed == "path: |":
			section = "artifact"
		case section == "artifact" && strings.HasPrefix(trimmed, "dist/"):
			if err := appendAsset("artifact", trimmed); err != nil {
				return nil, fmt.Errorf("release workflow line %d: %w", lineNumber+1, err)
			}
		case section == "artifact" && strings.HasPrefix(trimmed, "retention-days:"):
			section = ""
		case strings.HasPrefix(trimmed, `./scripts/publish-github-release.sh "${GITHUB_REF_NAME}"`):
			publisherInventories++
			section = "publisher"
		case section == "publisher":
			if strings.Contains(trimmed, "dist/") {
				if err := appendAsset("publisher", trimmed); err != nil {
					return nil, fmt.Errorf("release workflow line %d: %w", lineNumber+1, err)
				}
			}
			if !strings.HasSuffix(trimmed, "\\") {
				section = ""
			}
		}
	}
	if artifactInventories != 1 {
		return nil, fmt.Errorf("release workflow contains %d artifact inventories, want exactly one artifact inventory", artifactInventories)
	}
	if publisherInventories != 1 {
		return nil, fmt.Errorf("release workflow contains %d publisher invocations, want exactly one publisher inventory", publisherInventories)
	}
	want := expectedWorkcellAssetNames(tag)
	for _, label := range []string{"artifact", "publisher"} {
		if !reflect.DeepEqual(inventories[label], want) {
			return nil, fmt.Errorf("%s release workflow inventory does not match %s: got %q, want %q", label, WorkcellAssetManifestSchemaV1, inventories[label], want)
		}
	}
	return inventories, nil
}

func closeAssetsAtCleanup(t *testing.T, assets []localAsset) {
	t.Helper()
	t.Cleanup(func() {
		if err := closeLocalAssets(assets); err != nil {
			t.Errorf("closeLocalAssets() error = %v", err)
		}
	})
}
