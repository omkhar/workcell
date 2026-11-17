// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
)

// The weekly Pin Hygiene lane runs after the daily upstream refresh has
// opened its pull requests, so upstream drift alone is the normal state. The
// verdict fails only on drift that the refresh pipeline should already have
// handled: drift older than the cool-off plus a grace period that no fresh
// refresh pull request carries, and a provider release that a command
// namespace review has held for that long.
const (
	pinHygieneMaxGraceHours   = 720
	pinHygieneInputMaxBytes   = 8 << 20
	pinHygieneUnclassifiedTag = "unclassified-cli-surface"
	pinHygieneSnapshotLayout  = "20060102T150405Z"
)

// ErrPinHygieneProblems marks a verdict that found drift a person or the
// refresh pipeline must act on. Every other error is a bad input.
var ErrPinHygieneProblems = errors.New("pin hygiene found problems that the refresh pipeline did not handle")

// PinHygieneInput is the document that scripts/update-upstream-pins.sh
// --hygiene passes to the verdict.
type PinHygieneInput struct {
	// Now is the time of the check. LaggedProviderPlan must be the provider
	// bump plan made at Now minus the grace.
	Now        string `json:"now"`
	GraceHours int    `json:"grace_hours"`
	// ToolchainPending is true when any toolchain pin differs from upstream.
	ToolchainPending *bool `json:"toolchain_pending"`
	// DebianSnapshot is the pinned Debian snapshot. Each toolchain refresh
	// moves it to the snapshot of its day, so its age bounds the age of any
	// toolchain drift.
	DebianSnapshot     string          `json:"debian_snapshot"`
	ProviderPlan       json.RawMessage `json:"provider_plan"`
	LaggedProviderPlan json.RawMessage `json:"lagged_provider_plan"`
	// RefreshPRs holds the open refresh pull request of each kind, or null.
	RefreshPRs map[string]*PinHygieneRefreshPR `json:"refresh_prs"`
}

// PinHygieneRefreshPR is an open upstream refresh pull request.
type PinHygieneRefreshPR struct {
	URL       string `json:"url"`
	CreatedAt string `json:"created_at"`
}

type pinHygieneSelection struct {
	label     string
	selection ProviderBumpSelection
}

// EvaluatePinHygiene reads a PinHygieneInput from r, writes the verdict
// report to w, and returns ErrPinHygieneProblems when the report holds a
// problem.
func EvaluatePinHygiene(r io.Reader, w io.Writer) error {
	content, err := io.ReadAll(io.LimitReader(r, pinHygieneInputMaxBytes+1))
	if err != nil {
		return err
	}
	if len(content) > pinHygieneInputMaxBytes {
		return errors.New("pin hygiene input is too large")
	}
	var input PinHygieneInput
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("read pin hygiene input: %w", err)
	}
	if decoder.More() {
		return errors.New("pin hygiene input holds more than one document")
	}
	lines, problems, err := pinHygieneVerdict(input)
	if err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if len(problems) == 0 {
		_, err := fmt.Fprintln(w, "Pin hygiene passed: every pending update is within its grace or carried by a fresh refresh PR.")
		return err
	}
	for _, problem := range problems {
		if _, err := fmt.Fprintln(w, "  PROBLEM: "+problem); err != nil {
			return err
		}
	}
	return ErrPinHygieneProblems
}

