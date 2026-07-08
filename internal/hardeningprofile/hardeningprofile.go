// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package hardeningprofile is the deterministic conformance check behind
// roadmap item A6 (documented syscall/filesystem hardening profile). It reads
// the reviewed policy artifact policy/hardening-profile.toml and asserts that
// every declared `required` literal appears verbatim in its section's `target`
// file, and every declared `forbidden` literal is absent — so a drift that
// weakens the runtime's container hardening posture (dropping `--cap-drop ALL`,
// weakening a tmpfs mount, adding `--privileged`) or silently removes an
// outbound endpoint from the egress inventory FAILS CI.
//
// # Why artifact-driven
//
// The static-invariant checks in internal/workcellhardening hardcode their
// launcher literals in Go. A6 instead requires the EXPECTED posture to live in
// a checked-in, human-reviewed policy artifact, with the Go code enforcing
// artifact-vs-source conformance (the same split metadatautil uses for
// policy/github-hosted-controls.toml). This package therefore parses the TOML
// with the shared internal/tomlsubset AST parser and iterates its tables in
// source-declaration order, so the first-violation message is deterministic and
// diffs one-to-one against the artifact.
//
// # Matching semantics
//
// Each literal is matched as a verbatim substring of the target file
// (strings.Contains), mirroring the fixed-string `rg -q FIXED` idiom the
// existing invariants use. A missing/unreadable target file is treated as empty
// content: every `required` literal then fails (with a clear message) and every
// `forbidden` literal passes, exactly as a fixed-string grep on a missing file
// behaves.
package hardeningprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

// profileRelPath is the repo-relative path to the reviewed hardening-profile
// policy artifact this check enforces.
const profileRelPath = "policy/hardening-profile.toml"

// Check runs the hardening-profile conformance check against the repo rooted at
// rootDir. It returns nil when every section's required literals are present and
// forbidden literals are absent in the section's target file, or an error whose
// message describes the first violated section/literal.
//
// A missing or malformed policy/hardening-profile.toml is itself a violation:
// the check cannot certify a posture it cannot read.
func Check(rootDir string) error {
	content, err := os.ReadFile(filepath.Join(rootDir, profileRelPath))
	if err != nil {
		return fmt.Errorf("hardening-profile: cannot read %s: %w", profileRelPath, err)
	}
	doc, err := tomlsubset.ParseDocument(string(content), profileRelPath)
	if err != nil {
		return fmt.Errorf("hardening-profile: cannot parse %s: %w", profileRelPath, err)
	}

	if version := doc.TopLevel.Lookup("version"); version == nil || version.Value != 1 {
		return fmt.Errorf("hardening-profile: %s must declare version = 1", profileRelPath)
	}
	if len(doc.Tables) == 0 {
		return fmt.Errorf("hardening-profile: %s declares no hardening sections", profileRelPath)
	}

	cache := make(map[string]string)
	readTarget := func(rel string) string {
		if text, ok := cache[rel]; ok {
			return text
		}
		body, readErr := os.ReadFile(filepath.Join(rootDir, rel))
		if readErr != nil {
			body = nil
		}
		text := string(body)
		cache[rel] = text
		return text
	}

	for i := range doc.Tables {
		table := &doc.Tables[i]
		target, err := stringField(table, "target")
		if err != nil {
			return err
		}
		if target == "" {
			return fmt.Errorf("hardening-profile: section [%s] must declare a non-empty target", table.Name)
		}
		required, err := stringArrayField(table, "required")
		if err != nil {
			return err
		}
		forbidden, err := stringArrayField(table, "forbidden")
		if err != nil {
			return err
		}
		if len(required) == 0 && len(forbidden) == 0 {
			return fmt.Errorf("hardening-profile: section [%s] declares neither required nor forbidden literals", table.Name)
		}

		body := readTarget(target)
		for _, literal := range required {
			if !strings.Contains(body, literal) {
				return errors.New(missingRequiredMessage(table.Name, target, literal))
			}
		}
		for _, literal := range forbidden {
			if strings.Contains(body, literal) {
				return errors.New(presentForbiddenMessage(table.Name, target, literal))
			}
		}
	}
	return nil
}

// missingRequiredMessage is the violation message when a required posture
// literal is absent from its target file (a weakening/removal drift).
func missingRequiredMessage(section, target, literal string) string {
	return fmt.Sprintf(
		"hardening-profile: %s is missing required posture literal %q declared in %s [%s]",
		target, literal, profileRelPath, section,
	)
}

// presentForbiddenMessage is the violation message when a forbidden literal is
// present in its target file (an unconfined/privileged drift).
func presentForbiddenMessage(section, target, literal string) string {
	return fmt.Sprintf(
		"hardening-profile: %s contains forbidden posture literal %q declared in %s [%s]",
		target, literal, profileRelPath, section,
	)
}

// stringField returns the string value of key in table, "" if the key is
// absent, or an error if the value is present but not a string.
func stringField(table *tomlsubset.Table, key string) (string, error) {
	pair := table.Lookup(key)
	if pair == nil {
		return "", nil
	}
	value, ok := pair.Value.(string)
	if !ok {
		return "", fmt.Errorf("hardening-profile: section [%s] key %q must be a string", table.Name, key)
	}
	return value, nil
}

// stringArrayField returns the []string value of key in table, nil if the key
// is absent, or an error if the value is not an array of strings.
func stringArrayField(table *tomlsubset.Table, key string) ([]string, error) {
	pair := table.Lookup(key)
	if pair == nil {
		return nil, nil
	}
	items, ok := pair.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("hardening-profile: section [%s] key %q must be an array", table.Name, key)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("hardening-profile: section [%s] key %q must contain only strings", table.Name, key)
		}
		out = append(out, text)
	}
	return out, nil
}
