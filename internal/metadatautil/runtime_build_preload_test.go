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
