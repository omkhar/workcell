// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

func ListHostedControlRulesetIDs(summaryPath string, output io.Writer) error {
	summary, err := readRulesetSummary(summaryPath)
	if err != nil {
		return err
	}
	for _, raw := range summary {
		id, err := rulesetSummaryID(raw)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(output, id); err != nil {
			return err
		}
	}
	return nil
}

func NormalizeHostedControlRuleset(input io.Reader, output io.Writer, expectedID string) error {
	id, err := parseExpectedRulesetID(expectedID)
	if err != nil {
		return err
	}
	detail, err := readRulesetDetail(input)
	if err != nil {
		return err
	}
	if err := requireRulesetDetailID(detail, id); err != nil {
		return err
	}
	return encodeHostedControlPage(output, detail)
}

func AssembleHostedControlRulesets(summaryPath, detailsPath, outputPath string) error {
	summary, err := readRulesetSummary(summaryPath)
	if err != nil {
		return err
	}
	details, err := readRulesetDetails(detailsPath)
	if err != nil {
		return err
	}
	if len(details) != len(summary) {
		return fmt.Errorf("ruleset detail count = %d, want %d", len(details), len(summary))
	}
	if err := matchRulesetDetails(summary, details); err != nil {
		return err
	}
	return writeJSONFile(outputPath, details)
}

func readRulesetSummary(path string) ([]any, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values, err := decodeHostedControlPages(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, errors.New("ruleset summary must contain exactly one JSON value")
	}
	summary, ok := values[0].([]any)
	if !ok {
		return nil, errors.New("ruleset summary must be an array")
	}
	return summary, nil
}

func readRulesetDetails(path string) ([]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	details, err := decodeHostedControlPages(file)
	if err != nil {
		return nil, err
	}
	if details == nil {
		details = make([]any, 0)
	}
	return details, nil
}

func readRulesetDetail(input io.Reader) (map[string]any, error) {
	values, err := decodeHostedControlPages(input)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, errors.New("ruleset detail must contain exactly one JSON value")
	}
	detail, ok := values[0].(map[string]any)
	if !ok {
		return nil, errors.New("ruleset detail must be an object")
	}
	return detail, nil
}

func parseExpectedRulesetID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, fmt.Errorf("expected ruleset id must be a canonical positive integer: %q", raw)
	}
	return id, nil
}

func rulesetSummaryID(raw any) (int64, error) {
	entry, ok := raw.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("unexpected ruleset summary entry: %v", raw)
	}
	number, ok := entry["id"].(json.Number)
	if !ok {
		return 0, fmt.Errorf("unexpected ruleset summary id: %v", entry)
	}
	value, err := number.Int64()
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("unexpected ruleset summary id: %v", entry)
	}
	return value, nil
}

func requireRulesetDetailID(detail map[string]any, expected int64) error {
	actual, err := rulesetSummaryID(detail)
	if err != nil {
		return fmt.Errorf("unexpected ruleset detail id: %v", detail)
	}
	if actual != expected {
		return fmt.Errorf("ruleset detail id = %d, want %d", actual, expected)
	}
	return nil
}

func matchRulesetDetails(summary, details []any) error {
	for index := range summary {
		expected, err := rulesetSummaryID(summary[index])
		if err != nil {
			return err
		}
		detail, ok := details[index].(map[string]any)
		if !ok {
			return fmt.Errorf("ruleset detail %d must be an object", index+1)
		}
		if err := requireRulesetDetailID(detail, expected); err != nil {
			return fmt.Errorf("ruleset detail %d: %w", index+1, err)
		}
	}
	return nil
}
