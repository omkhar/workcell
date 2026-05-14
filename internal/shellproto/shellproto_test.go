// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package shellproto

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteFieldHappyPath(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteField(&b, "session_id", "abc-123"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if got, want := b.String(), "session_id=abc-123\n"; got != want {
		t.Fatalf("WriteField stdout: got %q, want %q", got, want)
	}
}

func TestWriteFieldEmptyValueAccepted(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteField(&b, "key", ""); err != nil {
		t.Fatalf("WriteField empty value: %v", err)
	}
	if got, want := b.String(), "key=\n"; got != want {
		t.Fatalf("WriteField empty value stdout: got %q, want %q", got, want)
	}
}

func TestWriteFieldRejectsNewlineInValue(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	err := WriteField(&b, "key", "first\nforged=value")
	if err == nil {
		t.Fatal("WriteField with newline in value: want error, got nil")
	}
	if !strings.Contains(err.Error(), "newline") {
		t.Fatalf("WriteField newline error: %v", err)
	}
	if b.Len() != 0 {
		t.Fatalf("WriteField wrote %q on validation failure; expected nothing", b.String())
	}
}

func TestWriteFieldRejectsCarriageReturnInValue(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteField(&b, "key", "v\rval"); err == nil {
		t.Fatal("WriteField with CR in value: want error, got nil")
	}
	if b.Len() != 0 {
		t.Fatalf("WriteField wrote %q on validation failure; expected nothing", b.String())
	}
}

func TestWriteFieldRejectsNULInValue(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteField(&b, "key", "v\x00v"); err == nil {
		t.Fatal("WriteField with NUL in value: want error, got nil")
	}
}

func TestWriteFieldRejectsEmptyKey(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteField(&b, "", "value"); err == nil {
		t.Fatal("WriteField with empty key: want error, got nil")
	}
}

func TestWriteFieldRejectsEqualsInKey(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteField(&b, "key=injected", "value"); err == nil {
		t.Fatal("WriteField with '=' in key: want error, got nil")
	}
}

func TestWriteFieldRejectsNewlineInKey(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	if err := WriteField(&b, "key\nforged", "value"); err == nil {
		t.Fatal("WriteField with newline in key: want error, got nil")
	}
}

func TestWriteFieldsEmitsInOrder(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	fields := []Field{
		{Key: "session_id", Value: "abc"},
		{Key: "force", Value: "1"},
		{Key: "profile", Value: "default"},
	}
	if err := WriteFields(&b, fields); err != nil {
		t.Fatalf("WriteFields: %v", err)
	}
	want := "session_id=abc\nforce=1\nprofile=default\n"
	if got := b.String(); got != want {
		t.Fatalf("WriteFields stdout: got %q, want %q", got, want)
	}
}

func TestWriteFieldsStopsOnFirstError(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	fields := []Field{
		{Key: "ok", Value: "1"},
		{Key: "bad", Value: "v\nforged"},
		{Key: "after", Value: "x"},
	}
	err := WriteFields(&b, fields)
	if err == nil {
		t.Fatal("WriteFields with newline value: want error, got nil")
	}
	if got := b.String(); got != "ok=1\n" {
		t.Fatalf("WriteFields partial output: got %q, want %q", got, "ok=1\n")
	}
}

// TestRoundTripWithInTestParser exercises a minimal in-test parser that
// mirrors the bash `while IFS=read; key=${line%%=*}; value=${line#*=}`
// pattern.  This is the cross-language invariant the package is the
// single source of truth for, so we exercise it here rather than rely
// on a bash-shim integration test for round-trip coverage.
func TestRoundTripWithInTestParser(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	fields := []Field{
		{Key: "session_id", Value: "abc-123"},
		{Key: "force", Value: "1"},
		{Key: "profile", Value: "default"},
		{Key: "container_name", Value: "workcell-default-detached-abc"},
	}
	if err := WriteFields(&b, fields); err != nil {
		t.Fatalf("WriteFields: %v", err)
	}
	got := parseShellProto(b.String())
	want := map[string]string{
		"session_id":     "abc-123",
		"force":          "1",
		"profile":        "default",
		"container_name": "workcell-default-detached-abc",
	}
	if len(got) != len(want) {
		t.Fatalf("round-trip parse count: got %d, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("round-trip parse key %q: got %q, want %q", k, got[k], v)
		}
	}
}

func parseShellProto(plan string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(plan, "\n") {
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		out[line[:idx]] = line[idx+1:]
	}
	return out
}
