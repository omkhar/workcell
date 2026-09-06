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

type releaseImageIndex struct {
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
}

type releaseImageManifest struct {
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
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
	index, manifest, archiveDigest, err := inspectReleaseImageArchive(archivePath, manifestDigest, configDigest)
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

func inspectReleaseImageArchive(path, manifestDigest, configDigest string) (releaseImageIndex, releaseImageManifest, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return releaseImageIndex{}, releaseImageManifest{}, "", err
	}
	defer file.Close()
	archiveDigest, err := hashAndRewindReleaseImage(file)
	if err != nil {
		return releaseImageIndex{}, releaseImageManifest{}, "", err
	}
	reader := tar.NewReader(file)
	layoutBytes, indexBytes, manifestBytes, configBytes, err := readReleaseImageMembers(reader, manifestDigest, configDigest)
	if err != nil {
		return releaseImageIndex{}, releaseImageManifest{}, "", err
	}
	index, manifest, err := decodeAndValidateReleaseImage(layoutBytes, indexBytes, manifestBytes, configBytes, manifestDigest, configDigest)
	return index, manifest, archiveDigest, err
}

func decodeAndValidateReleaseImage(layoutBytes, indexBytes, manifestBytes, configBytes []byte, manifestDigest, configDigest string) (releaseImageIndex, releaseImageManifest, error) {
	index, manifest, err := decodeReleaseImageDocuments(indexBytes, manifestBytes)
	if err != nil {
		return index, manifest, err
	}
	if err := validateReleaseImageLayout(layoutBytes); err != nil {
		return index, manifest, err
	}
	err = validateReleaseImageBlobs(manifestBytes, manifestDigest, configBytes, configDigest)
	return index, manifest, err
}

func validateReleaseImageLayout(content []byte) error {
	var layout releaseImageLayout
	if err := json.Unmarshal(content, &layout); err != nil {
		return fmt.Errorf("parse OCI layout: %w", err)
	}
	if layout.Version != "1.0.0" {
		return fmt.Errorf("unsupported OCI image layout version %q", layout.Version)
	}
	return nil
}

func hashAndRewindReleaseImage(file *os.File) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	_, err := file.Seek(0, io.SeekStart)
	return hex.EncodeToString(hash.Sum(nil)), err
}

func validateReleaseImageBlobs(manifest []byte, manifestDigest string, config []byte, configDigest string) error {
	if err := validateReleaseImageBlob(manifest, manifestDigest); err != nil {
		return err
	}
	return validateReleaseImageBlob(config, configDigest)
}

func validateReleaseImageBlob(content []byte, digest string) error {
	actual := sha256.Sum256(content)
	if hex.EncodeToString(actual[:]) != strings.TrimPrefix(digest, "sha256:") {
		return fmt.Errorf("OCI blob content does not match %s", digest)
	}
	return nil
}

func decodeReleaseImageDocuments(indexBytes, manifestBytes []byte) (releaseImageIndex, releaseImageManifest, error) {
	var index releaseImageIndex
	var manifest releaseImageManifest
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		return index, manifest, fmt.Errorf("parse OCI index: %w", err)
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return index, manifest, fmt.Errorf("parse OCI manifest: %w", err)
	}
	return index, manifest, nil
}