func pinHygieneVerdict(input PinHygieneInput) ([]string, []string, error) {
	now, err := time.Parse(time.RFC3339, input.Now)
	if err != nil {
		return nil, nil, fmt.Errorf("pin hygiene now %q is not RFC 3339", input.Now)
	}
	now = now.UTC()
	if input.GraceHours < 1 || input.GraceHours > pinHygieneMaxGraceHours {
		return nil, nil, fmt.Errorf("pin hygiene grace must be 1 through %d hours, got %d", pinHygieneMaxGraceHours, input.GraceHours)
	}
	grace := time.Duration(input.GraceHours) * time.Hour
	if input.ToolchainPending == nil {
		return nil, nil, errors.New("pin hygiene input must set toolchain_pending to a boolean")
	}
	snapshot, err := time.Parse(pinHygieneSnapshotLayout, input.DebianSnapshot)
	if err != nil || !snapshot.Equal(snapshot.Truncate(24*time.Hour)) {
		return nil, nil, fmt.Errorf("pin hygiene Debian snapshot %q is not a YYYYMMDDT000000Z stamp", input.DebianSnapshot)
	}
	if snapshot.After(now) {
		return nil, nil, fmt.Errorf("pin hygiene Debian snapshot %s is later than now %s", input.DebianSnapshot, input.Now)
	}
	current, err := decodePinHygienePlan("provider_plan", input.ProviderPlan)
	if err != nil {
		return nil, nil, err
	}
	lagged, err := decodePinHygienePlan("lagged_provider_plan", input.LaggedProviderPlan)
	if err != nil {
		return nil, nil, err
	}
	laggedAt := now.Add(-grace).Format(time.RFC3339)
	if lagged.GeneratedAt != laggedAt {
		return nil, nil, fmt.Errorf("the lagged provider plan was made at %q, not at now minus the grace (%s)", lagged.GeneratedAt, laggedAt)
	}
	if len(input.RefreshPRs) != 2 {
		return nil, nil, errors.New("pin hygiene refresh_prs must name exactly the provider and toolchain kinds")
	}
	freshPR := map[string]*PinHygieneRefreshPR{}
	prNotes := map[string]string{}
	// uncovered says why no fresh refresh PR carries a kind's drift.
	uncovered := map[string]string{}
	for _, kind := range []string{"provider", "toolchain"} {
		pr, ok := input.RefreshPRs[kind]
		if !ok {
			return nil, nil, fmt.Errorf("pin hygiene refresh_prs must name the %s kind", kind)
		}
		if pr == nil {
			prNotes[kind] = "no open " + kind + " refresh PR"
			uncovered[kind] = prNotes[kind]
			continue
		}
		if pr.URL == "" {
			return nil, nil, fmt.Errorf("the open %s refresh PR has no URL", kind)
		}
		opened, err := time.Parse(time.RFC3339, pr.CreatedAt)
		if err != nil {
			return nil, nil, fmt.Errorf("the open %s refresh PR %s has no RFC 3339 created_at", kind, pr.URL)
		}
		if now.Sub(opened) > grace {
			prNotes[kind] = fmt.Sprintf("open %s refresh PR %s is stale: opened %s, %s ago, past the grace", kind, pr.URL, pr.CreatedAt, pinHygieneAge(now.Sub(opened)))
			uncovered[kind] = "only a stale PR: " + prNotes[kind]
			continue
		}
		freshPR[kind] = pr
		prNotes[kind] = fmt.Sprintf("open %s refresh PR %s, opened %s", kind, pr.URL, pr.CreatedAt)
	}

	lines := []string{fmt.Sprintf("Pin hygiene verdict at %s with a %dh grace:", now.Format(time.RFC3339), input.GraceHours)}
	var problems []string

	toolchainAge := now.Sub(snapshot)
	switch {
	case !*input.ToolchainPending:
		lines = append(lines, "  toolchain: up to date")
	case toolchainAge <= grace:
		lines = append(lines, fmt.Sprintf("  toolchain: update pending, expected: the last toolchain refresh, at Debian snapshot %s, is %s old, within the grace", input.DebianSnapshot, pinHygieneAge(toolchainAge)))
	case freshPR["toolchain"] != nil:
		lines = append(lines, fmt.Sprintf("  toolchain: update pending since the last toolchain refresh, at Debian snapshot %s, %s old, carried by the %s", input.DebianSnapshot, pinHygieneAge(toolchainAge), prNotes["toolchain"]))
	default:
		problems = append(problems, fmt.Sprintf("a toolchain update is pending and the last toolchain refresh, at Debian snapshot %s, is %s old, past the grace; there is %s", input.DebianSnapshot, pinHygieneAge(toolchainAge), uncovered["toolchain"]))
	}

	currentSelections := pinHygieneSelections(current)
	laggedSelections := pinHygieneSelections(lagged)
	currentByLabel := make(map[string]ProviderBumpSelection, len(currentSelections))
	for _, entry := range currentSelections {
		currentByLabel[entry.label] = entry.selection
	}
	cutoff := fmt.Sprintf("%dh cool-off plus the grace (released before %s)", lagged.CooloffHours, lagged.Cutoff)
	var staleProvider, heldProvider []string
	for _, entry := range laggedSelections {
		currentSelection, known := currentByLabel[entry.label]
		if !known {
			return nil, nil, fmt.Errorf("the lagged provider plan names %s, which the provider plan does not", entry.label)
		}
		currentHold, currentHeld := pinHygieneUnclassifiedHold(currentSelection)
		if laggedHold, held := pinHygieneUnclassifiedHold(entry.selection); held && currentHeld {
			heldProvider = append(heldProvider, entry.label)
			problems = append(problems, fmt.Sprintf("%s %s, released %s, is held on an unclassified CLI surface past the %s, and the newest eligible release is still held (%s %s); classify the surface in the provider policy", entry.label, laggedHold.Version, laggedHold.PublishedAt, cutoff, currentHold.Version, currentHold.Reason))
			continue
		}
		if !entry.selection.Changed {
			continue
		}
		staleProvider = append(staleProvider, entry.label)
		switch {
		case currentHeld:
			problems = append(problems, fmt.Sprintf("%s %s -> %s, released %s, is missing past the %s, and no refresh PR can carry it while the newest release is held (%s %s)", entry.label, entry.selection.CurrentVersion, entry.selection.TargetVersion, entry.selection.PublishedAt, cutoff, currentHold.Version, currentHold.Reason))
		case freshPR["provider"] != nil:
			lines = append(lines, fmt.Sprintf("  provider: %s %s -> %s, released %s, is past the %s, carried by the %s", entry.label, entry.selection.CurrentVersion, entry.selection.TargetVersion, entry.selection.PublishedAt, cutoff, prNotes["provider"]))
		default:
			problems = append(problems, fmt.Sprintf("%s %s -> %s, released %s, is missing past the %s, and there is %s", entry.label, entry.selection.CurrentVersion, entry.selection.TargetVersion, entry.selection.PublishedAt, cutoff, uncovered["provider"]))
		}
	}
	if len(staleProvider) == 0 && len(heldProvider) == 0 {
		if current.HasChanges {
			lines = append(lines, fmt.Sprintf("  provider: update pending, expected: no release from before the %s is missing", cutoff))
		} else {
			lines = append(lines, "  provider: up to date")
		}
	}
	for _, entry := range currentSelections {
		if hold, held := pinHygieneUnclassifiedHold(entry.selection); held && !slices.Contains(heldProvider, entry.label) {
			lines = append(lines, fmt.Sprintf("  provider: %s %s, released %s, is held on an unclassified CLI surface within the %s: %s", entry.label, hold.Version, hold.PublishedAt, cutoff, hold.Reason))
		}
	}
	lines = append(lines, "  refresh PRs: "+prNotes["provider"]+"; "+prNotes["toolchain"])
	return lines, problems, nil
}

