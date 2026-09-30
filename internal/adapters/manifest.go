// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

// ManifestSchema is the only adapters/<id>/adapter.toml schema version that
// parseManifest accepts.
const ManifestSchema = 1

// Manifest is the declarative form of one adapter. The parity test in
// manifest_test.go keeps it equal to the hand-written registry in data.go,
// the providerid lists, the launcher shell tables, and the Rust launcher.
type Manifest struct {
	ID                string
	Tier              string // certified | uncertified | planned
	Binary            string
	SharedCredentials bool
	ManagedDocument   bool
	Install           Install
	ReservedTargets   []string
	Credentials       []Credential // declaration order
	EgressEndpoints   []string
}

// Install names where the provider pin lives. The pins themselves stay in
// the Dockerfile (VersionArg) or runtime/container/providers/package.json
// (Package), because the existing pin tools own them.
type Install struct {
	Method     string // binary | npm
	VersionArg string
	Package    string
	Provenance string
}

// Credential is one adapter-scoped credential key.
type Credential struct {
	Key           string
	ContainerPath string
}

var manifestIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// LoadManifests loads every adapters/<id>/adapter.toml under root, in
// directory-name order, and requires each id to match its directory. It fails
// closed: a missing or unreadable root, a symlinked adapter directory, and a
// directory without a regular adapter.toml are errors. Each adapter directory
// and each manifest is opened with O_NOFOLLOW from the descriptor of its
// parent, so a path swapped after the listing cannot redirect a read.
func LoadManifests(root string) ([]Manifest, error) {
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", root, err)
	}
	rootDir := os.NewFile(uintptr(rootFD), root)
	defer rootDir.Close()
	entries, err := rootDir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", root, err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var out []Manifest
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name(), "adapter.toml")
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s: adapter entry must not be a symlink", filepath.Join(root, entry.Name()))
		}
		if !entry.IsDir() {
			continue
		}
		content, err := readManifestFile(rootFD, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		m, err := parseManifest(path, content)
		if err != nil {
			return nil, err
		}
		if m.ID != entry.Name() {
			return nil, fmt.Errorf("%s: id %q does not match directory %q", path, m.ID, entry.Name())
		}
		out = append(out, m)
	}
	return out, nil
}

// readManifestFile reads <dir>/adapter.toml below rootFD. O_NONBLOCK keeps a
// FIFO swapped in for the file from blocking the open; the fstat then rejects it.
func readManifestFile(rootFD int, dir string) ([]byte, error) {
	dirFD, err := unix.Openat(rootFD, dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)
	fileFD, err := unix.Openat(dirFD, "adapter.toml", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fileFD), "adapter.toml")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("adapter.toml must be a regular file")
	}
	return io.ReadAll(file)
}

// parseManifest parses one adapter.toml with the strict TOML subset parser.
// It rejects unknown tables, unknown keys, and values of the wrong type.
func parseManifest(path string, content []byte) (Manifest, error) {
	doc, err := tomlsubset.ParseDocument(string(content), path)
	if err != nil {
		return Manifest{}, err
	}

	var m Manifest
	schema := 0
	if err := bindTable(path, doc.TopLevel, map[string]any{
		"schema":             &schema,
		"id":                 &m.ID,
		"tier":               &m.Tier,
		"binary":             &m.Binary,
		"shared_credentials": &m.SharedCredentials,
		"managed_document":   &m.ManagedDocument,
	}); err != nil {
		return Manifest{}, err
	}
	for _, table := range doc.Tables {
		var fields map[string]any
		switch table.Name {
		case "install":
			fields = map[string]any{
				"method":      &m.Install.Method,
				"version_arg": &m.Install.VersionArg,
				"package":     &m.Install.Package,
				"provenance":  &m.Install.Provenance,
			}
		case "home":
			fields = map[string]any{"reserved_targets": &m.ReservedTargets}
		case "egress":
			fields = map[string]any{"endpoints": &m.EgressEndpoints}
		default:
			key, ok := strings.CutPrefix(table.Name, "credentials.")
			if !ok || strings.Contains(key, ".") {
				return Manifest{}, fmt.Errorf("%s:%d: unknown table [%s]", path, table.Line, table.Name)
			}
			m.Credentials = append(m.Credentials, Credential{Key: key})
			fields = map[string]any{"container_path": &m.Credentials[len(m.Credentials)-1].ContainerPath}
		}
		if err := bindTable(path, table, fields); err != nil {
			return Manifest{}, err
		}
	}

	if err := validateManifest(m, schema); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func bindTable(path string, table tomlsubset.Table, fields map[string]any) error {
	for _, pair := range table.Pairs {
		dst, ok := fields[pair.Key]
		if !ok {
			return fmt.Errorf("%s:%d: unknown key %q", path, pair.Line, pair.Key)
		}
		switch d := dst.(type) {
		case *string:
			*d, ok = pair.Value.(string)
		case *bool:
			*d, ok = pair.Value.(bool)
		case *int:
			*d, ok = pair.Value.(int)
		case *[]string:
			*d, ok = stringList(pair.Value)
		}
		if !ok {
			return fmt.Errorf("%s:%d: key %q has the wrong type", path, pair.Line, pair.Key)
		}
	}
	return nil
}

func stringList(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func validateManifest(m Manifest, schema int) error {
	if schema != ManifestSchema {
		return fmt.Errorf("schema must be %d, got %d", ManifestSchema, schema)
	}
	if !manifestIDPattern.MatchString(m.ID) {
		return fmt.Errorf("invalid id %q", m.ID)
	}
	for _, c := range m.Credentials {
		if !filepath.IsAbs(c.ContainerPath) {
			return fmt.Errorf("credential %q container_path must be absolute", c.Key)
		}
	}
	switch m.Tier {
	case "certified", "uncertified":
	case "planned":
		return nil
	default:
		return fmt.Errorf("invalid tier %q", m.Tier)
	}
	if m.Binary == "" {
		return fmt.Errorf("binary is required for tier %q", m.Tier)
	}
	switch m.Install.Method {
	case "binary":
		if m.Install.VersionArg == "" || m.Install.Package != "" {
			return fmt.Errorf("install method binary needs version_arg and no package")
		}
	case "npm":
		if m.Install.Package == "" || m.Install.VersionArg != "" {
			return fmt.Errorf("install method npm needs package and no version_arg")
		}
	default:
		return fmt.Errorf("invalid install method %q", m.Install.Method)
	}
	if m.Install.Provenance == "" {
		return fmt.Errorf("install provenance is required")
	}
	return nil
}
