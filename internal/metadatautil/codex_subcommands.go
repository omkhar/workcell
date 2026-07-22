// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultCodexCLISourceAPIURLFormat = "https://api.github.com/repos/openai/codex/contents/codex-rs/cli/src/main.rs?ref=rust-v%s"
	maxCodexCLISourceBytes            = 2 << 20
)

var (
	codexSubcommandEnumStartPattern = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+Subcommand\s*\{\s*$`)
	codexVariantIdentifierPattern   = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)
	codexClapScalarPattern          = regexp.MustCompile(`^(name|alias|visible_alias)\s*=\s*"([a-z0-9][a-z0-9-]*)"$`)
	codexClapListPattern            = regexp.MustCompile(`^(aliases|visible_aliases)\s*=\s*\[(.*)\]$`)
	codexQuotedTokenPattern         = regexp.MustCompile(`^"([a-z0-9][a-z0-9-]*)"$`)
	codexDeriveAttributePattern     = regexp.MustCompile(`^#\[derive\(([^()]*)\)\]$`)
	codexRustPathPattern            = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:::[A-Za-z_][A-Za-z0-9_]*)*$`)
	codexFixtureStampPattern        = regexp.MustCompile(`(?m)^# codex-version: ([0-9]+\.[0-9]+\.[0-9]+)$`)
	codexFixtureSourceTagPattern    = regexp.MustCompile(`openai/codex tag rust-v([0-9]+\.[0-9]+\.[0-9]+)`)
	codexSubcommandTokenPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	codexGitBlobSHAPattern          = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type githubContentsFile struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	SHA      string `json:"sha"`
}

// PrepareCodexSubcommandFixture fetches the authoritative CLI source at the
// exact pinned release tag and prepares a replacement fixture. It only advances
// the version stamp when the complete command namespace is unchanged. A new or
// removed command therefore stops automated provider refresh before any pin is
// applied and requires an explicit policy classification review.
func PrepareCodexSubcommandFixture(version, fixturePath, outputPath string) error {
	if !stableVersionPattern.MatchString(version) {
		return fmt.Errorf("Codex fixture version must be an exact stable version, got %q", version)
	}
	sourceURL := fmt.Sprintf(defaultCodexCLISourceAPIURLFormat, version)
	return prepareCodexSubcommandFixture(version, fixturePath, outputPath, sourceURL, &http.Client{Timeout: providerBumpHTTPTimeout})
}

func prepareCodexSubcommandFixture(version, fixturePath, outputPath, sourceURL string, client *http.Client) error {
	if fixturePath == outputPath {
		return errors.New("Codex fixture output must be a separate prepared file")
	}
	var sourceFile githubContentsFile
	if err := fetchJSON(client, sourceURL, &sourceFile); err != nil {
		return fmt.Errorf("fetch authoritative Codex CLI source for %s: %w", version, err)
	}
	if sourceFile.Type != "file" || sourceFile.Encoding != "base64" || !codexGitBlobSHAPattern.MatchString(sourceFile.SHA) {
		return fmt.Errorf("authoritative Codex CLI source for %s has malformed GitHub content metadata", version)
	}
	encoded := strings.ReplaceAll(sourceFile.Content, "\n", "")
	source, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode authoritative Codex CLI source for %s: %w", version, err)
	}
	if len(source) == 0 || len(source) > maxCodexCLISourceBytes {
		return fmt.Errorf("authoritative Codex CLI source for %s has %d decoded bytes, want 1..%d", version, len(source), maxCodexCLISourceBytes)
	}
	if actualSHA := codexGitBlobObjectID(source); sourceFile.SHA != actualSHA {
		return fmt.Errorf("authoritative Codex CLI source for %s disagrees with its Git blob SHA: metadata=%s actual=%s", version, sourceFile.SHA, actualSHA)
	}
	subcommands, err := parseCodexSubcommands(source)
	if err != nil {
		return fmt.Errorf("parse authoritative Codex CLI source for %s: %w", version, err)
	}
	fixtureInfo, err := os.Lstat(fixturePath)
	if err != nil {
		return err
	}
	if !fixtureInfo.Mode().IsRegular() || fixtureInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Codex subcommand fixture must be a regular file: %s", fixturePath)
	}
	if outputInfo, statErr := os.Lstat(outputPath); statErr == nil && os.SameFile(fixtureInfo, outputInfo) {
		return errors.New("Codex fixture output must not alias the source fixture")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		return err
	}
	updated, err := updateCodexSubcommandFixture(version, fixture, subcommands)
	if err != nil {
		return err
	}
	return writePreparedCodexFixture(outputPath, updated)
}

