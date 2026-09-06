// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

type releaseImageDescriptor struct {
	Digest   string `json:"digest"`
	Platform *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

type releaseImageIndex struct {
	Manifests []releaseImageDescriptor `json:"manifests"`
}

type releaseImageManifest struct {
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Digest string `json:"digest"`
	} `json:"layers"`
}

type releaseImageLayout struct {
	Version string `json:"imageLayoutVersion"`
}

const maxReleaseImageDocumentSize = 1 << 20

type releaseImageHandoff struct {
	Repository     string `json:"repository"`
	RunID          string `json:"run_id"`
	Tag            string `json:"tag"`
	Commit         string `json:"commit"`
	Platform       string `json:"platform"`
	ImageDigest    string `json:"image_digest"`
	ManifestDigest string `json:"manifest_digest"`
	ConfigDigest   string `json:"config_digest"`
	ArchiveSHA256  string `json:"archive_sha256"`
}

func CreateReleaseImageHandoff(archivePath, outputPath, repository, runID, tag, commit, platform, imageDigest, manifestDigest, configDigest string) error {
	index, manifest, archiveDigest, err := inspectReleaseImageArchive(archivePath, imageDigest, manifestDigest, configDigest)
	if err != nil {
		return err
	}
	if err := validateReleaseImageDescriptor(index, platform, manifestDigest); err != nil {
		return err
	}
	if manifest.Config.Digest != configDigest {
		return fmt.Errorf("release image config digest %q does not match %q", manifest.Config.Digest, configDigest)
	}
	handoff := releaseImageHandoff{
		Repository: repository, RunID: runID, Tag: tag, Commit: commit,
		Platform: platform, ImageDigest: imageDigest, ManifestDigest: manifestDigest,
		ConfigDigest: configDigest, ArchiveSHA256: archiveDigest,
	}
	content, err := json.Marshal(handoff)
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(outputPath, content, 0o600)
}

func inspectReleaseImageArchive(path, imageDigest, manifestDigest, configDigest string) (releaseImageIndex, releaseImageManifest, string, error) {
	var index releaseImageIndex
	var manifest releaseImageManifest
	file, err := os.Open(path)
	if err != nil {
		return index, manifest, "", err
	}
	defer file.Close()
	archiveDigest, err := hashAndRewindReleaseImage(file)
	if err != nil {
		return index, manifest, "", err
	}
	members := releaseImageMembers{
		manifestName: releaseImageBlobName(manifestDigest),
		configName:   releaseImageBlobName(configDigest),
		seen:         map[string]struct{}{},
	}
	if err := members.collect(tar.NewReader(file)); err != nil {
		return index, manifest, "", err
	}
	index, manifest, err = members.decode()
	if err != nil {
		return index, manifest, "", err
	}
	if err := members.requireLayers(manifest); err != nil {
		return index, manifest, "", err
	}
	index, err = resolveReleaseImageSubject(file, index, imageDigest, manifestDigest)
	return index, manifest, archiveDigest, err
}

// resolveReleaseImageSubject binds the caller's image digest to the archive and
// returns the index that carries the platform descriptors. A BuildKit export
// built with BUILDKIT_MULTI_PLATFORM=1 wraps those descriptors in a nested
// index, and the wrapper descriptor digest is the image digest BuildKit
// reports. A flat export names the bound manifest itself.
func resolveReleaseImageSubject(file *os.File, index releaseImageIndex, imageDigest, manifestDigest string) (releaseImageIndex, error) {
	if len(index.Manifests) == 1 && index.Manifests[0].Platform == nil {
		if index.Manifests[0].Digest != imageDigest {
			return index, fmt.Errorf("release image index subject %q does not match image digest %q", index.Manifests[0].Digest, imageDigest)
		}
		return readReleaseImageNestedIndex(file, imageDigest)
	}
	if imageDigest != manifestDigest {
		return index, fmt.Errorf("release image digest %q does not match the bound manifest digest %q", imageDigest, manifestDigest)
	}
	return index, nil
}

func readReleaseImageNestedIndex(file *os.File, digest string) (releaseImageIndex, error) {
	var nested releaseImageIndex
	name := releaseImageBlobName(digest)
	content, err := readReleaseImageMember(file, name)
	if err != nil {
		return nested, err
	}
	if err := validateReleaseImageBlob(content, path.Base(name)); err != nil {
		return nested, err
	}
	if err := json.Unmarshal(content, &nested); err != nil {
		return nested, fmt.Errorf("parse nested OCI index: %w", err)
	}
	return nested, nil
}

func readReleaseImageMember(file *os.File, name string) ([]byte, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("release image archive lacks member %q", name)
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimPrefix(header.Name, "./") == name {
			return readReleaseImageDocument(reader, name)
		}
	}
}

func releaseImageBlobName(digest string) string {
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

func hashAndRewindReleaseImage(file *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	_, err := file.Seek(0, io.SeekStart)
	return hex.EncodeToString(hash.Sum(nil)), err
}

type releaseImageMembers struct {
	manifestName string
	configName   string
	seen         map[string]struct{}
	layout       []byte
	index        []byte
	manifest     []byte
	config       []byte
}

func (members *releaseImageMembers) collect(reader *tar.Reader) error {
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := validateReleaseImageMember(header)
		if err != nil {
			return err
		}
		if err := members.read(reader, name); err != nil {
			return err
		}
	}
}

