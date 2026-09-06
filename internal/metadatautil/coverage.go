// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
)

func CoveragePercent(reportPath string) (float64, error) {
	var report map[string]any
	if err := readJSONFile(reportPath, &report); err != nil {
		return 0, err
	}
	if totals, ok := report["totals"].(map[string]any); ok {
		if percent, ok := totals["percent_covered"].(float64); ok {
			return percent, nil
		}
	}
	if data, ok := report["data"].([]any); ok && len(data) > 0 {
		if entry, ok := data[0].(map[string]any); ok {
			if totals, ok := entry["totals"].(map[string]any); ok {
				if lines, ok := totals["lines"].(map[string]any); ok {
					if percent, ok := lines["percent"].(float64); ok {
						return percent, nil
					}
				}
			}
		}
	}
	return 0, errors.New("coverage report does not contain a supported totals.percent_covered or data[0].totals.lines.percent field")
}

func CoverageExecutables(messagePath string) ([]string, error) {
	handle, err := os.Open(messagePath)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	reader := bufio.NewReader(handle)
	executables := make([]string, 0)
	var lineBuf bytes.Buffer
	for {
		fragment, isPrefix, readErr := reader.ReadLine()
		if len(fragment) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if len(fragment) > 0 {
			lineBuf.Write(fragment)
		}
		if isPrefix {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return nil, readErr
			}
			continue
		}

		line := bytes.TrimSpace(lineBuf.Bytes())
		if len(line) > 0 && line[0] == '{' {
			var message map[string]any
			if err := json.Unmarshal(line, &message); err == nil {
				if message["reason"] == "compiler-artifact" {
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
			}
		}
		lineBuf.Reset()

		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	if len(executables) == 0 {
		return nil, errors.New("unable to locate instrumented Rust test executables for coverage")
	}
	slices.Sort(executables)
	return slices.Compact(executables), nil
}
