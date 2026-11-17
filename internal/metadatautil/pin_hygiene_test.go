// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	pinHygieneTestNow    = "2026-11-17T14:09:51Z"
	pinHygieneTestLagged = "2026-11-14T14:09:51Z"
)

func pinHygieneTestPlan(generatedAt string, hasChanges bool, providers map[string]ProviderBumpSelection, uncertified map[string]ProviderBumpSelection) ProviderBumpPlan {
	return ProviderBumpPlan{
		GeneratedAt:  generatedAt,
		Cutoff:       pinHygieneTestTime(generatedAt).Add(-48 * time.Hour).Format(time.RFC3339),
		CooloffHours: 48,
		HasChanges:   hasChanges,
		Providers:    providers,
		Uncertified:  uncertified,
	}
}

func pinHygieneTestTime(text string) time.Time {
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		panic(err)
	}
	return parsed
}

func pinHygieneUpToDate(version string) ProviderBumpSelection {
	return ProviderBumpSelection{Channel: "stable", CurrentVersion: version, TargetVersion: version}
}

func pinHygieneBump(current, target, published string) ProviderBumpSelection {
	return ProviderBumpSelection{Channel: "stable", CurrentVersion: current, TargetVersion: target, PublishedAt: published, Changed: true}
}

func pinHygieneHeld(current, held, published string) ProviderBumpSelection {
	return ProviderBumpSelection{
		Channel:        "stable",
		CurrentVersion: current,
		TargetVersion:  current,
		SkippedReleases: []ProviderBumpSkippedRelease{
			{Version: held, PublishedAt: published, Reason: "unclassified-cli-surface added=[migrate-nfs-db] removed=[]"},
		},
	}
}

func pinHygieneCoolOffSkip(current, skipped, published string) ProviderBumpSelection {
	selection := pinHygieneUpToDate(current)
	selection.SkippedReleases = []ProviderBumpSkippedRelease{{Version: skipped, PublishedAt: published, Reason: "within configured cool-off window"}}
	return selection
}

func pinHygieneProviders(overrides map[string]ProviderBumpSelection) map[string]ProviderBumpSelection {
	providers := map[string]ProviderBumpSelection{
		"claude":  pinHygieneUpToDate("2.1.319"),
		"codex":   pinHygieneUpToDate("0.169.0"),
		"copilot": pinHygieneUpToDate("1.0.108"),
		"gemini":  pinHygieneUpToDate("0.67.0"),
	}
	for key, value := range overrides {
		providers[key] = value
	}
	return providers
}

type pinHygieneTestCase struct {
	name        string
	toolchain   bool
	snapshot    string
	current     ProviderBumpPlan
	lagged      ProviderBumpPlan
	providerPR  *PinHygieneRefreshPR
	toolchainPR *PinHygieneRefreshPR
	// toolchainMerged is when the last toolchain refresh PR merged: empty
	// for the afternoon of the snapshot day, "none" for no merged PR.
	toolchainMerged string
	wantProblem     bool
	wantInOutput    []string
	wantNotOutput   []string
	// wantOnce lists text that the verdict must print exactly once.
	wantOnce []string
}

func freshProviderPR() *PinHygieneRefreshPR {
	return &PinHygieneRefreshPR{URL: "https://github.com/omkhar/workcell/pull/1069", CreatedAt: "2026-11-17T13:21:47Z"}
}

func freshToolchainPR() *PinHygieneRefreshPR {
	return &PinHygieneRefreshPR{URL: "https://github.com/omkhar/workcell/pull/1070", CreatedAt: "2026-11-17T13:21:55Z"}
}

func stalePR(kind string) *PinHygieneRefreshPR {
	return &PinHygieneRefreshPR{URL: "https://github.com/omkhar/workcell/pull/985-" + kind, CreatedAt: "2026-11-08T13:19:17Z"}
}

