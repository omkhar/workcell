// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package mutation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// ScorePolicy is the reviewed baseline for the mutation score. A drop below
// BaselineScore is a diff a reviewer must approve, so the mutation safety net
// cannot silently regress.
//
// BaselineScore is compared as a float, so it must be an exactly achievable
// fraction of the mutant count to avoid rounding surprises; 100.0 (kill every
// mutant) is the recommended and current value.
//
// ExpectedMutants pins the reviewed size of the mutant set. Without it, a
// percentage baseline could be met by shrinking the set (removing a mutant so
// 13/13 still scores 100%); requiring an exact count makes any change to the
// mutant set a reviewed policy diff.
type ScorePolicy struct {
	Version         int     `json:"version"`
	BaselineScore   float64 `json:"baseline_score"`
	ExpectedMutants int     `json:"expected_mutants"`
}

// LoadScorePolicy reads and validates a version-1 mutation-score policy. It
// rejects unknown fields and a missing baseline_score so a typo (for example
// `baseline-score`) or omission cannot silently leave the gate at an effective
// 0% baseline that would let surviving mutants pass.
func LoadScorePolicy(path string) (ScorePolicy, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return ScorePolicy{}, err
	}
	// Pointers distinguish "field absent" from "field set to zero".
	var raw struct {
		Version         *int     `json:"version"`
		BaselineScore   *float64 `json:"baseline_score"`
		ExpectedMutants *int     `json:"expected_mutants"`
	}
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return ScorePolicy{}, fmt.Errorf("%s: %w", path, err)
	}
	// Reject trailing content so a policy with two top-level values (for example
	// a stale baseline followed by the reviewed one) cannot silently gate on the
	// first object.
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return ScorePolicy{}, fmt.Errorf("%s must contain a single JSON object", path)
	}
	if raw.Version == nil {
		return ScorePolicy{}, fmt.Errorf("%s must set version", path)
	}
	if raw.BaselineScore == nil {
		return ScorePolicy{}, fmt.Errorf("%s must set baseline_score", path)
	}
	if raw.ExpectedMutants == nil {
		return ScorePolicy{}, fmt.Errorf("%s must set expected_mutants", path)
	}
	policy := ScorePolicy{Version: *raw.Version, BaselineScore: *raw.BaselineScore, ExpectedMutants: *raw.ExpectedMutants}
	if policy.Version != 1 {
		return ScorePolicy{}, fmt.Errorf("%s must use version 1", path)
	}
	if policy.BaselineScore < 0 || policy.BaselineScore > 100 {
		return ScorePolicy{}, fmt.Errorf("%s baseline_score must be between 0 and 100, got %.2f", path, policy.BaselineScore)
	}
	if policy.ExpectedMutants <= 0 {
		return ScorePolicy{}, fmt.Errorf("%s expected_mutants must be positive, got %d", path, policy.ExpectedMutants)
	}
	return policy, nil
}

// CheckScore returns an error when the result's score is below the policy
// baseline. It reports the surviving mutants so a regression is diagnosable.
func CheckScore(result Result, policy ScorePolicy) error {
	if result.Total == 0 {
		return fmt.Errorf("no mutants were evaluated")
	}
	// The mutant set must match the reviewed size so the score cannot be met by
	// shrinking the set (removing a mutant to keep 100%).
	if result.Total != policy.ExpectedMutants {
		return fmt.Errorf("mutant set changed: policy expects %d mutants, harness ran %d (update the reviewed policy)",
			policy.ExpectedMutants, result.Total)
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
