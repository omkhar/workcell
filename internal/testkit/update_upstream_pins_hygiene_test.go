// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func updaterHygieneProviderPlan(t *testing.T, generatedAt time.Time) string {
	t.Helper()

	plan, err := json.Marshal(map[string]any{
		"generated_at":  generatedAt.UTC().Format(time.RFC3339),
		"cutoff":        generatedAt.UTC().Add(-48 * time.Hour).Format(time.RFC3339),
		"cooloff_hours": 48,
		"has_changes":   false,
		"providers": map[string]any{
			"codex": map[string]any{"channel": "stable", "current_version": "0.169.0", "target_version": "0.169.0", "changed": false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(plan)
}

func updaterHygieneRefreshPR(t *testing.T, kind, headRepository string, createdAt time.Time) map[string]any {
	t.Helper()

	return map[string]any{
		"title":      "Refresh pinned upstreams (" + kind + ")",
		"html_url":   "https://github.com/omkhar/workcell/pull/4242",
		"created_at": createdAt.UTC().Format(time.RFC3339),
		"head":       map[string]any{"ref": "codex/upstream-refresh-" + kind + "-4242", "repo": map[string]any{"full_name": headRepository}},
		"base":       map[string]any{"ref": "main", "repo": map[string]any{"full_name": "omkhar/workcell"}},
	}
}

func updaterHygieneJSON(t *testing.T, value any) string {
	t.Helper()

	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// TestUpdateUpstreamPinsHygieneJudgesDriftAge proves that --hygiene passes
// toolchain drift that the last refresh is too recent to have missed or that
// a fresh refresh PR carries, fails drift that outlived the grace, and fails
// closed when it cannot read the refresh PRs or the lagged provider plan.
func TestUpdateUpstreamPinsHygieneJudgesDriftAge(t *testing.T) {
	t.Parallel()

	pins := readUpdaterFixturePins(t)
	plan := updaterTargetDebianPlan()
	drifted := updaterDriftedManifest(t, plan.Snapshot)
	snapshot, err := time.Parse("20060102T150405Z", drifted.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	toolsRoot := t.TempDir()
	citoolsPath := buildUpdaterFixtureCITools(t, toolsRoot)
	goWrapperPath := writeUpdaterFixtureGoWrapper(t, toolsRoot)
	fixtureRoot := writeUpdaterFixture(t, drifted, 0o640)

	const grace = 72 * time.Hour
	fresh := snapshot.Add(30 * time.Hour)
	stale := snapshot.Add(100 * time.Hour)
	fullPage := make([]map[string]any, 100)
	for index := range fullPage {
		fullPage[index] = updaterHygieneRefreshPR(t, "docs", "omkhar/workcell", stale)
		fullPage[index]["head"] = map[string]any{"ref": fmt.Sprintf("topic-%d", index), "repo": map[string]any{"full_name": "omkhar/workcell"}}
	}

	for _, testCase := range []struct {
		name      string
		now       time.Time
		openPRs   string
		extraEnv  []string
		extraArgs []string
		wantCode  int
		wantIn    []string
		wantNotIn []string
		wantLog   bool
	}{
		{
			name:     "toolchain drift within the grace passes",
			now:      fresh,
			openPRs:  `[]`,
			wantCode: 0,
			wantIn:   []string{"toolchain: update pending, expected", "30h old, within the grace", "Pin hygiene passed"},
			wantLog:  true,
		},
		{
			name:      "toolchain drift past the grace with no refresh PR fails",
			now:       stale,
			openPRs:   `[]`,
			wantCode:  1,
			wantIn:    []string{"PROBLEM: a toolchain update is pending", "100h old, past the grace", "no open toolchain refresh PR"},
			wantNotIn: []string{"Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:     "toolchain drift past the grace carried by a fresh refresh PR passes",
			now:      stale,
			openPRs:  updaterHygieneJSON(t, []any{updaterHygieneRefreshPR(t, "toolchain", "omkhar/workcell", stale.Add(-time.Hour))}),
			wantCode: 0,
			wantIn:   []string{"carried by the open toolchain refresh PR https://github.com/omkhar/workcell/pull/4242", "Pin hygiene passed"},
			wantLog:  true,
		},
		{
			name:      "a fork PR on a refresh branch name carries nothing",
			now:       stale,
			openPRs:   updaterHygieneJSON(t, []any{updaterHygieneRefreshPR(t, "toolchain", "someone/workcell", stale.Add(-time.Hour))}),
			wantCode:  1,
			wantIn:    []string{"no open toolchain refresh PR"},
			wantNotIn: []string{"pull/4242"},
			wantLog:   true,
		},
		{
			name:      "a provider refresh PR does not carry toolchain drift",
			now:       stale,
			openPRs:   updaterHygieneJSON(t, []any{updaterHygieneRefreshPR(t, "provider", "omkhar/workcell", stale.Add(-time.Hour))}),
			wantCode:  1,
			wantIn:    []string{"PROBLEM: a toolchain update is pending", "open provider refresh PR https://github.com/omkhar/workcell/pull/4242"},
			wantNotIn: []string{"Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:      "an unreadable pull request list fails",
			now:       fresh,
			extraEnv:  []string{"WORKCELL_FIXTURE_OPEN_PRS_CODE=1"},
			wantCode:  1,
			wantIn:    []string{"Unable to list the open pull requests of omkhar/workcell."},
			wantNotIn: []string{"Pin hygiene verdict", "Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:      "a full page of pull requests fails",
			now:       fresh,
			openPRs:   updaterHygieneJSON(t, fullPage),
			wantCode:  1,
			wantIn:    []string{"may be incomplete", "Unable to read the open refresh pull requests of omkhar/workcell."},
			wantNotIn: []string{"Pin hygiene verdict", "Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:      "a pull request list that is not an array fails",
			now:       fresh,
			openPRs:   `{"message":"Not Found"}`,
			wantCode:  1,
			wantIn:    []string{"Unable to read the open refresh pull requests of omkhar/workcell."},
			wantNotIn: []string{"Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:      "a failed lagged provider plan fails",
			now:       fresh,
			openPRs:   `[]`,
			extraEnv:  []string{"WORKCELL_FIXTURE_LAGGED_PROVIDER_PLAN_CODE=1"},
			wantCode:  1,
			wantIn:    []string{"fixture lagged provider plan failure", "Unable to compute the provider bump plan as of " + fresh.Add(-grace).Format(time.RFC3339) + "."},
			wantNotIn: []string{"Pin hygiene passed"},
		},
		{
			name:      "hygiene refuses the toolchain-only scope",
			now:       fresh,
			openPRs:   `[]`,
			extraArgs: []string{"--toolchain-only"},
			wantCode:  2,
			wantIn:    []string{"--hygiene judges every pin at the live upstream state"},
		},
		{
			name:     "hygiene refuses a zero grace",
			now:      fresh,
			openPRs:  `[]`,
			extraEnv: []string{"WORKCELL_PIN_HYGIENE_GRACE_HOURS=0"},
			wantCode: 2,
			wantIn:   []string{"WORKCELL_PIN_HYGIENE_GRACE_HOURS must be an integer from 1 through 720"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			options := defaultUpdaterFixtureOptions()
			options.ProviderPlan = updaterHygieneProviderPlan(t, testCase.now)
			options.ExtraArgs = testCase.extraArgs
			options.ExtraEnv = append([]string{
				fmt.Sprintf("WORKCELL_FIXTURE_NOW_EPOCH=%d", testCase.now.Unix()),
				"WORKCELL_FIXTURE_LAGGED_PROVIDER_PLAN=" + updaterHygieneProviderPlan(t, testCase.now.Add(-grace)),
				"WORKCELL_FIXTURE_OPEN_PRS=" + testCase.openPRs,
			}, testCase.extraEnv...)
			run := runUpdaterFixtureWithOptions(t, fixtureRoot, t.TempDir(), citoolsPath, goWrapperPath, pins, plan, "--hygiene", options)
			if run.Code != testCase.wantCode {
				t.Fatalf("update-upstream-pins --hygiene exit code = %d, want %d\n%s", run.Code, testCase.wantCode, run.Output)
			}
			for _, want := range testCase.wantIn {
				if !strings.Contains(run.Output, want) {
					t.Errorf("update-upstream-pins --hygiene output lacks %q\n%s", want, run.Output)
				}
			}
			for _, unwanted := range testCase.wantNotIn {
				if strings.Contains(run.Output, unwanted) {
					t.Errorf("update-upstream-pins --hygiene output holds %q\n%s", unwanted, run.Output)
				}
			}
			if testCase.wantLog {
				want := []string{"summary", "json", "json-now " + testCase.now.Add(-grace).Format(time.RFC3339)}
				if !reflect.DeepEqual(run.ProviderLog, want) {
					t.Fatalf("provider updater command log = %q, want %q", run.ProviderLog, want)
				}
			}
		})
	}
}