func runPinHygieneTest(t *testing.T, tc pinHygieneTestCase) (string, error) {
	t.Helper()
	pending := tc.toolchain
	current, err := json.Marshal(tc.current)
	if err != nil {
		t.Fatal(err)
	}
	lagged, err := json.Marshal(tc.lagged)
	if err != nil {
		t.Fatal(err)
	}
	merged := json.RawMessage(`null`)
	switch tc.toolchainMerged {
	case "none":
	case "":
		snapshot, err := time.Parse("20060102T150405Z", tc.snapshot)
		if err != nil {
			t.Fatal(err)
		}
		merged = json.RawMessage(`"` + snapshot.Add(14*time.Hour).Format(time.RFC3339) + `"`)
	default:
		merged = json.RawMessage(`"` + tc.toolchainMerged + `"`)
	}
	input, err := json.Marshal(PinHygieneInput{
		Now:                      pinHygieneTestNow,
		GraceHours:               72,
		ToolchainPending:         &pending,
		DebianSnapshot:           tc.snapshot,
		ToolchainRefreshMergedAt: merged,
		ProviderPlan:             current,
		LaggedProviderPlan:       lagged,
		RefreshPRs:               map[string]*PinHygieneRefreshPR{"provider": tc.providerPR, "toolchain": tc.toolchainPR},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = EvaluatePinHygiene(bytes.NewReader(input), &output)
	return output.String(), err
}

func TestEvaluatePinHygieneVerdicts(t *testing.T) {
	t.Parallel()

	upToDateLagged := pinHygieneTestPlan(pinHygieneTestLagged, false, pinHygieneProviders(nil), nil)
	ampCurrent := map[string]ProviderBumpSelection{"@ampcode/cli": pinHygieneBump("0.0.1794657642-gc1e41b", "0.0.1794744038-ga74b43", "2026-11-15T12:19:14Z")}
	ampLaggedUpToDate := map[string]ProviderBumpSelection{"@ampcode/cli": pinHygieneUpToDate("0.0.1794657642-gc1e41b")}
	ampLaggedStale := map[string]ProviderBumpSelection{"@ampcode/cli": pinHygieneBump("0.0.1793534443-ga508b6", "0.0.1793950000-gaaaaaa", "2026-11-09T12:00:00Z")}
	staleClaude := pinHygieneProviders(map[string]ProviderBumpSelection{"claude": pinHygieneBump("2.1.316", "2.1.319", "2026-11-06T00:58:56Z")})
	heldCodex := pinHygieneProviders(map[string]ProviderBumpSelection{"codex": pinHygieneHeld("0.163.0", "0.166.0", "2026-10-29T19:56:19Z")})
	newestHeldCodex := pinHygieneProviders(map[string]ProviderBumpSelection{"codex": pinHygieneHeld("0.163.0", "0.167.0", "2026-11-06T01:46:01Z")})

	for _, tc := range []pinHygieneTestCase{
		{
			// Run 44100102243: syft and Amp updates that the open refresh
			// PRs carried, 45 minutes after the refresh opened them.
			name:         "drift of the day carried by fresh refresh PRs passes",
			toolchain:    true,
			snapshot:     "20261116T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, true, pinHygieneProviders(nil), ampCurrent),
			lagged:       pinHygieneTestPlan(pinHygieneTestLagged, false, pinHygieneProviders(nil), ampLaggedUpToDate),
			providerPR:   freshProviderPR(),
			toolchainPR:  freshToolchainPR(),
			wantInOutput: []string{"toolchain: update pending, expected", "38h old", "provider: update pending, expected", "pull/1069", "Pin hygiene passed"},
		},
		{
			name:         "toolchain drift within the grace passes with no refresh PR",
			toolchain:    true,
			snapshot:     "20261116T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:       upToDateLagged,
			wantInOutput: []string{"toolchain: update pending, expected", "no open toolchain refresh PR", "Pin hygiene passed"},
		},
		{
			// Run 42831297040: no toolchain refresh had landed for eight days.
			name:         "toolchain drift past the grace with no refresh PR fails",
			toolchain:    true,
			snapshot:     "20261110T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:       upToDateLagged,
			wantProblem:  true,
			wantInOutput: []string{"PROBLEM: a toolchain update is pending", "Debian snapshot 20261110T000000Z, toolchain refresh PR merged 2026-11-10T14:00:00Z) is 182h old", "no open toolchain refresh PR"},
		},
		{
			name:         "toolchain drift past the grace carried by a fresh refresh PR passes",
			toolchain:    true,
			snapshot:     "20261110T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:       upToDateLagged,
			toolchainPR:  freshToolchainPR(),
			wantInOutput: []string{"carried by the open toolchain refresh PR https://github.com/omkhar/workcell/pull/1070", "Pin hygiene passed"},
		},
		{
			name:         "toolchain drift past the grace with only a stale refresh PR fails",
			toolchain:    true,
			snapshot:     "20261110T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:       upToDateLagged,
			toolchainPR:  stalePR("toolchain"),
			wantProblem:  true,
			wantInOutput: []string{"there is only the open toolchain refresh PR https://github.com/omkhar/workcell/pull/985-toolchain awaiting review/merge since 2026-11-08T13:19:17Z, 216h old, past the grace"},
		},
		{
			name:          "an old toolchain snapshot with no pending update passes",
			toolchain:     false,
			snapshot:      "20261001T000000Z",
			current:       pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:        upToDateLagged,
			wantInOutput:  []string{"toolchain: up to date", "provider: up to date", "Pin hygiene passed"},
			wantNotOutput: []string{"PROBLEM"},
		},
		{
			name:         "a provider release missing past the cool-off and grace with no refresh PR fails",
			snapshot:     "20261116T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, true, staleClaude, nil),
			lagged:       pinHygieneTestPlan(pinHygieneTestLagged, true, staleClaude, nil),
			wantProblem:  true,
			wantInOutput: []string{"PROBLEM: claude 2.1.316 -> 2.1.319, released 2026-11-06T00:58:56Z, is missing past the 48h cool-off plus the grace", "no open provider refresh PR"},
		},
		{
			name:         "a provider release missing past the grace carried by a fresh refresh PR passes",
			snapshot:     "20261116T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, true, staleClaude, nil),
			lagged:       pinHygieneTestPlan(pinHygieneTestLagged, true, staleClaude, nil),
			providerPR:   freshProviderPR(),
			wantInOutput: []string{"provider: claude 2.1.316 -> 2.1.319", "carried by the open provider refresh PR", "Pin hygiene passed"},
		},
		{
			// PR 985 stayed open from 8 to 15 November and blocked every
			// newer provider candidate.
			name:         "a provider release missing past the grace with only a stale refresh PR fails",
			snapshot:     "20261116T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, true, staleClaude, nil),
			lagged:       pinHygieneTestPlan(pinHygieneTestLagged, true, staleClaude, nil),
			providerPR:   stalePR("provider"),
			wantProblem:  true,
			wantInOutput: []string{"there is only the open provider refresh PR https://github.com/omkhar/workcell/pull/985-provider awaiting review/merge since 2026-11-08T13:19:17Z"},
		},
		{
			name:         "an uncertified npm release missing past the grace with no refresh PR fails",
			snapshot:     "20261116T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, true, pinHygieneProviders(nil), ampLaggedStale),
			lagged:       pinHygieneTestPlan(pinHygieneTestLagged, true, pinHygieneProviders(nil), ampLaggedStale),
			wantProblem:  true,
			wantInOutput: []string{"PROBLEM: @ampcode/cli (uncertified npm) 0.0.1793534443-ga508b6 -> 0.0.1793950000-gaaaaaa"},
		},
		{
			// Runs 41650958903 and 42831297040: Codex held on migrate-nfs-db
			// for weeks while each new release kept the same new command.
			name:          "a release held on an unclassified CLI surface past the grace fails",
			snapshot:      "20261116T000000Z",
			current:       pinHygieneTestPlan(pinHygieneTestNow, false, newestHeldCodex, nil),
			lagged:        pinHygieneTestPlan(pinHygieneTestLagged, false, heldCodex, nil),
			providerPR:    freshProviderPR(),
			wantProblem:   true,
			wantInOutput:  []string{"PROBLEM: codex 0.166.0, released 2026-10-29T19:56:19Z, is held on an unclassified CLI surface", "(0.167.0 unclassified-cli-surface added=[migrate-nfs-db] removed=[])"},
			wantOnce:      []string{"held on an unclassified CLI surface", "migrate-nfs-db"},
			wantNotOutput: []string{"provider: up to date", "provider: update pending"},
		},
		{
			// The lagged plan bumps to the release before the hold while the
			// current plan holds the newest one: one problem, not two.
			name:     "a held release whose older release is missing is one problem",
			snapshot: "20261116T000000Z",
			current:  pinHygieneTestPlan(pinHygieneTestNow, false, newestHeldCodex, nil),
			lagged: func() ProviderBumpPlan {
				held := pinHygieneHeld("0.163.0", "0.166.0", "2026-10-29T19:56:19Z")
				held.TargetVersion, held.PublishedAt, held.Changed = "0.165.0", "2026-10-20T10:00:00Z", true
				return pinHygieneTestPlan(pinHygieneTestLagged, true, pinHygieneProviders(map[string]ProviderBumpSelection{"codex": held}), nil)
			}(),
			wantProblem:   true,
			wantOnce:      []string{"PROBLEM", "migrate-nfs-db"},
			wantNotOutput: []string{"no refresh PR can carry it"},
		},
		{
			name:          "a release held on an unclassified CLI surface within the grace passes",
			snapshot:      "20261116T000000Z",
			current:       pinHygieneTestPlan(pinHygieneTestNow, false, newestHeldCodex, nil),
			lagged:        upToDateLagged,
			wantInOutput:  []string{"provider: codex 0.167.0, released 2026-11-06T01:46:01Z, is held on an unclassified CLI surface within the 48h cool-off plus the grace", "Pin hygiene passed"},
			wantNotOutput: []string{"PROBLEM"},
		},
		{
			name:         "an old hold that a newer classified release replaces passes",
			snapshot:     "20261116T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, true, pinHygieneProviders(map[string]ProviderBumpSelection{"codex": pinHygieneBump("0.163.0", "0.169.0", "2026-11-13T10:00:00Z")}), nil),
			lagged:       pinHygieneTestPlan(pinHygieneTestLagged, false, heldCodex, nil),
			wantInOutput: []string{"provider: update pending, expected", "Pin hygiene passed"},
		},
		{
			name:         "a provider release missing past the grace while the newest release is held fails even with a fresh refresh PR",
			snapshot:     "20261116T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, false, newestHeldCodex, nil),
			lagged:       pinHygieneTestPlan(pinHygieneTestLagged, true, pinHygieneProviders(map[string]ProviderBumpSelection{"codex": pinHygieneBump("0.163.0", "0.165.0", "2026-10-20T10:00:00Z")}), nil),
			providerPR:   freshProviderPR(),
			wantProblem:  true,
			wantInOutput: []string{"PROBLEM: codex 0.163.0 -> 0.165.0", "no refresh PR can carry it while the newest release is held"},
		},
		{
			// Run 39437518741: a Copilot release inside its cool-off.
			name:          "a release skipped inside its cool-off passes",
			snapshot:      "20261116T000000Z",
			current:       pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(map[string]ProviderBumpSelection{"copilot": pinHygieneCoolOffSkip("1.0.108", "1.0.109", "2026-11-16T18:43:10Z")}), nil),
			lagged:        upToDateLagged,
			wantInOutput:  []string{"provider: up to date", "Pin hygiene passed"},
			wantNotOutput: []string{"PROBLEM"},
		},
		{
			// A change that moves only the Debian snapshot leaves the other
			// toolchain pins as old as the last toolchain refresh PR.
			name:            "a snapshot-only bump does not hide toolchain drift from an older refresh",
			toolchain:       true,
			snapshot:        "20261116T000000Z",
			toolchainMerged: "2026-11-10T14:00:00Z",
			current:         pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:          upToDateLagged,
			wantProblem:     true,
			wantInOutput:    []string{"PROBLEM: a toolchain update is pending and the last toolchain refresh (Debian snapshot 20261116T000000Z, toolchain refresh PR merged 2026-11-10T14:00:00Z) is 168h old, past the grace"},
		},
		{
			name:            "toolchain drift with no merged toolchain refresh PR in view fails",
			toolchain:       true,
			snapshot:        "20261116T000000Z",
			toolchainMerged: "none",
			current:         pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:          upToDateLagged,
			wantProblem:     true,
			wantInOutput:    []string{"no merged toolchain refresh PR found) is of unknown age, past the grace"},
		},
		{
			name:            "no merged toolchain refresh PR in view passes with no pending update",
			snapshot:        "20261116T000000Z",
			toolchainMerged: "none",
			current:         pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:          upToDateLagged,
			wantInOutput:    []string{"toolchain: up to date", "Pin hygiene passed"},
		},
		{
			name:            "toolchain drift exactly the grace old passes",
			toolchain:       true,
			snapshot:        "20261116T000000Z",
			toolchainMerged: pinHygieneTestLagged,
			current:         pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:          upToDateLagged,
			wantInOutput:    []string{"merged 2026-11-14T14:09:51Z) is 72h old, within the grace", "Pin hygiene passed"},
		},
		{
			name:            "toolchain drift one second past the grace fails",
			toolchain:       true,
			snapshot:        "20261116T000000Z",
			toolchainMerged: "2026-11-14T14:09:50Z",
			current:         pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:          upToDateLagged,
			wantProblem:     true,
			wantInOutput:    []string{"is 72h old, past the grace; there is no open toolchain refresh PR"},
		},
		{
			name:         "a refresh PR opened exactly the grace ago still carries drift",
			toolchain:    true,
			snapshot:     "20261110T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:       upToDateLagged,
			toolchainPR:  &PinHygieneRefreshPR{URL: "https://github.com/omkhar/workcell/pull/1", CreatedAt: pinHygieneTestLagged},
			wantInOutput: []string{"carried by the open toolchain refresh PR https://github.com/omkhar/workcell/pull/1, opened 2026-11-14T14:09:51Z", "Pin hygiene passed"},
		},
		{
			name:         "a refresh PR opened one second before the grace is awaiting review",
			toolchain:    true,
			snapshot:     "20261110T000000Z",
			current:      pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil),
			lagged:       upToDateLagged,
			toolchainPR:  &PinHygieneRefreshPR{URL: "https://github.com/omkhar/workcell/pull/1", CreatedAt: "2026-11-14T14:09:50Z"},
			wantProblem:  true,
			wantInOutput: []string{"awaiting review/merge since 2026-11-14T14:09:50Z, 72h old, past the grace"},
		},
		{
			name:     "a goose release missing past the grace with no refresh PR fails",
			snapshot: "20261116T000000Z",
			current: func() ProviderBumpPlan {
				plan := pinHygieneTestPlan(pinHygieneTestNow, true, pinHygieneProviders(nil), nil)
				plan.UncertifiedReleases = map[string]ProviderBumpSelection{"goose": pinHygieneBump("1.13.0", "1.14.0", "2026-11-09T18:00:00Z")}
				return plan
			}(),
			lagged: func() ProviderBumpPlan {
				plan := pinHygieneTestPlan(pinHygieneTestLagged, true, pinHygieneProviders(nil), nil)
				plan.UncertifiedReleases = map[string]ProviderBumpSelection{"goose": pinHygieneBump("1.13.0", "1.14.0", "2026-11-09T18:00:00Z")}
				return plan
			}(),
			wantProblem:  true,
			wantInOutput: []string{"PROBLEM: goose (uncertified GitHub release) 1.13.0 -> 1.14.0, released 2026-11-09T18:00:00Z, is missing past the 48h cool-off plus the grace"},
		},
		{
			// A Claude approved_version skips the cool-off, so the lagged
			// plan can pick a release from inside it.
			name:          "an approved release inside the cool-off passes with no refresh PR",
			snapshot:      "20261116T000000Z",
			current:       pinHygieneTestPlan(pinHygieneTestNow, true, pinHygieneProviders(map[string]ProviderBumpSelection{"claude": pinHygieneBump("2.1.319", "2.1.321", "2026-11-16T20:00:00Z")}), nil),
			lagged:        pinHygieneTestPlan(pinHygieneTestLagged, true, pinHygieneProviders(map[string]ProviderBumpSelection{"claude": pinHygieneBump("2.1.319", "2.1.321", "2026-11-16T20:00:00Z")}), nil),
			wantInOutput:  []string{"provider: claude 2.1.319 -> 2.1.321, released 2026-11-16T20:00:00Z, is an approved release within the 48h cool-off plus the grace", "Pin hygiene passed"},
			wantNotOutput: []string{"PROBLEM"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			output, err := runPinHygieneTest(t, tc)
			if tc.wantProblem {
				if !errors.Is(err, ErrPinHygieneProblems) {
					t.Fatalf("EvaluatePinHygiene error = %v, want ErrPinHygieneProblems\n%s", err, output)
				}
			} else if err != nil {
				t.Fatalf("EvaluatePinHygiene error = %v, want nil\n%s", err, output)
			}
			for _, want := range tc.wantInOutput {
				if !strings.Contains(output, want) {
					t.Errorf("verdict output lacks %q\n%s", want, output)
				}
			}
			for _, unwanted := range tc.wantNotOutput {
				if strings.Contains(output, unwanted) {
					t.Errorf("verdict output holds %q\n%s", unwanted, output)
				}
			}
			for _, once := range tc.wantOnce {
				if count := strings.Count(output, once); count != 1 {
					t.Errorf("verdict output holds %q %d times, want once\n%s", once, count, output)
				}
			}
		})
	}
}