func decodePinHygienePlan(field string, raw json.RawMessage) (*ProviderBumpPlan, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("pin hygiene input must hold %s", field)
	}
	var plan ProviderBumpPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, fmt.Errorf("read pin hygiene %s: %w", field, err)
	}
	if plan.GeneratedAt == "" || plan.Cutoff == "" || len(plan.Providers) == 0 {
		return nil, fmt.Errorf("pin hygiene %s is not a provider bump plan", field)
	}
	return &plan, nil
}

// pinHygieneSelections lists every pin of a provider bump plan under the
// label that update-provider-pins.sh prints for it.
func pinHygieneSelections(plan *ProviderBumpPlan) []pinHygieneSelection {
	var selections []pinHygieneSelection
	add := func(entries map[string]ProviderBumpSelection, suffix string) {
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			selections = append(selections, pinHygieneSelection{label: key + suffix, selection: entries[key]})
		}
	}
	add(plan.Providers, "")
	add(plan.Uncertified, " (uncertified npm)")
	add(plan.UncertifiedReleases, " (uncertified GitHub release)")
	return selections
}

func pinHygieneUnclassifiedHold(selection ProviderBumpSelection) (ProviderBumpSkippedRelease, bool) {
	for _, skipped := range selection.SkippedReleases {
		if strings.HasPrefix(skipped.Reason, pinHygieneUnclassifiedTag) {
			return skipped, true
		}
	}
	return ProviderBumpSkippedRelease{}, false
}

func pinHygieneAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	return fmt.Sprintf("%dh", int(age/time.Hour))
}
