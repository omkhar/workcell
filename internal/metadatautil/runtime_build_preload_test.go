// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckPinnedInputsRuntimeBuildPreload(t *testing.T) {
	if err := metadatautil.CheckPinnedInputs(writePinnedInputsFixture(t)); err != nil {
		t.Fatal(err)
	}
}

// A comment naming the guard variable assigns nothing, so the count is unchanged.
func TestCheckPinnedInputsAcceptsInertBuildPreloadMentions(t *testing.T) {
	const laterStage = "FROM runtime-base AS provider-builder\n"
	for name, rewrite := range map[string]func(string) string{
		"comment": func(body string) string {
			return body + "\n# LD_PRELOAD activation is reviewed above; do not add another assignment.\n"
		},
		"words in a command": func(body string) string {
			return strings.Replace(body, laterStage, laterStage+"\nRUN printf '%s\\n' export LD_PRELOAD\n", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := rewritePinnedInputsFixtureFile(t, "runtime/container/Dockerfile", func(body string) string {
				mutated := rewrite(body)
				if mutated == body {
					t.Fatalf("mention %q did not change the build file", name)
				}
				return mutated
			})
			if err := metadatautil.CheckPinnedInputs(cfg); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// ENV and export persist every key, so a key that hides the guard still overrides.
func TestCheckPinnedInputsRejectsMultiKeyBuildPreloadOverride(t *testing.T) {
	const canonicalExport = "  && export LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so \\\n"
	for name, rewrite := range map[string]func(string) string{
		"multi-key ENV": func(body string) string {
			return body + "\nENV MARKER=x LD_PRELOAD=/workspace/evil.so\n"
		},
		"legacy ENV syntax": func(body string) string {
			return body + "\nENV LD_PRELOAD /workspace/evil.so\n"
		},
		"key split across a continuation": func(body string) string {
			return body + "\nENV MARKER=x \\\n    LD_PRELOAD=/workspace/evil.so\n"
		},
		"key behind a comment inside a continuation": func(body string) string {
			return body + "\nENV MARKER=x \\\n# Docker drops this line before it joins the continuation.\n    LD_PRELOAD=/workspace/evil.so\n"
		},
		"multi-key export in the builder RUN": func(body string) string {
			return strings.Replace(body, canonicalExport,
				canonicalExport+"  && export MARKER=x LD_PRELOAD=/workspace/evil.so \\\n", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := rewritePinnedInputsFixtureFile(t, "runtime/container/Dockerfile", func(body string) string {
				mutated := rewrite(body)
				if mutated == body {
					t.Fatalf("override %q did not change the build file", name)
				}
				return mutated
			})
			requirePinnedInputsErrorContains(t, cfg, "early runtime build preload")
		})
	}
}

func TestCheckPinnedInputsRejectsLateBuildPreload(t *testing.T) {
	preload := "LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so"
	for _, index := range []int{0, 1} {
		for _, replacement := range []string{"", "# ENV " + preload + "\n", "ENV LD_PRELOAD=/tmp/guard.so\n"} {
			t.Run(fmt.Sprintf("%d/%s", index, replacement), func(t *testing.T) {
				cfg := rewritePinnedInputsFixtureFile(t, "runtime/container/Dockerfile", func(body string) string {
					parts := strings.Split(body, "ENV "+preload+"\n")
					if len(parts) != 3 {
						t.Fatal("expected exactly two stage preload declarations")
					}
					parts[index] += replacement + parts[index+1]
					parts = append(parts[:index+1], parts[index+2:]...)
					return strings.Join(parts, "ENV "+preload+"\n") + "\nENV " + preload + "\n"
				})
				requirePinnedInputsErrorContains(t, cfg, "early runtime build preload")
			})
		}
	}
}

func TestCheckPinnedInputsRejectsBuildPreloadOverrides(t *testing.T) {
	preload := "LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so"
	for name, rewrite := range map[string]func(string) string{
		"missing inline export": func(body string) string {
			return strings.Replace(body, "  && export "+preload+" \\\n", "", 1)
		},
		"wrong inline export": func(body string) string {
			return strings.Replace(body, "export "+preload, "export LD_PRELOAD=/tmp/guard.so", 1)
		},
		"later override": func(body string) string {
			return body + "\nENV LD_PRELOAD=/tmp/guard.so\n"
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := rewritePinnedInputsFixtureFile(t, "runtime/container/Dockerfile", rewrite)
			requirePinnedInputsErrorContains(t, cfg, "early runtime build preload")
		})
	}
}
