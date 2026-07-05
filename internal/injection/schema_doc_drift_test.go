// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/providerid"
)

// docSchemaScope names the doc-key anchor labels and expects each label's Go
// source-of-truth set to be defined in schemaScopeSets below.  The anchors live
// in docs/injection-policy.md as `<!-- schema:<label>:begin -->` /
// `<!-- schema:<label>:end -->` markers wrapping a Markdown table whose first
// column holds each documented key in a backtick code span.

// schemaDocPath is the operator-facing schema documentation whose annotated key
// tables must stay in lock-step with the parser's accepted key sets.
const schemaDocPath = "docs/injection-policy.md"

// docSchemaKeyCell matches a Markdown table row whose first data cell is a
// single backtick-wrapped key token (e.g. `| ` + "`version`" + ` | integer |`).
// Header rows ("| Key |") and separator rows ("|---|") do not match because
// their first cell is not backtick-wrapped, so they are ignored.
var docSchemaKeyCell = regexp.MustCompile("^\\|\\s*`([A-Za-z0-9_]+)`\\s*\\|")

// schemaScopeSets binds each documentation anchor label to the exact
// parser-accepted key set the injection code enforces.  Every entry is the same
// value the parser passes to validateAllowedKeys (or the same registry map it
// keys credentials/documents on), so this test fails if a key is documented but
// not accepted, or accepted but not documented.
func schemaScopeSets() map[string]map[string]struct{} {
	return map[string]map[string]struct{}{
		"root":              allowedRootPolicyKeys,
		"documents":         providerid.DocumentKeySet(),
		"credentials":       mapKeysSet(sortedKeys(credentialContainerPaths)),
		"credentials-entry": allowedCredentialEntryKeys,
		"ssh":               allowedSSHKeys,
		"copies":            allowedCopyEntryKeys,
		"network":           allowedNetworkKeys,
	}
}

// TestInjectionPolicyDocSchemaMatchesParser is the E5 drift check: for every
// schema scope, the keys documented in docs/injection-policy.md must equal the
// keys the parser accepts.  It is a white-box test in package injection so it
// consumes the real accepted-key sets directly (no re-encoded key list), which
// is the strongest possible grounding against doc/parser drift.
func TestInjectionPolicyDocSchemaMatchesParser(t *testing.T) {
	t.Parallel()
	repoRoot := findRepoRoot(t)
	docBytes, err := os.ReadFile(filepath.Join(repoRoot, schemaDocPath))
	if err != nil {
		t.Fatalf("read %s: %v", schemaDocPath, err)
	}
	doc := string(docBytes)

	for scope, parserKeys := range schemaScopeSets() {
		scope, parserKeys := scope, parserKeys
		t.Run(scope, func(t *testing.T) {
			t.Parallel()
			documented, err := extractDocSchemaKeys(doc, scope)
			if err != nil {
				t.Fatalf("scope %q: %v", scope, err)
			}
			documentedOnly := setDifference(documented, parserKeys)
			parserOnly := setDifference(parserKeys, documented)
			if len(documentedOnly) > 0 || len(parserOnly) > 0 {
				t.Fatalf(
					"scope %q drift: keys documented but not accepted by the parser: %v; keys accepted by the parser but not documented: %v",
					scope, documentedOnly, parserOnly,
				)
			}
		})
	}
}

// TestInjectionPolicyDocSchemaScopesAreExhaustive guards the drift check itself:
// it fails if the doc grows a `schema:<label>` anchor block that no scope in
// schemaScopeSets validates, so nobody can add an unchecked schema table.
func TestInjectionPolicyDocSchemaScopesAreExhaustive(t *testing.T) {
	t.Parallel()
	repoRoot := findRepoRoot(t)
	docBytes, err := os.ReadFile(filepath.Join(repoRoot, schemaDocPath))
	if err != nil {
		t.Fatalf("read %s: %v", schemaDocPath, err)
	}
	anchorLabels := regexp.MustCompile(`<!-- schema:([a-z-]+):begin -->`).FindAllStringSubmatch(string(docBytes), -1)
	known := schemaScopeSets()
	for _, match := range anchorLabels {
		label := match[1]
		if _, ok := known[label]; !ok {
			t.Fatalf("doc declares schema anchor %q that no drift-check scope validates", label)
		}
	}
}

// extractDocSchemaKeys returns the set of keys documented inside the
// `<!-- schema:<scope>:begin -->` / `<!-- schema:<scope>:end -->` block, read
// from the first (backtick-wrapped) column of every Markdown table data row.
func extractDocSchemaKeys(doc, scope string) (map[string]struct{}, error) {
	begin := fmt.Sprintf("<!-- schema:%s:begin -->", scope)
	end := fmt.Sprintf("<!-- schema:%s:end -->", scope)
	beginIdx := strings.Index(doc, begin)
	if beginIdx < 0 {
		return nil, fmt.Errorf("missing anchor %q in doc", begin)
	}
	endIdx := strings.Index(doc, end)
	if endIdx < 0 {
		return nil, fmt.Errorf("missing anchor %q in doc", end)
	}
	if endIdx < beginIdx {
		return nil, fmt.Errorf("anchor %q appears before %q", end, begin)
	}
	block := doc[beginIdx+len(begin) : endIdx]
	keys := map[string]struct{}{}
	for _, line := range strings.Split(block, "\n") {
		match := docSchemaKeyCell.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		if _, dup := keys[match[1]]; dup {
			return nil, fmt.Errorf("scope %q documents key %q more than once", scope, match[1])
		}
		keys[match[1]] = struct{}{}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("scope %q anchor block documents no keys", scope)
	}
	return keys, nil
}

// setDifference returns the sorted keys present in a but not in b.
func setDifference(a, b map[string]struct{}) []string {
	var out []string
	for key := range a {
		if _, ok := b[key]; !ok {
			out = append(out, key)
		}
	}
	slices.Sort(out)
	return out
}