func TestEvaluatePinHygieneRejectsBadInput(t *testing.T) {
	t.Parallel()

	validInput := func() map[string]any {
		current, _ := json.Marshal(pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil))
		lagged, _ := json.Marshal(pinHygieneTestPlan(pinHygieneTestLagged, false, pinHygieneProviders(nil), nil))
		return map[string]any{
			"now":                         pinHygieneTestNow,
			"grace_hours":                 72,
			"toolchain_pending":           true,
			"debian_snapshot":             "20261116T000000Z",
			"toolchain_refresh_merged_at": "2026-11-16T14:00:00Z",
			"provider_plan":               json.RawMessage(current),
			"lagged_provider_plan":        json.RawMessage(lagged),
			"refresh_prs":                 map[string]any{"provider": nil, "toolchain": nil},
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"the lagged plan was made at another time", func(m map[string]any) {
			lagged, _ := json.Marshal(pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil))
			m["lagged_provider_plan"] = json.RawMessage(lagged)
		}, "not at now minus the grace"},
		{"toolchain_pending is missing", func(m map[string]any) { delete(m, "toolchain_pending") }, "toolchain_pending"},
		{"toolchain_pending is not a boolean", func(m map[string]any) { m["toolchain_pending"] = "yes" }, "toolchain_pending"},
		{"the Debian snapshot is not a stamp", func(m map[string]any) { m["debian_snapshot"] = "20261116" }, "not a YYYYMMDDT000000Z stamp"},
		{"the Debian snapshot is not at midnight", func(m map[string]any) { m["debian_snapshot"] = "20261116T010000Z" }, "not a YYYYMMDDT000000Z stamp"},
		{"the Debian snapshot is later than now", func(m map[string]any) { m["debian_snapshot"] = "20261118T000000Z" }, "later than now"},
		{"the grace is zero", func(m map[string]any) { m["grace_hours"] = 0 }, "grace must be"},
		{"the grace is too long", func(m map[string]any) { m["grace_hours"] = 721 }, "grace must be"},
		{"now is not RFC 3339", func(m map[string]any) { m["now"] = "yesterday" }, "not RFC 3339"},
		{"an unknown field", func(m map[string]any) { m["skip"] = true }, "unknown field"},
		{"the provider plan is missing", func(m map[string]any) { delete(m, "provider_plan") }, "must hold provider_plan"},
		{"the lagged plan is null", func(m map[string]any) { m["lagged_provider_plan"] = nil }, "must hold lagged_provider_plan"},
		{"the provider plan is not a plan", func(m map[string]any) { m["provider_plan"] = json.RawMessage(`{"has_changes":false}`) }, "is not a provider bump plan"},
		{"a refresh PR kind is missing", func(m map[string]any) { m["refresh_prs"] = map[string]any{"provider": nil} }, "exactly the provider and toolchain kinds"},
		{"a refresh PR kind is unknown", func(m map[string]any) { m["refresh_prs"] = map[string]any{"provider": nil, "toolchains": nil} }, "must name the toolchain kind"},
		{"a refresh PR has no created_at", func(m map[string]any) {
			m["refresh_prs"] = map[string]any{"provider": map[string]any{"url": "https://github.com/omkhar/workcell/pull/1"}, "toolchain": nil}
		}, "no RFC 3339 created_at"},
		{"a refresh PR has no URL", func(m map[string]any) {
			m["refresh_prs"] = map[string]any{"provider": map[string]any{"created_at": pinHygieneTestNow}, "toolchain": nil}
		}, "has no URL"},
		{"the toolchain merge time is missing", func(m map[string]any) { delete(m, "toolchain_refresh_merged_at") }, "toolchain_refresh_merged_at to a time or null"},
		{"the toolchain merge time is not a string", func(m map[string]any) { m["toolchain_refresh_merged_at"] = 5 }, "an RFC 3339 string or null"},
		{"the toolchain merge time is not RFC 3339", func(m map[string]any) { m["toolchain_refresh_merged_at"] = "2026-11-16" }, "is not RFC 3339"},
		{"the toolchain merge time is later than now", func(m map[string]any) { m["toolchain_refresh_merged_at"] = "2026-11-17T14:09:52Z" }, "is later than now"},
		{"the provider plan has no cutoff", func(m map[string]any) {
			plan := pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil)
			plan.Cutoff = ""
			current, _ := json.Marshal(plan)
			m["provider_plan"] = json.RawMessage(current)
		}, "is not a provider bump plan"},
		{"the provider plan has no generation time", func(m map[string]any) {
			plan := pinHygieneTestPlan(pinHygieneTestNow, false, pinHygieneProviders(nil), nil)
			plan.GeneratedAt = ""
			current, _ := json.Marshal(plan)
			m["provider_plan"] = json.RawMessage(current)
		}, "is not a provider bump plan"},
		{"the provider plan has no providers", func(m map[string]any) {
			current, _ := json.Marshal(pinHygieneTestPlan(pinHygieneTestNow, false, map[string]ProviderBumpSelection{}, nil))
			m["provider_plan"] = json.RawMessage(current)
		}, "is not a provider bump plan"},
		{"the lagged plan cutoff is not RFC 3339", func(m map[string]any) {
			plan := pinHygieneTestPlan(pinHygieneTestLagged, false, pinHygieneProviders(nil), nil)
			plan.Cutoff = "soon"
			lagged, _ := json.Marshal(plan)
			m["lagged_provider_plan"] = json.RawMessage(lagged)
		}, "cutoff \"soon\" is not RFC 3339"},
		{"a lagged release date is not RFC 3339", func(m map[string]any) {
			lagged, _ := json.Marshal(pinHygieneTestPlan(pinHygieneTestLagged, true, pinHygieneProviders(map[string]ProviderBumpSelection{"claude": pinHygieneBump("2.1.316", "2.1.319", "Nov 6")}), nil))
			m["lagged_provider_plan"] = json.RawMessage(lagged)
		}, "published_at \"Nov 6\" that is not RFC 3339"},
		{"the lagged plan names a pin that the plan does not", func(m map[string]any) {
			lagged, _ := json.Marshal(pinHygieneTestPlan(pinHygieneTestLagged, false, pinHygieneProviders(map[string]ProviderBumpSelection{"newcli": pinHygieneUpToDate("1.0.0")}), nil))
			m["lagged_provider_plan"] = json.RawMessage(lagged)
		}, "which the provider plan does not"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := validInput()
			tc.mutate(input)
			content, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err = EvaluatePinHygiene(bytes.NewReader(content), &output)
			if err == nil || errors.Is(err, ErrPinHygieneProblems) {
				t.Fatalf("EvaluatePinHygiene error = %v, want an input error containing %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("EvaluatePinHygiene error = %q, want it to contain %q", err, tc.want)
			}
			if output.Len() != 0 {
				t.Fatalf("EvaluatePinHygiene wrote a verdict for bad input:\n%s", output.String())
			}
		})
	}

	for _, grace := range []int{1, pinHygieneMaxGraceHours} {
		t.Run(fmt.Sprintf("a grace of %d hours is read", grace), func(t *testing.T) {
			t.Parallel()
			input := validInput()
			input["grace_hours"] = grace
			laggedAt := pinHygieneTestTime(pinHygieneTestNow).Add(-time.Duration(grace) * time.Hour).Format(time.RFC3339)
			lagged, _ := json.Marshal(pinHygieneTestPlan(laggedAt, false, pinHygieneProviders(nil), nil))
			input["lagged_provider_plan"] = json.RawMessage(lagged)
			content, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			// A verdict, with or without problems, means the input was read.
			if err := EvaluatePinHygiene(bytes.NewReader(content), &bytes.Buffer{}); err != nil && !errors.Is(err, ErrPinHygieneProblems) {
				t.Fatalf("EvaluatePinHygiene error = %v for a grace of %d hours", err, grace)
			}
		})
	}

	t.Run("input of exactly the size limit is read", func(t *testing.T) {
		t.Parallel()
		content, err := json.Marshal(validInput())
		if err != nil {
			t.Fatal(err)
		}
		padded := append(content, bytes.Repeat([]byte(" "), pinHygieneInputMaxBytes-len(content))...)
		if err := EvaluatePinHygiene(bytes.NewReader(padded), &bytes.Buffer{}); err != nil {
			t.Fatalf("EvaluatePinHygiene error = %v for input of exactly %d bytes", err, pinHygieneInputMaxBytes)
		}
		err = EvaluatePinHygiene(bytes.NewReader(append(padded, ' ')), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("EvaluatePinHygiene error = %v, want a size error for input over the limit", err)
		}
	})

	t.Run("two documents", func(t *testing.T) {
		t.Parallel()
		content, err := json.Marshal(validInput())
		if err != nil {
			t.Fatal(err)
		}
		err = EvaluatePinHygiene(bytes.NewReader(append(append(content, '\n'), content...)), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "more than one document") {
			t.Fatalf("EvaluatePinHygiene error = %v, want a second-document error", err)
		}
	})
}
