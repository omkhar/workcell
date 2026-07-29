// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package runtimebuilder owns Workcell's temporary Buildx builder lifecycle.
package runtimebuilder

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/launcher"
	"golang.org/x/sys/unix"
)

const (
	ownershipFile = "workcell.builder-owned"
	maxInventory  = 8 << 20
	maxRecord     = 32 << 10
)

var dockerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
var nonceValue = regexp.MustCompile(`^[a-f0-9]{32}$`)
var dockerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Config binds builder ownership and Docker execution to one validated target.
type Config struct {
	Profile, Backend, TargetKind, TargetStateRoot, ColimaStateRoot                string
	Workspace, DockerHost, DockerContext, DockerEndpoint, DockerBin, DockerConfig string
	DockerHome, RealHome, DockerCWD, BuildxBin, BuildkitdConfig, ToolPath         string
}

type ownershipRecord struct {
	Version   int           `json:"version"`
	Profile   string        `json:"profile"`
	Backend   string        `json:"backend"`
	Builder   string        `json:"builder"`
	Nonce     string        `json:"nonce"`
	Endpoint  string        `json:"endpoint"`
	Workspace string        `json:"workspace"`
	Adopted   bool          `json:"adopted"`
	Objects   []ownedObject `json:"objects,omitempty"`
}

type ownedObject struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Identity string `json:"identity"`
}

type target struct {
	cfg                  Config
	endpoint, recordPath string
}

type dockerRunner interface {
	Run(args ...string) (string, error)
}

type commandRunner struct {
	cfg Config
	bin string
}

type limitedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	size := len(data)
	room := maxInventory - b.Len()
	if room < size {
		b.overflow = true
		data = data[:max(room, 0)]
	}
	_, _ = b.Buffer.Write(data)
	return size, nil
}

