// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package jsonstrict checks JSON that arrives as untrusted input before
// encoding/json decodes it.
package jsonstrict

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// RejectDuplicateKeys returns an error when an object in the first JSON value
// of raw repeats a key at any depth, or when arrays and objects nest more
// than maxDepth levels deep. encoding/json keeps the last of a repeated key, so a
// repeated key could carry a value past a check that read another copy.
func RejectDuplicateKeys(raw []byte, maxDepth int) error {
	return scanValue(json.NewDecoder(bytes.NewReader(raw)), 0, maxDepth)
}

func scanValue(decoder *json.Decoder, depth, maxDepth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	if depth >= maxDepth {
		return fmt.Errorf("JSON nesting exceeds %d levels", maxDepth)
	}
	if delimiter == '{' {
		seen := make(map[string]struct{})
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("JSON object key must be a string")
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", name)
			}
			seen[name] = struct{}{}
			if err := scanValue(decoder, depth+1, maxDepth); err != nil {
				return err
			}
		}
	} else {
		for decoder.More() {
			if err := scanValue(decoder, depth+1, maxDepth); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}
