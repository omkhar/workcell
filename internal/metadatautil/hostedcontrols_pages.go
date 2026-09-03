// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var hostedControlCollectionFields = map[string]struct{}{
	"branch_policies": {},
	"environments":    {},
	"secrets":         {},
	"variables":       {},
}

type hostedControlCollectionPage struct {
	totalCount int64
	items      []any
}

func MergeHostedControlObjectPages(input io.Reader, output io.Writer, field string) error {
	if _, ok := hostedControlCollectionFields[field]; !ok {
		return fmt.Errorf("unsupported hosted-control collection field %q", field)
	}
	values, err := decodeHostedControlPages(input)
	if err != nil {
		return err
	}
	pages, err := parseHostedControlCollectionPages(values, field)
	if err != nil {
		return err
	}
	merged, err := mergeHostedControlCollectionPages(pages, field)
	if err != nil {
		return err
	}
	return encodeHostedControlPage(output, merged)
}

func MergeHostedControlArrayPages(input io.Reader, output io.Writer) error {
	values, err := decodeHostedControlPages(input)
	if err != nil {
		return err
	}
	merged, err := mergeHostedControlArrayPages(values)
	if err != nil {
		return err
	}
	return encodeHostedControlPage(output, merged)
}

func decodeHostedControlPages(input io.Reader) ([]any, error) {
	decoder := json.NewDecoder(input)
	decoder.UseNumber()
	var values []any
	for {
		var value any
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			return values, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode hosted-control page %d: %w", len(values)+1, err)
		}
		values = append(values, value)
	}
}

func parseHostedControlCollectionPages(values []any, field string) ([]hostedControlCollectionPage, error) {
	if len(values) == 0 {
		return nil, errors.New("hosted-control collection response must contain at least one page")
	}
	pages := make([]hostedControlCollectionPage, 0, len(values))
	for index, value := range values {
		page, err := parseHostedControlCollectionPage(value, field, index)
		if err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, nil
}

func parseHostedControlCollectionPage(value any, field string, index int) (hostedControlCollectionPage, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return hostedControlCollectionPage{}, fmt.Errorf("hosted-control collection page %d must be an object", index+1)
	}
	totalCount, err := hostedControlPageTotal(object, index)
	if err != nil {
		return hostedControlCollectionPage{}, err
	}
	items, ok := object[field].([]any)
	if !ok {
		return hostedControlCollectionPage{}, fmt.Errorf("hosted-control collection page %d field %q must be an array", index+1, field)
	}
	return hostedControlCollectionPage{totalCount: totalCount, items: items}, nil
}

func hostedControlPageTotal(object map[string]any, index int) (int64, error) {
	value, present := object["total_count"]
	if !present {
		return 0, fmt.Errorf("hosted-control collection page %d must declare total_count", index+1)
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("hosted-control collection page %d total_count must be a nonnegative integer", index+1)
	}
	totalCount, err := number.Int64()
	if err != nil {
		return 0, fmt.Errorf("hosted-control collection page %d total_count must be a nonnegative integer", index+1)
	}
	if totalCount < 0 {
		return 0, fmt.Errorf("hosted-control collection page %d total_count must be a nonnegative integer", index+1)
	}
	return totalCount, nil
}

func mergeHostedControlCollectionPages(pages []hostedControlCollectionPage, field string) (map[string]any, error) {
	items := hostedControlCollectionItems(pages)
	if err := validateHostedControlCollectionTotals(pages, len(items)); err != nil {
		return nil, err
	}
	totalCount := pages[0].totalCount
	return map[string]any{"total_count": totalCount, field: items}, nil
}

func hostedControlCollectionItems(pages []hostedControlCollectionPage) []any {
	items := make([]any, 0)
	for _, page := range pages {
		items = append(items, page.items...)
	}
	return items
}

func validateHostedControlCollectionTotals(pages []hostedControlCollectionPage, itemCount int) error {
	expected := pages[0].totalCount
	for index, page := range pages[1:] {
		if page.totalCount != expected {
			return fmt.Errorf("hosted-control collection page %d total_count is inconsistent", index+2)
		}
	}
	if int64(itemCount) != expected {
		return errors.New("hosted-control collection total_count must equal the combined array length")
	}
	return nil
}

func mergeHostedControlArrayPages(values []any) ([]any, error) {
	if len(values) == 0 {
		return nil, errors.New("hosted-control array response must contain at least one page")
	}
	merged := make([]any, 0)
	for index, value := range values {
		page, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("hosted-control array page %d must be an array", index+1)
		}
		merged = append(merged, page...)
	}
	return merged, nil
}

func encodeHostedControlPage(output io.Writer, value any) error {
	if err := json.NewEncoder(output).Encode(value); err != nil {
		return fmt.Errorf("encode merged hosted-control pages: %w", err)
	}
	return nil
}