func (members *releaseImageMembers) read(reader io.Reader, name string) error {
	if _, ok := members.seen[name]; ok {
		return fmt.Errorf("release image archive contains duplicate member %q", name)
	}
	members.seen[name] = struct{}{}
	var err error
	switch {
	case name == "oci-layout":
		members.layout, err = readReleaseImageDocument(reader, name)
	case name == "index.json":
		members.index, err = readReleaseImageDocument(reader, name)
	case validReleaseImageBlobName(name):
		err = members.readBlob(reader, name)
	}
	return err
}

func (members *releaseImageMembers) readBlob(reader io.Reader, name string) error {
	if name != members.manifestName && name != members.configName {
		return validateReleaseImageBlobStream(reader, name)
	}
	content, err := readReleaseImageDocument(reader, name)
	if err != nil {
		return err
	}
	if err := validateReleaseImageBlob(content, path.Base(name)); err != nil {
		return err
	}
	if name == members.manifestName {
		members.manifest = content
	}
	if name == members.configName {
		members.config = content
	}
	return nil
}

// requireLayers rejects a manifest whose layer blobs the archive does not
// carry. Every collected blob already matched the digest in its own name, so
// presence is enough here.
func (members *releaseImageMembers) requireLayers(manifest releaseImageManifest) error {
	if len(manifest.Layers) == 0 {
		return errors.New("release image manifest does not reference any layer")
	}
	for _, layer := range manifest.Layers {
		name := releaseImageBlobName(layer.Digest)
		if _, ok := members.seen[name]; !ok {
			return fmt.Errorf("release image archive lacks layer blob %q", layer.Digest)
		}
	}
	return nil
}

func (members *releaseImageMembers) decode() (releaseImageIndex, releaseImageManifest, error) {
	var index releaseImageIndex
	var manifest releaseImageManifest
	if len(members.layout) == 0 || len(members.index) == 0 || len(members.manifest) == 0 || len(members.config) == 0 {
		return index, manifest, errors.New("release image archive lacks the OCI layout, index, bound manifest, or config")
	}
	if err := json.Unmarshal(members.index, &index); err != nil {
		return index, manifest, fmt.Errorf("parse OCI index: %w", err)
	}
	if err := json.Unmarshal(members.manifest, &manifest); err != nil {
		return index, manifest, fmt.Errorf("parse OCI manifest: %w", err)
	}
	var layout releaseImageLayout
	if err := json.Unmarshal(members.layout, &layout); err != nil {
		return index, manifest, fmt.Errorf("parse OCI layout: %w", err)
	}
	if layout.Version != "1.0.0" {
		return index, manifest, fmt.Errorf("unsupported OCI image layout version %q", layout.Version)
	}
	return index, manifest, nil
}

func validateReleaseImageMember(header *tar.Header) (string, error) {
	name := strings.TrimPrefix(header.Name, "./")
	if header.Typeflag == tar.TypeDir {
		name = strings.TrimSuffix(name, "/")
	}
	if path.Clean(name) != name {
		return "", fmt.Errorf("release image archive contains non-canonical member %q", header.Name)
	}
	if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
		return "", fmt.Errorf("release image archive contains special member %q", header.Name)
	}
	if !validReleaseImageMemberName(name, header.Typeflag == tar.TypeDir) {
		return "", fmt.Errorf("release image archive contains unexpected member %q", header.Name)
	}
	return name, nil
}

func validReleaseImageMemberName(name string, directory bool) bool {
	if directory {
		return name == "blobs" || name == "blobs/sha256"
	}
	return name == "oci-layout" || name == "index.json" || validReleaseImageBlobName(name)
}

func validReleaseImageBlobName(name string) bool {
	encoded := strings.TrimPrefix(name, "blobs/sha256/")
	if encoded == name || len(encoded) != 64 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func readReleaseImageDocument(reader io.Reader, name string) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maxReleaseImageDocumentSize+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxReleaseImageDocumentSize {
		return nil, fmt.Errorf("release image document %q exceeds %d bytes", name, maxReleaseImageDocumentSize)
	}
	return content, nil
}

func validateReleaseImageBlob(content []byte, encoded string) error {
	actual := sha256.Sum256(content)
	if hex.EncodeToString(actual[:]) != encoded {
		return fmt.Errorf("OCI blob content does not match sha256:%s", encoded)
	}
	return nil
}

func validateReleaseImageBlobStream(reader io.Reader, name string) error {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != path.Base(name) {
		return fmt.Errorf("OCI blob content does not match sha256:%s", path.Base(name))
	}
	return nil
}

func validateReleaseImageDescriptor(index releaseImageIndex, platform, digest string) error {
	osName, architecture, ok := strings.Cut(platform, "/")
	if !ok || osName == "" || architecture == "" {
		return fmt.Errorf("invalid release image platform %q", platform)
	}
	matches := 0
	for _, descriptor := range index.Manifests {
		if descriptor.Platform == nil {
			continue
		}
		if descriptor.Digest == digest && descriptor.Platform.OS == osName && descriptor.Platform.Architecture == architecture {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("release image must contain one %s descriptor for %s, found %d", platform, digest, matches)
	}
	return nil
}
