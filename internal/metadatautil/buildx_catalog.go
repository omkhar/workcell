// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type buildxCatalogContents struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type buildxCatalogRelease struct {
	TagName string `json:"tag_name"`
}

// SelectInstallableBuildxVersion chooses the newest candidate only after the
// catalog consumed by docker/setup-buildx-action can install it. The current
// pin must remain present so falling back cannot select an unusable version.
func SelectInstallableBuildxVersion(r io.Reader, current, candidate string) (string, error) {
	if current == "" || candidate == "" {
		return "", fmt.Errorf("Buildx current and candidate versions must be non-empty")
	}

	decoder := json.NewDecoder(r)
	var contents buildxCatalogContents
	if err := decoder.Decode(&contents); err != nil {
		return "", fmt.Errorf("decode Buildx install catalog response: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return "", fmt.Errorf("decode Buildx install catalog response: %w", err)
	}
	if contents.Encoding != "base64" {
		return "", fmt.Errorf("Buildx install catalog encoding is %q, want base64", contents.Encoding)
	}

	encoded := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, contents.Content)
	catalogBytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode Buildx install catalog content: %w", err)
	}
	var catalog map[string]buildxCatalogRelease
	catalogDecoder := json.NewDecoder(strings.NewReader(string(catalogBytes)))
	if err := catalogDecoder.Decode(&catalog); err != nil {
		return "", fmt.Errorf("decode Buildx install catalog: %w", err)
	}
	if err := requireJSONEOF(catalogDecoder); err != nil {
		return "", fmt.Errorf("decode Buildx install catalog: %w", err)
	}

	currentRelease, ok := catalog[current]
	if !ok {
		return "", fmt.Errorf("current Buildx pin %s is absent from the setup-buildx-action install catalog", current)
	}
	if currentRelease.TagName != current {
		return "", fmt.Errorf("Buildx install catalog entry %s has tag_name %q", current, currentRelease.TagName)
	}
	candidateRelease, ok := catalog[candidate]
	if !ok {
		return current, nil
	}
	if candidateRelease.TagName != candidate {
		return "", fmt.Errorf("Buildx install catalog entry %s has tag_name %q", candidate, candidateRelease.TagName)
	}
	return candidate, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
