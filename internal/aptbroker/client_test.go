// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestPreservedEnvironmentUsesOnlyRequestedApprovedNames(t *testing.T) {
	lookup := func(name string) (string, bool) {
		values := map[string]string{
			"APT_LISTCHANGES_FRONTEND":    "none",
			"DEBCONF_NONINTERACTIVE_SEEN": "true",
			"DEBIAN_FRONTEND":             "noninteractive",
		}
		value, ok := values[name]
		return value, ok
	}
	got, err := preservedEnvironment([]string{"DEBIAN_FRONTEND", "APT_LISTCHANGES_FRONTEND"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"DEBIAN_FRONTEND": "noninteractive", "APT_LISTCHANGES_FRONTEND": "none"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preservedEnvironment() = %#v, want %#v", got, want)
	}
}

func TestPreservedEnvironmentRejectsUnsupportedAndDuplicateNames(t *testing.T) {
	lookup := func(string) (string, bool) { return "noninteractive", true }
	for name, preserve := range map[string][]string{
		"unsupported": {"PATH"},
		"duplicate":   {"DEBIAN_FRONTEND", "DEBIAN_FRONTEND"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := preservedEnvironment(preserve, lookup); err == nil {
				t.Fatal("preservedEnvironment() accepted invalid names")
			}
		})
	}
}

func TestRunClientRejectsMalformedRequestBeforeDial(t *testing.T) {
	response, status, err := RunClient(context.Background(), "/does/not/exist", nil, nil, func(string) (string, bool) { return "", false })
	if err == nil || status != 2 || !reflect.DeepEqual(response, Response{}) {
		t.Fatalf("RunClient() response=%#v status=%d error=%v", response, status, err)
	}
}

func TestRunClientRejectsPreservedInteractiveValueBeforeDial(t *testing.T) {
	lookup := func(string) (string, bool) { return "dialog", true }
	response, status, err := RunClient(context.Background(), "/does/not/exist", []string{"apt-get"}, []string{"DEBIAN_FRONTEND"}, lookup)
	if err == nil || status != 2 || !reflect.DeepEqual(response, Response{}) {
		t.Fatalf("RunClient() response=%#v status=%d error=%v", response, status, err)
	}
}

func TestWriteFullHandlesPartialWrites(t *testing.T) {
	writer := &partialWriter{limit: 2}
	if err := writeFull(writer, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := writer.buffer.String(); got != "abcdef" {
		t.Fatalf("writeFull() wrote %q", got)
	}
}

func TestWriteFullRejectsNoProgress(t *testing.T) {
	if err := writeFull(zeroWriter{}, []byte("data")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeFull() error = %v, want io.ErrShortWrite", err)
	}
}

type partialWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *partialWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit {
		data = data[:w.limit]
	}
	return w.buffer.Write(data)
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }
