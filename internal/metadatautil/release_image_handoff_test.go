// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCreateReleaseImageHandoffBindsArchiveIdentity(t *testing.T) {
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 1, "", false)
	output := filepath.Join(t.TempDir(), "handoff.json")
	err := metadatautil.CreateReleaseImageHandoff(archive, output, "owner/repo", "12", "v1", "commit", "linux/amd64", manifestDigest, manifestDigest, configDigest)
	if err != nil {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
	assertReleaseImageHandoff(t, output)
}

func TestCreateReleaseImageHandoffUnwrapsMultiPlatformIndex(t *testing.T) {
	archive, digests := writeWrappedReleaseImageArchive(t)
	output := filepath.Join(t.TempDir(), "handoff.json")
	err := metadatautil.CreateReleaseImageHandoff(archive, output, "owner/repo", "12", "v1", "commit", "linux/amd64", digests.image, digests.manifest, digests.config)
	if err != nil {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
	assertReleaseImageHandoff(t, output)
}

func TestCreateReleaseImageHandoffRejectsForeignWrappedImageDigest(t *testing.T) {
	archive, digests := writeWrappedReleaseImageArchive(t)
	foreign := "sha256:" + strings.Repeat("4", 64)
	err := metadatautil.CreateReleaseImageHandoff(archive, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", foreign, digests.manifest, digests.config)
	if err == nil || !strings.Contains(err.Error(), "does not match image digest") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsForeignFlatImageDigest(t *testing.T) {
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 1, "", false)
	foreign := "sha256:" + strings.Repeat("5", 64)
	err := metadatautil.CreateReleaseImageHandoff(archive, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", foreign, manifestDigest, configDigest)
	if err == nil || !strings.Contains(err.Error(), "does not match the bound manifest digest") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsMissingLayerBlob(t *testing.T) {
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 1, "", false)
	members := readReleaseArchiveMembers(t, archive)
	kept := members[:0]
	for _, member := range members {
		if member.name != releaseArchiveBlobName(contentDigest([]byte("layer"))) {
			kept = append(kept, member)
		}
	}
	err := metadatautil.CreateReleaseImageHandoff(writeReleaseArchiveMembers(t, kept), filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", manifestDigest, manifestDigest, configDigest)
	if err == nil || !strings.Contains(err.Error(), "lacks layer blob") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsMissingNestedDescriptorBlob(t *testing.T) {
	archive, digests := writeWrappedReleaseImageArchive(t)
	stripped := writeWrappedArchiveWithout(t, archive, digests.attestation)
	err := metadatautil.CreateReleaseImageHandoff(stripped, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", digests.image, digests.manifest, digests.config)
	if err == nil || !strings.Contains(err.Error(), "lacks descriptor blob") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

// The attestation manifest is present but the statement layer it references is
// not, so only a walk into the descriptor rejects this archive.
func TestCreateReleaseImageHandoffRejectsIncompleteAttestationManifest(t *testing.T) {
	archive, digests := writeWrappedReleaseImageArchive(t)
	stripped := writeWrappedArchiveWithout(t, archive, digests.attestationStatement)
	err := metadatautil.CreateReleaseImageHandoff(stripped, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", digests.image, digests.manifest, digests.config)
	if err == nil || !strings.Contains(err.Error(), "lacks layer blob") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsUnqualifiedDigest(t *testing.T) {
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 1, "", false)
	raw := strings.TrimPrefix(manifestDigest, "sha256:")
	err := metadatautil.CreateReleaseImageHandoff(archive, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", raw, raw, configDigest)
	if err == nil || !strings.Contains(err.Error(), "malformed release") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsMismatchedArchive(t *testing.T) {
	archive, _, configDigest := writeReleaseImageArchive(t, 1, "", false)
	err := metadatautil.CreateReleaseImageHandoff(archive, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", "sha256:"+strings.Repeat("0", 64), "sha256:"+strings.Repeat("0", 64), configDigest)
	if err == nil || !strings.Contains(err.Error(), "lacks the OCI layout") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsWrongConfiguration(t *testing.T) {
	wrongDigest := "sha256:" + strings.Repeat("1", 64)
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 1, wrongDigest, false)
	err := metadatautil.CreateReleaseImageHandoff(archive, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", manifestDigest, manifestDigest, configDigest)
	if err == nil || !strings.Contains(err.Error(), "config digest") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsDuplicateDescriptors(t *testing.T) {
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 2, "", false)
	err := metadatautil.CreateReleaseImageHandoff(archive, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", manifestDigest, manifestDigest, configDigest)
	if err == nil || !strings.Contains(err.Error(), "found 2") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsDuplicateMembers(t *testing.T) {
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 1, "", true)
	err := metadatautil.CreateReleaseImageHandoff(archive, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", manifestDigest, manifestDigest, configDigest)
	if err == nil || !strings.Contains(err.Error(), "duplicate member") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsMislabeledBlob(t *testing.T) {
	configDigest := "sha256:" + strings.Repeat("2", 64)
	manifest := []byte(fmt.Sprintf(`{"config":{"digest":%q}}`, configDigest))
	manifestDigest := contentDigest(manifest)
	index := []byte(fmt.Sprintf(`{"manifests":[{"digest":%q,"platform":{"os":"linux","architecture":"amd64"}}]}`, manifestDigest))
	members := []releaseArchiveMember{
		{name: "oci-layout", content: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{name: "index.json", content: index},
		{name: "blobs/sha256/" + strings.TrimPrefix(manifestDigest, "sha256:"), content: manifest},
		{name: "blobs/sha256/" + strings.TrimPrefix(configDigest, "sha256:"), content: []byte("wrong")},
	}
	archive := writeReleaseArchiveMembers(t, members)
	err := metadatautil.CreateReleaseImageHandoff(archive, filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", manifestDigest, manifestDigest, configDigest)
	if err == nil || !strings.Contains(err.Error(), "blob content") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsTraversal(t *testing.T) {
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 1, "", false)
	members := readReleaseArchiveMembers(t, archive)
	members = append(members, releaseArchiveMember{name: "../preflight-subjects/workcell.rb", content: []byte("replacement")})
	err := metadatautil.CreateReleaseImageHandoff(writeReleaseArchiveMembers(t, members), filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", manifestDigest, manifestDigest, configDigest)
	if err == nil || !strings.Contains(err.Error(), "unexpected member") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func TestCreateReleaseImageHandoffRejectsLinks(t *testing.T) {
	archive, manifestDigest, configDigest := writeReleaseImageArchive(t, 1, "", false)
	members := readReleaseArchiveMembers(t, archive)
	members = append(members, releaseArchiveMember{name: "blobs/sha256/" + strings.Repeat("3", 64), typeflag: tar.TypeSymlink, linkname: "../index.json"})
	err := metadatautil.CreateReleaseImageHandoff(writeReleaseArchiveMembers(t, members), filepath.Join(t.TempDir(), "out"), "r", "1", "v1", "c", "linux/amd64", manifestDigest, manifestDigest, configDigest)
	if err == nil || !strings.Contains(err.Error(), "special member") {
		t.Fatalf("CreateReleaseImageHandoff() error = %v", err)
	}
}

func assertReleaseImageHandoff(t *testing.T, output string) {
	t.Helper()
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var handoff map[string]any
	if err := json.Unmarshal(content, &handoff); err != nil {
		t.Fatal(err)
	}
	if !validReleaseImageHandoff(handoff) {
		t.Fatalf("handoff = %#v", handoff)
	}
}

func validReleaseImageHandoff(handoff map[string]any) bool {
	return handoff["repository"] == "owner/repo" && handoff["platform"] == "linux/amd64" && len(handoff["archive_sha256"].(string)) == 64
}

func writeReleaseImageArchive(t *testing.T, descriptorCount int, manifestConfigDigest string, duplicateManifest bool) (string, string, string) {
	t.Helper()
	manifestDigest, configDigest, blobs := releaseImageBlobs(t, manifestConfigDigest)
	descriptor := fmt.Sprintf(`{"digest":%q,"platform":{"os":"linux","architecture":"amd64"}}`, manifestDigest)
	index := []byte(`{"manifests":[` + strings.TrimSuffix(strings.Repeat(descriptor+",", descriptorCount), ",") + `]}`)
	members := append([]releaseArchiveMember{
		{name: "oci-layout", content: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{name: "index.json", content: index},
	}, blobs...)
	if duplicateManifest {
		members = append(members, members[1])
	}
	return writeReleaseArchiveMembers(t, members), manifestDigest, configDigest
}

// releaseImageBlobs builds the manifest, config and layer blobs of a
// single-platform OCI image.
func releaseImageBlobs(t *testing.T, manifestConfigDigest string) (string, string, []releaseArchiveMember) {
	t.Helper()
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	configDigest := contentDigest(config)
	layer := []byte("layer")
	layerDigest := contentDigest(layer)
	if manifestConfigDigest == "" {
		manifestConfigDigest = configDigest
	}
	manifest := []byte(fmt.Sprintf(`{"config":{"digest":%q},"layers":[{"digest":%q}]}`, manifestConfigDigest, layerDigest))
	manifestDigest := contentDigest(manifest)
	blobs := []releaseArchiveMember{
		{name: releaseArchiveBlobName(manifestDigest), content: manifest},
		{name: releaseArchiveBlobName(configDigest), content: config},
		{name: releaseArchiveBlobName(layerDigest), content: layer},
	}
	return manifestDigest, configDigest, blobs
}

// writeWrappedReleaseImageArchive mirrors a BUILDKIT_MULTI_PLATFORM=1 export:
// index.json holds one platform-free wrapper descriptor for a nested index that
// carries the platform descriptors.
func writeWrappedReleaseImageArchive(t *testing.T) (string, wrappedReleaseDigests) {
	t.Helper()
	manifestDigest, configDigest, blobs := releaseImageBlobs(t, "")
	statement := []byte(`{"_type":"https://in-toto.io/Statement/v0.1"}`)
	attestationConfig := []byte(`{"architecture":"unknown","os":"unknown"}`)
	attestation := []byte(fmt.Sprintf(`{"config":{"digest":%q},"layers":[{"digest":%q}]}`,
		contentDigest(attestationConfig), contentDigest(statement)))
	attestationDigest := contentDigest(attestation)
	nested := []byte(fmt.Sprintf(`{"manifests":[{"digest":%q,"platform":{"os":"linux","architecture":"amd64"}},`+
		`{"digest":%q,"platform":{"os":"unknown","architecture":"unknown"}}]}`, manifestDigest, attestationDigest))
	imageDigest := contentDigest(nested)
	members := append([]releaseArchiveMember{
		{name: "oci-layout", content: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{name: "index.json", content: []byte(fmt.Sprintf(`{"manifests":[{"digest":%q}]}`, imageDigest))},
		{name: releaseArchiveBlobName(imageDigest), content: nested},
		{name: releaseArchiveBlobName(attestationDigest), content: attestation},
		{name: releaseArchiveBlobName(contentDigest(attestationConfig)), content: attestationConfig},
		{name: releaseArchiveBlobName(contentDigest(statement)), content: statement},
	}, blobs...)
	digests := wrappedReleaseDigests{
		image: imageDigest, manifest: manifestDigest, config: configDigest,
		attestation: attestationDigest, attestationStatement: contentDigest(statement),
	}
	return writeReleaseArchiveMembers(t, members), digests
}

type wrappedReleaseDigests struct {
	image                string
	manifest             string
	config               string
	attestation          string
	attestationStatement string
}

// writeWrappedArchiveWithout rebuilds the wrapped archive with one blob removed.
func writeWrappedArchiveWithout(t *testing.T, archive, digest string) string {
	t.Helper()
	members := readReleaseArchiveMembers(t, archive)
	kept := members[:0]
	for _, member := range members {
		if member.name != releaseArchiveBlobName(digest) {
			kept = append(kept, member)
		}
	}
	return writeReleaseArchiveMembers(t, kept)
}

func releaseArchiveBlobName(digest string) string {
	return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
}

type releaseArchiveMember struct {
	name     string
	content  []byte
	typeflag byte
	linkname string
}

func readReleaseArchiveMembers(t *testing.T, archive string) []releaseArchiveMember {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	var members []releaseArchiveMember
	for {
		member, done, err := readReleaseArchiveMember(reader)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return members
		}
		members = append(members, member)
	}
}

func readReleaseArchiveMember(reader *tar.Reader) (releaseArchiveMember, bool, error) {
	header, err := reader.Next()
	if err == io.EOF {
		return releaseArchiveMember{}, true, nil
	}
	if err != nil {
		return releaseArchiveMember{}, false, err
	}
	content, err := io.ReadAll(reader)
	member := releaseArchiveMember{name: header.Name, content: content, typeflag: header.Typeflag, linkname: header.Linkname}
	return member, false, err
}

func writeReleaseArchiveMembers(t *testing.T, members []releaseArchiveMember) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.tar")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	for _, member := range members {
		writeReleaseArchiveMember(t, writer, member)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeReleaseArchiveMember(t *testing.T, writer *tar.Writer, member releaseArchiveMember) {
	t.Helper()
	typeflag := member.typeflag
	if typeflag == 0 {
		typeflag = tar.TypeReg
	}
	if err := writer.WriteHeader(&tar.Header{Name: member.name, Mode: 0o600, Size: int64(len(member.content)), Typeflag: typeflag, Linkname: member.linkname}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(member.content); err != nil {
		t.Fatal(err)
	}
}

func contentDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}
