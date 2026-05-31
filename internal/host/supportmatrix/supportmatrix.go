// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportmatrix

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Query struct {
	HostOS               string
	HostArch             string
	HostDistro           string
	HostDistroVersion    string
	TargetKind           string
	TargetProvider       string
	TargetAssuranceClass string
}

type Result struct {
	HostOS               string
	HostArch             string
	HostDistro           string
	HostDistroVersion    string
	TargetKind           string
	TargetProvider       string
	TargetAssuranceClass string
	Status               string
	Launch               string
	Evidence             string
	ValidationLane       string
	Reason               string
}

var columns = []string{
	"host_os",
	"host_arch",
	"host_distro",
	"host_distro_version",
	"target_kind",
	"target_provider",
	"target_assurance_class",
	"status",
	"launch",
	"evidence",
	"validation_lane",
	"reason",
}

func Evaluate(path string, query Query) (Result, error) {
	query = normalizeQuery(query)
	result := defaultResult(query)

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
			if err := validateHeader(path, lineNo, fields); err != nil {
				return result, err
			}
			headerSeen = true
			continue
		}
		entry, err := parseEntry(path, lineNo, fields)
		if err != nil {
			return result, err
		}
		if entry.matches(query) {
			return entry.withQueryHost(query), nil
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

func MetadataLines(result Result) []string {
	return []string{
		fmt.Sprintf("host_os=%s", result.HostOS),
		fmt.Sprintf("host_arch=%s", result.HostArch),
		fmt.Sprintf("host_distro=%s", result.HostDistro),
		fmt.Sprintf("host_distro_version=%s", result.HostDistroVersion),
		fmt.Sprintf("support_matrix_status=%s", result.Status),
		fmt.Sprintf("support_matrix_launch=%s", result.Launch),
		fmt.Sprintf("support_matrix_evidence=%s", result.Evidence),
		fmt.Sprintf("support_matrix_validation_lane=%s", result.ValidationLane),
		fmt.Sprintf("support_matrix_reason=%s", result.Reason),
	}
}

func normalizeQuery(query Query) Query {
	query.HostOS = strings.TrimSpace(query.HostOS)
	query.HostArch = strings.TrimSpace(query.HostArch)
	query.HostDistro = strings.TrimSpace(query.HostDistro)
	query.HostDistroVersion = strings.TrimSpace(query.HostDistroVersion)
	query.TargetKind = strings.TrimSpace(query.TargetKind)
	query.TargetProvider = strings.TrimSpace(query.TargetProvider)
	query.TargetAssuranceClass = strings.TrimSpace(query.TargetAssuranceClass)
	return query
}

func defaultResult(query Query) Result {
	return Result{
		HostOS:               query.HostOS,
		HostArch:             query.HostArch,
		HostDistro:           query.HostDistro,
		HostDistroVersion:    query.HostDistroVersion,
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

func validateHeader(path string, lineNo int, fields []string) error {
	if len(fields) != len(columns) {
		return fmt.Errorf("%s:%d: expected %d tab-separated columns in header, got %d", path, lineNo, len(columns), len(fields))
	}
	for idx, want := range columns {
		if fields[idx] != want {
			return fmt.Errorf("%s:%d: header column %d must be %q, got %q", path, lineNo, idx+1, want, fields[idx])
		}
	}
	return nil
}

func parseEntry(path string, lineNo int, fields []string) (Result, error) {
	if len(fields) != len(columns) {
		return Result{}, fmt.Errorf("%s:%d: expected %d tab-separated columns, got %d", path, lineNo, len(columns), len(fields))
	}

	entry := Result{
		HostOS:               strings.TrimSpace(fields[0]),
		HostArch:             strings.TrimSpace(fields[1]),
		HostDistro:           strings.TrimSpace(fields[2]),
		HostDistroVersion:    strings.TrimSpace(fields[3]),
		TargetKind:           strings.TrimSpace(fields[4]),
		TargetProvider:       strings.TrimSpace(fields[5]),
		TargetAssuranceClass: strings.TrimSpace(fields[6]),
		Status:               strings.TrimSpace(fields[7]),
		Launch:               strings.TrimSpace(fields[8]),
		Evidence:             strings.TrimSpace(fields[9]),
		ValidationLane:       strings.TrimSpace(fields[10]),
		Reason:               strings.TrimSpace(fields[11]),
	}

	for _, pair := range []struct {
		name  string
		value string
	}{
		{name: "host_os", value: entry.HostOS},
		{name: "host_arch", value: entry.HostArch},
		{name: "host_distro", value: entry.HostDistro},
		{name: "host_distro_version", value: entry.HostDistroVersion},
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
			return Result{}, fmt.Errorf("%s:%d: %s may not be empty", path, lineNo, pair.name)
		}
	}

	switch entry.Status {
	case "supported", "validation-host-only", "preview-only", "unsupported":
	default:
		return Result{}, fmt.Errorf("%s:%d: unsupported status %q", path, lineNo, entry.Status)
	}
	switch entry.Launch {
	case "allowed", "blocked":
	default:
		return Result{}, fmt.Errorf("%s:%d: unsupported launch value %q", path, lineNo, entry.Launch)
	}
	switch entry.Evidence {
	case "repo-required", "certification-only", "manual-only", "none":
	default:
		return Result{}, fmt.Errorf("%s:%d: unsupported evidence value %q", path, lineNo, entry.Evidence)
	}
	if entry.ValidationLane == "none" && entry.Status == "validation-host-only" {
		return Result{}, fmt.Errorf("%s:%d: validation-host-only rows must name a validation_lane", path, lineNo)
	}
	if entry.ValidationLane != "none" && (entry.Status == "supported" || entry.Status == "preview-only") {
		return Result{}, fmt.Errorf("%s:%d: supported launch rows must not set a validation-only lane", path, lineNo)
	}
	if entry.Status == "preview-only" && entry.Launch != "blocked" {
		return Result{}, fmt.Errorf("%s:%d: preview-only rows must set launch=blocked", path, lineNo)
	}
	return entry, nil
}

func (entry Result) matches(query Query) bool {
	return matrixFieldMatches(entry.HostOS, query.HostOS) &&
		matrixFieldMatches(entry.HostArch, query.HostArch) &&
		matrixFieldMatches(entry.HostDistro, query.HostDistro) &&
		matrixFieldMatches(entry.HostDistroVersion, query.HostDistroVersion) &&
		entry.TargetKind == query.TargetKind &&
		entry.TargetProvider == query.TargetProvider &&
		entry.TargetAssuranceClass == query.TargetAssuranceClass
}

func matrixFieldMatches(rowValue, queryValue string) bool {
	return rowValue == "any" || rowValue == queryValue
}

func (entry Result) withQueryHost(query Query) Result {
	entry.HostOS = query.HostOS
	entry.HostArch = query.HostArch
	entry.HostDistro = query.HostDistro
	entry.HostDistroVersion = query.HostDistroVersion
	return entry
}
