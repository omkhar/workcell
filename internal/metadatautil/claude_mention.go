// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// claudeMentionWorkflowName is the only workflow that may run
// anthropics/claude-code-action or read its environment and secret.
const claudeMentionWorkflowName = "claude-mention.yml"

// claudeMentionContractSHA256 covers the whole parsed claude-mention workflow
// with comments removed and the action commit replaced by a fixed word, so a
// pin bump keeps the digest. The owner-id gate, the triggers, the permissions,
// the fork refusal and the tool deny list are all inside it: any change to
// them is a reviewed change to this constant.
const claudeMentionContractSHA256 = "0902b84bf84fdb7786e06320154adc6e61f2c56d11d7b243d3e751a497e56e5c"

const claudeMentionContractError = "claude-mention.yml must match the reviewed owner-gated contract"

var claudeMentionActionRef = regexp.MustCompile(`^anthropics/claude-code-action@[0-9a-f]{40}$`)

// claudeMentionReferences names what only claude-mention.yml may contain: the
// action, its secret and its environment. Any mention elsewhere in any case, a
// comment included, fails, because a second workflow could bind the
// environment and read the secret without the owner gate.
var claudeMentionReferences = []string{"claude-code-action", "claude_code_oauth_token", "claude-mention"}

// secretsContext finds the secrets context in an expression. A word character,
// a dot or a hyphen before it makes it another name, as in steps.secrets.
var secretsContext = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_.-])secrets(?:[^A-Za-z0-9_-]|$)`)

// secretsAccess is one named secret after the context: secrets.NAME or
// secrets['NAME']. Any other use, such as toJSON(secrets) or a computed index,
// can reach every secret, the claude-mention one included.
var secretsAccess = regexp.MustCompile(`^\s*(?:\.\s*([A-Za-z_][A-Za-z0-9_]*)|\[\s*'([^']*)'\s*\])`)

// validateClaudeMentionPlacement keeps the action, the secret and the
// environment in claude-mention.yml, and pins that file to its contract.
func validateClaudeMentionPlacement(text, path string) error {
	if filepath.Base(path) == claudeMentionWorkflowName {
		return ValidateClaudeMentionWorkflow(text)
	}
	lower := strings.ToLower(text)
	for _, reference := range claudeMentionReferences {
		if strings.Contains(lower, reference) {
			return fmt.Errorf("%s must not name %s in any case; only .github/workflows/%s may", path, reference, claudeMentionWorkflowName)
		}
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		return fmt.Errorf("%s: parse for the claude-mention secret check: %w", path, err)
	}
	return checkClaudeMentionReach(&root, "", path, map[*yaml.Node]bool{})
}

// checkClaudeMentionReach walks every scalar. An environment chosen by an
// expression may name claude-mention at run time. In an expression, which is
// a ${{ }} span or a whole if: value, the secrets context may only name one
// secret literally, and that secret must not be the claude-mention one.
func checkClaudeMentionReach(node *yaml.Node, key, path string, open map[*yaml.Node]bool) error {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		// An alias carries its anchor's value to a new key, such as
		// environment. An alias inside its own anchor would recurse forever.
		if open[node.Alias] {
			return fmt.Errorf("%s holds a YAML alias inside its own anchor", path)
		}
		open[node.Alias] = true
		defer delete(open, node.Alias)
		return checkClaudeMentionReach(node.Alias, key, path, open)
	}
	if node.Kind == yaml.ScalarNode {
		// A quoted YAML escape spells a name the raw-text check cannot see.
		decoded := strings.ToLower(node.Value)
		for _, reference := range claudeMentionReferences {
			if strings.Contains(decoded, reference) {
				return fmt.Errorf("%s must not name %s in any spelling; only .github/workflows/%s may", path, reference, claudeMentionWorkflowName)
			}
		}
		expression := strings.Contains(node.Value, "${{") || key == "if"
		if key == "environment" && strings.Contains(node.Value, "${{") {
			return fmt.Errorf("%s must name its environment literally, not by an expression", path)
		}
		if expression {
			return checkSecretsUse(node.Value, path)
		}
		return nil
	}
	for i, child := range node.Content {
		childKey := key
		if node.Kind == yaml.MappingNode {
			if i%2 == 0 {
				childKey = ""
			} else {
				childKey = node.Content[i-1].Value
				// environment: {name: ...} names it one level down.
				if key == "environment" && childKey == "name" {
					childKey = "environment"
				}
			}
		}
		if err := checkClaudeMentionReach(child, childKey, path, open); err != nil {
			return err
		}
	}
	return nil
}

func checkSecretsUse(expression, path string) error {
	for _, at := range secretsContext.FindAllStringIndex(expression, -1) {
		rest := expression[at[1]:]
		if at[1] > at[0] && !strings.HasSuffix(strings.ToLower(expression[at[0]:at[1]]), "secrets") {
			rest = expression[at[1]-1:]
		}
		access := secretsAccess.FindStringSubmatch(rest)
		if access == nil {
			return fmt.Errorf("%s must read each secret by a literal name; a computed index or the whole secrets context can reach CLAUDE_CODE_OAUTH_TOKEN", path)
		}
		if strings.EqualFold(access[1]+access[2], "CLAUDE_CODE_OAUTH_TOKEN") {
			return fmt.Errorf("%s must not read CLAUDE_CODE_OAUTH_TOKEN; only .github/workflows/%s may", path, claudeMentionWorkflowName)
		}
	}
	return nil
}

// ValidateClaudeMentionWorkflow compares the parsed workflow with the reviewed
// digest. Exactly one step may use anthropics/claude-code-action, pinned by a
// full commit SHA.
func ValidateClaudeMentionWorkflow(workflowText string) error {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(workflowText), &root); err != nil {
		return fmt.Errorf("%s: %w", claudeMentionContractError, err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return errors.New(claudeMentionContractError + ": the file must hold one workflow mapping")
	}
	actions, err := normalizeClaudeMentionNode(&root)
	if err != nil {
		return err
	}
	if actions != 1 {
		return fmt.Errorf("%s: want one anthropics/claude-code-action step, found %d", claudeMentionContractError, actions)
	}
	content, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("%s: %w", claudeMentionContractError, err)
	}
	sum := sha256.Sum256(content)
	if digest := hex.EncodeToString(sum[:]); digest != claudeMentionContractSHA256 {
		return fmt.Errorf("%s: got digest %s", claudeMentionContractError, digest)
	}
	return nil
}

// normalizeClaudeMentionNode removes comments and replaces each pinned action
// commit with a fixed word. It returns the number of action references. A
// reference that is not a full commit SHA, or an alias, which could move a
// reviewed value out of sight, is an error.
func normalizeClaudeMentionNode(node *yaml.Node) (int, error) {
	node.HeadComment, node.LineComment, node.FootComment = "", "", ""
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return 0, errors.New(claudeMentionContractError + ": YAML anchors and aliases are not allowed")
	}
	actions := 0
	for i, child := range node.Content {
		if node.Kind == yaml.MappingNode && i%2 == 1 && node.Content[i-1].Value == "uses" &&
			child.Kind == yaml.ScalarNode && strings.Contains(child.Value, "claude-code-action") {
			if !claudeMentionActionRef.MatchString(child.Value) {
				return 0, fmt.Errorf("%s: pin anthropics/claude-code-action by full commit SHA, found %q", claudeMentionContractError, child.Value)
			}
			child.Value = "anthropics/claude-code-action@pinned"
			actions++
		}
		found, err := normalizeClaudeMentionNode(child)
		if err != nil {
			return 0, err
		}
		actions += found
	}
	return actions, nil
}
