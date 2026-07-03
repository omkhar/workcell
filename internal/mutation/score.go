// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package mutation

import (
	"encoding/json"
	"fmt"
	"os"
)

// ScorePolicy is the reviewed baseline for the mutation score. A drop below
// BaselineScore is a diff a reviewer must approve, so the mutation safety net
// cannot silently regress.
//
// BaselineScore is compared as a float, so it must be an exactly achievable
// fraction of the mutant count to avoid rounding surprises; 100.0 (kill every
// mutant) is the recommended and current value.
type ScorePolicy struct {
	Version       int     `json:"version"`
	BaselineScore float64 `json:"baseline_score"`
}

// LoadScorePolicy reads and validates a version-1 mutation-score policy.
func LoadScorePolicy(path string) (ScorePolicy, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return ScorePolicy{}, err
	}
	var policy ScorePolicy
	if err := json.Unmarshal(content, &policy); err != nil {
		return ScorePolicy{}, fmt.Errorf("%s: %w", path, err)
	}
	if policy.Version != 1 {
		return ScorePolicy{}, fmt.Errorf("%s must use version 1", path)
	}
	if policy.BaselineScore < 0 || policy.BaselineScore > 100 {
		return ScorePolicy{}, fmt.Errorf("%s baseline_score must be between 0 and 100, got %.2f", path, policy.BaselineScore)
	}
	return policy, nil
}

// CheckScore returns an error when the result's score is below the policy
// baseline. It reports the surviving mutants so a regression is diagnosable.
func CheckScore(result Result, policy ScorePolicy) error {
	if result.Total == 0 {
		return fmt.Errorf("no mutants were evaluated")
	}
	// Fail closed if the run is incomplete (a mutant errored and was neither
	// killed nor recorded as a survivor). This makes "an incomplete run can
	// never be scored" an enforced invariant rather than a caller convention.
	if result.Killed+len(result.Survivors) != result.Total {
		return fmt.Errorf("incomplete mutation run: %d killed + %d survivors != %d total (harness error?)",
			result.Killed, len(result.Survivors), result.Total)
	}
	score := result.Score()
	if score < policy.BaselineScore {
		msg := fmt.Sprintf("mutation score %.2f%% (%d/%d killed) is below the baseline %.2f%%",
			score, result.Killed, result.Total, policy.BaselineScore)
		if len(result.Survivors) > 0 {
			msg += fmt.Sprintf("; survivors: %v", result.Survivors)
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