// codexGitBlobObjectID computes the SHA-1 object ID mandated by Git's current
// blob format. This is a metadata/content consistency check; release assets
// remain independently bound by SHA-256 digests.
func codexGitBlobObjectID(content []byte) string {
	// #nosec G401 -- Git object IDs require SHA-1; this is not a cryptographic
	// trust decision and the trusted API transport remains separately enforced.
	digest := sha1.New()
	_, _ = digest.Write([]byte(fmt.Sprintf("blob %d\x00", len(content))))
	_, _ = digest.Write(content)
	return hex.EncodeToString(digest.Sum(nil))
}

func parseCodexSubcommands(source []byte) ([]string, error) {
	if strings.Contains(string(source), "\r") {
		return nil, errors.New("source must use LF line endings")
	}
	lines := strings.Split(string(source), "\n")
	inEnum := false
	closed := false
	seenEnum := false
	inPreludeMultilineAttribute := false
	enumAttributes := make([]string, 0, 2)
	attributes := make([]string, 0, 2)
	commands := make([]string, 0, 32)
	seen := make(map[string]bool)
	for lineNumber, line := range lines {
		if !inEnum {
			trimmed := strings.TrimSpace(line)
			if inPreludeMultilineAttribute {
				if strings.HasSuffix(trimmed, "]") {
					inPreludeMultilineAttribute = false
				}
				continue
			}
			if codexSubcommandEnumStartPattern.MatchString(line) {
				if seenEnum {
					return nil, errors.New("source contains more than one enum Subcommand")
				}
				if err := validateCodexSubcommandEnumAttributes(enumAttributes); err != nil {
					return nil, fmt.Errorf("Subcommand enum attributes: %w", err)
				}
				enumAttributes = enumAttributes[:0]
				seenEnum = true
				inEnum = true
				continue
			}
			if strings.HasPrefix(trimmed, "#[") {
				if !strings.HasSuffix(trimmed, "]") {
					enumAttributes = []string{fmt.Sprintf("<multiline attribute at line %d>", lineNumber+1)}
					inPreludeMultilineAttribute = true
					continue
				}
				enumAttributes = append(enumAttributes, trimmed)
				continue
			}
			if trimmed != "" && !strings.HasPrefix(trimmed, "///") && !strings.HasPrefix(trimmed, "//") {
				enumAttributes = enumAttributes[:0]
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "}" {
			if len(attributes) != 0 {
				return nil, fmt.Errorf("dangling Subcommand attribute before line %d", lineNumber+1)
			}
			inEnum = false
			closed = true
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "///") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "#[") {
			if !strings.HasSuffix(trimmed, "]") {
				return nil, fmt.Errorf("multiline Subcommand attribute at line %d is unsupported", lineNumber+1)
			}
			attributes = append(attributes, trimmed)
			continue
		}
		variant, err := parseCodexSubcommandVariant(line)
		if err != nil {
			return nil, fmt.Errorf("unrecognized Subcommand variant at line %d: %s", lineNumber+1, trimmed)
		}
		variantNames, err := codexVariantCommandNames(variant, attributes)
		if err != nil {
			return nil, fmt.Errorf("Subcommand variant %s: %w", variant, err)
		}
		attributes = attributes[:0]
		for _, name := range variantNames {
			if seen[name] {
				return nil, fmt.Errorf("duplicate Codex subcommand token %q", name)
			}
			seen[name] = true
			commands = append(commands, name)
		}
	}
	if !seenEnum || !closed || inEnum {
		return nil, errors.New("source does not contain one complete enum Subcommand")
	}
	if seen["help"] {
		return nil, errors.New("source declares help explicitly; implicit Clap help token would be ambiguous")
	}
	commands = append(commands, "help")
	if len(commands) < 2 {
		return nil, errors.New("source contains no Codex subcommands")
	}
	return commands, nil
}

func parseCodexSubcommandVariant(line string) (string, error) {
	trimmedRight := strings.TrimRight(line, " \t")
	if !strings.HasPrefix(trimmedRight, "    ") || len(trimmedRight) <= 4 || trimmedRight[4] == ' ' || trimmedRight[4] == '\t' {
		return "", errors.New("variant must use exactly four spaces of indentation")
	}
	declaration := strings.TrimSuffix(trimmedRight[4:], ",")
	if declaration == trimmedRight[4:] || declaration == "" {
		return "", errors.New("variant must end with one comma")
	}
	if codexVariantIdentifierPattern.MatchString(declaration) {
		return declaration, nil
	}
	open := strings.IndexByte(declaration, '(')
	if open <= 0 || !codexVariantIdentifierPattern.MatchString(declaration[:open]) || !strings.HasSuffix(declaration, ")") {
		return "", errors.New("variant must be one unit or tuple declaration")
	}
	payload := declaration[open+1 : len(declaration)-1]
	if !codexRustPathPattern.MatchString(payload) {
		return "", errors.New("tuple variant must contain exactly one reviewed Rust type path")
	}
	return declaration[:open], nil
}

func codexVariantCommandNames(variant string, attributes []string) ([]string, error) {
	primary := rustVariantKebabCase(variant)
	hasExplicitName := false
	aliases := make([]string, 0, 2)
	for _, attribute := range attributes {
		if isCodexCfgAttribute(attribute) {
			continue
		}
		items, err := codexCommandAttributeItems(attribute)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item == "hide = true" {
				continue
			}
			if match := codexClapScalarPattern.FindStringSubmatch(item); len(match) == 3 {
				switch match[1] {
				case "name":
					if hasExplicitName {
						return nil, fmt.Errorf("multiple explicit command names in %s", attribute)
					}
					hasExplicitName = true
					primary = match[2]
				case "alias", "visible_alias":
					aliases = append(aliases, match[2])
				}
				continue
			}
			if match := codexClapListPattern.FindStringSubmatch(item); len(match) == 3 {
				listItems, splitErr := splitCodexAttributeItems(match[2])
				if splitErr != nil || len(listItems) == 0 {
					return nil, fmt.Errorf("%s has an empty or malformed alias list", attribute)
				}
				for _, rawAlias := range listItems {
					alias := codexQuotedTokenPattern.FindStringSubmatch(rawAlias)
					if len(alias) != 2 {
						return nil, fmt.Errorf("%s has an empty or malformed alias list", attribute)
					}
					aliases = append(aliases, alias[1])
				}
				continue
			}
			return nil, fmt.Errorf("unsupported dispatch-affecting Clap item %q in %s", item, attribute)
		}
	}
	names := append([]string{primary}, aliases...)
	for _, name := range names {
		if !codexSubcommandTokenPattern.MatchString(name) {
			return nil, fmt.Errorf("invalid command token %q", name)
		}
	}
	return names, nil
}

