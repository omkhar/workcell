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
const claudeMentionContractSHA256 = "2685ea2409901c4fb03663ff7ba09dd8a5206a8afc02955c3de64f3da2687907"

const claudeMentionContractError = "claude-mention.yml must match the reviewed owner-gated contract"

var claudeMentionActionRef = regexp.MustCompile(`^anthropics/claude-code-action@[0-9a-f]{40}$`)

// claudeMentionReferences names what only claude-mention.yml may contain: the
// action, its secret and its environment. Any mention elsewhere, a comment
// included, fails, because a second workflow could bind the environment and
// read the secret without the owner gate.
var claudeMentionReferences = []string{"claude-code-action", "CLAUDE_CODE_OAUTH_TOKEN", "claude-mention"}

// validateClaudeMentionPlacement keeps the action, the secret and the
// environment in claude-mention.yml, and pins that file to its contract.
func validateClaudeMentionPlacement(text, path string) error {
	if filepath.Base(path) == claudeMentionWorkflowName {
		return ValidateClaudeMentionWorkflow(text)
	}
	for _, reference := range claudeMentionReferences {
		if strings.Contains(text, reference) {
			return fmt.Errorf("%s must not name %s; only .github/workflows/%s may", path, reference, claudeMentionWorkflowName)
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
