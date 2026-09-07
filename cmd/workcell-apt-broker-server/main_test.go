// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/aptbroker"
)

func TestHelperTimeout(t *testing.T) {
	for _, test := range []struct {
		raw   string
		want  time.Duration
		valid bool
	}{
		{"", aptbroker.DefaultTimeout, true},
		{"7", 7 * time.Second, true},
		{"bad", 0, false},
	} {
		got, err := helperTimeout(test.raw)
		if (err == nil) != test.valid || got != test.want {
			t.Fatalf("helperTimeout(%q) = (%s, %v)", test.raw, got, err)
		}
	}
}

func TestServerDiagnosticIncludesLifecycleFailure(t *testing.T) {
	got := serverDiagnostic(errors.New("handler shutdown exceeded limit"))
	if !strings.Contains(got, "Workcell apt broker failed:") || !strings.Contains(got, "handler shutdown exceeded limit") {
		t.Fatalf("server diagnostic = %q", got)
	}
}

func TestPeerUIDValueRejectsUnsafeValues(t *testing.T) {
	for _, value := range []uint64{0, uint64(^uint32(0)) + 1} {
		if _, err := peerUIDValue(value); err == nil {
			t.Fatalf("peerUIDValue(%d) succeeded", value)
		}
	}
	if got, err := peerUIDValue(1000); err != nil || got != 1000 {
		t.Fatalf("peerUIDValue(1000) = (%d, %v)", got, err)
	}
}
