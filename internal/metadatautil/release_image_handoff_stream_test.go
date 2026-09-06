// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestReleaseImageNonTargetBlobUsesBoundedStreaming(t *testing.T) {
	const size = 32 << 20
	digest := repeatedReleaseImageByteDigest('x', size)
	reader := &boundedReleaseImageReader{remaining: size, value: 'x'}
	members := releaseImageMembers{manifestName: "manifest", configName: "config"}
	if err := members.readBlob(reader, "blobs/sha256/"+digest); err != nil {
		t.Fatalf("readBlob() error = %v", err)
	}
	if reader.maxRequest > 64<<10 {
		t.Fatalf("largest read buffer = %d, want at most 65536", reader.maxRequest)
	}
}

func TestReleaseImageDocumentLimit(t *testing.T) {
	oversized := strings.NewReader(strings.Repeat("x", maxReleaseImageDocumentSize+1))
	_, err := readReleaseImageDocument(oversized, "index.json")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("readReleaseImageDocument() error = %v", err)
	}
}

type boundedReleaseImageReader struct {
	remaining  int
	maxRequest int
	value      byte
}

func (reader *boundedReleaseImageReader) Read(buffer []byte) (int, error) {
	if len(buffer) > reader.maxRequest {
		reader.maxRequest = len(buffer)
	}
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	count := min(len(buffer), reader.remaining)
	for index := range buffer[:count] {
		buffer[index] = reader.value
	}
	reader.remaining -= count
	return count, nil
}

func repeatedReleaseImageByteDigest(value byte, size int) string {
	hash := sha256.New()
	reader := &boundedReleaseImageReader{remaining: size, value: value}
	_, _ = io.Copy(hash, reader)
	return hex.EncodeToString(hash.Sum(nil))
}
