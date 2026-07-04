// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package mutation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResultScore(t *testing.T) {
	cases := []struct {
		name   string
		result Result
		want   float64
	}{
		{"all killed", Result{Killed: 14, Total: 14}, 100},
		{"one survivor", Result{Killed: 13, Total: 14}, 13.0 / 14.0 * 100},
		{"empty", Result{Killed: 0, Total: 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.Score(); got != tc.want {
				t.Fatalf("Score() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckScorePassesAtBaseline(t *testing.T) {
	err := CheckScore(Result{Killed: 14, Total: 14}, ScorePolicy{Version: 1, BaselineScore: 100})
	if err != nil {
		t.Fatalf("expected pass at baseline, got: %v", err)
	}
}

// TestCheckScoreTripsOnSurvivor is the deliberate mutant-survival dry run: a
// single survivor drops the score below baseline and must trip the gate.
func TestCheckScoreTripsOnSurvivor(t *testing.T) {
	err := CheckScore(
		Result{Killed: 13, Total: 14, Survivors: []string{"go/injection/example"}},
		ScorePolicy{Version: 1, BaselineScore: 100},
	)
	if err == nil {
		t.Fatal("expected the gate to trip on a surviving mutant, got nil")
	}
	if !strings.Contains(err.Error(), "below the baseline") || !strings.Contains(err.Error(), "go/injection/example") {
		t.Fatalf("expected below-baseline error naming the survivor, got: %v", err)
	}
}

func TestCheckScoreRejectsEmptyRun(t *testing.T) {
	if err := CheckScore(Result{}, ScorePolicy{Version: 1, BaselineScore: 100}); err == nil {
		t.Fatal("expected error when no mutants were evaluated")
	}
}

// TestCheckScoreRejectsIncompleteRun proves the fail-closed invariant: a result
// where a mutant errored (killed+survivors < total) must not be scored, even
// though killed/total alone would look like a passing 13/13.
func TestCheckScoreRejectsIncompleteRun(t *testing.T) {
	err := CheckScore(Result{Killed: 13, Total: 14}, ScorePolicy{Version: 1, BaselineScore: 100})
	if err == nil || !strings.Contains(err.Error(), "incomplete mutation run") {
		t.Fatalf("expected incomplete-run error, got: %v", err)
	}
}

func TestLoadScorePolicy(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return p
	}

	valid := write("valid.json", `{"version":1,"baseline_score":100.0}`)
	policy, err := LoadScorePolicy(valid)
	if err != nil {
		t.Fatalf("valid policy: %v", err)
	}
	if policy.BaselineScore != 100 {
		t.Fatalf("baseline = %v, want 100", policy.BaselineScore)
	}

	badVersion := write("badver.json", `{"version":2,"baseline_score":100}`)
	if _, err := LoadScorePolicy(badVersion); err == nil || !strings.Contains(err.Error(), "version 1") {
		t.Fatalf("expected version error, got: %v", err)
	}

	outOfRange := write("range.json", `{"version":1,"baseline_score":150}`)
	if _, err := LoadScorePolicy(outOfRange); err == nil || !strings.Contains(err.Error(), "between 0 and 100") {
		t.Fatalf("expected range error, got: %v", err)
	}

	// A typo in the key must not silently default the baseline to 0%.
	typo := write("typo.json", `{"version":1,"baseline-score":100}`)
	if _, err := LoadScorePolicy(typo); err == nil {
		t.Fatal("expected error for unknown field baseline-score")
	}

	missing := write("missing.json", `{"version":1}`)
	if _, err := LoadScorePolicy(missing); err == nil || !strings.Contains(err.Error(), "baseline_score") {
		t.Fatalf("expected missing-baseline error, got: %v", err)
	}
}