func (r commandRunner) Run(args ...string) (string, error) {
	cmd := exec.Command(r.bin, args...)
	cmd.Dir = r.cfg.DockerCWD
	cmd.Env = []string{"HOME=" + r.cfg.DockerHome, "DOCKER_CONFIG=" + r.cfg.DockerConfig, "LANG=C", "LC_ALL=C", "PATH=" + r.cfg.ToolPath}
	if r.cfg.DockerEndpoint != "" {
		cmd.Env = append(cmd.Env, "DOCKER_HOST="+r.cfg.DockerEndpoint)
	}
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return "", errors.New("Docker command output exceeds size limit")
	}
	if err != nil {
		return "", fmt.Errorf("%s %s failed: %w: %s", filepath.Base(r.bin), strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func prepare(cfg Config) (target, error) {
	if err := hoststate.ValidateProfileName(cfg.Profile); err != nil {
		return target{}, err
	}
	if cfg.Workspace == "" || strings.ContainsAny(cfg.Workspace, "\r\n\x00") {
		return target{}, errors.New("runtime-image builder workspace must be a non-empty single-line path")
	}
	for _, item := range []struct{ label, path string }{
		{"target state root", cfg.TargetStateRoot},
		{"Colima state root", cfg.ColimaStateRoot},
		{"workspace", cfg.Workspace},
		{"Docker binary", cfg.DockerBin},
		{"Buildx binary", cfg.BuildxBin},
		{"Docker config", cfg.DockerConfig},
		{"Docker home", cfg.DockerHome},
		{"real home", cfg.RealHome},
		{"Docker working directory", cfg.DockerCWD},
	} {
		if !filepath.IsAbs(item.path) {
			return target{}, fmt.Errorf("runtime-image builder %s must be an absolute path", item.label)
		}
	}
	if cfg.BuildkitdConfig != "" && !filepath.IsAbs(cfg.BuildkitdConfig) {
		return target{}, errors.New("runtime-image builder BuildKit config must be an absolute path")
	}
	for _, entry := range filepath.SplitList(cfg.ToolPath) {
		if !filepath.IsAbs(entry) {
			return target{}, errors.New("runtime-image builder tool PATH must contain only absolute entries")
		}
	}
	endpoint, err := validateAuthority(cfg)
	if err != nil {
		return target{}, err
	}
	stateDir, err := hoststate.ProfileTargetStateDir(cfg.TargetStateRoot, cfg.TargetKind, cfg.Backend, cfg.Profile)
	if err != nil {
		return target{}, err
	}
	marker, err := readRegularFile(filepath.Join(stateDir, "workcell.managed"), maxRecord)
	if err != nil {
		return target{}, fmt.Errorf("validate managed profile marker: %w", err)
	}
	if string(marker) != cfg.Workspace+"\n" {
		return target{}, errors.New("managed profile marker does not match current workspace authority")
	}
	return target{cfg: cfg, endpoint: endpoint, recordPath: filepath.Join(stateDir, ownershipFile)}, nil
}

func validateAuthority(cfg Config) (string, error) {
	switch cfg.Backend {
	case "colima":
		want := "unix://" + filepath.Join(cfg.ColimaStateRoot, cfg.Profile, "docker.sock")
		if cfg.TargetKind != "local_vm" || cfg.DockerHost != want || cfg.DockerEndpoint != want || cfg.DockerContext != "" {
			return "", errors.New("Colima runtime-image builder lacks exact local_vm profile socket authority")
		}
		return want, nil
	case "docker-desktop":
		want := "unix://" + filepath.Join(cfg.RealHome, ".docker", "run", "docker.sock")
		if cfg.TargetKind != "local_compat" || cfg.DockerHost != "" ||
			cfg.DockerContext != launcher.DockerDesktopContextName || cfg.DockerEndpoint != want {
			return "", errors.New("Docker Desktop runtime-image builder lacks reviewed local_compat context authority")
		}
		return cfg.DockerContext + ":" + cfg.DockerEndpoint, nil
	default:
		return "", fmt.Errorf("unsupported runtime-image builder backend %q", cfg.Backend)
	}
}

func claim(t target, runner dockerRunner, random io.Reader, sleep func(time.Duration)) (string, error) {
	if existing, err := readOwnership(t); err != nil {
		return "", err
	} else if existing != nil {
		if err := cleanup(t, runner, sleep); err != nil {
			return "", fmt.Errorf("recover prior runtime-image builder: %w", err)
		}
	}
	for attempt := 0; attempt < 4; attempt++ {
		nonceBytes := make([]byte, 16)
		if _, err := io.ReadFull(random, nonceBytes); err != nil {
			return "", fmt.Errorf("generate runtime-image builder identity: %w", err)
		}
		nonce := hex.EncodeToString(nonceBytes)
		record := ownershipRecord{
			Version:   1,
			Profile:   t.cfg.Profile,
			Backend:   t.cfg.Backend,
			Builder:   "workcell-runtime-" + t.cfg.Profile + "-" + nonce,
			Nonce:     nonce,
			Endpoint:  t.endpoint,
			Workspace: t.cfg.Workspace,
		}
		objects, err := observe(runner, record.Builder)
		if err != nil {
			return "", err
		}
		if len(objects) != 0 {
			continue
		}
		if err := createOwnership(t.recordPath, record); err != nil {
			return "", err
		}
		return record.Builder, nil
	}
	return "", errors.New("could not allocate a collision-free runtime-image builder identity")
}

func adopt(t target, runner dockerRunner) error {
	record, err := requireOwnership(t)
	if err != nil {
		return err
	}
	objects, err := observe(runner, record.Builder)
	if err != nil {
		return err
	}
	var containers, volumes int
	for _, object := range objects {
		switch object.Kind {
		case "container":
			containers++
		case "volume":
			volumes++
		}
	}
	if containers != 1 || volumes != 1 {
		return fmt.Errorf("runtime-image builder created %d containers and %d volumes; expected one of each", containers, volumes)
	}
	record.Adopted = true
	record.Objects = objects
	return replaceOwnership(t.recordPath, *record)
}

func create(t target, docker, buildx dockerRunner) error {
	record, err := requireOwnership(t)
	if err != nil {
		return err
	}
	args := []string{"create", "--driver", "docker-container"}
	if t.cfg.BuildkitdConfig != "" {
		args = append(args, "--buildkitd-config", t.cfg.BuildkitdConfig)
	}
	args = append(args, "--name", record.Builder, "--use")
	if _, err := buildx.Run(args...); err != nil {
		return err
	}
	if _, err := buildx.Run("inspect", record.Builder, "--bootstrap"); err != nil {
		return err
	}
	return adopt(t, docker)
}

func cleanup(t target, runner dockerRunner, sleep func(time.Duration)) error {
	record, err := requireOwnership(t)
	if err != nil {
		return err
	}
	if !record.Adopted {
		record.Objects, err = observe(runner, record.Builder)
		if err != nil {
			return err
		}
		record.Adopted = true
		if err := replaceOwnership(t.recordPath, *record); err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		present, err := verifyOwned(runner, *record)
		if err != nil {
			return err
		}
		if len(present) == 0 {
			return removeOwnership(t.recordPath, *record)
		}
		for _, object := range present {
			switch object.Kind {
			case "container":
				_, _ = runner.Run("rm", "-f", object.Identity)
			case "volume":
				_, _ = runner.Run("volume", "rm", object.Name)
			}
		}
		if attempt < 4 {
			sleep(time.Second)
		}
	}
	present, err := verifyOwned(runner, *record)
	if err != nil {
		return err
	}
	if len(present) == 0 {
		return removeOwnership(t.recordPath, *record)
	}
	return errors.New("could not remove every exact owned runtime-image builder resource")
}

func verifyOwned(runner dockerRunner, record ownershipRecord) ([]ownedObject, error) {
	current, err := observe(runner, record.Builder)
	if err != nil {
		return nil, err
	}
	expected := make(map[string]ownedObject, len(record.Objects))
	for _, object := range record.Objects {
		expected[object.Kind+"\x00"+object.Name] = object
	}
	for _, object := range current {
		want, ok := expected[object.Kind+"\x00"+object.Name]
		if !ok || want.Identity != object.Identity {
			return nil, fmt.Errorf("runtime-image builder object identity changed for %s %s", object.Kind, object.Name)
		}
	}
	return current, nil
}

func observe(runner dockerRunner, builder string) ([]ownedObject, error) {
	containers, err := inventory(runner, "ps", "-a", "--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("capture full Docker container inventory: %w", err)
	}
	volumes, err := inventory(runner, "volume", "ls", "--format", "{{.Name}}")
	if err != nil {
		return nil, fmt.Errorf("capture full Docker volume inventory: %w", err)
	}
	names := resourceNames(builder)
	objects := make([]ownedObject, 0, 2)
	for _, name := range names[:2] {
		if _, ok := containers[name]; !ok {
			continue
		}
		id, err := runner.Run("inspect", "--type", "container", "--format", "{{.Id}}", name)
		if err != nil {
			return nil, err
		}
		id = strings.TrimSpace(id)
		if !dockerID.MatchString(id) {
			return nil, fmt.Errorf("Docker returned malformed container identity %q", id)
		}
		objects = append(objects, ownedObject{Kind: "container", Name: name, Identity: id})
	}
	for _, name := range names[2:] {
		if _, ok := volumes[name]; !ok {
			continue
		}
		raw, err := runner.Run("volume", "inspect", "--format", "{{json .}}", name)
		if err != nil {
			return nil, err
		}
		identity, err := volumeIdentity(name, raw)
		if err != nil {
			return nil, err
		}
		objects = append(objects, ownedObject{Kind: "volume", Name: name, Identity: identity})
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].Kind == objects[j].Kind {
			return objects[i].Name < objects[j].Name
		}
		return objects[i].Kind < objects[j].Kind
	})
	return objects, nil
}

