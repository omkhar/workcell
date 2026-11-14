// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/rootio"
	"gopkg.in/yaml.v3"
)

const (
	actionMetadataMaxBytes = 1 << 20
	actionMetadataTimeout  = 60 * time.Second
	actionMetadataBaseURL  = "https://raw.githubusercontent.com"
)

// pinnedActionRefPattern is a SHA-pinned remote action: owner/repo, an optional
// subdirectory, and a full commit SHA. Each path part is one plain name, so a
// ref cannot leave the repository in the fetched URL.
var pinnedActionRefPattern = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)((?:/[A-Za-z0-9_.-]+)*)@([0-9a-f]{40})$`)

// parseActionRef splits a SHA-pinned ref into owner, repo, subdirectory ("" at
// the repository root) and SHA. It refuses a "." or ".." path part.
func parseActionRef(ref string) (owner, repo, dir, sha string, ok bool) {
	match := pinnedActionRefPattern.FindStringSubmatch(ref)
	if match == nil {
		return "", "", "", "", false
	}
	dir = strings.TrimPrefix(match[3], "/")
	parts := []string{match[1], match[2]}
	if dir != "" {
		parts = append(parts, strings.Split(dir, "/")...)
	}
	for _, part := range parts {
		if part == "." || part == ".." {
			return "", "", "", "", false
		}
	}
	return match[1], match[2], dir, match[4], true
}

// actionInputNamePattern is an input name that the inputs table can hold: no
// comma, tab, newline or Markdown control character.
var actionInputNamePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// ActionMetadataFetcher returns the action.yml (or action.yaml) of one action
// at one commit. owner, repo and dir are plain names checked against
// pinnedActionRefPattern; dir is "" for the repository root.
type ActionMetadataFetcher func(ctx context.Context, owner, repo, dir, sha string) ([]byte, error)

// RefreshActionInputs rewrites tests/fixtures/actions/inputs.tsv so it holds
// one row for each SHA-pinned remote action that a workflow in
// .github/workflows uses, and no other row. A commit SHA names fixed content,
// so a row that the table already holds is kept as it is, and only a new pin
// is fetched. With check set, it writes nothing and reports whether the table
// would change. It reads the workflows as data and runs nothing from rootDir.
func RefreshActionInputs(ctx context.Context, rootDir string, fetch ActionMetadataFetcher, check bool) (bool, error) {
	refs, err := pinnedWorkflowActionRefs(rootDir)
	if err != nil {
		return false, err
	}
	current, err := readActionInputRows(rootDir)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		if _, ok := current[ref]; check && !ok {
			return true, nil // --check fetches nothing: a pin with no row is a change.
		}
	}
	updated, err := buildActionInputsTable(ctx, refs, current, fetch)
	if err != nil {
		return false, err
	}
	existing, err := readActionInputsText(rootDir)
	if err != nil {
		return false, err
	}
	if updated == existing {
		return false, nil
	}
	if check {
		return true, nil
	}
	path := filepath.Join(rootDir, workflowActionInputsPath)
	parent, cleaned, err := rootio.OpenParentDirectoryNoFollow(path)
	if err != nil {
		return false, err
	}
	defer parent.Close()
	if err := rootio.WriteFileAtomicAtNoFollow(parent, filepath.Base(cleaned), []byte(updated), 0o644, ".action-inputs-"); err != nil {
		return false, err
	}
	return true, nil
}

// buildActionInputsTable returns the table text for refs: the row in known
// when there is one, else a row built from the fetched action metadata.
func buildActionInputsTable(ctx context.Context, refs []string, known map[string]string, fetch ActionMetadataFetcher) (string, error) {
	var rows []string
	for _, ref := range refs {
		if row, ok := known[ref]; ok {
			rows = append(rows, row)
			continue
		}
		row, err := fetchActionInputsRow(ctx, ref, fetch)
		if err != nil {
			return "", err
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return "", nil
	}
	return strings.Join(rows, "\n") + "\n", nil
}

func fetchActionInputsRow(ctx context.Context, ref string, fetch ActionMetadataFetcher) (string, error) {
	owner, repo, dir, sha, _ := parseActionRef(ref)
	data, err := fetch(ctx, owner, repo, dir, sha)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ref, err)
	}
	names, err := actionInputNames(data)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ref, err)
	}
	return ref + "\t" + strings.Join(names, ","), nil
}

// VerifyActionInputs checks the inputs table in rootDir, a bump tree with
// the follow-up patch applied, against baseDir, the reviewed commit the bump
// starts from. The table must hold exactly one row for each pin in the
// rootDir workflows. A pin that baseDir has a row for keeps that row byte for
// byte. Any other pin must be new: no baseDir workflow uses it, so it is in the
// bump's workflow diff, and its row must equal the input names of the
// action.yml that fetch returns for that commit. It reads both trees as data.
func VerifyActionInputs(ctx context.Context, baseDir, rootDir string, fetch ActionMetadataFetcher) error {
	refs, err := pinnedWorkflowActionRefs(rootDir)
	if err != nil {
		return err
	}
	baseRefs, err := pinnedWorkflowActionRefs(baseDir)
	if err != nil {
		return fmt.Errorf("base: %w", err)
	}
	baseRows, err := readActionInputRows(baseDir)
	if err != nil {
		return fmt.Errorf("base: %w", err)
	}
	for _, ref := range refs {
		if _, ok := baseRows[ref]; !ok && slices.Contains(baseRefs, ref) {
			return fmt.Errorf("%s: the base workflows use this pin but the base table has no row for it", ref)
		}
	}
	want, err := buildActionInputsTable(ctx, refs, baseRows, fetch)
	if err != nil {
		return err
	}
	got, err := readActionInputsText(rootDir)
	if err != nil {
		return err
	}
	gotLines, wantLines := strings.SplitAfter(got, "\n"), strings.SplitAfter(want, "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			return fmt.Errorf("%s line %d is %q; the base rows and the fetched action metadata give %q", workflowActionInputsPath, i+1, g, w)
		}
	}
	return nil
}

// pinnedWorkflowActionRefs returns each remote action ref that a workflow step
// uses, sorted and unique. A local (./) or docker:// step has no row. Any
// other ref must be SHA-pinned, as pin hygiene requires.
func pinnedWorkflowActionRefs(rootDir string) ([]string, error) {
	documents, files, err := loadWorkflowDocuments(rootDir)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, file := range files {
		for job, definition := range documents[file].Jobs {
			for _, step := range definition.Steps {
				uses := step.Uses
				if uses == "" || strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "docker://") {
					continue
				}
				if _, _, _, _, ok := parseActionRef(uses); !ok {
					return nil, fmt.Errorf("%q job %q: uses %q is not owner/repo[/path]@<40-hex commit>", file, job, uses)
				}
				refs = append(refs, uses)
			}
		}
	}
	slices.Sort(refs)
	return slices.Compact(refs), nil
}

func readActionInputsText(rootDir string) (string, error) {
	data, err := rootio.ReadFileNoFollow(filepath.Join(rootDir, workflowActionInputsPath), "action inputs cache", workflowRefsMaxBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// readActionInputRows maps each ref in the table to its whole row. A missing
// table is empty, so the first run fetches every pin.
func readActionInputRows(rootDir string) (map[string]string, error) {
	text, err := readActionInputsText(rootDir)
	if err != nil {
		return nil, err
	}
	rows := map[string]string{}
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		ref, _, found := strings.Cut(line, "\t")
		if !found || ref == "" {
			return nil, fmt.Errorf("%s: malformed row %q", workflowActionInputsPath, line)
		}
		rows[ref] = line
	}
	return rows, nil
}

// actionInputNames returns the sorted input names that an action metadata
// file declares, spelled as the file spells them.
func actionInputNames(data []byte) ([]string, error) {
	var metadata struct {
		Inputs yaml.Node `yaml:"inputs"`
	}
	if err := yaml.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("parse action metadata: %w", err)
	}
	inputs := resolveYAMLAlias(&metadata.Inputs)
	var names []string
	switch {
	case inputs.Kind == 0:
	case inputs.Kind == yaml.ScalarNode && inputs.Tag == "!!null":
	case inputs.Kind == yaml.MappingNode:
		for i := 0; i < len(inputs.Content); i += 2 {
			key := resolveYAMLAlias(inputs.Content[i])
			if key.Kind != yaml.ScalarNode || !actionInputNamePattern.MatchString(key.Value) {
				return nil, fmt.Errorf("action metadata declares an input name the table cannot hold: %q", key.Value)
			}
			names = append(names, key.Value)
		}
	default:
		return nil, errors.New("action metadata inputs is not a mapping")
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// resolveYAMLAlias returns the node that an alias names, or node itself.
func resolveYAMLAlias(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return node.Alias
	}
	return node
}

// FetchActionMetadata gets action.yml, then action.yaml, for one pinned
// action from raw.githubusercontent.com. It sends no credential, follows no
// redirect, and bounds the size and time of each request.
func FetchActionMetadata(ctx context.Context, owner, repo, dir, sha string) ([]byte, error) {
	client := &http.Client{
		Transport: newUpstreamRetryTransport(newUpstreamHTTPTransport()),
		Timeout:   actionMetadataTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return fetchActionMetadata(ctx, client, actionMetadataBaseURL, owner, repo, dir, sha)
}

func fetchActionMetadata(ctx context.Context, client upstreamHTTPDoer, baseURL, owner, repo, dir, sha string) ([]byte, error) {
	ref := owner + "/" + repo
	if dir != "" {
		ref += "/" + dir
	}
	if _, _, gotDir, _, ok := parseActionRef(ref + "@" + sha); !ok || gotDir != dir {
		return nil, fmt.Errorf("refusing to fetch metadata for %q", ref+"@"+sha)
	}
	prefix := baseURL + "/" + owner + "/" + repo + "/" + sha + "/"
	if dir != "" {
		prefix += dir + "/"
	}
	for _, name := range []string{"action.yml", "action.yaml"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, prefix+name, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("User-Agent", upstreamUserAgent)
		resp, err := client.Do(req)
		if err != nil {
			return nil, errors.New("unable to download action metadata")
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				continue
			}
			return nil, fmt.Errorf("unexpected action metadata response %d", resp.StatusCode)
		}
		defer resp.Body.Close()
		return readBoundedHTTPBody(resp, actionMetadataMaxBytes, "action metadata")
	}
	return nil, errors.New("the action has no action.yml or action.yaml at that commit")
}