func readReleaseImageMembers(reader *tar.Reader, manifestDigest, configDigest string) ([]byte, []byte, []byte, []byte, error) {
	members := releaseImageMembers{
		manifestName: "blobs/sha256/" + strings.TrimPrefix(manifestDigest, "sha256:"),
		configName:   "blobs/sha256/" + strings.TrimPrefix(configDigest, "sha256:"),
		seen:         map[string]struct{}{},
	}
	if err := collectReleaseImageMembers(reader, &members); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := requireReleaseImageMembers(members.layout, members.index, members.manifest, members.config); err != nil {
		return nil, nil, nil, nil, err
	}
	return members.layout, members.index, members.manifest, members.config, nil
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

func collectReleaseImageMembers(reader *tar.Reader, members *releaseImageMembers) error {
	for {
		done, err := readReleaseImageMember(reader, members)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func readReleaseImageMember(reader *tar.Reader, members *releaseImageMembers) (bool, error) {
	header, err := reader.Next()
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	name, err := validateReleaseImageMember(header)
	if err != nil {
		return false, err
	}
	return false, members.read(reader, name)
}

func validateReleaseImageMember(header *tar.Header) (string, error) {
	name := strings.TrimPrefix(header.Name, "./")
	if header.Typeflag == tar.TypeDir {
		name = strings.TrimSuffix(name, "/")
	}
	if path.Clean(name) != name {
		return "", fmt.Errorf("release image archive contains non-canonical member %q", header.Name)
	}
	if !validReleaseImageMemberType(header.Typeflag) {
		return "", fmt.Errorf("release image archive contains special member %q", header.Name)
	}
	if !validReleaseImageMemberName(name, header.Typeflag == tar.TypeDir) {
		return "", fmt.Errorf("release image archive contains unexpected member %q", header.Name)
	}
	return name, nil
}

func validReleaseImageMemberType(typeflag byte) bool {
	return typeflag == tar.TypeReg || typeflag == tar.TypeRegA || typeflag == tar.TypeDir
}

func validReleaseImageMemberName(name string, directory bool) bool {
	if directory {
		return name == "blobs" || name == "blobs/sha256"
	}
	return name == "oci-layout" || name == "index.json" || validReleaseImageBlobName(name)
}

func validReleaseImageBlobName(name string) bool {
	const prefix = "blobs/sha256/"
	encoded := strings.TrimPrefix(name, prefix)
	if encoded == name || len(encoded) != 64 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func recordReleaseImageMember(seen map[string]struct{}, name string) error {
	if _, ok := seen[name]; ok {
		return fmt.Errorf("release image archive contains duplicate member %q", name)
	}
	seen[name] = struct{}{}
	return nil
}

func requireReleaseImageMembers(layoutBytes, indexBytes, manifestBytes, configBytes []byte) error {
	if len(layoutBytes) == 0 || len(indexBytes) == 0 || len(manifestBytes) == 0 || len(configBytes) == 0 {
		return errors.New("release image archive lacks the OCI layout, index, bound manifest, or config")
	}
	return nil
}

func (members *releaseImageMembers) read(reader io.Reader, name string) error {
	if err := recordReleaseImageMember(members.seen, name); err != nil {
		return err
	}
	if validReleaseImageBlobName(name) {
		return members.readBlob(reader, name)
	}
	var err error
	if name == "index.json" {
		members.index, err = readReleaseImageDocument(reader, name)
	}
	if name == "oci-layout" {
		members.layout, err = readReleaseImageDocument(reader, name)
	}
	return err
}

func (members *releaseImageMembers) readBlob(reader io.Reader, name string) error {
	if !members.isBoundBlob(name) {
		return validateReleaseImageBlobStream(reader, name)
	}
	content, err := readReleaseImageDocument(reader, name)
	if err != nil {
		return err
	}
	if err := validateReleaseImageBlob(content, "sha256:"+path.Base(name)); err != nil {
		return err
	}
	members.storeBoundBlob(name, content)
	return nil
}

func (members *releaseImageMembers) isBoundBlob(name string) bool {
	return name == members.manifestName || name == members.configName
}

func (members *releaseImageMembers) storeBoundBlob(name string, content []byte) {
	if name == members.manifestName {
		members.manifest = content
	}
	if name == members.configName {
		members.config = content
	}
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
	osName, architecture, err := splitReleaseImagePlatform(platform)
	if err != nil {
		return err
	}
	matches := countReleaseImageDescriptors(index, digest, osName, architecture)
	if matches != 1 {
		return fmt.Errorf("release image must contain one %s descriptor for %s, found %d", platform, digest, matches)
	}
	return nil
}

func splitReleaseImagePlatform(platform string) (string, string, error) {
	osName, architecture, ok := strings.Cut(platform, "/")
	if !ok || osName == "" || architecture == "" {
		return "", "", fmt.Errorf("invalid release image platform %q", platform)
	}
	return osName, architecture, nil
}

func countReleaseImageDescriptors(index releaseImageIndex, digest, osName, architecture string) int {
	matches := 0
	for _, descriptor := range index.Manifests {
		if releaseImageDescriptorMatches(descriptor.Digest, descriptor.Platform.OS, descriptor.Platform.Architecture, digest, osName, architecture) {
			matches++
		}
	}
	return matches
}

func releaseImageDescriptorMatches(actualDigest, actualOS, actualArchitecture, digest, osName, architecture string) bool {
	return actualDigest == digest && actualOS == osName && actualArchitecture == architecture
}
