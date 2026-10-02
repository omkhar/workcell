// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/injectionpolicy"
	"github.com/omkhar/workcell/internal/tomlsubset"
)

// ManifestSchema is the only adapters/<id>/adapter.toml schema version that
// parseManifest accepts.
const ManifestSchema = 1

// Manifest is the declarative form of one adapter. gen.go renders the
// registry in data_gen.go and the providerid lists from it. The parity test in
// manifest_test.go keeps it equal to the launcher shell tables and the Rust
// launcher.
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

var (
	manifestIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// The generated shell files embed credential keys and endpoints as text,
	// so each must be a plain token.
	credentialKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	endpointPattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?:[0-9]{1,5}$`)
)

// LoadManifests loads every <root>/<id>/adapter.toml in name order; each id must
// match its directory. It fails closed: only a regular file whose name cannot be
// an id (README.md) is skipped. Entries and manifests open with O_NOFOLLOW from
// the parent descriptor, so a path swapped after the listing cannot redirect a
// read or drop an adapter.
func LoadManifests(root string) ([]Manifest, error) {
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", root, err)
	}
	rootDir := os.NewFile(uintptr(rootFD), root)
	defer rootDir.Close()
	names, err := rootDir.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", root, err)
	}
	slices.Sort(names)
	var out []Manifest
	for _, name := range names {
		path := filepath.Join(root, name, "adapter.toml")
		content, err := readManifestFile(rootFD, name)
		// A regular file named like an adapter id could be a replaced adapter.
		if errors.Is(err, errRegularFile) && !manifestIDPattern.MatchString(name) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		m, err := parseManifest(path, content)
		if err != nil {
			return nil, err
		}
		if m.ID != name {
			return nil, fmt.Errorf("%s: id %q does not match directory %q", path, m.ID, name)
		}
		out = append(out, m)
	}
	return out, nil
}

var errRegularFile = errors.New("regular file, not an adapter directory")

// readManifestFile reads <dir>/adapter.toml below rootFD (O_NONBLOCK: a FIFO
// cannot block the open). A non-directory dir gives errRegularFile only for a
// regular file; a symlink or any other type is an error.
func readManifestFile(rootFD int, dir string) ([]byte, error) {
	dirFD, err := unix.Openat(rootFD, dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOTDIR) {
		var st unix.Stat_t
		if unix.Fstatat(rootFD, dir, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFREG {
			return nil, errRegularFile
		}
		return nil, errors.New("adapter entry must be a directory")
	}
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
		if !credentialKeyPattern.MatchString(c.Key) {
			return fmt.Errorf("invalid credential key %q", c.Key)
		}
		if !filepath.IsAbs(c.ContainerPath) {
			return fmt.Errorf("credential %q container_path must be absolute", c.Key)
		}
	}
	switch m.Tier {
	case "certified", "uncertified":
	case "planned":
		// A planned scaffold declares nothing that a parity check would skip.
		if !reflect.DeepEqual(m, Manifest{ID: m.ID, Tier: m.Tier}) {
			return errors.New("a planned manifest declares only schema, id, and tier")
		}
		return nil
	default:
		return fmt.Errorf("invalid tier %q", m.Tier)
	}
	for _, e := range m.EgressEndpoints {
		if !endpointPattern.MatchString(e) {
			return fmt.Errorf("invalid egress endpoint %q", e)
		}
		// The runtime allowlist grammar owns host and port semantics.
		if err := injectionpolicy.ValidateEgressEndpoint(e, "egress endpoint"); err != nil {
			return err
		}
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
