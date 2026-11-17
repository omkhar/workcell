// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const updaterHygieneRefreshApp = "workcell-upstream-refresh[bot]"

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
		"draft":      false,
		"user":       map[string]any{"login": updaterHygieneRefreshApp},
		"head":       map[string]any{"ref": "codex/upstream-refresh-" + kind + "-4242", "repo": map[string]any{"full_name": headRepository}},
		"base":       map[string]any{"ref": "main", "repo": map[string]any{"full_name": "omkhar/workcell"}},
	}
}

// updaterHygieneMergedPR is a closed toolchain refresh PR that merged at
// mergedAt.
func updaterHygieneMergedPR(t *testing.T, mergedAt time.Time) map[string]any {
	t.Helper()

	pr := updaterHygieneRefreshPR(t, "toolchain", "omkhar/workcell", mergedAt.Add(-time.Hour))
	pr["merged_at"] = mergedAt.UTC().Format(time.RFC3339)
	return pr
}

func updaterHygieneWith(pr map[string]any, key string, value any) map[string]any {
	pr[key] = value
	return pr
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
	lastRefresh := updaterHygieneJSON(t, []any{updaterHygieneMergedPR(t, snapshot.Add(14*time.Hour))})
	fullPage := make([]map[string]any, 100)
	for index := range fullPage {
		fullPage[index] = updaterHygieneRefreshPR(t, "docs", "omkhar/workcell", stale)
		fullPage[index]["head"] = map[string]any{"ref": fmt.Sprintf("topic-%d", index), "repo": map[string]any{"full_name": "omkhar/workcell"}}
	}

	for _, testCase := range []struct {
		name      string
		now       time.Time
		openPRs   string
		closedPRs string
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
			wantIn:   []string{"toolchain: update pending, expected", "toolchain refresh PR merged " + snapshot.Add(14*time.Hour).Format(time.RFC3339) + ") is 30h old, within the grace", "Pin hygiene passed"},
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
			name:      "a snapshot-only bump does not hide older toolchain drift",
			now:       fresh,
			openPRs:   `[]`,
			closedPRs: updaterHygieneJSON(t, []any{updaterHygieneMergedPR(t, snapshot.Add(-60*time.Hour))}),
			wantCode:  1,
			wantIn:    []string{"toolchain refresh PR merged " + snapshot.Add(-60*time.Hour).Format(time.RFC3339) + ") is 90h old, past the grace"},
			wantNotIn: []string{"Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:    "merged PRs that are not toolchain refreshes from the app or the owner leave the toolchain age unknown",
			now:     fresh,
			openPRs: `[]`,
			closedPRs: updaterHygieneJSON(t, []any{
				updaterHygieneWith(updaterHygieneMergedPR(t, snapshot.Add(14*time.Hour)), "user", map[string]any{"login": "someone"}),
				updaterHygieneWith(updaterHygieneMergedPR(t, snapshot.Add(15*time.Hour)), "head", map[string]any{"ref": "codex/upstream-refresh-toolchain-1", "repo": map[string]any{"full_name": "someone/workcell"}}),
				updaterHygieneWith(updaterHygieneMergedPR(t, snapshot.Add(16*time.Hour)), "head", map[string]any{"ref": "codex/upstream-refresh-provider-1", "repo": map[string]any{"full_name": "omkhar/workcell"}}),
				updaterHygieneWith(updaterHygieneMergedPR(t, snapshot.Add(17*time.Hour)), "merged_at", nil),
			}),
			wantCode:  1,
			wantIn:    []string{"no merged toolchain refresh PR found) is of unknown age, past the grace"},
			wantNotIn: []string{"Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:     "an owner's toolchain refresh PR carries drift",
			now:      stale,
			openPRs:  updaterHygieneJSON(t, []any{updaterHygieneWith(updaterHygieneRefreshPR(t, "toolchain", "omkhar/workcell", stale.Add(-time.Hour)), "user", map[string]any{"login": "omkhar"})}),
			wantCode: 0,
			wantIn:   []string{"toolchain refresh PR merged " + snapshot.Add(14*time.Hour).Format(time.RFC3339) + "), 100h old, carried by the open toolchain refresh PR https://github.com/omkhar/workcell/pull/4242", "Pin hygiene passed"},
			wantLog:  true,
		},
		{
			name:      "a draft refresh PR carries nothing",
			now:       stale,
			openPRs:   updaterHygieneJSON(t, []any{updaterHygieneWith(updaterHygieneRefreshPR(t, "toolchain", "omkhar/workcell", stale.Add(-time.Hour)), "draft", true)}),
			wantCode:  1,
			wantIn:    []string{"no open toolchain refresh PR"},
			wantNotIn: []string{"pull/4242"},
			wantLog:   true,
		},
		{
			name:      "a refresh PR that does not say it is ready carries nothing",
			now:       stale,
			openPRs:   updaterHygieneJSON(t, []any{updaterHygieneWith(updaterHygieneRefreshPR(t, "toolchain", "omkhar/workcell", stale.Add(-time.Hour)), "draft", nil)}),
			wantCode:  1,
			wantIn:    []string{"no open toolchain refresh PR"},
			wantNotIn: []string{"pull/4242"},
			wantLog:   true,
		},
		{
			name:    "the newest toolchain refresh merge sets the clock",
			now:     fresh,
			openPRs: `[]`,
			closedPRs: updaterHygieneJSON(t, []any{
				updaterHygieneMergedPR(t, snapshot.Add(-60*time.Hour)),
				updaterHygieneMergedPR(t, snapshot.Add(14*time.Hour)),
			}),
			wantCode: 0,
			wantIn:   []string{"toolchain refresh PR merged " + snapshot.Add(14*time.Hour).Format(time.RFC3339) + ") is 30h old, within the grace", "Pin hygiene passed"},
			wantLog:  true,
		},
		{
			name:      "a merge time that is not a timestamp fails",
			now:       fresh,
			openPRs:   `[]`,
			closedPRs: updaterHygieneJSON(t, []any{updaterHygieneWith(updaterHygieneMergedPR(t, snapshot), "merged_at", "yesterday")}),
			wantCode:  1,
			wantIn:    []string{"Unable to read the merged toolchain refresh pull requests of omkhar/workcell."},
			wantNotIn: []string{"Pin hygiene verdict", "Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:      "a refresh PR from another author carries nothing",
			now:       stale,
			openPRs:   updaterHygieneJSON(t, []any{updaterHygieneWith(updaterHygieneRefreshPR(t, "toolchain", "omkhar/workcell", stale.Add(-time.Hour)), "user", map[string]any{"login": "someone"})}),
			wantCode:  1,
			wantIn:    []string{"no open toolchain refresh PR"},
			wantNotIn: []string{"pull/4242"},
			wantLog:   true,
		},
		{
			name:      "a refresh PR left unmerged past the grace fails",
			now:       stale,
			openPRs:   updaterHygieneJSON(t, []any{updaterHygieneRefreshPR(t, "toolchain", "omkhar/workcell", stale.Add(-80*time.Hour))}),
			wantCode:  1,
			wantIn:    []string{"there is only the open toolchain refresh PR https://github.com/omkhar/workcell/pull/4242 awaiting review/merge since " + stale.Add(-80*time.Hour).Format(time.RFC3339) + ", 80h old, past the grace"},
			wantNotIn: []string{"Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:      "an unreadable closed pull request list fails",
			now:       fresh,
			openPRs:   `[]`,
			extraEnv:  []string{"WORKCELL_FIXTURE_CLOSED_PRS_CODE=1"},
			wantCode:  1,
			wantIn:    []string{"Unable to list the closed pull requests of omkhar/workcell."},
			wantNotIn: []string{"Pin hygiene verdict", "Pin hygiene passed"},
			wantLog:   true,
		},
		{
			name:      "a closed pull request list that is not an array fails",
			now:       fresh,
			openPRs:   `[]`,
			closedPRs: `{"message":"Not Found"}`,
			wantCode:  1,
			wantIn:    []string{"Unable to read the merged toolchain refresh pull requests of omkhar/workcell."},
			wantNotIn: []string{"Pin hygiene verdict", "Pin hygiene passed"},
			wantLog:   true,
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
			closedPRs := testCase.closedPRs
			if closedPRs == "" {
				closedPRs = lastRefresh
			}
			options := defaultUpdaterFixtureOptions()
			options.ProviderPlan = updaterHygieneProviderPlan(t, testCase.now)
			options.ExtraArgs = testCase.extraArgs
			options.ExtraEnv = append([]string{
				fmt.Sprintf("WORKCELL_FIXTURE_NOW_EPOCH=%d", testCase.now.Unix()),
				"WORKCELL_FIXTURE_LAGGED_PROVIDER_PLAN=" + updaterHygieneProviderPlan(t, testCase.now.Add(-grace)),
				"WORKCELL_FIXTURE_OPEN_PRS=" + testCase.openPRs,
				"WORKCELL_FIXTURE_CLOSED_PRS=" + closedPRs,
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

// TestJobPinHygieneSummaryEscapesTheReport proves that a release name in the
// report cannot end the job summary block or add markup to it.
func TestJobPinHygieneSummaryEscapesTheReport(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "ci", "job-pin-hygiene.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	report := filepath.Join(dir, "report.txt")
	summary := filepath.Join(dir, "summary.md")
	if err := os.WriteFile(report, []byte("  provider: tool 1.0 -> 1.1```\n</pre><img src=x> & more\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(summary, []byte("earlier step\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	program := extractShellFunction(t, string(script), "append_pin_hygiene_summary") + "\n" +
		`append_pin_hygiene_summary "$1" "$2"` + "\n"
	command := exec.Command("bash", "-c", program, "summary", report, summary)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("append_pin_hygiene_summary failed: %v\n%s", err, output)
	}
	got, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	want := "earlier step\n## Pin hygiene\n\n<pre>\n" +
		"  provider: tool 1.0 -&gt; 1.1```\n&lt;/pre&gt;&lt;img src=x&gt; &amp; more\n" +
		"</pre>\n"
	if string(got) != want {
		t.Fatalf("job summary = %q, want %q", got, want)
	}
}