func isCodexCfgAttribute(attribute string) bool {
	const prefix = "#[cfg("
	if !strings.HasPrefix(attribute, prefix) {
		return false
	}
	depth := 1
	inString := false
	escaped := false
	for index := len(prefix); index < len(attribute); index++ {
		current := attribute[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch current {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch current {
		case '"':
			inString = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index > len(prefix) && attribute[index:] == ")]"
			}
			if depth < 0 {
				return false
			}
		}
	}
	return false
}

func validateCodexSubcommandEnumAttributes(attributes []string) error {
	if len(attributes) != 1 {
		for _, attribute := range attributes {
			if !codexDeriveAttributePattern.MatchString(attribute) {
				return fmt.Errorf("unsupported enum-level attribute %s", attribute)
			}
		}
		return fmt.Errorf("want exactly one clap::Subcommand derive, got %v", attributes)
	}
	match := codexDeriveAttributePattern.FindStringSubmatch(attributes[0])
	if len(match) != 2 {
		return fmt.Errorf("unsupported enum-level attribute %s", attributes[0])
	}
	foundSubcommand := false
	for _, rawDerive := range strings.Split(match[1], ",") {
		derive := strings.TrimSpace(rawDerive)
		if !codexRustPathPattern.MatchString(derive) {
			return fmt.Errorf("unsupported derive %q", derive)
		}
		if derive == "clap::Subcommand" {
			foundSubcommand = true
		}
	}
	if !foundSubcommand {
		return errors.New("missing clap::Subcommand derive")
	}
	return nil
}

func codexCommandAttributeItems(attribute string) ([]string, error) {
	var body string
	switch {
	case strings.HasPrefix(attribute, "#[clap(") && strings.HasSuffix(attribute, ")]"):
		body = strings.TrimSuffix(strings.TrimPrefix(attribute, "#[clap("), ")]")
	case strings.HasPrefix(attribute, "#[command(") && strings.HasSuffix(attribute, ")]"):
		body = strings.TrimSuffix(strings.TrimPrefix(attribute, "#[command("), ")]")
	default:
		return nil, fmt.Errorf("unsupported Subcommand attribute %s", attribute)
	}
	return splitCodexAttributeItems(body)
}

func splitCodexAttributeItems(value string) ([]string, error) {
	items := make([]string, 0, 2)
	start := 0
	depth := 0
	inString := false
	escaped := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch current {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch current {
		case '"':
			inString = true
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
			if depth < 0 {
				return nil, errors.New("unbalanced Clap attribute delimiters")
			}
		case ',':
			if depth == 0 {
				item := strings.TrimSpace(value[start:index])
				if item == "" {
					return nil, errors.New("empty Clap attribute item")
				}
				items = append(items, item)
				start = index + 1
			}
		}
	}
	if inString || escaped || depth != 0 {
		return nil, errors.New("unterminated Clap attribute item")
	}
	last := strings.TrimSpace(value[start:])
	if last == "" {
		return nil, errors.New("empty Clap attribute item")
	}
	return append(items, last), nil
}

func rustVariantKebabCase(variant string) string {
	var out strings.Builder
	for i := 0; i < len(variant); i++ {
		current := variant[i]
		if current >= 'A' && current <= 'Z' {
			if i > 0 {
				previous := variant[i-1]
				nextIsLower := i+1 < len(variant) && variant[i+1] >= 'a' && variant[i+1] <= 'z'
				if (previous >= 'a' && previous <= 'z') || (previous >= '0' && previous <= '9') || ((previous >= 'A' && previous <= 'Z') && nextIsLower) {
					out.WriteByte('-')
				}
			}
			out.WriteByte(current + ('a' - 'A'))
			continue
		}
		out.WriteByte(current)
	}
	return out.String()
}

func updateCodexSubcommandFixture(version string, fixture []byte, authoritative []string) ([]byte, error) {
	text := string(fixture)
	if strings.Contains(text, "\r") {
		return nil, errors.New("Codex subcommand fixture must use LF line endings")
	}
	stampMatches := codexFixtureStampPattern.FindAllStringSubmatch(text, -1)
	if len(stampMatches) != 1 {
		return nil, errors.New("Codex subcommand fixture must contain exactly one version stamp")
	}
	sourceMatches := codexFixtureSourceTagPattern.FindAllStringSubmatch(text, -1)
	if len(sourceMatches) != 1 || sourceMatches[0][1] != stampMatches[0][1] {
		return nil, errors.New("Codex subcommand fixture source tag must occur once and match its version stamp")
	}
	lines := strings.Split(text, "\n")
	firstToken := -1
	existing := make([]string, 0, len(authoritative))
	seenExisting := make(map[string]bool, len(authoritative))
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if firstToken >= 0 && trimmed != "" {
				return nil, errors.New("Codex subcommand fixture comments must precede every token")
			}
			continue
		}
		if !codexSubcommandTokenPattern.MatchString(trimmed) {
			return nil, fmt.Errorf("Codex subcommand fixture contains invalid token %q", trimmed)
		}
		if firstToken < 0 {
			firstToken = index
		}
		if seenExisting[trimmed] {
			return nil, fmt.Errorf("Codex subcommand fixture contains duplicate token %q", trimmed)
		}
		seenExisting[trimmed] = true
		existing = append(existing, trimmed)
	}
	if firstToken < 0 {
		return nil, errors.New("Codex subcommand fixture contains no tokens")
	}
	added, removed := stringSetDifference(authoritative, existing), stringSetDifference(existing, authoritative)
	if len(added) != 0 || len(removed) != 0 {
		return nil, fmt.Errorf("Codex %s changes the classified subcommand namespace (added=%v removed=%v); review and classify the new authoritative enum before updating the fixture", version, added, removed)
	}
	header := strings.Join(lines[:firstToken], "\n")
	header = codexFixtureStampPattern.ReplaceAllString(header, "# codex-version: "+version)
	header = codexFixtureSourceTagPattern.ReplaceAllString(header, "openai/codex tag rust-v"+version)
	return []byte(strings.TrimRight(header, "\n") + "\n" + strings.Join(authoritative, "\n") + "\n"), nil
}

func stringSetDifference(left, right []string) []string {
	rightSet := make(map[string]bool, len(right))
	for _, item := range right {
		rightSet[item] = true
	}
	var difference []string
	for _, item := range left {
		if !rightSet[item] {
			difference = append(difference, item)
		}
	}
	sort.Strings(difference)
	return difference
}

func writePreparedCodexFixture(path string, content []byte) error {
	flags := os.O_WRONLY | os.O_TRUNC
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Codex fixture output must be a regular file: %s", path)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		flags |= os.O_CREATE | os.O_EXCL
	} else {
		return err
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
