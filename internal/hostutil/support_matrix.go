// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type SupportMatrixQuery struct {
	HostOS               string
	HostArch             string
	TargetKind           string
	TargetProvider       string
	TargetAssuranceClass string
}

type SupportMatrixResult struct {
	HostOS               string
	HostArch             string
	TargetKind           string
	TargetProvider       string
	TargetAssuranceClass string
	Status               string
	Launch               string
	Evidence             string
	ValidationLane       string
	Reason               string
}

var supportMatrixColumns = []string{
	"host_os",
	"host_arch",
	"target_kind",
	"target_provider",
	"target_assurance_class",
	"status",
	"launch",
	"evidence",
	"validation_lane",
	"reason",
}

func EvaluateSupportMatrix(path string, query SupportMatrixQuery) (SupportMatrixResult, error) {
	query = normalizeSupportMatrixQuery(query)
	result := defaultSupportMatrixResult(query)

	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	headerSeen := false
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if !headerSeen {
			if err := validateSupportMatrixHeader(path, lineNo, fields); err != nil {
				return result, err
			}
			headerSeen = true
			continue
		}
		entry, err := parseSupportMatrixEntry(path, lineNo, fields)
		if err != nil {
			return result, err
		}
		if entry.matches(query) {
			return entry, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if !headerSeen {
		return result, fmt.Errorf("%s: missing support-matrix header", path)
	}
	return result, nil
}

func SupportMatrixMetadataLines(result SupportMatrixResult) []string {
	return []string{
		fmt.Sprintf("host_os=%s", result.HostOS),
		fmt.Sprintf("host_arch=%s", result.HostArch),
		fmt.Sprintf("support_matrix_status=%s", result.Status),
		fmt.Sprintf("support_matrix_launch=%s", result.Launch),
		fmt.Sprintf("support_matrix_evidence=%s", result.Evidence),
		fmt.Sprintf("support_matrix_validation_lane=%s", result.ValidationLane),
		fmt.Sprintf("support_matrix_reason=%s", result.Reason),
	}
}

func normalizeSupportMatrixQuery(query SupportMatrixQuery) SupportMatrixQuery {
	query.HostOS = strings.TrimSpace(query.HostOS)
	query.HostArch = strings.TrimSpace(query.HostArch)
	query.TargetKind = strings.TrimSpace(query.TargetKind)
	query.TargetProvider = strings.TrimSpace(query.TargetProvider)
	query.TargetAssuranceClass = strings.TrimSpace(query.TargetAssuranceClass)
	return query
}

func defaultSupportMatrixResult(query SupportMatrixQuery) SupportMatrixResult {
	return SupportMatrixResult{
		HostOS:               query.HostOS,
		HostArch:             query.HostArch,
		TargetKind:           query.TargetKind,
		TargetProvider:       query.TargetProvider,
		TargetAssuranceClass: query.TargetAssuranceClass,
		Status:               "unsupported",
		Launch:               "blocked",
		Evidence:             "none",
		ValidationLane:       "none",
		Reason:               "not-in-reviewed-support-matrix",
	}
}

func validateSupportMatrixHeader(path string, lineNo int, fields []string) error {
	if len(fields) != len(supportMatrixColumns) {
		return fmt.Errorf("%s:%d: expected %d tab-separated columns in header, got %d", path, lineNo, len(supportMatrixColumns), len(fields))
	}
	for idx, want := range supportMatrixColumns {
		if fields[idx] != want {
			return fmt.Errorf("%s:%d: header column %d must be %q, got %q", path, lineNo, idx+1, want, fields[idx])
		}
	}
	return nil
}

func parseSupportMatrixEntry(path string, lineNo int, fields []string) (SupportMatrixResult, error) {
	if len(fields) != len(supportMatrixColumns) {
		return SupportMatrixResult{}, fmt.Errorf("%s:%d: expected %d tab-separated columns, got %d", path, lineNo, len(supportMatrixColumns), len(fields))
	}

	entry := SupportMatrixResult{
		HostOS:               strings.TrimSpace(fields[0]),
		HostArch:             strings.TrimSpace(fields[1]),
		TargetKind:           strings.TrimSpace(fields[2]),
		TargetProvider:       strings.TrimSpace(fields[3]),
		TargetAssuranceClass: strings.TrimSpace(fields[4]),
		Status:               strings.TrimSpace(fields[5]),
		Launch:               strings.TrimSpace(fields[6]),
		Evidence:             strings.TrimSpace(fields[7]),
		ValidationLane:       strings.TrimSpace(fields[8]),
		Reason:               strings.TrimSpace(fields[9]),
	}

	for _, pair := range []struct {
		name  string
		value string
	}{
		{name: "host_os", value: entry.HostOS},
		{name: "host_arch", value: entry.HostArch},
		{name: "target_kind", value: entry.TargetKind},
		{name: "target_provider", value: entry.TargetProvider},
		{name: "target_assurance_class", value: entry.TargetAssuranceClass},
		{name: "status", value: entry.Status},
		{name: "launch", value: entry.Launch},
		{name: "evidence", value: entry.Evidence},
		{name: "validation_lane", value: entry.ValidationLane},
		{name: "reason", value: entry.Reason},
	} {
		if pair.value == "" {
			return SupportMatrixResult{}, fmt.Errorf("%s:%d: %s may not be empty", path, lineNo, pair.name)
		}
	}

	switch entry.Status {
	case "supported", "validation-host-only", "unsupported":
	default:
		return SupportMatrixResult{}, fmt.Errorf("%s:%d: unsupported status %q", path, lineNo, entry.Status)
	}
	switch entry.Launch {
	case "allowed", "blocked":
	default:
		return SupportMatrixResult{}, fmt.Errorf("%s:%d: unsupported launch value %q", path, lineNo, entry.Launch)
	}
	switch entry.Evidence {
	case "repo-required", "certification-only", "manual-only", "none":
	default:
		return SupportMatrixResult{}, fmt.Errorf("%s:%d: unsupported evidence value %q", path, lineNo, entry.Evidence)
	}
	if entry.ValidationLane == "none" && entry.Status == "validation-host-only" {
		return SupportMatrixResult{}, fmt.Errorf("%s:%d: validation-host-only rows must name a validation_lane", path, lineNo)
	}
	if entry.ValidationLane != "none" && entry.Status == "supported" {
		return SupportMatrixResult{}, fmt.Errorf("%s:%d: supported launch rows must not set a validation-only lane", path, lineNo)
	}
	return entry, nil
}

func (entry SupportMatrixResult) matches(query SupportMatrixQuery) bool {
	return entry.HostOS == query.HostOS &&
		entry.HostArch == query.HostArch &&
		entry.TargetKind == query.TargetKind &&
		entry.TargetProvider == query.TargetProvider &&
		entry.TargetAssuranceClass == query.TargetAssuranceClass
}
