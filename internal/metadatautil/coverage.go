package metadatautil

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

func CoveragePercent(reportPath string) (float64, error) {
	var report map[string]any
	if err := readJSONFile(reportPath, &report); err != nil {
		return 0, err
	}
	if totals, ok := report["totals"].(map[string]any); ok {
		if percent, ok := jsonNumberToFloat64(totals["percent_covered"]); ok {
			return percent, nil
		}
	}
	if data, ok := report["data"].([]any); ok && len(data) > 0 {
		if entry, ok := data[0].(map[string]any); ok {
			if totals, ok := entry["totals"].(map[string]any); ok {
				if lines, ok := totals["lines"].(map[string]any); ok {
					if percent, ok := jsonNumberToFloat64(lines["percent"]); ok {
						return percent, nil
					}
				}
			}
		}
	}
	return 0, errors.New("coverage report does not contain a supported totals.percent_covered or data[0].totals.lines.percent field")
}

func CoverageExecutables(messagePath string) ([]string, error) {
	content, err := os.ReadFile(messagePath)
	if err != nil {
		return nil, err
	}
	executables := make([]string, 0)
	for _, rawLine := range splitLines(string(content)) {
		if rawLine == "" || rawLine[0] != '{' {
			continue
		}
		var message map[string]any
		if err := json.Unmarshal([]byte(rawLine), &message); err != nil {
			continue
		}
		if message["reason"] != "compiler-artifact" {
			continue
		}
		executable, _ := message["executable"].(string)
		target, _ := message["target"].(map[string]any)
		if executable != "" && target != nil {
			if kind, _ := target["kind"].([]any); len(kind) == 1 {
				if label, _ := kind[0].(string); label == "bin" {
					executables = append(executables, executable)
				}
			}
		}
	}
	if len(executables) == 0 {
		return nil, errors.New("Unable to locate instrumented Rust test executables for coverage")
	}
	sort.Strings(executables)
	unique := executables[:0]
	var last string
	for i, executable := range executables {
		if i == 0 || executable != last {
			unique = append(unique, executable)
			last = executable
		}
	}
	return unique, nil
}

func jsonNumberToFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := make([]string, 0)
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lines = append(lines, text[start:i])
			start = i + 1
		}
	}
	if start <= len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

func formatCoveragePercent(label string, percent float64) string {
	return fmt.Sprintf("%s: %.2f%%", label, percent)
}