func inventory(runner dockerRunner, args ...string) (map[string]struct{}, error) {
	raw, err := runner.Run(args...)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxInventory {
		return nil, errors.New("Docker inventory exceeds size limit")
	}
	result := make(map[string]struct{})
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		name := strings.TrimSuffix(scanner.Text(), "\r")
		if !dockerName.MatchString(name) {
			return nil, fmt.Errorf("Docker inventory contains malformed name %q", name)
		}
		result[name] = struct{}{}
	}
	return result, scanner.Err()
}

type volumeInspect struct {
	Name, CreatedAt, Driver, Mountpoint, Scope string
	Labels, Options                            map[string]string
}

func volumeIdentity(name, raw string) (string, error) {
	var volume volumeInspect
	if err := decodeStrict(strings.NewReader(raw), &volume); err != nil {
		return "", fmt.Errorf("decode Docker volume identity: %w", err)
	}
	if volume.Name != name || volume.CreatedAt == "" || volume.Driver == "" || volume.Mountpoint == "" {
		return "", errors.New("Docker volume identity is incomplete or names a different volume")
	}
	canonical, err := json.Marshal(volume)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func resourceNames(builder string) [4]string {
	base := "buildx_buildkit_" + builder
	return [4]string{base, base + "0", base + "_state", base + "0_state"}
}

func readOwnership(t target) (*ownershipRecord, error) {
	data, err := readRegularFile(t.recordPath, maxRecord)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read runtime-image builder ownership: %w", err)
	}
	var record ownershipRecord
	if err := decodeStrict(bytes.NewReader(data), &record); err != nil {
		return nil, fmt.Errorf("decode runtime-image builder ownership: %w", err)
	}
	if record.Version != 1 || record.Profile != t.cfg.Profile || record.Backend != t.cfg.Backend ||
		record.Endpoint != t.endpoint || record.Workspace != t.cfg.Workspace ||
		!nonceValue.MatchString(record.Nonce) ||
		record.Builder != "workcell-runtime-"+record.Profile+"-"+record.Nonce {
		return nil, errors.New("runtime-image builder ownership does not match current authority")
	}
	allowed := make(map[string]struct{}, 4)
	names := resourceNames(record.Builder)
	for _, name := range names[:2] {
		allowed["container\x00"+name] = struct{}{}
	}
	for _, name := range names[2:] {
		allowed["volume\x00"+name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(record.Objects))
	for _, object := range record.Objects {
		key := object.Kind + "\x00" + object.Name
		if _, ok := allowed[key]; !ok || !dockerID.MatchString(object.Identity) {
			return nil, errors.New("runtime-image builder ownership contains a malformed object identity")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("runtime-image builder ownership contains a duplicate object")
		}
		seen[key] = struct{}{}
	}
	if !record.Adopted && len(record.Objects) != 0 {
		return nil, errors.New("unadopted runtime-image builder ownership contains objects")
	}
	return &record, nil
}

func decodeStrict(reader io.Reader, output any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func requireOwnership(t target) (*ownershipRecord, error) {
	record, err := readOwnership(t)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, errors.New("runtime-image builder ownership record is missing")
	}
	return record, nil
}

func createOwnership(path string, record ownershipRecord) error {
	data, err := ownershipData(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create runtime-image builder ownership: %w", err)
	}
	if err := writeSynced(file, data); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

func replaceOwnership(path string, record ownershipRecord) error {
	data, err := ownershipData(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".workcell-builder-owned.*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if err := writeSynced(file, data); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removeOwnership(path string, want ownershipRecord) error {
	data, err := readRegularFile(path, maxRecord)
	if err != nil {
		return err
	}
	wantData, err := ownershipData(want)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, wantData) {
		return errors.New("runtime-image builder ownership changed before finalization")
	}
	return os.Remove(path)
}

func ownershipData(record ownershipRecord) ([]byte, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeSynced(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func readRegularFile(path string, limit int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("file exceeds size limit")
	}
	return data, nil
}
