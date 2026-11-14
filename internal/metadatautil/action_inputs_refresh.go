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
	var rows []string
	for _, ref := range refs {
		if row, ok := current[ref]; ok {
			rows = append(rows, row)
			continue
		}
		if check {
			rows = append(rows, ref+"\t?")
			continue
		}
		owner, repo, dir, sha, _ := parseActionRef(ref)
		data, err := fetch(ctx, owner, repo, dir, sha)
		if err != nil {
			return false, fmt.Errorf("%s: %w", ref, err)
		}
		names, err := actionInputNames(data)
		if err != nil {
			return false, fmt.Errorf("%s: %w", ref, err)
		}
		rows = append(rows, ref+"\t"+strings.Join(names, ","))
	}
	updated := strings.Join(rows, "\n")
	if updated != "" {
		updated += "\n"
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
	var names []string
	switch metadata.Inputs.Kind {
	case 0:
	case yaml.MappingNode:
		for i := 0; i < len(metadata.Inputs.Content); i += 2 {
			name := metadata.Inputs.Content[i].Value
			if !actionInputNamePattern.MatchString(name) {
				return nil, fmt.Errorf("action metadata declares an input name the table cannot hold: %q", name)
			}
			names = append(names, name)
		}
	default:
		return nil, errors.New("action metadata inputs is not a mapping")
	}
	slices.Sort(names)
	return slices.Compact(names), nil
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
		if resp.StatusCode == http.StatusNotFound {
			if resp.Body != nil {
				resp.Body.Close()
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			if resp.Body != nil {
				resp.Body.Close()
			}
			return nil, fmt.Errorf("unexpected action metadata response %d", resp.StatusCode)
		}
		data, err := readBoundedHTTPBody(resp, actionMetadataMaxBytes, "action metadata")
		if resp.Body != nil {
			resp.Body.Close()
		}
		return data, err
	}
	return nil, errors.New("the action has no action.yml or action.yaml at that commit")
}
