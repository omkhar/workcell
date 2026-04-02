package metadatautil

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func stripComment(line string) string {
	escaped := false
	quoteChar := byte(0)
	result := make([]byte, 0, len(line))

	for i := 0; i < len(line); i++ {
		char := line[i]
		if escaped {
			result = append(result, char)
			escaped = false
			continue
		}
		if char == '\\' && quoteChar == '"' {
			result = append(result, char)
			escaped = true
			continue
		}
		if char == '\'' || char == '"' {
			if quoteChar == 0 {
				quoteChar = char
			} else if quoteChar == char {
				quoteChar = 0
			}
			result = append(result, char)
			continue
		}
		if char == '#' && quoteChar == 0 {
			break
		}
		result = append(result, char)
	}
	return strings.TrimSpace(string(result))
}

func parseTomlValue(raw string, context string) (any, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, fmt.Errorf("%s: expected a value", context)
	}
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid quoted string: %w", context, err)
		}
		return parsed, nil
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return strings.Trim(value, "'"), nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return parseTomlArray(value, context)
	}
	if i, err := strconv.Atoi(value); err == nil {
		return i, nil
	}
	return nil, fmt.Errorf("%s: unsupported TOML value", context)
}

func parseTomlArray(raw string, context string) ([]any, error) {
	inner := strings.TrimSpace(raw[1 : len(raw)-1])
	if inner == "" {
		return []any{}, nil
	}

	items := splitTomlArray(inner)
	values := make([]any, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			continue
		}
		value, err := parseTomlValue(item, context)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func splitTomlArray(raw string) []string {
	items := make([]string, 0)
	quoteChar := byte(0)
	escaped := false
	start := 0
	for i := 0; i < len(raw); i++ {
		char := raw[i]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quoteChar == '"' {
			escaped = true
			continue
		}
		if char == '\'' || char == '"' {
			if quoteChar == 0 {
				quoteChar = char
			} else if quoteChar == char {
				quoteChar = 0
			}
			continue
		}
		if char == ',' && quoteChar == 0 {
			if item := strings.TrimSpace(raw[start:i]); item != "" {
				items = append(items, item)
			}
			start = i + 1
		}
	}
	if item := strings.TrimSpace(raw[start:]); item != "" {
		items = append(items, item)
	}
	return items
}

func tomlArrayClosed(raw string) bool {
	depth := 0
	quoteChar := byte(0)
	escaped := false
	for i := 0; i < len(raw); i++ {
		char := raw[i]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && quoteChar == '"' {
			escaped = true
			continue
		}
		if char == '\'' || char == '"' {
			if quoteChar == 0 {
				quoteChar = char
			} else if quoteChar == char {
				quoteChar = 0
			}
			continue
		}
		if quoteChar != 0 {
			continue
		}
		switch char {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth == 0
}

func parseTomlPath(raw string) ([]string, error) {
	parts := strings.Split(raw, ".")
	path := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, errors.New("empty table component")
		}
		if strings.Contains(name, " ") {
			return nil, fmt.Errorf("unsupported table component %q", name)
		}
		path = append(path, name)
	}
	return path, nil
}

func ensureTomlTable(root map[string]any, path []string) (map[string]any, error) {
	current := root
	for _, part := range path {
		existing, ok := current[part]
		if !ok {
			child := map[string]any{}
			current[part] = child
			current = child
			continue
		}
		child, ok := existing.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s is not a table", strings.Join(path, "."))
		}
		current = child
	}
	return current, nil
}

func ParseTOMLSubset(content string, sourcePath string) (map[string]any, error) {
	root := map[string]any{}
	current := root
	seenTables := map[string]struct{}{}

	lines := strings.Split(content, "\n")
	for idx := 0; idx < len(lines); idx++ {
		rawLine := lines[idx]
		lineNo := idx + 1
		line := stripComment(rawLine)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			return nil, fmt.Errorf("%s:%d: unsupported array-of-table", sourcePath, lineNo)
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			tableName := strings.TrimSpace(line[1 : len(line)-1])
			if tableName == "" {
				return nil, fmt.Errorf("%s:%d: empty table name", sourcePath, lineNo)
			}
			if _, exists := seenTables[tableName]; exists {
				return nil, fmt.Errorf("%s:%d: duplicate table [%s]", sourcePath, lineNo, tableName)
			}
			path, err := parseTomlPath(tableName)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", sourcePath, lineNo, err)
			}
			current, err = ensureTomlTable(root, path)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", sourcePath, lineNo, err)
			}
			seenTables[tableName] = struct{}{}
			continue
		}

		if !strings.Contains(line, "=") {
			return nil, fmt.Errorf("%s:%d: expected key = value", sourcePath, lineNo)
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", sourcePath, lineNo)
		}
		if strings.Contains(key, ".") {
			return nil, fmt.Errorf("%s:%d: dotted TOML keys are not supported", sourcePath, lineNo)
		}
		if _, exists := current[key]; exists {
			return nil, fmt.Errorf("%s:%d: duplicate key: %s", sourcePath, lineNo, key)
		}
		valueText := parts[1]
		if strings.HasPrefix(strings.TrimSpace(valueText), "[") && !tomlArrayClosed(valueText) {
			for {
				idx++
				if idx >= len(lines) {
					return nil, fmt.Errorf("%s:%d: unterminated TOML array", sourcePath, lineNo)
				}
				nextLine := stripComment(lines[idx])
				if nextLine == "" {
					continue
				}
				valueText += "\n" + nextLine
				if tomlArrayClosed(valueText) {
					break
				}
			}
		}
		value, err := parseTomlValue(valueText, fmt.Sprintf("%s:%d", sourcePath, lineNo))
		if err != nil {
			return nil, err
		}
		current[key] = value
	}

	return root, nil
}

func MustString(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok
}

func MustStringMap(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok
}

func MustStringSlice(value any) ([]string, bool, error) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false, nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false, errors.New("array value must contain only strings")
		}
		result = append(result, s)
	}
	return result, true, nil
}
